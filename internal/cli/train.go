package cli

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

// defaultLabel — the inscription on the tender (centered in the field); fixed.
const defaultLabel = "MGE"

// labelWidth — width of the inscription field on the tender (characters inside |...|).
const labelWidth = 8

// defaultAfter — the line after the little locomotive passes; with an argument, mge "Str" prints only Str.
const defaultAfter = "Choo-choo! Have a nice day."

// afterMax — maximum number of characters of the after-pass text.
const afterMax = 64

// parseAfter checks the mge argument: text in matching double or single quotes,
// 1–64 printable ASCII characters inside, no line breaks. Returns the quote and the text.
func parseAfter(arg string) (byte, string, error) {
	const usage = `text must be in matching double or single quotes, 1-64 characters: mge "TEXT" or mge 'TEXT'`
	if len(arg) < 3 {
		return 0, "", fmt.Errorf("%s", usage)
	}
	q := arg[0]
	if (q != '"' && q != '\'') || arg[len(arg)-1] != q {
		return 0, "", fmt.Errorf("%s", usage)
	}
	inner := arg[1 : len(arg)-1]
	if n := len(inner); n < 1 || n > afterMax {
		return 0, "", fmt.Errorf("%s", usage)
	}
	for _, c := range inner {
		if c < 0x20 || c > 0x7e {
			return 0, "", fmt.Errorf("text may contain only printable ASCII characters")
		}
	}
	return q, inner, nil
}

// afterArg takes everything after the command word as is (quotes are kept).
func afterArg(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.IndexAny(raw, " \t"); i >= 0 {
		return strings.TrimSpace(raw[i:])
	}
	return ""
}

// fitLabel centers the inscription in the labelWidth field.
func fitLabel(s string) string {
	if s == "" {
		s = defaultLabel
	}
	left := (labelWidth - len(s)) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", labelWidth-len(s)-left)
}

// Animated ASCII little locomotive: the mge command.
//
// Frames are drawn on a fixed-width canvas and printed over one another
// (ESC[nA — up n lines). The animation does not hold the router lock and
// is interrupted by Ctrl-C (Session.Interrupt); the cursor is restored in any case.

const (
	trainWidth  = 64 // canvas width
	trainSmoke  = 4  // lines for smoke
	trainBody   = 6  // lines of the body
	trainStepPx = 2  // columns per frame
)

var trainDelay = 45 * time.Millisecond

var (
	locoArt = []string{
		"      _                  ",
		"     | |        ____     ",
		"     |_|_______|[__]|__  ",
		"   __|  ___________   |_ ",
		"  |  | |__|__|__|__|  | >",
		"  |__|________________|__|",
	}
	tenderArt = []string{
		"            ",
		"            ",
		"            ",
		"  ________  ",
		" |%s|=",
		" |________|=",
	}
	wagonArt = []string{
		"             ",
		" ___________ ",
		"|  _  _  _  |",
		"| |_||_||_| |",
		"|___________|=",
		"             ",
	}
	// wheels: rotation frames (templates for each car)
	locoWheels   = []string{"   (+)  (+)  (+)   (+) ", "   (x)  (x)  (x)   (x) "}
	tenderWheels = []string{"  (+)    (+) ", "  (x)    (x) "}
	wagonWheels  = []string{" (+)     (+) ", " (x)     (x) "}
)

// trainSprite assembles the train (car, tender, locomotive) for frame step.
func trainSprite(step int, label string) []string {
	pad := func(l []string, w int) []string {
		out := make([]string, len(l))
		for i, s := range l {
			out[i] = s + strings.Repeat(" ", max(0, w-len(s)))
		}
		return out
	}
	w1, w2, w3 := 14, 13, 26
	wag := append(pad(wagonArt, w1), pad([]string{wagonWheels[step%2]}, w1)[0])
	tenderRows := make([]string, len(tenderArt))
	for i, l := range tenderArt {
		tenderRows[i] = strings.Replace(l, "%s", fitLabel(label), 1)
	}
	tnd := append(pad(tenderRows, w2), pad([]string{tenderWheels[step%2]}, w2)[0])
	loc := append(pad(locoArt, w3), pad([]string{locoWheels[step%2]}, w3)[0])
	out := make([]string, len(loc))
	for i := range out {
		out[i] = wag[i] + tnd[i] + loc[i]
	}
	return out
}

