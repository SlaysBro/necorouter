package cli

import (
	"os"
	"strings"
	"testing"

	"necorouter/internal/svc"
)

// Tab on an empty line does nothing: no command list.
func TestTabOnEmptyLineGivesNothing(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	runCmd(s, "configure terminal")
	for _, line := range []string{"", "   "} {
		if got, opts := s.Complete(line); got != line || len(opts) != 0 {
			t.Fatalf("Tab on %q: line=%q opts=%v", line, got, opts)
		}
	}
	// a partial word still completes
	if got, _ := s.Complete("us"); got != "username " {
		t.Fatalf("Tab on 'us' must still complete: %q", got)
	}
}

// User names are offered by Tab, but '?' shows only the NAME hint.
func TestUsernamesOnlyInTab(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	runCmd(s, "configure terminal")
	runCmd(s, "username bob")
	runCmd(s, "exit")
	h := s.Help("username ")
	if strings.Contains(h, "admin") || strings.Contains(h, "bob") {
		t.Fatalf("'?' lists user names: %q", h)
	}
	if !strings.Contains(h, "NAME") {
		t.Fatalf("'?' lost the NAME hint: %q", h)
	}
	if _, opts := s.Complete("username "); len(opts) != 2 || opts[0] != "admin" || opts[1] != "bob" {
		t.Fatalf("Tab does not offer user names: %v", opts)
	}
}

// clear dhcp-server clients drops every lease and reports how many.
func TestClearDHCPClients(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	if h := runCmd(s, "clear dhcp-server clients"); !strings.Contains(h, "[OK] 0 lease(s) cleared") {
		t.Fatalf("no leases: %q", h)
	}
	if err := os.MkdirAll(r.P.Var, 0o755); err != nil {
		t.Fatal(err)
	}
	leases := "1900000000 00:11:22:33:44:55 10.0.0.5 host-a *\n1900000100 00:11:22:33:44:66 10.0.0.6 host-b *\n"
	if err := os.WriteFile(svc.LeaseFile(r.P), []byte(leases), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := runCmd(s, "show dhcp-server clients"); !strings.Contains(out, "10.0.0.5") {
		t.Fatalf("lease not shown before clearing: %q", out)
	}
	if out := runCmd(s, "clear dhcp-server clients"); !strings.Contains(out, "[OK] 2 lease(s) cleared") {
		t.Fatalf("clear: %q", out)
	}
	if b, _ := os.ReadFile(svc.LeaseFile(r.P)); len(b) != 0 {
		t.Fatalf("lease file not emptied: %q", b)
	}
	if out := runCmd(s, "show dhcp-server clients"); strings.Contains(out, "10.0.0.5") {
		t.Fatalf("lease still shown after clearing: %q", out)
	}
	// clear alone is still the screen clear; a partial command is rejected
	if out := runCmd(s, "clear dhcp-server"); !strings.Contains(out, "%") {
		t.Fatalf("incomplete command accepted: %q", out)
	}
	if h := s.Help("clear "); !strings.Contains(h, "dhcp-server") {
		t.Fatalf("'?' after clear lacks dhcp-server: %q", h)
	}
}

// After 'copy file' only saved files and the two real targets are offered: no crash, no startup aliases.
func TestCopyFileOffersNoDuplicates(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	enableServices(s, "config-features")
	runCmd(s, "write file test")
	h := s.Help("copy file ")
	for _, bad := range []string{"crash", "startup ", "config "} {
		if strings.Contains(h, bad) {
			t.Fatalf("'?' after 'copy file' lists %q:\n%s", bad, h)
		}
	}
	if !strings.Contains(h, "test") {
		t.Fatalf("saved file missing: %s", h)
	}
	h = s.Help("copy file test ")
	if strings.Contains(h, "startup ") || strings.Contains(h, "crash") || !strings.Contains(h, "startup-config") || !strings.Contains(h, "running-config") {
		t.Fatalf("'?' after 'copy file test' must list only startup-config and running-config:\n%s", h)
	}
	if got, _ := s.Complete("copy file "); got != "copy file test " {
		t.Fatalf("Tab after 'copy file': %q", got)
	}
	// delete config file: crash-config is listed once
	h = s.Help("delete config file ")
	if strings.Count(h, "crash-config") != 1 || strings.Contains(h, "  crash ") {
		t.Fatalf("delete config file duplicates:\n%s", h)
	}
}
