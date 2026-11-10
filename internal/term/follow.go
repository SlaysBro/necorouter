package term

import (
	"fmt"
	"io"
	"os"
)

// Follow shows lines as they arrive, on a full-screen page (the alternate screen, as the pager does).
// The view changes only through the state machine in follow_state.go: FOLLOW keeps the last line on
// screen, PAUSED keeps the position the user chose. q or Ctrl-C leaves the page and calls stop once,
// so that the producer of the lines can end. The page also stays open after lines is closed, until
// the user quits. Without a terminal the lines are printed as they come.
func Follow(in *os.File, out io.Writer, lines <-chan string, stop func()) {
	if !IsTerminal(in) {
		printLines(out, lines)
		return
	}
	restore, err := Raw(in)
	if err != nil {
		printLines(out, lines)
		return
	}
	defer restore()
	fd := int(in.Fd())
	rows, cols := Size(in)
	p := &pager{window: rows - 1}
	if p.window < 1 {
		p.window = 1
	}
	fmt.Fprint(out, altScreenOn)
	defer fmt.Fprint(out, altScreenOff)

	state := stateFollow
	dirty, prev := true, 0
	for {
		// take every line that has arrived; do not wait for them
	drain:
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					lines = nil // no more lines; the page stays open until the user quits
					break drain
				}
				p.lines = append(p.lines, wrapLines(l, cols)...)
				state = onLine(state)
				dirty = true
			default:
				break drain
			}
		}
		if top := viewTop(state, p.top, p.maxTop()); top != p.top {
			p.top = top
			dirty = true
		}
		if p.mode != state.Name() {
			p.mode = state.Name()
			dirty = true
		}
		if dirty {
			fmt.Fprint(out, p.render(prev, cols))
			prev = p.window
			dirty = false
		}

		b := readByteTimeout(fd, 50) // poll: new lines are drawn even while no key is pressed
		if b == -1 {
			continue
		}
		if b < 0 {
			break // the terminal is gone
		}
		k := keyFromByte(fd, b)
		if isIgnored(k) {
			continue
		}
		if isQuit(k) {
			if stop != nil {
				stop()
			}
			return
		}
		if k != KeyEnd {
			p.handle(k, "")
		}
		state = onKey(state, k, p.atEnd())
		dirty = true
	}
}

func printLines(out io.Writer, lines <-chan string) {
	for l := range lines {
		fmt.Fprint(out, l, "\r\n")
	}
}
