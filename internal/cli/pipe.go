package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Output modifiers for show commands: `show ... | include TEXT`, `| redirect FILE`, `> FILE`.

type pipeMod struct{ kind, arg string }

var pipeKinds = []struct{ word, help, arg string }{
	{"begin", "Begin with the line that contains the text", "TEXT"},
	{"exclude", "Exclude lines that contain the text", "TEXT"},
	{"grep", "Show lines that match the regular expression", "REGEXP"},
	{"include", "Include only lines that contain the text", "TEXT"},
	{"nopager", "Do not paginate the output", ""},
	{"redirect", "Write the output to a file", "FILE"},
}

// pipeSplit splits a string by `|` and `>` outside quotes. The first element is the command itself.
func pipeSplit(line string) (seps []byte, parts []string) {
	in := false
	start := 0
	seps = append(seps, 0)
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '"':
			in = !in
		case (c == '|' || c == '>') && !in:
			parts = append(parts, strings.TrimSpace(line[start:i]))
			seps = append(seps, c)
			start = i + 1
		}
	}
	parts = append(parts, strings.TrimSpace(line[start:]))
	return
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// splitPipe separates modifiers from the command. Without `|` and `>` there are no modifiers.
func splitPipe(line string) (cmd string, mods []pipeMod, err error) {
	seps, parts := pipeSplit(line)
	if len(parts) == 1 {
		return line, nil, nil
	}
	for i := 1; i < len(parts); i++ {
		p := parts[i]
		if seps[i] == '>' {
			if p == "" {
				return "", nil, fmt.Errorf("Incomplete command: a file name is required after '>'")
			}
			if i != len(parts)-1 {
				return "", nil, fmt.Errorf("'>' must be the last modifier")
			}
			mods = append(mods, pipeMod{"redirect", unquote(p)})
			continue
		}
		w, arg, _ := strings.Cut(p, " ")
		if w == "" {
			return "", nil, fmt.Errorf("Incomplete command: a modifier is required after '|'")
		}
		var hit []int
		for k, pk := range pipeKinds {
			if strings.HasPrefix(pk.word, strings.ToLower(w)) {
				hit = append(hit, k)
			}
		}
		if len(hit) != 1 {
			if len(hit) == 0 {
				return "", nil, fmt.Errorf("Unrecognized modifier \"%s\"", w)
			}
			return "", nil, fmt.Errorf("Ambiguous modifier \"%s\"", w)
		}
		pk := pipeKinds[hit[0]]
		arg = strings.TrimSpace(arg)
		if pk.arg != "" && arg == "" {
			return "", nil, fmt.Errorf("Incomplete command: %s requires %s", pk.word, pk.arg)
		}
		if pk.arg == "" && arg != "" {
			return "", nil, fmt.Errorf("%s takes no arguments", pk.word)
		}
		if pk.word == "redirect" && i != len(parts)-1 {
			return "", nil, fmt.Errorf("redirect must be the last modifier")
		}
		arg = unquote(arg)
		if pk.word == "grep" {
			if _, e := regexp.Compile(arg); e != nil {
				return "", nil, fmt.Errorf("Invalid regular expression: %v", e)
			}
		}
		mods = append(mods, pipeMod{pk.word, arg})
	}
	return parts[0], mods, nil
}

var redirectNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// filterWriter applies modifiers line by line (works for show log follow too).
type filterWriter struct {
	dst   io.Writer
	mods  []pipeMod
	res   []*regexp.Regexp
	begun []bool
	buf   []byte
	file  *os.File // redirect: output goes to a file, not the screen
}

