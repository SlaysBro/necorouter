package cli

import (
	"os"
	"strings"
	"testing"

	"necorouter/internal/config"
)

// show banner startup: the state and the text of the pre-login banner.
func TestShowBannerStartup(t *testing.T) {
	r := newTestRouter(t)
	s := NewSession(r, &config.User{Name: "a", Role: "admin"})
	if err := os.MkdirAll(r.P.Etc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.P.Banner(), []byte("Authorized access only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runCmd(s, "show banner startup")
	if !strings.Contains(out, "banner startup: enabled") || !strings.Contains(out, "Authorized access only") {
		t.Fatalf("show banner startup: %q", out)
	}
	runCmd(s, "enable")
	runCmd(s, "configure terminal")
	runCmd(s, "no banner startup")
	runCmd(s, "end")
	if out := runCmd(s, "show banner startup"); !strings.Contains(out, "disabled") {
		t.Fatalf("after 'no banner startup': %q", out)
	}
}

// A configuration with '!' comment lines loads without complaints (the Replay path of startup).
func TestReplayAcceptsBangComments(t *testing.T) {
	r := newTestRouter(t)
	text := "!\n! comment\nhostname bang-test\n!\n ip domain-name bang.test\n!\nend\n"
	if errs := r.Replay(text); len(errs) != 0 {
		t.Fatalf("errors on '!' lines: %v", errs)
	}
	if r.Cfg.Hostname != "bang-test" {
		t.Fatalf("hostname not applied: %q", r.Cfg.Hostname)
	}
}
