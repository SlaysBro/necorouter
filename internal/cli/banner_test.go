package cli

import (
	"strings"
	"testing"
)

func TestBannerMotd(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")

	// by default — a string with the version
	if m := r.Motd(); !strings.HasPrefix(m, "necorouter version ") {
		t.Fatalf("default motd: %q", m)
	}
	if out := runCmd(s, "show banner motd"); !strings.Contains(out, "necorouter version") {
		t.Fatalf("show default: %q", out)
	}
	if strings.Contains(r.Cfg.Render(), "banner motd") {
		t.Fatal("the default motd must not be written to the configuration")
	}

	runCmd(s, "configure terminal")
	if out := runCmd(s, "banner motd Hello, World!!!"); strings.Contains(out, "%") {
		t.Fatalf("banner motd rejected: %q", out)
	}
	if r.Motd() != "Hello, World!!!" {
		t.Fatalf("custom motd: %q", r.Motd())
	}
	rc := r.Cfg.Render()
	if !strings.Contains(rc, "banner motd Hello, World!!!") {
		t.Fatalf("running-config lacks the banner:\n%s", rc)
	}
	if cfg, errs := r.validateConfig(rc); len(errs) > 0 || cfg.BannerMotd != "Hello, World!!!" {
		t.Fatalf("replay lost the banner: %v %q", errs, cfg.BannerMotd)
	}
	runCmd(s, "end")
	if out := runCmd(s, "show banner motd"); strings.TrimSpace(out) != "Hello, World!!!" {
		t.Fatalf("show custom: %q", out)
	}

	// no banner motd — no greeting, and this is recorded in the configuration
	runCmd(s, "configure terminal")
	runCmd(s, "no banner motd")
	if r.Motd() != "" {
		t.Fatalf("motd must be empty: %q", r.Motd())
	}
	rc = r.Cfg.Render()
	if !strings.Contains(rc, "no banner motd") {
		t.Fatalf("running-config lacks 'no banner motd':\n%s", rc)
	}
	if cfg, errs := r.validateConfig(rc); len(errs) > 0 || !cfg.BannerOff {
		t.Fatalf("replay lost 'no banner motd': %v", errs)
	}
	runCmd(s, "end")
	if out := runCmd(s, "show banner motd"); strings.TrimSpace(out) != "" {
		t.Fatalf("show must be empty: %q", out)
	}

	// default returns the default greeting
	runCmd(s, "configure terminal")
	runCmd(s, "banner motd default")
	if !strings.HasPrefix(r.Motd(), "necorouter version ") || strings.Contains(r.Cfg.Render(), "banner motd") {
		t.Fatalf("banner motd default: %q", r.Motd())
	}

	// text that is too long is rejected; quotes are removed by string parsing and do not get into the configuration
	if out := runCmd(s, "banner motd "+strings.Repeat("x", maxMotd+1)); !strings.Contains(out, "%") {
		t.Errorf("a too long message must be rejected: %q", out)
	}
	runCmd(s, `banner motd say "hi there"`)
	if r.Motd() != "say hi there" {
		t.Errorf("quotes must be consumed by the parser: %q", r.Motd())
	}
}
