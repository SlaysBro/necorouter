// Package cli — a Cisco-style command-line interpreter:
// modes, command abbreviations, "no", help on "?" and Tab completion.
package cli

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"necorouter/internal/config"
)

type ArgKind int

const (
	KWord ArgKind = iota + 1
	KInt
	KIP
	KCIDR
	KRange
	KRest
)

type Handler func(s *Session, v []string) error

type Node struct {
	Word     string // keyword or argument name
	Help     string
	Arg      ArgKind // 0 — keyword
	Min, Max int
	Check    func(string) bool
	Complete func(s *Session) []string
	Kids     []*Node
	Run, Neg Handler
	Hidden   bool
	Gate     func(*Session) bool // the command is visible and available only if true (additional features)
	Secret   bool                // everything entered after this word is a secret: it does not go to the journal
}

// K — keyword, A — argument.
func K(word, help string, kids ...*Node) *Node {
	return &Node{Word: word, Help: help, Kids: kids}
}
func A(kind ArgKind, name, help string, kids ...*Node) *Node {
	return &Node{Word: name, Help: help, Arg: kind, Kids: kids}
}
func (n *Node) R(h Handler) *Node { n.Run = h; return n }
func (n *Node) N(h Handler) *Node { n.Neg = h; return n }
func (n *Node) Rng(lo, hi int) *Node {
	n.Min, n.Max = lo, hi
	return n
}
func (n *Node) Chk(f func(string) bool) *Node { n.Check = f; return n }
func (n *Node) Cmp(f func(s *Session) []string) *Node {
	n.Complete = f
	return n
}
func (n *Node) Hide() *Node { n.Hidden = true; return n }

// Sec marks a word followed by a secret (key, password).
func (n *Node) Sec() *Node { n.Secret = true; return n }

// When restricts a command by a condition: while it is false, the command is hidden and does not execute.
func (n *Node) When(g func(*Session) bool) *Node { n.Gate = g; return n }

func (n *Node) hint() string {
	switch n.Arg {
	case KInt:
		return fmt.Sprintf("<%d-%d>", n.Min, n.Max)
	case KIP:
		return "A.B.C.D"
	case KCIDR:
		return "A.B.C.D/M"
	case KRange:
		return "A.B.C.D[-A.B.C.D]"
	case KRest:
		return n.Word // argument name: LINE, COMMAND, TEXT...
	}
	return n.Word
}

func (n *Node) validate(tok string) bool {
	switch n.Arg {
	case KWord, KRest:
		if n.Check != nil {
			return n.Check(tok)
		}
		return tok != ""
	case KInt:
		v, err := strconv.Atoi(tok)
		return err == nil && v >= n.Min && v <= n.Max
	case KIP:
		a, err := netip.ParseAddr(tok)
		return err == nil && a.Is4()
	case KCIDR:
		p, err := netip.ParsePrefix(tok)
		return err == nil && p.Addr().Is4()
	case KRange:
		_, err := config.ParseIPv4Ranges(tok)
		return err == nil
	}
	return false
}

func hasNeg(n *Node) bool {
	if n.Neg != nil {
		return true
	}
	for _, k := range n.Kids {
		if hasNeg(k) {
			return true
		}
	}
	return false
}

