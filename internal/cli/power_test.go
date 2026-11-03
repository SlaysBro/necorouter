package cli

import (
	"os"
	"strings"
	"testing"

	"necorouter/internal/config"
)

// recordPower replaces the system reboot: tests never run a real reboot.
func recordPower(r *Router) *[]string {
	var calls []string
	r.power = func(kind string) error {
		calls = append(calls, kind)
		return nil
	}
	return &calls
}

func TestReloadWarnsConfirmsAndAnswersNo(t *testing.T) {
	r := newTestRouter(t)
	calls := recordPower(r)
	s := sessionOn(r, "root")
	runCmd(s, "write")

	runCmd(s, "configure terminal")
	runCmd(s, "hostname power-test")
	runCmd(s, "end")

	out := runCmd(s, "reload")
	if !strings.Contains(out, "Unsaved changes") || !strings.Contains(s.Prompt(), "reboot system?") {
		t.Fatalf("reload must warn about unsaved changes and ask: out=%q prompt=%q", out, s.Prompt())
	}
	runCmd(s, "n")
	if len(*calls) != 0 {
		t.Fatalf("answer n must not reboot: %v", *calls)
	}

	runCmd(s, "reload")
	runCmd(s, "y")
	if len(*calls) != 1 || (*calls)[0] != "reboot" {
		t.Fatalf("reload y must call reboot once: %v", *calls)
	}
}

func TestReloadNoWarningWhenSaved(t *testing.T) {
	r := newTestRouter(t)
	recordPower(r)
	s := sessionOn(r, "root")
	runCmd(s, "write")
	if out := runCmd(s, "reload"); strings.Contains(out, "Unsaved") {
		t.Fatalf("saved config must not warn: %q", out)
	}
}

func TestReloadFromConfigModeViaDo(t *testing.T) {
	r := newTestRouter(t)
	recordPower(r)
	s := sessionOn(r, "root")
	runCmd(s, "configure terminal")
	runCmd(s, "do reload")
	if !strings.Contains(s.Prompt(), "reboot system?") {
		t.Fatalf("do reload must ask for confirmation: prompt=%q", s.Prompt())
	}
}

func TestReloadInAndCancel(t *testing.T) {
	r := newTestRouter(t)
	recordPower(r)
	s := sessionOn(r, "root")
	runCmd(s, "write")
	runCmd(s, "reload in 5")
	runCmd(s, "y")
	if r.reloadTimer == nil {
		t.Fatal("reload in 5 must schedule a timer")
	}
	if out := runCmd(s, "reload cancel"); !strings.Contains(out, "canceled") {
		t.Fatalf("cancel: %q", out)
	}
	if r.reloadTimer != nil {
		t.Fatal("cancel must clear the timer")
	}
	if out := runCmd(s, "reload cancel"); !strings.Contains(out, "No reload") {
		t.Fatalf("second cancel: %q", out)
	}
}

func TestReloadInRange(t *testing.T) {
	r := newTestRouter(t)
	recordPower(r)
	s := sessionOn(r, "root")
	if out := runCmd(s, "reload in 0"); !strings.Contains(out, "%") {
		t.Fatalf("reload in 0 must be rejected: %q", out)
	}
	if out := runCmd(s, "reload in 601"); !strings.Contains(out, "%") {
		t.Fatalf("reload in 601 must be rejected: %q", out)
	}
}

func TestReloadAt(t *testing.T) {
	r := newTestRouter(t)
	recordPower(r)
	s := sessionOn(r, "root")
	runCmd(s, "write")
	runCmd(s, "reload at 25:00")
	if r.reloadTimer != nil {
		t.Fatal("bad time must not schedule anything")
	}
	runCmd(s, "reload at 23:59")
	runCmd(s, "y")
	if r.reloadTimer == nil {
		t.Fatal("reload at must schedule a timer")
	}
	runCmd(s, "reload cancel")
}

func TestPoweroff(t *testing.T) {
	r := newTestRouter(t)
	calls := recordPower(r)
	s := sessionOn(r, "root")
	runCmd(s, "write")
	runCmd(s, "poweroff")
	runCmd(s, "y")
	if len(*calls) != 1 || (*calls)[0] != "poweroff" {
		t.Fatalf("poweroff y must call poweroff: %v", *calls)
	}
}

func TestCopyEmptyConfigWritesDefaultsAndKeepsRunning(t *testing.T) {
	r := newTestRouter(t)
	recordPower(r)
	s := sessionOn(r, "root")
	runCmd(s, "configure terminal")
	runCmd(s, "hostname keep-me")
	runCmd(s, "end")
	runCmd(s, "copy empty-config startup-config")
	runCmd(s, "y")

	b, err := os.ReadFile(r.P.Startup())
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != config.New().Render() {
		t.Fatalf("startup must hold factory defaults, got:\n%s", b)
	}
	if r.Cfg.Hostname == config.New().Hostname {
		t.Fatal("running-config must stay unchanged until reload")
	}
}

func TestPowerNeverRunsDuringReplay(t *testing.T) {
	r := newTestRouter(t)
	calls := recordPower(r)
	s := NewSession(r, &config.User{Name: "root", Role: "admin"})
	s.noApply = true
	for _, line := range []string{"reload", "poweroff", "reload in 1", "copy empty-config startup-config"} {
		s.Exec(line, &strings.Builder{})
	}
	if len(*calls) != 0 || r.reloadTimer != nil {
		t.Fatalf("replay must not reboot or schedule: %v", *calls)
	}
}
