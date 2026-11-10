package cli

import (
	"strings"
	"testing"

	"necorouter/internal/config"
)

// Top-level configuration commands typed without indentation are accepted from any sub-mode,
// and the sub-mode is left (as in a configuration file). Indented lines stay in their sub-mode.
func TestTopLevelCommandsFromSubModes(t *testing.T) {
	r := newTestRouter(t)
	s := NewSession(r, &config.User{Name: "a", Role: "admin"})
	runCmd(s, "enable")
	runCmd(s, "configure terminal")
	runCmd(s, "port te1")
	runCmd(s, "service-instance te1/vl200")
	if s.top().kind != "si" {
		t.Fatalf("expected service-instance mode, got %q", s.top().kind)
	}
	if out := runCmd(s, "ip pool client-pool 192.168.200.2-192.168.200.14"); strings.Contains(out, "%") {
		t.Fatalf("ip pool from service-instance: %q", out)
	}
	if s.top().kind != modeConfig {
		t.Fatalf("the sub-mode is not left: %q", s.top().kind)
	}

	runCmd(s, "router ospf 1")
	if out := runCmd(s, "ip route 0.0.0.0/0 172.16.1.1"); strings.Contains(out, "%") {
		t.Fatalf("ip route from router ospf: %q", out)
	}
	if s.top().kind != modeConfig {
		t.Fatalf("router mode not left: %q", s.top().kind)
	}

	// an indented line is still checked against its own sub-mode
	runCmd(s, "port te1")
	runCmd(s, "service-instance te1/vl200")
	if out := runCmd(s, "  ip pool other 192.168.200.20-192.168.200.30"); !strings.Contains(out, "%") {
		t.Fatalf("indented top-level command must stay invalid in a sub-mode: %q", out)
	}
	if s.top().kind != "si" {
		t.Fatalf("indented line must not leave the sub-mode")
	}
}

// A configuration file gets the same rule: an indented line stays in its sub-mode, so a top-level
// command with indentation is an error there, while a column-0 line goes to the top level.
func TestReplayIndentedTopLevelCommandIsError(t *testing.T) {
	r := newTestRouter(t)
	if errs := r.Replay("port te1\n service-instance te1/vl200\n  ip pool client 192.168.200.2-192.168.200.14\n"); len(errs) == 0 {
		t.Fatal("indented top-level command inside a sub-mode was accepted")
	}
	r = newTestRouter(t)
	if errs := r.Replay("port te1\n service-instance te1/vl200\nip pool client 192.168.200.2-192.168.200.14\n"); len(errs) != 0 {
		t.Fatalf("column-0 top-level command inside a sub-mode was rejected: %v", errs)
	}
}
