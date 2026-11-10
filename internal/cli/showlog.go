package cli

import (
	"context"
	"fmt"
	"strconv"

	"necorouter/internal/logx"
)

// show log (all|) (excessive|) (lines NUM|) (follow|reverse|)

type logOpt struct {
	all, exc, follow, rev, lines bool
}

func logNode() *Node {
	run := func(o logOpt) Handler {
		return func(s *Session, v []string) error { return showLog(s, o, v) }
	}
	tail := func(o logOpt) []*Node {
		f, r := o, o
		f.follow, r.rev = true, true
		return []*Node{
			K("follow", "Show messages as they arrive (Ctrl-C to stop; use '| nopager')").R(run(f)),
			K("reverse", "Show messages in reverse order").R(run(r)),
		}
	}
	lines := func(o logOpt) *Node {
		o.lines = true
		return K("lines", "Show only the last messages",
			A(KInt, "NUM", "Number of messages", tail(o)...).Rng(1, 100000).R(run(o)))
	}
	exc := func(o logOpt) *Node {
		o.exc = true
		kids := append([]*Node{lines(o)}, tail(o)...)
		return K("excessive", "Add source file, function and line to every message", kids...).R(run(o))
	}
	all := func(o logOpt) *Node {
		o.all = true
		kids := append([]*Node{exc(o), lines(o)}, tail(o)...)
		return K("all", "Show all retained messages, not only since boot", kids...).R(run(o))
	}
	var o logOpt
	kids := append([]*Node{all(o), exc(o), lines(o)}, tail(o)...)
	return K("log", "Show the system log", kids...).R(run(o))
}

func showLog(s *Session, o logOpt, v []string) error {
	if o.follow && o.rev {
		return fmt.Errorf("follow and reverse cannot be combined")
	}
	n := 0
	if o.lines && len(v) > 0 {
		n, _ = strconv.Atoi(v[0])
	}
	s.cmu.Lock()
	ctx := s.ctx
	s.cmu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	dir := s.R.P.LogDir()
	es, off, err := logx.ReadAll(dir)
	if err != nil {
		return err
	}
	if bt := logx.BootTime(); !o.all && !bt.IsZero() { // without all — only since boot
		k := 0
		for _, e := range es {
			if !e.Time.Before(bt) {
				es[k] = e
				k++
			}
		}
		es = es[:k]
	}
	if o.lines && n < len(es) {
		es = es[len(es)-n:]
	}
	if o.rev {
		for i, j := 0, len(es)-1; i < j; i, j = i+1, j-1 {
			es[i], es[j] = es[j], es[i]
		}
	}
	for _, e := range es {
		s.Printf("%s\n", e.String(o.exc))
	}
	if !o.follow {
		return nil
	}
	// follow: the router is not blocked until the user presses Ctrl-C
	s.R.mu.Unlock()
	defer s.R.mu.Lock()
	w := s.W
	logx.Follow(ctx, dir, off, func(e logx.Entry) {
		fmt.Fprintf(w, "%s\n", e.String(o.exc))
	})
	return nil
}
