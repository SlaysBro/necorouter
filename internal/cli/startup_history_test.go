package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"necorouter/internal/config"
	"necorouter/internal/svc"
)

// Startup history: the feature is hidden until enabled via service; disabling deletes nothing.
func TestStartupHistoryGateAndRotation(t *testing.T) {
	r := newTestRouter(t)
	s := NewSession(r, &config.User{Name: "root", Role: "admin"})
	run := func(line string) string {
		var sb strings.Builder
		s.Exec(line, &sb)
		return sb.String()
	}
	run("enable")
	run("configure terminal")

	// before feature, the command is not recognized
	if out := run("startup-history keep 2"); !strings.Contains(out, "%") {
		t.Fatalf("hidden feature accepted before service: %q", out)
	}
	if out := run("feature config-features"); strings.Contains(out, "%") {
		t.Fatalf("feature config-features rejected: %q", out)
	}
	run("startup-history keep 2")
	if r.Cfg.StartupKeep != 2 {
		t.Fatalf("keep not set: %d", r.Cfg.StartupKeep)
	}

	// four saves: three old startups go to history, the last two remain
	for i := 0; i < 4; i++ {
		run(fmt.Sprintf("hostname h%d", i))
		if err := r.Save(); err != nil {
			t.Fatal(err)
		}
	}
	hist := svc.DefaultPaths().History()
	ents, _ := os.ReadDir(hist)
	if len(ents) != 2 {
		t.Fatalf("history must keep 2 copies, has %d", len(ents))
	}
	var got []string
	for _, e := range ents {
		b, _ := os.ReadFile(filepath.Join(hist, e.Name()))
		got = append(got, string(b))
	}
	if !strings.Contains(got[0], "hostname h1") || !strings.Contains(got[1], "hostname h2") {
		t.Fatalf("wrong copies kept: %q", got)
	}

	// disabling: the setting stays, files are not deleted, no new copies are created
	run("no feature config-features")
	if r.Cfg.StartupKeep != 2 {
		t.Fatalf("keep must survive disabling: %d", r.Cfg.StartupKeep)
	}
	run("hostname h9")
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	ents, _ = os.ReadDir(hist)
	if len(ents) != 2 {
		t.Fatalf("disabling must not delete or add copies: %d", len(ents))
	}
	if out := run("startup-history keep 3"); !strings.Contains(out, "%") {
		t.Fatalf("command still available after disabling: %q", out)
	}
}

// Configuration text with features is replayed at startup (gates are not checked on load).
func TestStartupHistoryConfigRoundTrip(t *testing.T) {
	r := newTestRouter(t)
	s := NewSession(r, &config.User{Name: "root", Role: "admin"})
	var sb strings.Builder
	for _, l := range []string{"enable", "configure terminal", "feature config-features", "startup-history keep 5", "exit", "exit"} {
		s.Exec(l, &sb)
	}
	text := r.Cfg.Render()
	if !strings.Contains(text, "feature config-features") || !strings.Contains(text, "startup-history keep 5") {
		t.Fatalf("features not rendered:\n%s", text)
	}
	r2 := newTestRouter(t)
	if errs := r2.Replay(text); len(errs) > 0 {
		t.Fatalf("replay errors: %v", errs)
	}
	if !r2.Cfg.Features["config-features"] || r2.Cfg.StartupKeep != 5 {
		t.Fatalf("features lost on replay: %+v keep=%d", r2.Cfg.Features, r2.Cfg.StartupKeep)
	}
}

// Hints: the feature command is always visible, while feature commands (startup-history) only after it is enabled.
func TestStartupHistoryHiddenInHelp(t *testing.T) {
	r := newTestRouter(t)
	s := NewSession(r, &config.User{Name: "root", Role: "admin"})
	var sb strings.Builder
	s.Exec("enable", &sb)
	s.Exec("configure terminal", &sb)
	if out := s.Help(""); !strings.Contains(out, "feature") {
		t.Fatalf("feature must be visible in help:\n%s", out)
	}
	if out := s.Help(""); strings.Contains(out, "startup-history") {
		t.Fatalf("feature command leaked into help before enabling:\n%s", out)
	}
	s.Exec("feature config-features", &sb)
	if out := s.Help(""); !strings.Contains(out, "startup-history") {
		t.Fatalf("feature missing from help after feature:\n%s", out)
	}
}
