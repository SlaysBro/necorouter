package cli

import (
	"slices"
	"testing"

	"necorouter/internal/config"
)

// Invariants of the command tree. A new command that breaks one of them fails here, not in production.

// Every top-level configuration command (trees[modeConfig]) is reachable without indentation from every
// other configuration mode: directly, or through the rule of placeLine (it leaves the sub-mode).
// Modes are found from trees, so a new mode is checked without changing this test.
func TestEveryTopLevelCommandReachableFromEveryConfigMode(t *testing.T) {
	s := NewSession(newTestRouter(t), &config.User{Name: "a", Role: "admin"})
	s.noApply = true // gates ignored: every command of the tree is checked, whatever the features are
	for kind := range trees {
		if !isCfg(kind) {
			continue
		}
		for _, top := range trees[modeConfig] {
			if top.Hidden || top.Arg != 0 {
				continue
			}
			if _, d, _ := s.placeLine(kind, []string{top.Word}, false); d.errKind != "" {
				t.Errorf("mode %q: top-level command %q is not reachable without indentation (%s)", kind, top.Word, d.errKind)
			}
		}
	}
}

// Every show command is offered in at least one mode, with the roles that may use it. A show command
// without a mode would silently disappear from every mode.
func TestEveryShowCommandHasModeAndRoles(t *testing.T) {
	for _, k := range buildShow().Kids {
		if k.Hidden {
			continue
		}
		if len(k.access) == 0 {
			t.Errorf("show %s is in no mode: it is invisible to every role", k.Word)
		}
		for mode, roles := range k.access {
			if len(roles) == 0 {
				t.Errorf("show %s in mode %s has no roles", k.Word, mode)
			}
		}
	}
}

// Every For declaration names known roles and a mode that exists: a misspelt role or mode would hide a command
// from everyone, or declare a rule that never applies.
func TestVisibilityNamesOnlyKnownRolesAndModes(t *testing.T) {
	var walk func(n *Node, f func(*Node))
	walk = func(n *Node, f func(*Node)) {
		f(n)
		for _, k := range n.Kids {
			walk(k, f)
		}
	}
	seen := 0
	for _, nodes := range trees {
		for _, root := range nodes {
			walk(root, func(n *Node) {
				for mode, roles := range n.access {
					seen++
					if _, ok := trees[mode]; !ok {
						t.Errorf("%s: For names the unknown mode %q", n.Word, mode)
					}
					for _, r := range roles {
						if !slices.Contains(config.Roles, r) {
							t.Errorf("%s (%s): unknown role %q", n.Word, mode, r)
						}
					}
				}
			})
		}
	}
	if seen == 0 {
		t.Fatal("no For declaration was found: the check proved nothing")
	}
}

// Every mode of the tree has an explicit prompt suffix (a mode that is added without one would get a made-up prompt).
func TestEveryModeHasPromptSuffix(t *testing.T) {
	for kind := range trees {
		if _, ok := suffixes[kind]; !ok {
			t.Errorf("mode %q has no prompt suffix in suffixes", kind)
		}
	}
}

// The sub-modes that carry the global configuration commands (IPsec, route-map, ...) must carry all of them.
// buildSubModes copies trees[modeConfig] when it runs, so a changed build order would drop commands here
// without any other test noticing, because the fallback of placeLine still reaches the configuration mode.
func TestGlobalSubModesCarryEveryGlobalCommand(t *testing.T) {
	words := func(nodes []*Node) map[string]bool {
		m := map[string]bool{}
		for _, n := range nodes {
			m[n.Word] = true
		}
		return m
	}
	global := words(trees[modeConfig])
	for _, mode := range []string{modeIPProf, modeIKEP1, modeIKEP2, modeCMap, modeRMap} {
		have := words(trees[mode])
		for w := range global {
			if !have[w] {
				t.Errorf("mode %s lacks the global command %q", mode, w)
			}
		}
	}
}
