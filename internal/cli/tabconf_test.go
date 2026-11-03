package cli

import (
	"strings"
	"testing"
)

// conf without an argument enters configuration mode; "conf t" works too.
func TestConfShortcutAndRestart(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	if out := runCmd(s, "conf"); strings.Contains(out, "%") {
		t.Fatalf("conf rejected: %q", out)
	}
	if !isCfg(s.top().kind) {
		t.Fatalf("conf did not enter config mode, mode=%s", s.top().kind)
	}
	runCmd(s, "end")
	if out := runCmd(s, "conf t"); strings.Contains(out, "%") || !isCfg(s.top().kind) {
		t.Fatalf("conf t failed: %q", out)
	}
	runCmd(s, "end")
	// restart: an unknown process and the list of processes in the hint
	if out := runCmd(s, "restart foo"); !strings.Contains(out, "%") {
		t.Fatalf("restart foo must be rejected: %q", out)
	}
	if out := s.Help("restart "); !strings.Contains(out, "ospf") || !strings.Contains(out, "dhcp") {
		t.Fatalf("restart help lacks processes: %q", out)
	}
}

// With several options, Tab appends the common prefix and returns the list without descriptions.
func TestTabListsVariants(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	runCmd(s, "enable")
	line, opts := s.Complete("co")
	if len(opts) < 2 {
		t.Fatalf("Tab on co must list variants, got line=%q opts=%v", line, opts)
	}
	for _, o := range opts {
		if strings.Contains(o, " ") && !strings.HasPrefix(o, "\"") {
			t.Fatalf("option carries description text: %q", o)
		}
	}
}
