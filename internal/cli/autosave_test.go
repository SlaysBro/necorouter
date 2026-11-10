package cli

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"necorouter/internal/config"
)

// copy empty-config running-config: available to admin without any feature, asks first, replaces the
// running-config with factory defaults and applies it; the lockout protection of loadRunning still applies.
func TestEmptyConfigRunning(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	if h := s.Help("copy empty-config "); !strings.Contains(h, "running-config") {
		t.Fatalf("the hint does not offer running-config: %q", h)
	}
	runCmd(s, "configure terminal")
	runCmd(s, "hostname alpha")
	runCmd(s, "end")
	out := runCmd(s, "copy empty-config running-config")
	if !strings.Contains(out, "factory defaults") || s.Prompt() != "Continue? [y/N] " {
		t.Fatalf("no confirmation: %q", out)
	}
	if out := runCmd(s, "n"); !strings.Contains(out, "Aborted") || r.Cfg.Hostname != "alpha" {
		t.Fatalf("abort changed the configuration: %q host=%s", out, r.Cfg.Hostname)
	}
	runCmd(s, "copy empty-config running-config")
	if out := runCmd(s, "y"); !strings.Contains(out, "[OK]") {
		t.Fatalf("not applied: %q", out)
	}
	if r.Cfg.Hostname != config.DefaultHostname {
		t.Fatalf("hostname after factory reset: %q", r.Cfg.Hostname)
	}
	if !strings.Contains(runCmd(s, "show running-config"), "hostname "+config.DefaultHostname) {
		t.Fatal("show running-config does not show the factory configuration")
	}
	// the startup-config is not changed by the running-config reset
	if out := runCmd(s, "copy empty-config running-config"); strings.Contains(out, "startup") {
		t.Fatalf("unexpected: %q", out)
	}
	runCmd(s, "y")
}

// An administrator who is not part of the factory configuration cannot reset running-config from under themselves.
func TestEmptyConfigRunningKeepsCaller(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	enableServices(s, "config-features")
	runCmd(s, "configure terminal")
	runCmd(s, "username ivan")
	runCmd(s, "role admin")
	runCmd(s, "end")
	ivan := sessionOn(r, "ivan")
	ivan.Role = "admin"
	runCmd(ivan, "copy empty-config running-config")
	out := runCmd(ivan, "y")
	if strings.Contains(out, "[OK]") || !strings.Contains(out, "refusing") {
		t.Fatalf("reset must be refused for a caller outside the factory configuration: %q", out)
	}
	if r.Cfg.FindUser("ivan") == nil {
		t.Fatal("user ivan removed")
	}
}

// Autosave management: interval and keep, the list, a copy's content, deleting one copy, and turning it off.
func TestAutosaveManagement(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	enableServices(s, "config-features")
	runCmd(s, "configure terminal")
	if out := runCmd(s, "autosave interval 5 keep 3"); strings.Contains(out, "%") {
		t.Fatalf("interval rejected: %q", out)
	}
	if !r.Cfg.AutosaveOn || r.Cfg.AutosaveMin != 5 || r.Cfg.AutosaveKeep != 3 {
		t.Fatalf("autosave settings: on=%v min=%d keep=%d", r.Cfg.AutosaveOn, r.Cfg.AutosaveMin, r.Cfg.AutosaveKeep)
	}
	runCmd(s, "autosave keep 2")
	if r.Cfg.AutosaveKeep != 2 {
		t.Fatalf("keep not changed: %d", r.Cfg.AutosaveKeep)
	}
	runCmd(s, "end")
	for i := 0; i < 4; i++ {
		runCmd(s, "configure terminal")
		runCmd(s, fmt.Sprintf("hostname h%d", i))
		runCmd(s, "end")
		if err := r.autosaveOnce(r.Cfg.AutosaveKeep); err != nil {
			t.Fatal(err)
		}
		time.Sleep(1100 * time.Millisecond)
	}
	ents, _ := os.ReadDir(r.P.AutosaveDir())
	if len(ents) != 2 {
		t.Fatalf("keep 2 must leave 2 copies, got %d", len(ents))
	}
	list := runCmd(s, "show config history")
	if strings.Contains(list, " ! ") || strings.Contains(list, "  !\n") {
		t.Fatalf("the comment line is shown as a description:\n%s", list)
	}
	name := strings.TrimSuffix(ents[0].Name(), ".conf")
	if out := runCmd(s, "show config file "+name); !strings.Contains(out, "hostname") {
		t.Fatalf("copy content: %q", out)
	}
	// one copy goes, the other stays
	runCmd(s, "delete config file "+name)
	if out := runCmd(s, "y"); !strings.Contains(out, "[OK]") {
		t.Fatalf("delete: %q", out)
	}
	if left, _ := os.ReadDir(r.P.AutosaveDir()); len(left) != 1 {
		t.Fatalf("after delete: %d copies", len(left))
	}
	// turning autosave off keeps the copies
	runCmd(s, "configure terminal")
	runCmd(s, "no autosave")
	if r.Cfg.AutosaveOn {
		t.Fatal("no autosave must turn it off")
	}
	if left, _ := os.ReadDir(r.P.AutosaveDir()); len(left) != 1 {
		t.Fatal("no autosave must not delete copies")
	}
}
