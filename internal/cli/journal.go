package cli

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Configuration operations journal (config-features service): who did what and when.
// File /var/lib/necorouter/config.journal, mode 0600; when journalMax is exceeded,
// the second half of the records remains.
const journalMax = 256 * 1024

// journal appends a record if the service is enabled. Called from command handlers
// (r.mu is already held). No records are made while replaying a configuration.
func (s *Session) journal(f string, a ...any) {
	if s.noApply || !s.cfg().Features["config-features"] {
		return
	}
	msg := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, fmt.Sprintf(f, a...))
	line := fmt.Sprintf("%s %s %s\n", time.Now().Format("2006-01-02 15:04:05"), s.User, msg)
	path := s.R.P.Journal()
	if err := os.MkdirAll(s.R.P.Var, 0o755); err != nil {
		return
	}
	if st, err := os.Stat(path); err == nil && st.Size() > journalMax {
		if b, err := os.ReadFile(path); err == nil {
			b = b[len(b)/2:]
			if i := strings.IndexByte(string(b), '\n'); i >= 0 {
				b = b[i+1:]
			}
			_ = writeAtomic(path, string(b))
		}
	}
	f2, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f2.Close()
	_, _ = f2.WriteString(line)
}

// journalTail returns the last n records.
func (r *Router) journalTail(n int) []string {
	b, err := os.ReadFile(r.P.Journal())
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
