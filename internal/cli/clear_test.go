package cli

import (
	"strings"
	"testing"

	"necorouter/internal/config"
)

// The screen is cleared with Ctrl-L (line editor), not with a 'clear' command.
func TestClearScreenCommandRemoved(t *testing.T) {
	r := newTestRouter(t)
	for _, kind := range []string{modeExec, modePriv, "config", "if", "port", "si", "ospf", "user", "dhcp"} {
		s := &Session{R: r, User: "root", Role: "admin", TTY: true, frames: []frame{{kind: kind}}, noApply: true}
		var sb strings.Builder
		s.Exec("clear", &sb)
		if strings.Contains(sb.String(), "\x1b[2J") {
			t.Errorf("[%s] clear still clears the screen: %q", kind, sb.String())
		}
		if strings.Contains(s.Help(""), "clear") && kind != modePriv {
			t.Errorf("[%s] clear listed in '?' outside privileged mode", kind)
		}
	}
	s := NewSession(r, &config.User{Name: "a", Role: "admin"})
	s.Exec("enable", &strings.Builder{})
	if h := s.Help(""); !strings.Contains(h, "clear") {
		t.Errorf("admin in privileged mode must see clear: %q", h)
	}
}

// clear dhcp-server clients is a privileged-mode command for admin only.
func TestClearDHCPClientsIsPrivilegedAdminOnly(t *testing.T) {
	r := newTestRouter(t)
	user := NewSession(r, &config.User{Name: "a", Role: "admin"})
	if out := runCmd(user, "clear dhcp-server clients"); !strings.Contains(out, "%") {
		t.Errorf("clear dhcp-server clients accepted in user mode: %q", out)
	}
	runCmd(user, "enable")
	if out := runCmd(user, "clear dhcp-server clients"); strings.Contains(out, "%") {
		t.Errorf("admin in privileged mode rejected: %q", out)
	}
	noc := NewSession(r, &config.User{Name: "n", Role: "noc"})
	runCmd(noc, "enable")
	if out := runCmd(noc, "clear dhcp-server clients"); !strings.Contains(out, "%") {
		t.Errorf("noc may not clear leases: %q", out)
	}
}
