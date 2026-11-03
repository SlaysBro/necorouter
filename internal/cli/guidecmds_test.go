package cli

import (
	"strings"
	"testing"
)

// Commands from the demo-exam guide that were missing from the command tree.
func TestGuideOneLinerUsername(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	runCmd(s, "configure terminal")
	if out := runCmd(s, "username net_admin role noc"); strings.Contains(out, "%") {
		t.Fatalf("one-liner rejected: %q", out)
	}
	if u := r.Cfg.FindUser("net_admin"); u == nil || u.Role != "noc" {
		t.Fatalf("user not created with role: %+v", u)
	}
	if !strings.HasSuffix(s.Prompt(), "(config)#") {
		t.Fatalf("one-liner must not enter user mode, prompt %q", s.Prompt())
	}
	if out := runCmd(s, "username net_admin"); strings.Contains(out, "%") || !strings.HasSuffix(s.Prompt(), "(config-user)#") {
		t.Fatalf("plain username must enter user mode: %q %q", out, s.Prompt())
	}
	runCmd(s, "exit")
	if h := s.Help("username net_admin "); !strings.Contains(h, "role") || !strings.Contains(h, "password") {
		t.Fatalf("help: %q", h)
	}
}

func TestGuideDHCPDomainSearch(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	runCmd(s, "configure terminal")
	runCmd(s, "ip pool lan 192.168.0.10-192.168.0.20")
	runCmd(s, "dhcp-server 1")
	if out := runCmd(s, "domain-search au-team.irpo"); strings.Contains(out, "%") {
		t.Fatalf("domain-search rejected: %q", out)
	}
	if !strings.Contains(r.Cfg.Render(), " domain-search au-team.irpo") || strings.Contains(r.Cfg.Render(), " domain-name au-team.irpo") {
		t.Fatalf("domain-search must be option 119 only:\n%s", r.Cfg.Render())
	}
	runCmd(s, "domain-name example.local")
	if out := runCmd(s, "domain-name example.local"); strings.Contains(out, "%") {
		t.Fatalf("domain-name rejected: %q", out)
	}
	rc := r.Cfg.Render()
	if !strings.Contains(rc, " domain-name example.local") || !strings.Contains(rc, " domain-search au-team.irpo") {
		t.Fatalf("both options must be kept separately:\n%s", rc)
	}
}

func TestGuideNTPSynchronize(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	runCmd(s, "configure terminal")
	if h := s.Help("ntp "); !strings.Contains(h, "synchronize") {
		t.Fatalf("synchronize missing in help: %q", h)
	}
	// without a running NTP client the command answers with an error, not "Invalid input"
	out := runCmd(s, "ntp synchronize")
	if strings.Contains(out, "Invalid input") || !strings.Contains(out, "NTP client is not running") {
		t.Fatalf("ntp synchronize: %q", out)
	}
}