// tokenize splits a string into words, supporting double quotes.
func tokenize(line string) (toks []string, trailingSpace bool) {
	var cur strings.Builder
	in, has := false, false
	for _, r := range line {
		switch {
		case r == '"':
			in = !in
			has = true
		case (r == ' ' || r == '\t') && !in:
			if has {
				toks = append(toks, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if has {
		toks = append(toks, cur.String())
	}
	trailingSpace = len(line) > 0 && (line[len(line)-1] == ' ' || line[len(line)-1] == '\t') && !in
	return
}

type descent struct {
	last    *Node
	kids    []*Node
	vals    []string
	neg     bool
	errKind string // "", invalid, ambiguous
	errTok  int
	rest    bool // the last argument is KRest (swallowed the tail)
	secret  int  // index of the first secret word (after a Secret node); -1 — no secrets
}

func pick(kids []*Node, tok string, neg bool) (*Node, string) {
	lt := strings.ToLower(tok)
	var pref []*Node
	for _, k := range kids {
		if k.Arg != 0 || (neg && !hasNeg(k)) {
			continue
		}
		w := strings.ToLower(k.Word)
		if w == lt {
			return k, ""
		}
		if strings.HasPrefix(w, lt) {
			pref = append(pref, k)
		}
	}
	if len(pref) == 1 {
		return pref[0], ""
	}
	if len(pref) > 1 {
		return nil, "ambiguous"
	}
	for _, k := range kids {
		if k.Arg != 0 && (!neg || hasNeg(k)) && k.validate(tok) {
			return k, ""
		}
	}
	return nil, "invalid"
}

// descend walks the command tree by all complete tokens.
func descend(roots []*Node, toks []string) descent {
	d := descent{kids: roots, secret: -1}
	i := 0
	if len(toks) > 0 && strings.EqualFold(toks[0], "no") {
		d.neg = true
		i = 1
	}
	for ; i < len(toks); i++ {
		n, e := pick(d.kids, toks[i], d.neg)
		if e != "" {
			d.errKind, d.errTok = e, i
			return d
		}
		d.last = n
		if n.Secret && d.secret < 0 {
			d.secret = i + 1
		}
		if n.Arg == KRest {
			d.vals = append(d.vals, strings.Join(toks[i:], " "))
			d.kids = nil
			d.rest = true
			return d
		}
		if n.Arg != 0 {
			d.vals = append(d.vals, toks[i])
		}
		d.kids = n.Kids
	}
	return d
}

type helpItem struct{ word, help string }

// suggestions returns continuation options for a partially entered word.
func suggestions(s *Session, roots []*Node, toks []string, partial string, trailing bool) (items []helpItem, d descent) {
	d = descend(roots, toks)
	if d.errKind != "" || d.rest {
		return nil, d
	}
	lp := strings.ToLower(partial)
	for _, k := range d.kids {
		if k.Hidden || (d.neg && !hasNeg(k)) {
			continue
		}
		if k.Arg == 0 {
			if strings.HasPrefix(strings.ToLower(k.Word), lp) {
				items = append(items, helpItem{k.Word, k.Help})
			}
			continue
		}
		if partial == "" || k.validate(partial) || k.Complete != nil {
			items = append(items, helpItem{k.hint(), k.Help})
		}
		// known argument values (interfaces, pools…) are shown as a list
		if k.Complete != nil {
			for _, c := range k.Complete(s) {
				if strings.HasPrefix(strings.ToLower(c), lp) {
					items = append(items, helpItem{c, k.Help})
				}
			}
		}
	}
	return items, d
}

// Help — the result of pressing "?".
func (s *Session) Help(line string) string {
	kind, line := s.resolve(line)
	if pos, tail, ok := s.pipeTail(kind, line); ok {
		if line[pos] == '>' {
			return "  FILE                   File name\n"
		}
		h, _ := pipeHelp(tail)
		return h
	}
	roots := s.rootsFor(kind)
	toks, trailing := tokenize(line)
	partial := ""
	if !trailing && len(toks) > 0 {
		partial = toks[len(toks)-1]
		toks = toks[:len(toks)-1]
	}
	// "no" without a continuation
	items, d := suggestions(s, roots, toks, partial, trailing)
	if d.errKind != "" {
		return "% Unrecognized command\n"
	}
	if len(toks) == 0 && strings.EqualFold(partial, "no") {
		partial = ""
	}
	// "no ?": no <cr> is added, only the list
	var b strings.Builder
	for _, it := range items {
		fmt.Fprintf(&b, "  %-22s %s\n", it.word, it.help)
	}
	if partial == "" && d.last != nil && ((!d.neg && d.last.Run != nil) || (d.neg && d.last.Neg != nil)) {
		fmt.Fprintf(&b, "  %-22s %s\n", "<cr>", "")
	}
	if b.Len() == 0 {
		return "% No further options\n"
	}
	return b.String()
}

// Complete — the result of pressing Tab. Returns the new line and (when
// ambiguous) the list of options.
func (s *Session) Complete(line string) (string, []string) {
	kind, rest := s.resolve(line)
	prefix := line[:len(line)-len(rest)]
	if pos, tail, ok := s.pipeTail(kind, rest); ok && rest[pos] == '|' && !strings.Contains(strings.TrimLeft(tail, " "), " ") {
		_, cands := pipeHelp(tail)
		part := strings.TrimLeft(tail, " ")
		switch len(cands) {
		case 0:
			return line, nil
		case 1:
			return prefix + rest[:pos+1] + " " + cands[0] + " ", nil
		}
		nl := line
		if l := lcp(cands); len(l) > len(part) {
			nl = prefix + rest[:pos+1] + " " + l
		}
		return nl, cands
	}
	roots := s.rootsFor(kind)
	toks, trailing := tokenize(rest)
	partial := ""
	if !trailing && len(toks) > 0 {
		partial = toks[len(toks)-1]
		toks = toks[:len(toks)-1]
	}
	_, d := suggestions(s, roots, toks, partial, trailing)
	if d.errKind != "" {
		return line, nil
	}
	lp := strings.ToLower(partial)
	var cands []string
	for _, k := range d.kids {
		if k.Hidden || (d.neg && !hasNeg(k)) {
			continue
		}
		if k.Arg == 0 {
			if strings.HasPrefix(strings.ToLower(k.Word), lp) {
				cands = append(cands, k.Word)
			}
		} else if k.Complete != nil {
			for _, c := range k.Complete(s) {
				if strings.HasPrefix(strings.ToLower(c), lp) {
					cands = append(cands, c)
				}
			}
		}
	}
	cands = uniq(cands)
	head := rest[:len(rest)-len(partial)]
	switch len(cands) {
	case 0:
		return line, nil
	case 1:
		return prefix + head + quoteIf(cands[0]) + " ", nil
	}
	// several options: append the common prefix and return the list of continuations without descriptions
	nl := line
	if l := lcp(cands); len(l) > len(partial) {
		nl = prefix + head + l
	}
	return nl, cands
}

func quoteIf(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func lcp(l []string) string {
	p := l[0]
	for _, s := range l[1:] {
		i := 0
		for i < len(p) && i < len(s) && strings.EqualFold(p[i:i+1], s[i:i+1]) {
			i++
		}
		p = p[:i]
	}
	return p
}
