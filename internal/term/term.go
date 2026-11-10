// Package term is a minimal terminal using only the standard library:
// raw mode, a line editor with Tab/?/history, and password input without echo.
package term

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"unicode/utf8"
	"unsafe"
)

var ErrEOF = io.EOF

func tcget(fd int) (*syscall.Termios, error) {
	t := &syscall.Termios{}
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TCGETS, uintptr(unsafe.Pointer(t)))
	if e != 0 {
		return nil, e
	}
	return t, nil
}

func tcset(fd int, t *syscall.Termios) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TCSETS, uintptr(unsafe.Pointer(t)))
	if e != 0 {
		return e
	}
	return nil
}

func IsTerminal(f *os.File) bool { _, err := tcget(int(f.Fd())); return err == nil }

// Raw puts the terminal in per-character mode without echo and returns a restore function.
func Raw(f *os.File) (func(), error) {
	old, err := tcget(int(f.Fd()))
	if err != nil {
		return nil, err
	}
	n := *old
	n.Lflag &^= syscall.ICANON | syscall.ECHO | syscall.ISIG | syscall.IEXTEN
	n.Iflag &^= syscall.ICRNL | syscall.INLCR | syscall.IXON
	n.Cc[syscall.VMIN], n.Cc[syscall.VTIME] = 1, 0
	if err := tcset(int(f.Fd()), &n); err != nil {
		return nil, err
	}
	return func() { _ = tcset(int(f.Fd()), old) }, nil
}

// Hooks connects the editor to the command interpreter.
type Hooks struct {
	Help     func(line string) string
	Complete func(line string) (string, []string)
}

type Editor struct {
	In      *os.File
	Out     io.Writer
	History []string
	Secret  bool // password input: no echo, no history, no hints
	plain   *bufio.Reader
	pending []byte // typed-ahead input: bytes received while a command ran (see Typeahead)
}

// Typeahead gives back input that arrived while a command was running (for example the lines
// of a pasted configuration). The editor reads it before the terminal, so nothing is lost.
func (e *Editor) Typeahead(b []byte) {
	e.pending = append(e.pending, b...)
}

// readByte returns the next input byte: typed-ahead bytes first, then the terminal.
func (e *Editor) readByte(b []byte) error {
	if len(e.pending) > 0 {
		b[0] = e.pending[0]
		e.pending = e.pending[1:]
		return nil
	}
	_, err := e.In.Read(b)
	return err
}

func NewEditor(in *os.File, out io.Writer) *Editor {
	return &Editor{In: in, Out: out, plain: bufio.NewReader(in)}
}

