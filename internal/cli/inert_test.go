package cli

import (
	"strings"
	"testing"
)

// A setting that needs a feature is loaded from a file even while the feature is off: it stays in the
// configuration (and is written back by the next save), and it does nothing until the feature line is there.
func TestFeatureSettingStaysInertWhileFeatureIsOff(t *testing.T) {
	r := newTestRouter(t)
	if errs := r.Replay("hostname demo\nautosave interval 10\n"); len(errs) != 0 {
		t.Fatalf("the file must load: %v", errs)
	}
	if !r.Cfg.AutosaveOn {
		t.Fatal("the setting is not kept in the configuration")
	}
	if r.Cfg.Features["config-features"] {
		t.Fatal("the feature must stay off: the file has no feature line")
	}
	if !strings.Contains(r.Cfg.Render(), "autosave interval 10") {
		t.Fatal("the setting is not written back by a save")
	}
	if autosaveActive(r) {
		t.Fatal("the setting is active while the feature is off")
	}
	if errs := r.Replay("feature config-features\n"); len(errs) != 0 {
		t.Fatalf("the feature line must load: %v", errs)
	}
	if !autosaveActive(r) {
		t.Fatal("the setting does not take effect once the feature is on")
	}
}
