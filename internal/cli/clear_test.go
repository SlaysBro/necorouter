package cli

import (
	"strings"
	"testing"

	"necorouter/internal/config"
)

func TestClearCommandAllModes(t *testing.T) {
	r := newTestRouter(t)
	for _, kind := range []string{"exec", "priv", "config", "if", "port", "si", "ospf", "user", "dhcp"} {
		s := &Session{R: r, User: "root", Role: "admin", TTY: true, frames: []frame{{kind: kind}}, noApply: true}
		var sb strings.Builder
		s.Exec("clear", &sb)
		if !strings.Contains(sb.String(), "\x1b[2J") || !strings.Contains(sb.String(), "\x1b[H") || strings.Contains(sb.String(), "%") {
			t.Errorf("[%s] clear output: %q", kind, sb.String())
		}
		if !strings.Contains(s.Help(""), "clear") {
			t.Errorf("[%s] clear is not listed in '?'", kind)
		}
	}
	// outside a terminal there are no screen sequences
	s := NewSession(r, &config.User{Name: "a", Role: "admin"})
	var sb strings.Builder
	s.Exec("clear", &sb)
	if sb.String() != "" {
		t.Errorf("non-tty clear printed %q", sb.String())
	}
	// do clear from configuration mode and abbreviations
	s2 := &Session{R: r, User: "root", Role: "admin", TTY: true, frames: []frame{{kind: "config"}}, noApply: true}
	sb.Reset()
	s2.Exec("do cle", &sb)
	if !strings.Contains(sb.String(), "\x1b[2J") {
		t.Errorf("do clear: %q", sb.String())
	}
	if got, _ := s2.Complete("cl"); got != "clear " {
		t.Errorf("tab: %q", got)
	}
}