func newFilterWriter(dst io.Writer, mods []pipeMod, filesDir string) (*filterWriter, error) {
	fw := &filterWriter{dst: dst, mods: mods, res: make([]*regexp.Regexp, len(mods)), begun: make([]bool, len(mods))}
	for i, m := range mods {
		switch m.kind {
		case "grep":
			fw.res[i] = regexp.MustCompile(m.arg)
		case "redirect":
			if !redirectNameRe.MatchString(m.arg) || len(m.arg) > 128 {
				return nil, fmt.Errorf("invalid file name %q: use letters, digits, '.', '_' and '-'", m.arg)
			}
			if err := os.MkdirAll(filesDir, 0o750); err != nil {
				return nil, err
			}
			f, err := os.OpenFile(filepath.Join(filesDir, m.arg), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
			if err != nil {
				return nil, err
			}
			fw.file = f
		}
	}
	return fw, nil
}

func (f *filterWriter) pass(line string) bool {
	for i, m := range f.mods {
		switch m.kind {
		case "include":
			if !strings.Contains(line, m.arg) {
				return false
			}
		case "exclude":
			if strings.Contains(line, m.arg) {
				return false
			}
		case "grep":
			if !f.res[i].MatchString(line) {
				return false
			}
		case "begin":
			if !f.begun[i] {
				if !strings.Contains(line, m.arg) {
					return false
				}
				f.begun[i] = true
			}
		}
	}
	return true
}

func (f *filterWriter) emit(line string) {
	if !f.pass(line) {
		return
	}
	if f.file != nil {
		_, _ = io.WriteString(f.file, line+"\n")
		return
	}
	_, _ = io.WriteString(f.dst, line+"\n")
}

func (f *filterWriter) Write(p []byte) (int, error) {
	f.buf = append(f.buf, p...)
	for {
		i := strings.IndexByte(string(f.buf), '\n')
		if i < 0 {
			break
		}
		f.emit(strings.TrimSuffix(string(f.buf[:i]), "\r"))
		f.buf = f.buf[i+1:]
	}
	return len(p), nil
}

// Close appends the unfinished line and closes the file.
func (f *filterWriter) Close() error {
	if len(f.buf) > 0 {
		f.emit(string(f.buf))
		f.buf = nil
	}
	if f.file != nil {
		err := f.file.Close()
		f.file = nil
		return err
	}
	return nil
}

// pipeHelp — the "?" hint and Tab after `|`: tail is the text after the last `|`.
func pipeHelp(tail string) (string, []string) {
	w, rest, hasArg := strings.Cut(strings.TrimLeft(tail, " "), " ")
	var b strings.Builder
	var cands []string
	if !hasArg {
		for _, pk := range pipeKinds {
			if strings.HasPrefix(pk.word, strings.ToLower(w)) {
				fmt.Fprintf(&b, "  %-22s %s\n", pk.word, pk.help)
				cands = append(cands, pk.word)
			}
		}
		if b.Len() == 0 {
			return "% Unrecognized modifier\n", nil
		}
		return b.String(), cands
	}
	for _, pk := range pipeKinds {
		if pk.word == strings.ToLower(w) {
			if pk.arg == "" {
				return "  |                      Another modifier\n  <cr>\n", nil
			}
			if strings.TrimSpace(rest) == "" {
				return fmt.Sprintf("  %-22s %s\n", pk.arg, pk.help), nil
			}
			return "  |                      Another modifier\n  <cr>\n", nil
		}
	}
	return "% Unrecognized modifier\n", nil
}

// lastSep returns the position of the last `|` or `>` outside quotes (or -1).
func lastSep(line string) int {
	in, pos := false, -1
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '"':
			in = !in
		case (c == '|' || c == '>') && !in:
			pos = i
		}
	}
	return pos
}

// pipeTail: if the line is a show command with a modifier, returns the separator position and the text after it.
func (s *Session) pipeTail(kind, line string) (pos int, tail string, ok bool) {
	pos = lastSep(line)
	if pos < 0 {
		return
	}
	t0, _ := tokenize(line[:pos])
	if len(t0) == 0 {
		return
	}
	if n, _ := pick(s.rootsFor(kind), t0[0], false); n == nil || n.Word != "show" {
		return
	}
	return pos, line[pos+1:], true
}
