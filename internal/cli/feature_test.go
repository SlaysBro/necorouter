package cli

import (
	"strings"
	"testing"
)

// feature and show features are always visible (like ordinary commands); commands belonging to a specific feature
// appear only after it is enabled; service password-encryption is vanilla and always visible.
func TestFeatureGatingAndService(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	if out := s.Help("show "); !strings.Contains(out, "features") {
		t.Fatalf("show features must always be visible:\n%s", out)
	}
	runCmd(s, "configure terminal")
	if out := s.Help("ser"); !strings.Contains(out, "service") {
		t.Fatalf("service must be visible (vanilla): %q", out)
	}
	if out := runCmd(s, "service password-encryption"); strings.Contains(out, "%") {
		t.Fatalf("service password-encryption rejected: %q", out)
	}
	if !r.Cfg.PwEncrypt || !strings.Contains(r.Cfg.Render(), "service password-encryption") {
		t.Fatal("password-encryption not stored or rendered")
	}
	runCmd(s, "no service password-encryption")
	if r.Cfg.PwEncrypt {
		t.Fatal("no service password-encryption did not clear")
	}
}

// autosave is off by default: with no settings, it is not printed in the configuration
func TestAutosaveOffByDefault(t *testing.T) {
	r := newTestRouter(t)
	if r.Cfg.AutosaveOn {
		t.Fatal("autosave must be off by default")
	}
	if strings.Contains(r.Cfg.Render(), "autosave") {
		t.Fatalf("default autosave must not be written to the configuration:\n%s", r.Cfg.Render())
	}
}
