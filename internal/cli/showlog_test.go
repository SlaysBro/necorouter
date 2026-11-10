package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"necorouter/internal/config"
	"necorouter/internal/logx"
)

func logSetup(t *testing.T) (*Router, *Session) {
	t.Helper()
	r := newTestRouter(t)
	l, err := logx.Open(r.P.LogDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	logx.SetDefault(l)
	t.Cleanup(func() { logx.SetDefault(nil); _ = l.Close() })
	logx.Infof("DHCP", "alpha one")
	logx.Logf(0, logx.LevelWarn, "OSPF", "beta two")
	logx.Logf(0, logx.LevelError, "NAT", "gamma three")
	return r, sessionOn(r, "root")
}

func TestShowLogParams(t *testing.T) {
	_, s := logSetup(t)
	out := runCmd(s, "show log")
	mustContain(t, out, "[info] [DHCP] alpha one", "[warn] [OSPF] beta two", "[error] [NAT] gamma three")
	mustNotContain(t, out, "showlog_test.go")
	mustContain(t, runCmd(s, "show log excessive"), "[showlog_test.go:")
	two := runCmd(s, "show log lines 6") // the last entry is the audit of the command itself
	mustNotContain(t, two, "alpha one")
	mustContain(t, two, "beta two", "gamma three", "AUDIT [root] show log lines 6")
	rev := runCmd(s, "show log reverse")
	if strings.Index(rev, "gamma") > strings.Index(rev, "alpha") {
		t.Fatalf("not reversed:\n%s", rev)
	}
	mustContain(t, runCmd(s, "show log excessive lines 1 reverse"), "AUDIT [root] show log excessive lines 1 reverse")
	mustContain(t, runCmd(s, "show log lines 1 follow reverse"), "% ")
}

func TestShowLogFollow(t *testing.T) {
	_, s := logSetup(t)
	var sb strings.Builder
	done := make(chan struct{})
	go func() { s.Exec("show log lines 6 follow", &lockedWriter{b: &sb}); close(done) }()
	time.Sleep(500 * time.Millisecond)
	logx.Infof("TEST", "fresh entry")
	time.Sleep(700 * time.Millisecond)
	s.Interrupt()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("follow did not stop")
	}
	out := (&lockedWriter{b: &sb}).String()
	mustContain(t, out, "gamma three", "fresh entry")
}

type lockedWriter struct{ b *strings.Builder }

var lwMu = make(chan struct{}, 1)

func (l *lockedWriter) Write(p []byte) (int, error) {
	lwMu <- struct{}{}
	defer func() { <-lwMu }()
	return l.b.Write(p)
}
func (l *lockedWriter) String() string {
	lwMu <- struct{}{}
	defer func() { <-lwMu }()
	return l.b.String()
}

func TestShowLogModifiers(t *testing.T) {
	r, s := logSetup(t)
	out := runCmd(s, "show log | include beta")
	mustContain(t, out, "beta two")
	mustNotContain(t, out, "alpha", "gamma")
	mustNotContain(t, runCmd(s, "show log | exclude beta"), "beta")
	g := runCmd(s, `show log | grep "(alpha|gamma)"`)
	mustContain(t, g, "[DHCP] alpha", "[NAT] gamma")
	mustNotContain(t, g, "[OSPF]")
	b := runCmd(s, "show log | begin beta")
	mustContain(t, b, "[OSPF] beta", "[NAT] gamma")
	mustNotContain(t, b, "[DHCP]")
	mustContain(t, runCmd(s, "show log | nopager | include alpha"), "alpha")
	mustContain(t, runCmd(s, "show log | bogus x"), "% ")
	mustContain(t, runCmd(s, "show log | include"), "% ")
	// output redirection is not offered (EcoRouter has none): refused, and no file is written
	mustContain(t, runCmd(s, "show log | include alpha | redirect a.log"), "Unrecognized modifier")
	mustContain(t, runCmd(s, "show log > b.log"), "Unrecognized modifier")
	if _, err := os.Stat(filepath.Join(r.P.Var, "files", "b.log")); err == nil {
		t.Fatal("a file was written by show log")
	}
	// other commands are not affected
	mustNotContain(t, runCmd(s, "show version | include Hostname"), "% ")
	// not admin: the log is not offered to noc at all
	u := NewSession(r, &config.User{Name: "noc1", Role: "noc"})
	mustContain(t, runCmd(u, "show log"), "% Invalid input")
}

func TestAuditMasksSecrets(t *testing.T) {
	r, s := logSetup(t)
	runCmd(s, "configure terminal")
	runCmd(s, "enable password Abc12345")
	runCmd(s, "hostname r1")
	runCmd(s, "end")
	out := runCmd(s, "show log | include AUDIT")
	mustContain(t, out, "[IMISH] AUDIT [root] hostname r1", "AUDIT [root] enable password ***")
	mustNotContain(t, out, "Abc12345")
	_ = r
	if h := s.Help("show log | "); strings.Contains(h, "redirect") {
		t.Fatalf("redirect is offered: %q", h)
	}
}

// show log all — all saved entries, including those before boot; without all — only since boot.
func TestShowLogAllIncludesOlder(t *testing.T) {
	// the boot time is fixed here: /proc/uptime differs on every host (it is not readable on some)
	restore := logx.BootTimeFunc
	logx.BootTimeFunc = func() time.Time { return time.Now().Add(-time.Hour) }
	t.Cleanup(func() { logx.BootTimeFunc = restore })
	r, s := logSetup(t)
	old := "2001-01-02 03:04:05.000000 [info] [OLD]\t-\tancient entry\n"
	f := filepath.Join(r.P.LogDir(), "necorouter.log")
	cur, _ := os.ReadFile(f)
	if err := os.WriteFile(f, append([]byte(old), cur...), 0o640); err != nil {
		t.Fatal(err)
	}
	if out := runCmd(s, "show log all"); !strings.Contains(out, "ancient entry") || !strings.Contains(out, "alpha one") {
		t.Fatalf("show log all: %q", out)
	}
	if out := runCmd(s, "show log"); strings.Contains(out, "ancient entry") {
		t.Fatalf("show log must hide entries before boot")
	}
	// when the boot time is unknown, nothing is hidden (the fallback)
	logx.BootTimeFunc = func() time.Time { return time.Time{} }
	if out := runCmd(s, "show log"); !strings.Contains(out, "ancient entry") {
		t.Fatalf("with an unknown boot time show log must show everything: %q", out)
	}
}
