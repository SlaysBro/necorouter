package cli

import (
	"strings"
	"testing"

	"necorouter/internal/config"
)

// The visibility of a show command in a mode (For) and what its handler allows must agree: a command the
// role can see must not be refused by its handler, and a command it cannot see must be rejected as an
// invalid input. Show commands only read state, so running them here is safe.
func TestShowVisibilityAgreesWithHandlers(t *testing.T) {
	r := newTestRouter(t)
	for _, role := range config.Roles {
		for _, mode := range []string{modeExec, modePriv} {
			s := NewSession(r, &config.User{Name: "u-" + role, Role: role})
			if mode == modePriv {
				if out := runCmd(s, "enable"); strings.Contains(out, "%") {
					continue // the role may not enter privileged mode: nothing to compare
				}
			}
			for _, k := range buildShow().Kids {
				if k.Hidden {
					continue
				}
				visible := k.visibleIn(mode, role)
				out := runCmd(s, "show "+k.Word)
				refused := strings.Contains(out, "access denied")
				invalid := strings.Contains(out, "% Invalid input")
				switch {
				case visible && (refused || invalid):
					t.Errorf("role %s %s: show %s is offered but refused: %q", role, mode, k.Word, out)
				case !visible && !invalid:
					t.Errorf("role %s %s: show %s is not offered but was accepted: %q", role, mode, k.Word, out)
				}
			}
		}
	}
}

// Privileged-mode commands: visibility and handlers must agree for the roles that are not admin. Each
// command runs in a fresh session. Only the commands that the role is offered are run (for the other
// roles they are refused before any handler runs), so nothing on the device is changed: reload, write,
// copy and the other admin commands are checked for visibility only.
// probeArgs: a safe argument for the privileged commands that take one and are checked by running them.
var probeArgs = map[string]string{"ping": "127.0.0.1", "traceroute": "127.0.0.1", "terminal": "length 24"}

func TestPrivilegedVisibilityAgreesWithHandlersForNonAdmin(t *testing.T) {
	// The host ping and traceroute must not run here: ping without a count runs until it is stopped, so a
	// real run would hang the test. With an empty PATH the handler fails at once with "not installed", which
	// still proves that the command is not refused.
	t.Setenv("PATH", t.TempDir())
	r := newTestRouter(t)
	executed := 0
	for _, role := range []string{config.RoleNOC, config.RoleHelpdesk} {
		for _, n := range trees[modePriv] {
			if n.Hidden || (n.Arg != 0) {
				continue
			}
			fresh := func() *Session {
				s := NewSession(r, &config.User{Name: "u-" + role, Role: role})
				var sb strings.Builder
				s.Exec("enable", &sb)
				return s
			}
			probe := fresh()
			if probe.top().kind != modePriv {
				break // the role may not enter privileged mode
			}
			visible := n.visibleIn(modePriv, role) && (n.Gate == nil || n.Gate(probe))
			line := n.Word
			if probe, ok := probeArgs[n.Word]; ok {
				line += " " + probe // the commands that take a host: the loopback address, the only one they may reach here
			}
			out := runCmd(fresh(), line)
			if visible {
				executed++
			}
			refused := strings.Contains(out, "access denied")
			invalid := strings.Contains(out, "% Invalid input")
			switch {
			case visible && (refused || invalid):
				t.Errorf("role %s priv: %s is offered but refused: %q", role, n.Word, out)
			case !visible && !invalid:
				t.Errorf("role %s priv: %s is not offered but was accepted: %q", role, n.Word, out)
			}
		}
	}
	if executed == 0 {
		t.Fatal("no privileged command was run: the check proved nothing")
	}
}

// User mode (>): for every role, a command it is offered must not be refused, and a command it is not
// offered must be rejected as an invalid input. Each command runs in a fresh session.
func TestUserModeVisibilityAgreesWithHandlers(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no host tool is started by a probe
	r := newTestRouter(t)
	executed := 0
	for _, role := range config.Roles {
		for _, n := range trees[modeExec] {
			if n.Hidden || n.Arg != 0 {
				continue
			}
			s := NewSession(r, &config.User{Name: "u-" + role, Role: role})
			visible := n.visibleIn(modeExec, role) && (n.Gate == nil || n.Gate(s))
			out := runCmd(NewSession(r, &config.User{Name: "u-" + role, Role: role}), n.Word)
			if visible {
				executed++
			}
			refused := strings.Contains(out, "access denied")
			invalid := strings.Contains(out, "% Invalid input")
			switch {
			case visible && (refused || invalid):
				t.Errorf("role %s >: %s is offered but refused: %q", role, n.Word, out)
			case !visible && !invalid:
				t.Errorf("role %s >: %s is not offered but was accepted: %q", role, n.Word, out)
			}
		}
	}
	if executed == 0 {
		t.Fatal("no user-mode command was run: the check proved nothing")
	}
}

// Configuration mode (admin only): every top-level command the admin is offered must not be refused. The
// test router is a temporary directory, so running the commands changes nothing on the device.
func TestConfigModeVisibleCommandsAreNotRefusedToAdmin(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no host tool is started by a probe
	r := newTestRouter(t)
	fresh := func() *Session {
		s := NewSession(r, &config.User{Name: "root", Role: config.RoleAdmin})
		var sb strings.Builder
		s.Exec("enable", &sb)
		s.Exec("configure terminal", &sb)
		return s
	}
	probe := fresh()
	if probe.top().kind != modeConfig {
		t.Fatal("admin cannot enter configuration mode")
	}
	executed := 0
	for _, n := range trees[modeConfig] {
		if n.Hidden || n.Arg != 0 {
			continue
		}
		if n.Gate != nil && !n.Gate(probe) {
			continue // a feature is off: the command is not offered, so not checked here
		}
		executed++
		out := runCmd(fresh(), n.Word)
		if strings.Contains(out, "access denied") {
			t.Errorf("admin in configuration mode: %s is refused: %q", n.Word, out)
		}
	}
	if executed == 0 {
		t.Fatal("no configuration command was run: the check proved nothing")
	}
}
