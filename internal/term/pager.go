package term

import (
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"unicode/utf8"
	"unsafe"
)

// Paged output (pager) in the spirit of less / Cisco "--More--".
//
//	space, f, PgDn       page down
//	Enter, j, ↓          line down            b, PgUp   page up
//	k, ↑                 line up              g / G     top / bottom
//	d / u                half page down/up
//	/text, n             search forward / next match
//	q, Q, Ctrl-C, Esc    exit
//
// The pager opens a separate full-screen page (the terminal's alternate screen,
// as less does): on exit the console comes back as it was before the command.
// Text that fits on the screen is printed as usual, without the pager.

// Pager key codes.
const (
	KeyNone = iota
	KeyQuit
	KeyPageDown
	KeyPageUp
	KeyLineDown
	KeyLineUp
	KeyHome
	KeyEnd
	KeyHalfDown
	KeyHalfUp
	KeySearch
	KeyNext
	KeyEnter // Enter: line down, and at the end of the text: exit
	KeySpace // space: page down, and at the end of the text: exit
)

type winsize struct{ Row, Col, X, Y uint16 }

// Size returns the terminal size (rows, columns); 24x80 by default.
func Size(f *os.File) (rows, cols int) {
	var ws winsize
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
	if e != 0 || ws.Row == 0 || ws.Col == 0 {
		return 24, 80
	}
	return int(ws.Row), int(ws.Col)
}

// wrapLines cuts text into screen lines of width cols (without losing characters).
func wrapLines(text string, cols int) []string {
	if cols < 8 {
		cols = 8
	}
	text = strings.ReplaceAll(text, "\r", "")
	text = strings.TrimRight(text, "\n")
	var out []string
	for _, ln := range strings.Split(text, "\n") {
		ln = strings.ReplaceAll(ln, "\t", "    ")
		if utf8.RuneCountInString(ln) <= cols {
			out = append(out, ln)
			continue
		}
		r := []rune(ln)
		for len(r) > cols {
			out = append(out, string(r[:cols]))
			r = r[cols:]
		}
		out = append(out, string(r))
	}
	return out
}

type pager struct {
	lines  []string
	window int
	top    int
	query  string
	notice string
	mode   string // "FOLLOW" or "PAUSED" on a live page (see Follow); empty for a static text
}

func (p *pager) maxTop() int {
	if m := len(p.lines) - p.window; m > 0 {
		return m
	}
	return 0
}
func (p *pager) atEnd() bool { return p.top >= p.maxTop() }

func (p *pager) clamp() {
	if p.top > p.maxTop() {
		p.top = p.maxTop()
	}
	if p.top < 0 {
		p.top = 0
	}
}

// find looks for a line containing the substring, starting from line from.
func (p *pager) find(q string, from int) int {
	q = strings.ToLower(q)
	for i := from; i < len(p.lines); i++ {
		if strings.Contains(strings.ToLower(p.lines[i]), q) {
			return i
		}
	}
	return -1
}

// handle applies a key; true means exit.
func (p *pager) handle(k int, query string) bool {
	p.notice = ""
	switch k {
	case KeyQuit:
		return true
	case KeySpace:
		if p.atEnd() {
			return true
		}
		p.top += p.window
	case KeyPageDown:
		p.top += p.window
	case KeyEnter:
		if p.atEnd() {
			return true
		}
		p.top++
	case KeyLineDown:
		p.top++
	case KeyPageUp:
		p.top -= p.window
	case KeyLineUp:
		p.top--
	case KeyHalfDown:
		p.top += p.window / 2
	case KeyHalfUp:
		p.top -= p.window / 2
	case KeyHome:
		p.top = 0
	case KeyEnd:
		p.top = p.maxTop()
	case KeySearch:
		if query != "" {
			p.query = query
		}
		fallthrough
	case KeyNext:
		if p.query == "" {
			p.notice = "no search pattern"
			break
		}
		if i := p.find(p.query, p.top+1); i >= 0 {
			p.top = i
		} else if i := p.find(p.query, 0); i >= 0 && i != p.top {
			p.top = i
			p.notice = "search wrapped"
		} else {
			p.notice = "pattern not found: " + p.query
		}
	}
	p.clamp()
	return false
}

func (p *pager) status(cols int) string {
	var s string
	pct := 100
	if n := len(p.lines); n > 0 {
		pct = (p.top + p.window) * 100 / n
		if pct > 100 {
			pct = 100
		}
	}
	switch {
	case p.notice != "":
		s = " " + p.notice + " "
	case p.mode != "":
		s = " " + p.mode + "  b, k, PgUp: back  G, End: to the end  q, Ctrl-C: quit "
	case p.atEnd():
		s = " (END) q: quit  b/↑: back "
	default:
		s = fmt.Sprintf(" --More-- (%d%%)  space: next  b: back  ↑↓: line  /: search  q: quit ", pct)
	}
	if r := []rune(s); len(r) > cols-1 {
		s = string(r[:cols-1])
	}
	return "\x1b[7m" + s + "\x1b[0m"
}

// render returns the output sequence that draws the window. prevWindow > 0:
// the cursor first moves up over the previously drawn window.
func (p *pager) render(prevWindow, cols int) string {
	var b strings.Builder
	if prevWindow > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", prevWindow)
	}
	b.WriteString("\r\x1b[J") // erase everything below: the window size may have changed
	end := p.top + p.window
	if end > len(p.lines) {
		end = len(p.lines)
	}
	for i := p.top; i < end; i++ {
		b.WriteString(p.lines[i])
		b.WriteString("\r\n")
	}
	for i := end - p.top; i < p.window; i++ { // pad with empty lines so the window height does not change
		b.WriteString("\r\n")
	}
	b.WriteString(p.status(cols))
	return b.String()
}

