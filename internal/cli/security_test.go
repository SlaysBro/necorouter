package cli

import (
	"strings"
	"testing"

	"necorouter/internal/config"
)

// Admin-only commands, listed explicitly: the handlers of these commands no longer check the role
// themselves, so the command must be closed at its node. For every other role the command must be
// refused as an invalid input before any handler runs (nothing here runs for a role that may use it).
var adminOnlyCommands = []struct {
	mode string
	line string
}{
	{modeExec, "show users"},
	{modeExec, "terminal monitor"},
	{modePriv, "show startup-config"},
	{modePriv, "show log"},
	{modePriv, "show config"},
	{modePriv, "show files"},
	{modePriv, "show features"},
	{modePriv, "show ip prefix-list"},
	{modePriv, "terminal monitor"},
	{modePriv, "write"},
	{modePriv, "copy running-config startup-config"},
	{modePriv, "delete config file crash-config"},
	{modePriv, "clear dhcp-server clients"},
	{modePriv, "configure terminal"},
}

func TestAdminOnlyCommandsAreClosedForOtherRoles(t *testing.T) {
	r := newTestRouter(t)
	for _, role := range []string{config.RoleNOC, config.RoleHelpdesk} {
		for _, c := range adminOnlyCommands {
			s := NewSession(r, &config.User{Name: "u-" + role, Role: role})
			var sb strings.Builder
			s.Exec("enable", &sb) // the entry to privileged mode; for helpdesk too
			if c.mode == modeExec {
				s = NewSession(r, &config.User{Name: "u-" + role, Role: role})
			}
			enableServices(s, "config-features") // the feature is on, so only the role can close the command
			out := runCmd(s, c.line)
			if !strings.Contains(out, "% Invalid input") {
				t.Errorf("role %s %s: %q must be closed, got %q", role, c.mode, c.line, out)
			}
		}
	}
}

// The admin keeps every one of them: none is refused to the admin (the same commands, in privileged mode).
func TestAdminOnlyCommandsAreNotRefusedToAdmin(t *testing.T) {
	r := newTestRouter(t)
	for _, c := range adminOnlyCommands {
		s := NewSession(r, &config.User{Name: "root", Role: config.RoleAdmin})
		enableServices(s, "config-features") // the commands that need a feature are checked with it on
		runCmd(s, "end")
		if c.mode == modeExec {
			s = NewSession(r, &config.User{Name: "root", Role: config.RoleAdmin})
		} else {
			runCmd(s, "exit")
			runCmd(s, "enable")
		}
		out := runCmd(s, c.line)
		if strings.Contains(out, "access denied") || strings.Contains(out, "% Invalid input") {
			t.Errorf("admin: %q is refused: %q", c.line, out)
		}
	}
}

// noc may read the running configuration (table 128); the keys and password hashes are hidden for it, and
// admin still sees them.
func TestRunningConfigHidesSecretsForNoc(t *testing.T) {
	r := newTestRouter(t)
	admin := NewSession(r, &config.User{Name: "root", Role: config.RoleAdmin})
	runCmd(admin, "enable")
	runCmd(admin, "configure terminal")
	runCmd(admin, "interface lan")
	runCmd(admin, "ip ospf message-digest-key 1 md5 sekrit-key")
	runCmd(admin, "end")
	noc := NewSession(r, &config.User{Name: "n", Role: config.RoleNOC})
	out := runCmd(noc, "show running-config")
	if strings.Contains(out, "sekrit-key") || !strings.Contains(out, "*****") {
		t.Fatalf("noc sees the key or no mask: %q", out)
	}
	if out := runCmd(admin, "show running-config"); !strings.Contains(out, "sekrit-key") {
		t.Fatalf("admin does not see the configuration: %q", out)
	}
}
