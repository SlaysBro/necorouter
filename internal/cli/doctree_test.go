package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// Dump of the command tree for the docs/index.html reference (visible commands only: hidden ones
// and ones closed by services are left out of the reference). Run:
//
//	NECOROUTER_DOCTREE=/tmp/tree.json go test ./internal/cli -run TestDumpDocTree
func TestDumpDocTree(t *testing.T) {
	path := os.Getenv("NECOROUTER_DOCTREE")
	if path == "" {
		t.Skip("NECOROUTER_DOCTREE is not set")
	}
	var conv func(n *Node) map[string]any
	conv = func(n *Node) map[string]any {
		w := n.Word
		switch {
		case n.Arg == KCIDR:
			w = "A.B.C.D/M"
		case n.Arg == KIP:
			w = "A.B.C.D"
		case n.Arg == KInt && n.Max > 0:
			w = fmt.Sprintf("<%d-%d>", n.Min, n.Max)
		}
		m := map[string]any{"w": w, "h": n.Help}
		if hasNeg(n) {
			m["neg"] = true
		}
		if n.Arg != 0 {
			m["a"] = 1
		}
		var kids []any
		for _, k := range n.Kids {
			if k.Hidden || k.Gate != nil {
				continue
			}
			kids = append(kids, conv(k))
		}
		if len(kids) > 0 {
			m["k"] = kids
		}
		return m
	}
	out := map[string][]any{}
	for kind, roots := range trees {
		var l []any
		for _, n := range roots {
			if n.Hidden || n.Gate != nil {
				continue
			}
			l = append(l, conv(n))
		}
		out[kind] = l
	}
	b, _ := json.Marshal(out)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