func crlf(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

// tabList prints the completion options in columns, without descriptions.
func tabList(opts []string) string {
	w := 0
	for _, o := range opts {
		if len(o) > w {
			w = len(o)
		}
	}
	w += 2
	per := 80 / w
	if per < 1 {
		per = 1
	}
	var b strings.Builder
	for i, o := range opts {
		b.WriteString(o)
		if (i+1)%per == 0 || i == len(opts)-1 {
			b.WriteString("\n")
		} else {
			b.WriteString(strings.Repeat(" ", w-len(o)))
		}
	}
	return b.String()
}

func (e *Editor) readRune() (rune, error) {
	var buf [4]byte
	if err := e.readByte(buf[:1]); err != nil {
		return 0, err
	}
	if buf[0] < 0x80 {
		return rune(buf[0]), nil
	}
	n := 1
	for !utf8.FullRune(buf[:n]) && n < 4 {
		if err := e.readByte(buf[n : n+1]); err != nil {
			return 0, err
		}
		n++
	}
	r, _ := utf8.DecodeRune(buf[:n])
	return r, nil
}

// ReadLine reads a line. Without a terminal it works as ordinary line-by-line reading.
func (e *Editor) ReadLine(prompt string, h Hooks) (string, error) {
	if e.Secret {
		return e.readSecret(prompt)
	}
	if !IsTerminal(e.In) {
		fmt.Fprint(e.Out, prompt)
		ln, err := e.plain.ReadString('\n')
		if err != nil && ln == "" {
			return "", ErrEOF
		}
		ln = strings.TrimRight(ln, "\r\n")
		fmt.Fprintln(e.Out, ln) // echo, so the log is readable
		return ln, nil
	}
	restore, err := Raw(e.In)
	if err != nil {
		return "", err
	}
	defer restore()

	var buf []rune
	pos := 0
	hist := len(e.History)
	draw := func() {
		fmt.Fprintf(e.Out, "\r%s%s\x1b[K", prompt, string(buf))
		if back := len(buf) - pos; back > 0 {
			fmt.Fprintf(e.Out, "\x1b[%dD", back)
		}
	}
	inQuote := func() bool { return strings.Count(string(buf[:pos]), `"`)%2 == 1 }
	fmt.Fprint(e.Out, prompt)
	for {
		r, err := e.readRune()
		if err != nil {
			return "", ErrEOF
		}
		switch r {
		case '\r', '\n':
			fmt.Fprint(e.Out, "\r\n")
			line := string(buf)
			if strings.TrimSpace(line) != "" && (len(e.History) == 0 || e.History[len(e.History)-1] != line) {
				e.History = append(e.History, line)
			}
			return line, nil
		case 3: // Ctrl-C
			fmt.Fprint(e.Out, "^C\r\n")
			return "", nil
		case 4: // Ctrl-D
			if len(buf) == 0 {
				fmt.Fprint(e.Out, "\r\n")
				return "", ErrEOF
			}
			if pos < len(buf) {
				buf = append(buf[:pos], buf[pos+1:]...)
				draw()
			}
		case 26: // Ctrl-Z = end
			fmt.Fprint(e.Out, "^Z\r\n")
			return "end", nil
		case 1:
			pos = 0
			draw()
		case 5:
			pos = len(buf)
			draw()
		case 11:
			buf = buf[:pos]
			draw()
		case 21:
			buf, pos = buf[pos:], 0
			draw()
		case 23: // Ctrl-W
			i := pos
			for i > 0 && buf[i-1] == ' ' {
				i--
			}
			for i > 0 && buf[i-1] != ' ' {
				i--
			}
			buf = append(buf[:i], buf[pos:]...)
			pos = i
			draw()
		case 12:
			fmt.Fprint(e.Out, "\x1b[H\x1b[2J")
			draw()
		case 127, 8:
			if pos > 0 {
				buf = append(buf[:pos-1], buf[pos:]...)
				pos--
				draw()
			}
		case 9: // Tab
			if h.Complete == nil || inQuote() {
				continue
			}
			// Tab appends the continuation; with several options it shows their list (without descriptions)
			nl, opts := h.Complete(string(buf[:pos]))
			if nl != string(buf[:pos]) {
				tail := append([]rune(nil), buf[pos:]...)
				buf = append([]rune(nl), tail...)
				pos = len([]rune(nl))
			}
			if len(opts) > 0 {
				fmt.Fprint(e.Out, "\r\n"+crlf(tabList(opts)))
			}
			draw()
		case '?':
			if h.Help == nil || inQuote() {
				buf = append(buf[:pos], append([]rune{'?'}, buf[pos:]...)...)
				pos++
				draw()
				continue
			}
			fmt.Fprint(e.Out, "\r\n"+crlf(h.Help(string(buf[:pos]))))
			draw()
		case 27:
			r2, err := e.readRune()
			if err != nil {
				return "", ErrEOF
			}
			if r2 != '[' && r2 != 'O' {
				continue
			}
			r3, _ := e.readRune()
			switch r3 {
			case 'A':
				if hist > 0 {
					hist--
					buf = []rune(e.History[hist])
					pos = len(buf)
					draw()
				}
			case 'B':
				if hist < len(e.History)-1 {
					hist++
					buf = []rune(e.History[hist])
				} else {
					hist = len(e.History)
					buf = nil
				}
				pos = len(buf)
				draw()
			case 'C':
				if pos < len(buf) {
					pos++
					draw()
				}
			case 'D':
				if pos > 0 {
					pos--
					draw()
				}
			case 'H':
				pos = 0
				draw()
			case 'F':
				pos = len(buf)
				draw()
			case '1', '7':
				_, _ = e.readRune() // ~
				pos = 0
				draw()
			case '4', '8':
				_, _ = e.readRune()
				pos = len(buf)
				draw()
			case '3':
				_, _ = e.readRune()
				if pos < len(buf) {
					buf = append(buf[:pos], buf[pos+1:]...)
					draw()
				}
			}
		default:
			if r >= 32 {
				buf = append(buf[:pos], append([]rune{r}, buf[pos:]...)...)
				pos++
				draw()
			}
		}
	}
}

// WatchInterrupt polls input (terminal in raw mode) until stop is closed,
// and calls onIntr on Ctrl-C. Other bytes typed while the command runs
// go to keep (typeahead). Returns no later than ~50 ms after stop is closed,
// without reading anything extra.
func WatchInterrupt(f *os.File, stop <-chan struct{}, onIntr func(), keep func([]byte)) {
	fd := int(f.Fd())
	buf := make([]byte, 64)
	for {
		select {
		case <-stop:
			return
		default:
		}
		var set syscall.FdSet
		set.Bits[fd/64] |= 1 << (uint(fd) % 64)
		tv := syscall.Timeval{Usec: 50000}
		n, err := syscall.Select(fd+1, &set, nil, nil, &tv)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			return
		}
		if n == 0 {
			continue
		}
		m, err := syscall.Read(fd, buf)
		if err != nil || m <= 0 {
			return
		}
		var rest []byte
		for _, b := range buf[:m] {
			if b == 3 {
				onIntr()
			} else {
				rest = append(rest, b)
			}
		}
		if keep != nil && len(rest) > 0 {
			keep(rest) // typed while the command runs: the editor gets it back, nothing is dropped
		}
	}
}

// readSecret reads a password without echo. Ctrl-C and Ctrl-D cancel the input (an empty string is returned).
func (e *Editor) readSecret(prompt string) (string, error) {
	fmt.Fprint(e.Out, prompt)
	if !IsTerminal(e.In) {
		ln, err := e.plain.ReadString('\n')
		fmt.Fprintln(e.Out)
		if err != nil && ln == "" {
			return "", ErrEOF
		}
		return strings.TrimRight(ln, "\r\n"), nil
	}
	restore, err := Raw(e.In)
	if err != nil {
		return "", err
	}
	defer restore()
	var buf []rune
	for {
		r, err := e.readRune()
		if err != nil {
			return "", ErrEOF
		}
		switch {
		case r == '\r' || r == '\n':
			fmt.Fprint(e.Out, "\r\n")
			return string(buf), nil
		case r == 3 || r == 4:
			fmt.Fprint(e.Out, "\r\n")
			return "", nil
		case r == 127 || r == 8:
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
			}
		case r == 21:
			buf = buf[:0]
		case r >= 0x20 && r != 0x7f:
			buf = append(buf, r)
		}
	}
}
