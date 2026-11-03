package cli

import (
	"strings"
	"testing"
)

func TestOSPFRedistributeOptions(t *testing.T) {
	r := newTestRouter(t)
	script := "router ospf 1\n" +
		" redistribute static metric 20 metric-type 2 route-map RM1\n" +
		" redistribute connected metric-type 1\n" +
		" default-information originate always metric 5 metric-type 2 route-map RM1\n"
	if errs := r.Replay(script); len(errs) > 0 {
		t.Fatalf("replay: %v", errs)
	}
	out := r.Cfg.Render()
	for _, want := range []string{
		" redistribute static metric 20 metric-type 2 route-map RM1",
		" redistribute connected metric-type 1",
		" default-information originate always metric 5 metric-type 2 route-map RM1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	// re-parsing the rendered configuration yields the same text
	r2 := newTestRouter(t)
	if errs := r2.Replay(out); len(errs) > 0 {
		t.Fatalf("re-replay: %v", errs)
	}
	if r2.Cfg.Render() != out {
		t.Fatal("render is not stable after replay")
	}
	if errs := r.Replay("router ospf 1\n no redistribute static\n no default-information originate\n"); len(errs) > 0 {
		t.Fatalf("negation: %v", errs)
	}
	out = r.Cfg.Render()
	if strings.Contains(out, "redistribute static") || strings.Contains(out, "default-information") {
		t.Fatalf("not removed:\n%s", out)
	}
}
