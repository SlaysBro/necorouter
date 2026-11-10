package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"necorouter/internal/config"
)

// Output redirection to a file is not part of the command set (EcoRouter has none): '>' and '| redirect' are
// refused for every role, and no file is written.
func TestOutputRedirectionIsRefusedForEveryRole(t *testing.T) {
	r := newTestRouter(t)
	for _, role := range config.Roles {
		s := NewSession(r, &config.User{Name: "u-" + role, Role: role})
		// refused either as an unknown modifier, or because the role cannot see show at all
		refused := func(out string) bool {
			return strings.Contains(out, "Unrecognized modifier") || strings.Contains(out, "% Invalid input")
		}
		if out := runCmd(s, "show version > out-"+role+".txt"); !refused(out) {
			t.Fatalf("role %s: '>' not refused: %q", role, out)
		}
		if out := runCmd(s, "show version | redirect out-"+role+".txt"); !refused(out) {
			t.Fatalf("role %s: '| redirect' not refused: %q", role, out)
		}
		if _, err := os.Stat(filepath.Join(r.P.Var, "files", "out-"+role+".txt")); err == nil {
			t.Fatalf("role %s: a file was written", role)
		}
	}
}

// A configuration file is replayed without the role and feature checks: the startup configuration is read by the daemon,
// and only admin can load another file with copy. The same commands typed by a user stay closed while their feature is off.
func TestReplayIgnoresGatesButTypingDoesNot(t *testing.T) {
	r := newTestRouter(t)
	replay := &Session{R: r, User: "root", Role: config.RoleAdmin, noApply: true}
	typed := NewSession(r, &config.User{Name: "a", Role: config.RoleAdmin})
	gated := 0
	for _, n := range trees[modeConfig] {
		if n.Hidden || n.Gate == nil {
			continue
		}
		gated++
		if _, d, _ := replay.placeLine(modeConfig, []string{n.Word}, false); d.errKind != "" {
			t.Errorf("replay refused the gated command %q: %s", n.Word, d.errKind)
		}
		if typed.permitted(n, modeConfig) {
			t.Errorf("the gated command %q is offered to a typing user while its feature is off", n.Word)
		}
	}
	if gated == 0 {
		t.Skip("no gated configuration command: nothing to compare")
	}
}
