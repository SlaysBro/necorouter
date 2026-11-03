package cli

import (
	"strings"
	"testing"
)

func TestAAALoggingLevel(t *testing.T) {
	r := newTestRouter(t)
	if r.AAALevel() != 3 || strings.Contains(r.Cfg.Render(), "aaa logging") {
		t.Fatal("default must be 3 and not rendered")
	}
	if errs := r.Replay("aaa logging level 5\n"); len(errs) > 0 || r.AAALevel() != 5 {
		t.Fatalf("set level: %v (%d)", errs, r.AAALevel())
	}
	if !strings.Contains(r.Cfg.Render(), "aaa logging level 5") {
		t.Fatal("level not rendered")
	}
	if errs := r.Replay("no aaa logging level\n"); len(errs) > 0 || r.AAALevel() != 3 {
		t.Fatalf("reset: %v (%d)", errs, r.AAALevel())
	}
	if errs := r.Replay("aaa logging level 9\n"); len(errs) == 0 {
		t.Fatal("level 9 must be rejected")
	}
}
