package cli

import (
	"strings"
	"testing"

	"necorouter/internal/config"
)

func TestEnablePassword(t *testing.T) {
	r := newTestRouter(t)
	adm := sessionOnPrivless(r, "admin")
	runCmd(adm, "enable")
	runCmd(adm, "configure terminal")
	for _, bad := range []string{"1abc", "toolongpass", "a-b", ""} {
		if out := runCmd(adm, "enable password "+bad); !strings.Contains(out, "%") || r.Cfg.EnablePass != "" {
			t.Fatalf("bad password %q accepted: %q", bad, out)
		}
	}
	runCmd(adm, "enable password Pass123")
	if h := r.Cfg.EnablePass; !enableHashRe.MatchString(h) || strings.Contains(h, "Pass123") {
		t.Fatalf("hash not stored properly: %q", h)
	}
	if rc := r.Cfg.Render(); strings.Contains(rc, "Pass123") || !strings.Contains(rc, "enable password 8 ") {
		t.Fatalf("running-config leaks or lacks the hash:\n%s", rc)
	}
	// new session: enable asks for the password without echo
	s := NewSession(r, &config.User{Name: "admin", Role: "admin"})
	runCmd(s, "enable")
	if !s.PromptSecret() || s.Prompt() != "Password: " {
		t.Fatalf("no secret prompt: %q", s.Prompt())
	}
	if out := runCmd(s, "wrong"); !strings.Contains(out, "Access denied") || strings.HasSuffix(s.Prompt(), "#") {
		t.Fatalf("wrong password accepted: %q %q", out, s.Prompt())
	}
	runCmd(s, "enable")
	runCmd(s, "Pass123")
	if !strings.HasSuffix(s.Prompt(), "#") {
		t.Fatalf("correct password rejected: %q", s.Prompt())
	}
	// root (the local console) is not asked for a password
	root := NewSession(r, &config.User{Name: "root", Role: "admin"})
	runCmd(root, "enable")
	if !strings.HasSuffix(root.Prompt(), "#") {
		t.Fatal("root must not be asked for the enable password")
	}
	// the hash survives configuration replay
	cfg, errs := r.validateConfig(r.Cfg.Render())
	if len(errs) > 0 || cfg.EnablePass != r.Cfg.EnablePass {
		t.Fatalf("replay lost the hash: %v", errs)
	}
	// remove the password
	runCmd(adm, "no enable password")
	if r.Cfg.EnablePass != "" {
		t.Fatal("no enable password did not clear")
	}
	runCmd(adm, "enable secret abc1")
	if r.Cfg.EnablePass == "" {
		t.Fatal("enable secret not set")
	}
}

func sessionOnPrivless(r *Router, user string) *Session {
	return NewSession(r, &config.User{Name: user, Role: "admin"})
}

func TestSecurityProfiles(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	runCmd(s, "configure terminal")
	runCmd(s, "security-profile 1")
	if !strings.HasSuffix(s.Prompt(), "(config-security-profile)#") {
		t.Fatalf("prompt: %q", s.Prompt())
	}
	for _, l := range []string{
		"rule 0 permit tcp any any eq 23", "rule 1 deny udp any any eq bootp", "rule 2 deny ospf host 127.0.0.12 any",
		"rule 3 deny tcp any 192.168.10.2/24 range 21 23", "rule 4 permit icmp any any", "rule 5 deny 112 any any",
	} {
		if out := runCmd(s, l); strings.Contains(out, "%") {
			t.Fatalf("%q rejected: %q", l, out)
		}
	}
	for _, bad := range []string{
		"rule 6 permit tcp any", "rule 6 permit foo any any", "rule 6 permit icmp any any eq 5",
		"rule 6 permit tcp any any range 5", "rule 6 permit tcp any any range 9 1", "rule 6 permit tcp 1.2.3.4 any",
		"rule 1024 permit tcp any any", "rule 6 allow tcp any any", "rule 6 permit tcp any any eq 70000",
	} {
		if out := runCmd(s, bad); !strings.Contains(out, "%") {
			t.Fatalf("%q accepted", bad)
		}
	}
	runCmd(s, "no rule 5")
	if len(r.Cfg.FindSecProfile(1).Rules) != 5 {
		t.Fatalf("no rule failed: %d", len(r.Cfg.FindSecProfile(1).Rules))
	}
	runCmd(s, "exit")
	// the default profile by default; show shows default and the user one
	runCmd(s, "do show security-profile")
	out := runCmd(s, "do show security-profile")
	for _, w := range []string{"Security profile default", "0: deny tcp any any eq 22", "Security profile 1", "deny ospf 127.0.0.12/32 any", "Applied to line ports: default"} {
		if !strings.Contains(out, w) {
			t.Fatalf("show lacks %q:\n%s", w, out)
		}
	}
	// binding, a ban on changing a bound profile, unbinding
	if out := runCmd(s, "security 9"); !strings.Contains(out, "not found") {
		t.Fatalf("missing profile accepted: %q", out)
	}
	runCmd(s, "security 1")
	runCmd(s, "security-profile 1")
	if out := runCmd(s, "rule 7 permit any any any"); !strings.Contains(out, "Unset profile prior to change it") {
		t.Fatalf("applied profile edited: %q", out)
	}
	runCmd(s, "exit")
	if out := runCmd(s, "no security-profile 1"); !strings.Contains(out, "Unset profile") {
		t.Fatalf("applied profile removed: %q", out)
	}
	// configuration round trip
	rc := r.Cfg.Render()
	if !strings.Contains(rc, "security-profile 1") || !strings.Contains(rc, "security 1") || !strings.Contains(rc, " rule 3 deny tcp any 192.168.10.2/24 range 21 23") {
		t.Fatalf("render:\n%s", rc)
	}
	cfg, errs := r.validateConfig(rc)
	if len(errs) > 0 || cfg.Render() != rc {
		t.Fatalf("round trip: %v", errs)
	}
	runCmd(s, "no security 1")
	if r.Cfg.Security != "none" {
		t.Fatalf("no security: %q", r.Cfg.Security)
	}
	runCmd(s, "security default")
	runCmd(s, "no security default")
	if n, rules := r.Cfg.EffectiveSecurity(); n != "none" || len(rules) != 0 {
		t.Fatalf("no security default: %s", n)
	}
	runCmd(s, "no security-profile 1")
	if r.Cfg.FindSecProfile(1) != nil {
		t.Fatal("profile not removed")
	}
}
