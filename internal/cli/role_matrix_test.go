package cli

import (
	"strings"
	"testing"

	"necorouter/internal/config"
)

// Role matrix from EcoRouter table 128: which commands each role sees in user (>) and
// privileged (#) mode. A command that is not visible is rejected as an invalid input.
func TestRoleMatrixUserAndPrivilegedMode(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // ping and traceroute must not start on the host here (ping without a count runs for ever)
	r := newTestRouter(t)
	session := func(role string, priv bool) *Session {
		s := NewSession(r, &config.User{Name: "x", Role: role})
		if priv {
			var sb strings.Builder
			s.Exec("enable", &sb)
		}
		return s
	}
	visible := func(s *Session, line string) bool {
		var sb strings.Builder
		s.Exec(line, &sb)
		return !strings.Contains(sb.String(), "% Invalid input")
	}
	cases := []struct {
		role string
		priv bool
		cmd  string
		want bool
	}{
		{"admin", false, "show running-config", true},
		{"noc", false, "show running-config", true}, // as in table 128; the secrets are hidden for noc
		{"noc", false, "show version", true},
		{"helpdesk", false, "show version", false},
		{"admin", false, "ping 127.0.0.1", true},
		{"noc", false, "ping 127.0.0.1", false},
		{"noc", false, "traceroute 127.0.0.1", false},
		{"noc", true, "show users", true},
		{"noc", true, "show startup-config", false},
		{"noc", true, "configure terminal", false},
		{"noc", true, "traceroute 127.0.0.1", true},
		{"helpdesk", true, "ping 127.0.0.1", true},
		{"helpdesk", true, "traceroute 127.0.0.1", true},
		{"helpdesk", true, "show version", false},
		{"helpdesk", true, "configure terminal", false},
		{"admin", true, "configure terminal", true},
		{"admin", true, "show clock", true},
		{"admin", false, "show clock", false}, // extensions exist in privileged mode only
	}
	for _, c := range cases {
		s := session(c.role, c.priv)
		if got := visible(s, c.cmd); got != c.want {
			mode := ">"
			if c.priv {
				mode = "#"
			}
			t.Errorf("role %s %s %s: visible=%v, want %v", c.role, mode, c.cmd, got, c.want)
		}
	}
}
