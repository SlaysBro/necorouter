package cli

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// sample picks a valid value for a node's argument.
func sample(n *Node) string {
	for _, c := range []string{"X1", "10.0.0.1", "255.255.255.0", "24", "10.0.0.1/24", "10.0.0.1-10.0.0.9", "1", "5", "100", "tcp", "user@10.0.0.1:/tmp/x", "00000000$" + strings.Repeat("0", 64), "23:59"} {
		if n.validate(c) {
			return c
		}
	}
	return "X1"
}

// TestHelpEveryNode walks all command trees: at every point "?" must
// show every visible descendant (a word or an argument template), and a point from
// which the command does not execute and there is nowhere further to go is a "dead end".
func TestHelpEveryNode(t *testing.T) {
	r := newTestRouter(t)
	var problems []string
	kinds := make([]string, 0, len(trees))
	for k := range trees {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		s := &Session{R: r, User: "root", Role: "admin", noApply: true, frames: []frame{{kind: kind}}}
		var walk func(kids []*Node, prefix []string, depth int, neg bool)
		walk = func(kids []*Node, prefix []string, depth int, neg bool) {
			if depth > 14 {
				return
			}
			line := strings.Join(prefix, " ")
			if line != "" {
				line += " "
			}
			out := s.Help(line)
			vis := 0
			for _, k := range kids {
				if k.Hidden || (k.Gate != nil && !k.Gate(s)) || (neg && !hasNeg(k)) {
					continue
				}
				vis++
				want := k.Word
				if k.Arg != 0 {
					want = k.hint()
				}
				if !strings.Contains(out, want) {
					problems = append(problems, fmt.Sprintf("[%s] %q: '?' does not list %q\n%s", kind, line, want, out))
				}
			}
			if vis == 0 && strings.Contains(out, "Unrecognized") {
				problems = append(problems, fmt.Sprintf("[%s] %q: help says Unrecognized", kind, line))
			}
			for _, k := range kids {
				if k.Hidden || (k.Gate != nil && !k.Gate(s)) || (neg && !hasNeg(k)) {
					continue
				}
				w := k.Word
				if k.Arg == KRest || k.Word == "do" {
					continue // do <command> is checked separately (different tree)
				}
				if k.Arg != 0 {
					w = sample(k)
				}
				if len(k.Kids) == 0 && k.Run == nil && k.Neg == nil {
					problems = append(problems, fmt.Sprintf("[%s] dead end after %q %q (no kids, no handler)", kind, line, k.Word))
				}
				walk(k.Kids, append(append([]string(nil), prefix...), w), depth+1, neg)
			}
		}
		walk(trees[kind], nil, 0, false)
	}
	if len(problems) > 0 {
		if len(problems) > 40 {
			problems = problems[:40]
		}
		t.Fatalf("%d problem(s):\n%s", len(problems), strings.Join(problems, "\n"))
	}
}

func TestHelpNATDynamic(t *testing.T) {
	r := newTestRouter(t)
	s := &Session{R: r, User: "root", Role: "admin", noApply: true, frames: []frame{{kind: "config"}}}
	for _, l := range []string{"ip nat pool HQ 10.0.0.1-10.0.0.9", "interface isp"} {
		s.Exec(l, nil)
	}
	s.frames = []frame{{kind: "config"}}
	want := map[string][]string{
		"ip nat source dynamic ":                                              {"inside-to-outside"},
		"ip nat source dynamic inside-to-outside ":                            {"pool"},
		"ip nat source dynamic inside-to-outside pool ":                       {"POOL", "HQ"},
		"ip nat source dynamic inside-to-outside pool HQ ":                    {"overload"},
		"ip nat source dynamic inside-to-outside pool HQ overload ":           {"A.B.C.D", "interface"},
		"ip nat source dynamic inside-to-outside pool HQ overload interface ": {"isp"},
		"ip nat source ": {"dynamic", "static"},
		"ip nat ":        {"pool", "source"},
	}
	for l, ws := range want {
		out := s.Help(l)
		for _, w := range ws {
			if !strings.Contains(out, w) {
				t.Errorf("Help(%q) lacks %q:\n%s", l, w, out)
			}
		}
	}
	// Tab: ip nat source dynamic ins<Tab> -> inside-to-outside
	if got, _ := s.Complete("ip nat source dynamic ins"); got != "ip nat source dynamic inside-to-outside " {
		t.Errorf("Tab on 'ins': %q", got)
	}
}

// The old form (an arbitrary rule name) keeps working and stays idempotent.
func TestNATDynamicForms(t *testing.T) {
	_, out := replay(t, `
ip nat pool HQ 10.0.0.1-10.0.0.9
ip nat pool BR 10.1.0.1-10.1.0.9
ip nat source dynamic inside-to-outside pool HQ overload 172.16.1.2
ip nat source dynamic lan pool BR overload 172.16.1.3
`)
	mustContain(t, out,
		"ip nat source dynamic inside-to-outside pool HQ overload 172.16.1.2\n",
		"ip nat source dynamic lan pool BR overload 172.16.1.3\n")
	r := newTestRouter(t)
	s := &Session{R: r, User: "root", Role: "admin", noApply: true, frames: []frame{{kind: "config"}}}
	s.Exec("ip nat pool HQ 10.0.0.1-10.0.0.9", nil)
	s.Exec("ip nat source dynamic inside-to-outside pool HQ overload 1.1.1.1", nil)
	s.Exec("ip nat source dynamic inside-to-outside pool HQ overload 2.2.2.2", nil)
	if len(r.Cfg.NATDyn) != 1 || r.Cfg.NATDyn[0].Overload != "2.2.2.2" {
		t.Fatalf("not replaced: %+v", r.Cfg.NATDyn)
	}
	s.Exec("no ip nat source dynamic inside-to-outside", nil)
	if len(r.Cfg.NATDyn) != 0 {
		t.Fatalf("no form failed: %+v", r.Cfg.NATDyn)
	}
}

// TestListFreeWordArgs prints word arguments without value hints (for review).
func TestListFreeWordArgs(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	seen := map[string]bool{}
	for kind, roots := range trees {
		var walk func(kids []*Node, path string)
		walk = func(kids []*Node, path string) {
			for _, k := range kids {
				p := strings.TrimSpace(path + " " + k.Word)
				if k.Arg == KWord && k.Complete == nil && k.Check == nil && !seen[p] {
					seen[p] = true
					t.Logf("[%s] %s", kind, p)
				}
				walk(k.Kids, p)
			}
		}
		walk(roots, "")
	}
}