// runPager is the pager loop; keys blocks until the next key, size gives the current size,
// readQuery reads the search string (nil means search is unavailable).
func runPager(w io.Writer, text string, fixed int, keys func() int, size func() (int, int), readQuery func() string) {
	rows, cols := size()
	lines := wrapLines(text, cols)
	window := rows - 1
	if fixed > 0 {
		window = fixed
	}
	if window < 1 {
		window = 1
	}
	if len(lines) <= window { // fits: just print
		for _, l := range lines {
			fmt.Fprint(w, l, "\r\n")
		}
		return
	}
	p := &pager{lines: lines, window: window}
	fmt.Fprint(w, altScreenOn) // a separate page: the console is restored on exit
	defer fmt.Fprint(w, altScreenOff)
	fmt.Fprint(w, p.render(0, cols))
	prev := window
	for {
		k := keys()
		if k == KeyNone {
			continue
		}
		q := ""
		if k == KeySearch && readQuery != nil {
			// the search string is entered in place of the status
			fmt.Fprint(w, "\r\x1b[K/")
			q = readQuery()
		}
		if p.handle(k, q) {
			break
		}
		// the size may have changed: rebuild the lines when the width changes
		nr, nc := size()
		if nc != cols {
			cols = nc
			p.lines = wrapLines(text, cols)
			p.clamp()
		}
		if fixed <= 0 && nr-1 != p.window && nr-1 >= 1 {
			p.window = nr - 1
			p.clamp()
		}
		fmt.Fprint(w, p.render(prev, cols))
		prev = p.window
	}
}

// altScreenOn / altScreenOff switch the terminal to its alternate screen and back.
const (
	altScreenOn  = "\x1b[?1049h\x1b[H\x1b[2J"
	altScreenOff = "\x1b[?1049l"
)

// readKey reads one key from the raw terminal.
func readKey(fd int) int { return keyFromByte(fd, readByteTimeout(fd, -1)) }

// keyFromByte maps the first byte of a key (and the rest of an escape sequence) to a pager key.
func keyFromByte(fd, b int) int {
	switch b {
	case 'q', 'Q', 3, 4:
		return KeyQuit
	case ' ':
		return KeySpace
	case 'f', 6:
		return KeyPageDown
	case '\r', '\n':
		return KeyEnter
	case 'j', 14:
		return KeyLineDown
	case 'b', 2:
		return KeyPageUp
	case 'k', 16, 25:
		return KeyLineUp
	case 'g':
		return KeyHome
	case 'G':
		return KeyEnd
	case 'd':
		return KeyHalfDown
	case 'u':
		return KeyHalfUp
	case '/':
		return KeySearch
	case 'n':
		return KeyNext
	case 27: // ESC: alone means exit, otherwise an escape sequence
		n := readByteTimeout(fd, 40)
		if n < 0 {
			return KeyQuit
		}
		if n != '[' && n != 'O' {
			return KeyNone
		}
		c := readByteTimeout(fd, 40)
		switch c {
		case 'A':
			return KeyLineUp
		case 'B':
			return KeyLineDown
		case 'H':
			return KeyHome
		case 'F':
			return KeyEnd
		case '5', '6', '1', '4', '7', '8':
			readByteTimeout(fd, 40) // trailing '~'
			switch c {
			case '5':
				return KeyPageUp
			case '6':
				return KeyPageDown
			case '1', '7':
				return KeyHome
			default:
				return KeyEnd
			}
		}
	case -2: // end of input (EOF, closed terminal)
		return KeyQuit
	}
	return KeyNone
}

// readByteTimeout: a byte, -1 is a timeout (when timeoutMs>=0), -2 is EOF/error.
// timeoutMs<0 means wait forever (-1 is not returned).
func readByteTimeout(fd, timeoutMs int) int {
	for {
		var set syscall.FdSet
		set.Bits[fd/64] |= 1 << (uint(fd) % 64)
		var tv *syscall.Timeval
		if timeoutMs >= 0 {
			tv = &syscall.Timeval{Sec: int64(timeoutMs / 1000), Usec: int64(timeoutMs % 1000 * 1000)}
		}
		n, err := syscall.Select(fd+1, &set, nil, nil, tv)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			return -2
		}
		if n == 0 {
			return -1
		}
		var b [1]byte
		m, err := syscall.Read(fd, b[:])
		if err != nil || m <= 0 {
			return -2
		}
		return int(b[0])
	}
}

// readQueryLine reads the search string (Enter accepts, Esc/Ctrl-C cancels).
func readQueryLine(fd int, w io.Writer) string {
	var q []byte
	for {
		b := readByteTimeout(fd, -1)
		switch {
		case b == '\r' || b == '\n':
			return string(q)
		case b == 27 || b == 3 || b == -2:
			return ""
		case b == 127 || b == 8:
			if len(q) > 0 {
				q = q[:len(q)-1]
				fmt.Fprint(w, "\b \b")
			}
		case b >= 32:
			q = append(q, byte(b))
			fmt.Fprint(w, string(rune(b)))
		}
	}
}

// Page shows text page by page on terminal in. fixed>0 is the number of lines per
// page (terminal length N), otherwise the terminal window height.
// If text fits on the screen, it is simply printed.
func Page(in *os.File, out io.Writer, text string, fixed int) {
	if !IsTerminal(in) {
		fmt.Fprint(out, strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r\n"))
		return
	}
	restore, err := Raw(in)
	if err != nil {
		fmt.Fprint(out, text)
		return
	}
	defer restore()
	fd := int(in.Fd())
	runPager(out, text, fixed,
		func() int { return readKey(fd) },
		func() (int, int) { return Size(in) },
		func() string { return readQueryLine(fd, out) })
}
