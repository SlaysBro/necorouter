package cli

import (
	"os"
	"strings"
	"testing"
	"time"

	"necorouter/internal/config"
)

// sessionOn: an administrator session in EXEC mode with privileges.
func sessionOn(r *Router, user string) *Session {
	s := NewSession(r, &config.User{Name: user, Role: "admin"})
	var sb strings.Builder
	s.Exec("enable", &sb)
	return s
}

func runCmd(s *Session, line string) string {
	var sb strings.Builder
	s.Exec(line, &sb)
	return sb.String()
}

func enableServices(s *Session, names ...string) {
	runCmd(s, "configure terminal")
	for _, n := range names {
		runCmd(s, "feature "+n)
	}
	runCmd(s, "end")
}

// write file, show files config, show config file; overwriting only after confirmation.
func TestWriteFileListShowAndOverwriteConfirm(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	if out := runCmd(s, "write file lab1"); !strings.Contains(out, "[OK]") {
		t.Fatalf("write file failed: %q", out)
	}
	if out := runCmd(s, "show files config"); !strings.Contains(out, "lab1") {
		t.Fatalf("lab1 missing from listing: %q", out)
	}
	if out := runCmd(s, "show config file lab1"); !strings.Contains(out, "hostname") {
		t.Fatalf("show config file: %q", out)
	}
	runCmd(s, "write file lab1")
	if p := s.Prompt(); p != "Continue? [y/N] " {
		t.Fatalf("overwrite must ask for confirmation, prompt=%q", p)
	}
	if out := runCmd(s, "n"); !strings.Contains(out, "Aborted") {
		t.Fatalf("answer n must abort: %q", out)
	}
	if strings.HasSuffix(s.Prompt(), "[y/N] ") {
		t.Fatal("pending question not cleared")
	}
	for _, bad := range []string{"startup", "startup-config", "crash", "running", "startup-x", "autosave-1", "../x", "a b"} {
		if out := runCmd(s, "write file "+bad); strings.Contains(out, "[OK]") {
			t.Fatalf("reserved or bad name %q accepted: %q", bad, out)
		}
	}
}

