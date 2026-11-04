package cli

import (
	"strings"
	"testing"
)

// Commands from the demo-exam guide that were missing from the command tree.
func TestUsernameRoleAndPasswordRequireUserMode(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	runCmd(s, "configure terminal")
	for _, command := range []string{
		"username net_admin role noc",
		"username net_admin password Secret123",
	} {
		if out := runCmd(s, command); !strings.Contains(out, "%") {
			t.Fatalf("one-line user configuration must be rejected: %q", out)
		}
	}
	if u := r.Cfg.FindUser("net_admin"); u != nil {
		t.Fatalf("rejected one-liner created user: %+v", u)
	}
	if out := runCmd(s, "username net_admin"); strings.Contains(out, "%") || !strings.HasSuffix(s.Prompt(), "(config-user)#") {
		t.Fatalf("username must enter user mode: %q %q", out, s.Prompt())
	}
	if out := runCmd(s, "role noc"); strings.Contains(out, "%") {
		t.Fatalf("role command rejected in user mode: %q", out)
	}
	if u := r.Cfg.FindUser("net_admin"); u == nil || u.Role != "noc" {
		t.Fatalf("user role not set in user mode: %+v", u)
	}
	if h := s.Help("password "); !strings.Contains(h, "PASSWORD") {
		t.Fatalf("password command missing in user mode help: %q", h)
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
