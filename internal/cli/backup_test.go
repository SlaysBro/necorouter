package cli

import (
	"log"
	"os"
	"strings"
	"testing"

	"necorouter/internal/svc"
)

func backupRouter(t *testing.T, dir string) *Router {
	t.Helper()
	p := svc.Paths{Etc: dir + "/etc", Var: dir + "/var", Run: dir + "/run"}
	return NewRouter(svc.NewManager(p, log.New(os.Stderr, "", 0)), p)
}

// A clean exit leaves no marker, and no crash copy is created.
func TestCleanShutdownLeavesNoCrashBackup(t *testing.T) {
	dir := t.TempDir()
	r := backupRouter(t, dir)
	if err := os.MkdirAll(dir+"/var", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkRunning(); err != nil {
		t.Fatal(err)
	}
	r.Cfg.Hostname = "clean-host"
	if _, err := r.syncFile(r.P.RunSnap(), ""); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkClean(); err != nil {
		t.Fatal(err)
	}
	next := backupRouter(t, dir)
	crashed, err := next.RecoverCrash()
	if err != nil || crashed {
		t.Fatalf("clean exit reported as crash: crashed=%v err=%v", crashed, err)
	}
	if _, err := os.Stat(next.P.Crash()); err == nil {
		t.Fatal("crash-config written after clean exit")
	}
}

// An abnormal exit (marker left behind) copies the last snapshot to crash-config,
// without touching startup-config.
func TestCrashKeepsLastSnapshotInSeparateFile(t *testing.T) {
	dir := t.TempDir()
	r := backupRouter(t, dir)
	if err := os.MkdirAll(dir+"/var", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir+"/etc", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.P.Startup(), []byte("hostname startup\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkRunning(); err != nil {
		t.Fatal(err)
	}
	r.Cfg.Hostname = "crashed-host"
	if _, err := r.syncFile(r.P.RunSnap(), ""); err != nil {
		t.Fatal(err)
	}
	// marker not removed: simulates SIGKILL
	next := backupRouter(t, dir)
	crashed, err := next.RecoverCrash()
	if err != nil || !crashed || !next.Crashed() {
		t.Fatalf("crash not detected: crashed=%v err=%v", crashed, err)
	}
	crash, err := os.ReadFile(next.P.Crash())
	if err != nil {
		t.Fatal(err)
	}
	if string(crash) == "" || !strings.Contains(string(crash), "hostname crashed-host") {
		t.Fatalf("crash-config does not hold the last running config:\n%s", crash)
	}
	st, _ := os.ReadFile(next.P.Startup())
	if string(st) != "hostname startup\n" {
		t.Fatalf("startup-config changed by crash recovery: %q", st)
	}
}

// Autosave writes a separate file and only on change.
func TestSyncFileWritesOnlyOnChange(t *testing.T) {
	dir := t.TempDir()
	r := backupRouter(t, dir)
	if err := os.MkdirAll(dir+"/etc", 0o755); err != nil {
		t.Fatal(err)
	}
	fp1, err := r.syncFile(r.P.RunSnap(), "")
	if err != nil || fp1 == "" {
		t.Fatalf("first sync: fp=%q err=%v", fp1, err)
	}
	if err := os.Remove(r.P.RunSnap()); err != nil {
		t.Fatal(err)
	}
	fp2, _ := r.syncFile(r.P.RunSnap(), fp1)
	if fp2 != fp1 {
		t.Fatal("unchanged config changed fingerprint")
	}
	if _, err := os.Stat(r.P.RunSnap()); err == nil {
		t.Fatal("snapshot rewritten without changes")
	}
	r.Cfg.Hostname = "changed"
	if _, err := r.syncFile(r.P.RunSnap(), fp1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(r.P.RunSnap()); err != nil {
		t.Fatal("snapshot not written after change")
	}
	if _, err := os.Stat(r.P.Startup()); err == nil {
		t.Fatal("snapshot touched startup-config")
	}
}

// autosave: off by default; commands turn it on and off; the text is saved and read back.
func TestAutosaveCommandAndRender(t *testing.T) {
	r := newTestRouter(t)
	if r.Cfg.AutosaveOn || strings.Contains(r.Cfg.Render(), "autosave") {
		t.Fatal("autosave must be off by default and not written to the configuration")
	}
	if errs := r.Replay("autosave interval 7\n"); len(errs) > 0 {
		t.Fatalf("autosave interval: %v", errs)
	}
	if r.Cfg.AutosaveMin != 7 || !strings.Contains(r.Cfg.Render(), "autosave interval 7") {
		t.Fatalf("period not applied or not rendered: %d", r.Cfg.AutosaveMin)
	}
	if errs := r.Replay("no autosave\n"); len(errs) > 0 || r.Cfg.AutosaveOn || !strings.Contains(r.Cfg.Render(), "no autosave") {
		t.Fatalf("no autosave did not disable: %v", errs)
	}
	if errs := r.Replay("autosave interval 9\n"); len(errs) > 0 || !r.Cfg.AutosaveOn {
		t.Fatalf("autosave interval must turn autosave back on: %v", errs)
	}
}