// copy file ... startup-config: warning and confirmation; a file with errors is not accepted;
// running-config does not change; the old startup goes to history (config-history).
func TestCopyFileToStartup(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	enableServices(s, "config-history")
	runCmd(s, "configure terminal")
	runCmd(s, "hostname alpha")
	runCmd(s, "end")
	runCmd(s, "write file alpha")
	runCmd(s, "configure terminal")
	runCmd(s, "hostname beta")
	runCmd(s, "end")
	runCmd(s, "write")

	out := runCmd(s, "copy file alpha startup-config")
	if !strings.Contains(out, "overwritten") || s.Prompt() != "Continue? [y/N] " {
		t.Fatalf("no warning or question: out=%q prompt=%q", out, s.Prompt())
	}
	if out := runCmd(s, "y"); !strings.Contains(out, "[OK]") {
		t.Fatalf("copy not done: %q", out)
	}
	st, _ := os.ReadFile(r.P.Startup())
	if !strings.Contains(string(st), "hostname alpha") {
		t.Fatalf("startup not replaced:\n%s", st)
	}
	if r.Cfg.Hostname != "beta" {
		t.Fatalf("running-config must not change, hostname=%s", r.Cfg.Hostname)
	}
	if n := len(listHistory(r.P.History(), "startup-")); n != 1 {
		t.Fatalf("old startup must go to history, have %d", n)
	}

	// file with errors: refused before confirmation, startup untouched
	if err := os.MkdirAll(r.P.ConfigsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(r.P.ConfigsDir()+"/broken.conf", []byte("hostname gamma\nbogus command here\n"), 0o600)
	out = runCmd(s, "copy file broken startup-config")
	if !strings.Contains(out, "has errors") || strings.HasSuffix(s.Prompt(), "[y/N] ") {
		t.Fatalf("broken file must be refused: %q", out)
	}
	st2, _ := os.ReadFile(r.P.Startup())
	if string(st2) != string(st) {
		t.Fatal("startup changed by a refused copy")
	}
}

// copy ... running-config: only with config-manage; protection against self-lockout.
func TestCopyToRunningGateAndLockoutProtection(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	runCmd(s, "configure terminal")
	runCmd(s, "hostname alpha")
	runCmd(s, "end")
	runCmd(s, "write file alpha")
	runCmd(s, "configure terminal")
	runCmd(s, "hostname beta")
	runCmd(s, "end")

	if out := runCmd(s, "copy file alpha running-config"); !strings.Contains(out, "%") {
		t.Fatalf("copy to running must be hidden before config-manage: %q", out)
	}
	enableServices(s, "config-manage")
	out := runCmd(s, "copy file alpha running-config")
	if !strings.Contains(out, "Unsaved changes will be lost") || s.Prompt() != "Continue? [y/N] " {
		t.Fatalf("no warning: %q prompt=%q", out, s.Prompt())
	}
	if out := runCmd(s, "y"); !strings.Contains(out, "[OK]") {
		t.Fatalf("copy to running failed: %q", out)
	}
	if r.Cfg.Hostname != "alpha" {
		t.Fatalf("running-config not replaced: %s", r.Cfg.Hostname)
	}

	// a file without the current user (neto) is rejected
	_ = os.WriteFile(r.P.ConfigsDir()+"/other.conf", []byte("hostname lock\nusername other\n role admin\n!\nend\n"), 0o600)
	neto := sessionOn(r, "neto")
	out = runCmd(neto, "copy file other running-config")
	runCmd(neto, "y")
	if r.Cfg.Hostname == "lock" {
		t.Fatalf("lockout protection failed: %q", out)
	}
	// a file with no administrator at all is rejected for root too
	_ = os.WriteFile(r.P.ConfigsDir()+"/noadmin.conf", []byte("hostname noadm\nusername viewer\n role noc\n!\nend\n"), 0o600)
	runCmd(s, "copy file noadmin running-config")
	runCmd(s, "y")
	if r.Cfg.Hostname == "noadm" {
		t.Fatal("configuration without admin was loaded")
	}
}

// delete config file, show config diff, journal: only with their own services.
func TestDeleteDiffAndJournal(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	if out := runCmd(s, "delete config file x"); !strings.Contains(out, "%") {
		t.Fatalf("delete must be hidden before config-manage: %q", out)
	}
	if out := runCmd(s, "show config journal"); !strings.Contains(out, "%") {
		t.Fatalf("journal must be hidden before config-journal: %q", out)
	}
	enableServices(s, "config-journal", "config-manage")
	runCmd(s, "configure terminal")
	runCmd(s, "hostname alpha")
	runCmd(s, "end")
	runCmd(s, "write file a")
	runCmd(s, "configure terminal")
	runCmd(s, "hostname beta")
	runCmd(s, "end")
	runCmd(s, "write file b")

	d := runCmd(s, "show config diff a b")
	if !strings.Contains(d, "- hostname alpha") || !strings.Contains(d, "+ hostname beta") {
		t.Fatalf("diff wrong: %q", d)
	}
	if out := runCmd(s, "show config diff a a"); !strings.Contains(out, "identical") {
		t.Fatalf("identical files: %q", out)
	}
	if out := runCmd(s, "show config diff a running"); !strings.Contains(out, "alpha") {
		t.Fatalf("diff with running: %q", out)
	}

	runCmd(s, "delete config file a")
	if out := runCmd(s, "y"); !strings.Contains(out, "deleted") {
		t.Fatalf("delete failed: %q", out)
	}
	if _, err := os.Stat(r.P.ConfigsDir() + "/a.conf"); err == nil {
		t.Fatal("file not deleted")
	}
	if out := runCmd(s, "delete config file startup"); strings.HasSuffix(s.Prompt(), "[y/N] ") || !strings.Contains(out, "cannot be deleted") {
		t.Fatalf("startup-config must not be deletable: %q", out)
	}

	j := runCmd(s, "show config journal")
	for _, want := range []string{"write file a", "write file b", "delete config file a", "feature config-manage on"} {
		if !strings.Contains(j, want) {
			t.Fatalf("journal lacks %q:\n%s", want, j)
		}
	}
}

// Autosave history: only if running differs from startup and from the last copy; keep limits the count.
func TestAutosaveHistory(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	enableServices(s, "config-history")
	runCmd(s, "configure terminal")
	runCmd(s, "hostname one")
	runCmd(s, "end")
	if err := r.autosaveOnce(2); err != nil {
		t.Fatal(err)
	}
	if n := len(listHistory(r.P.AutosaveDir(), "autosave-")); n != 1 {
		t.Fatalf("first autosave missing: %d", n)
	}
	if err := r.autosaveOnce(2); err != nil || len(listHistory(r.P.AutosaveDir(), "autosave-")) != 1 {
		t.Fatal("identical autosave must not be duplicated")
	}
	for _, h := range []string{"two", "three"} {
		runCmd(s, "configure terminal")
		runCmd(s, "hostname "+h)
		runCmd(s, "end")
		if err := r.autosaveOnce(2); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(listHistory(r.P.AutosaveDir(), "autosave-")); n != 2 {
		t.Fatalf("keep 2 not applied: %d", n)
	}
	// after saving, startup equals running: no autocopy needed
	runCmd(s, "write")
	if err := r.autosaveOnce(2); err != nil {
		t.Fatal(err)
	}
	last, _ := os.ReadFile(newestHistory(r.P.AutosaveDir(), "autosave-"))
	if !strings.Contains(string(last), "hostname three") {
		t.Fatalf("newest autosave wrong:\n%s", last)
	}
	if n := len(listHistory(r.P.AutosaveDir(), "autosave-")); n != 2 {
		t.Fatalf("autosave written although running equals startup: %d", n)
	}
}

// write description: the description is stored with the copy and visible in show config history.
func TestWriteDescriptionAndHistoryList(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	if out := runCmd(s, "write description first"); !strings.Contains(out, "%") {
		t.Fatalf("write description must be hidden before config-history: %q", out)
	}
	enableServices(s, "config-history")
	if out := runCmd(s, "write description before ospf"); !strings.Contains(out, "[OK]") {
		t.Fatalf("write description failed: %q", out)
	}
	st, _ := os.ReadFile(r.P.Startup())
	if !strings.HasPrefix(string(st), "! description: before ospf\n") {
		t.Fatalf("description header missing:\n%s", st)
	}
	runCmd(s, "configure terminal")
	runCmd(s, "hostname next")
	runCmd(s, "end")
	runCmd(s, "write")
	if out := runCmd(s, "show config history"); !strings.Contains(out, "before ospf") {
		t.Fatalf("description not shown in history: %q", out)
	}
	if got := normalize("! description: x\nhostname a\n!\n"); got != "hostname a\n" {
		t.Fatalf("normalize keeps comments: %q", got)
	}
}

// Before feature, the new commands are not visible in hints.
func TestNewCommandsHiddenUntilService(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	if out := s.Help("show config "); strings.Contains(out, "diff") || strings.Contains(out, "history") || strings.Contains(out, "journal") {
		t.Fatalf("hidden commands leaked into help:\n%s", out)
	}
	if out := s.Help("copy "); strings.Contains(out, "crash-config") {
		t.Fatalf("crash-config leaked into copy help:\n%s", out)
	}
	if out := s.Help("write "); strings.Contains(out, "description") {
		t.Fatalf("description leaked into write help:\n%s", out)
	}
	enableServices(s, "config-history", "config-manage", "config-journal")
	if out := s.Help("show config "); !strings.Contains(out, "diff") || !strings.Contains(out, "history") || !strings.Contains(out, "journal") {
		t.Fatalf("enabled commands missing from help:\n%s", out)
	}
	if out := s.Help("copy "); !strings.Contains(out, "crash-config") {
		t.Fatalf("crash-config missing after config-manage:\n%s", out)
	}
	// disabling services changes no settings and deletes no files
	runCmd(s, "configure terminal")
	runCmd(s, "startup-history keep 7")
	runCmd(s, "no feature config-history")
	runCmd(s, "end")
	if r.Cfg.StartupKeep != 7 {
		t.Fatalf("keep must survive disabling: %d", r.Cfg.StartupKeep)
	}
}

// startup-config archives: copy startup-config storage, show backups, delete backup storage.
func TestBackupStorage(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	if out := runCmd(s, "copy startup-config storage"); !strings.Contains(out, "startup-config not found") {
		t.Fatalf("no startup-config yet: %q", out)
	}
	runCmd(s, "configure terminal")
	runCmd(s, "hostname alpha")
	runCmd(s, "end")
	runCmd(s, "write")
	out := runCmd(s, "copy startup-config storage description My_first backup")
	if !strings.Contains(out, "startup_backup_alpha_") || !strings.Contains(out, ".tar.gz") {
		t.Fatalf("backup not created: %q", out)
	}
	names := listBackups(r.P.Backups())
	if len(names) != 1 {
		t.Fatalf("want 1 backup, got %v", names)
	}
	st, desc, err := readBackup(r.P.Backups() + "/" + names[0])
	if err != nil || !strings.Contains(st, "hostname alpha") || desc != "My_first backup" {
		t.Fatalf("archive content wrong: %v %q %q", err, st, desc)
	}
	if out := runCmd(s, "show backups"); !strings.Contains(out, names[0]) || !strings.Contains(out, "My_first_backup") {
		t.Fatalf("show backups: %q", out)
	}
	// a corrupted archive is detected by its checksum
	bad := r.P.Backups() + "/startup_backup_x_2020-01-01_00-00-00.tar.gz"
	data, _ := makeBackup("hostname x\n", "", time.Now())
	data[len(data)/2] ^= 0xff
	_ = os.WriteFile(bad, data, 0o600)
	if _, _, err := readBackup(bad); err == nil {
		t.Fatal("damaged archive accepted")
	}
	if out := runCmd(s, "delete backup storage ../../etc/passwd"); !strings.Contains(out, "invalid backup name") {
		t.Fatalf("path accepted: %q", out)
	}
	runCmd(s, "delete backup storage "+names[0])
	runCmd(s, "n")
	if len(listBackups(r.P.Backups())) != 2 {
		t.Fatal("abort must keep backups")
	}
	runCmd(s, "delete backup storage *")
	runCmd(s, "y")
	if len(listBackups(r.P.Backups())) != 0 {
		t.Fatal("delete * left backups")
	}
	// the commands are visible without feature and do not conflict with config-manage
	if out := s.Help("copy startup-config "); !strings.Contains(out, "storage") || strings.Contains(out, "running-config") {
		t.Fatalf("help before service:\n%s", out)
	}
	enableServices(s, "config-manage")
	if out := s.Help("copy startup-config "); !strings.Contains(out, "storage") || !strings.Contains(out, "running-config") {
		t.Fatalf("help after service:\n%s", out)
	}
	if out := runCmd(s, "copy startup-config storage"); !strings.Contains(out, "[OK]") {
		t.Fatalf("storage after config-manage: %q", out)
	}
}