func trainLen() int { return len(trainSprite(0, defaultLabel)[0]) }

type puff struct{ x, y, age int }

// trainFrame draws one frame: the train shifted by x (may partly leave the screen).
func trainFrame(step, x int, puffs []puff, label string) []string {
	rows := trainSmoke + trainBody + 1 + 2
	cv := make([][]byte, rows)
	for i := range cv {
		cv[i] = []byte(strings.Repeat(" ", trainWidth))
	}
	put := func(row, col int, ch byte) {
		if row >= 0 && row < rows && col >= 0 && col < trainWidth {
			cv[row][col] = ch
		}
	}
	for _, p := range puffs {
		ch := byte('.')
		switch {
		case p.age < 3:
			ch = 'o'
		case p.age < 6:
			ch = 'O'
		}
		if p.age < 9 {
			put(p.y, p.x, ch)
		}
	}
	for r, line := range trainSprite(step, label) {
		for c := 0; c < len(line); c++ {
			if line[c] != ' ' {
				put(trainSmoke+r, x+c, line[c])
			}
		}
	}
	base := trainSmoke + trainBody + 1
	for c := 0; c < trainWidth; c++ { // rails and moving sleepers
		put(base, c, '=')
		if (c+trainWidth*8-step*trainStepPx)%4 == 0 {
			put(base+1, c, '|')
		} else {
			put(base+1, c, ' ')
		}
	}
	out := make([]string, rows)
	for i := range cv {
		out[i] = strings.TrimRight(string(cv[i]), " ")
	}
	return out
}

// cmdTrain: mge [TEXT] — the little locomotive; TEXT replaces the inscription on the tender.
func cmdTrain(s *Session, args []string) error {
	label := defaultLabel
	after := defaultAfter
	if arg := afterArg(s.rawLine); arg != "" {
		var err error
		if _, after, err = parseAfter(arg); err != nil {
			return err
		}
	}
	if !s.TTY { // output is not to a terminal (ssh host mge, a script): one static frame
		for _, l := range trainFrame(0, 12, nil, label) {
			s.Printf("%s\n", l)
		}
		s.Printf("%s\n", after)
		return nil
	}
	s.cmu.Lock()
	ctx := s.ctx
	s.cmu.Unlock()
	if ctx == nil {
		return fmt.Errorf("no context")
	}
	// the animation must not block other sessions and the CLI
	s.R.mu.Unlock()
	defer s.R.mu.Lock()

	w := s.W
	_, _ = w.Write([]byte("\x1b[?25l")) // hide the cursor
	defer func() { _, _ = w.Write([]byte("\x1b[?25h")) }()

	var puffs []puff
	total := (trainWidth + trainLen()) / trainStepPx
	x := -trainLen()
	first := true
	rows := trainSmoke + trainBody + 1 + 2
	for step := 0; step <= total; step++ {
		// smoke from the locomotive's chimney (the chimney is column 6 of the locomotive)
		chimney := x + 14 + 13 + 6
		for i := range puffs {
			puffs[i].age++
			if i%2 == 0 || puffs[i].age%2 == 0 {
				puffs[i].y--
			}
			puffs[i].x -= 1 + (puffs[i].age % 2) // the smoke is blown backward
		}
		if chimney >= 0 && chimney < trainWidth {
			puffs = append(puffs, puff{x: chimney, y: trainSmoke - 1})
		}
		keep := puffs[:0]
		for _, p := range puffs {
			if p.age < 9 && p.y >= 0 && p.x >= 0 {
				keep = append(keep, p)
			}
		}
		puffs = keep

		var b bytes.Buffer
		if !first {
			fmt.Fprintf(&b, "\x1b[%dA", rows)
		}
		first = false
		for _, l := range trainFrame(step, x, puffs, label) {
			b.WriteString("\r")
			b.WriteString(l)
			b.WriteString("\x1b[K\n")
		}
		_, _ = w.Write(b.Bytes())

		x += trainStepPx
		select {
		case <-ctx.Done():
			_, _ = w.Write([]byte("\r\nChoo!\r\n"))
			return nil
		case <-time.After(trainDelay):
		}
	}
	_, _ = w.Write([]byte("\r" + after + "\x1b[K\n"))
	return nil
}
