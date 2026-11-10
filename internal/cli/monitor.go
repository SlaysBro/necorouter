package cli

import (
	"fmt"
	"strings"

	"necorouter/internal/logx"
)

// terminal monitor [LINE] — journal messages that appear during the session are printed to the screen.
// LINE (if set) keeps only the lines containing that word. no terminal monitor — turns it off.
func monitorNode() *Node {
	on := func(s *Session, word string) error {
		if s.noApply {
			return nil
		}
		s.monitorOff()
		s.monMu.Lock()
		s.monOut = s.W
		s.monMu.Unlock()
		cancel := logx.Subscribe(func(e logx.Entry) {
			line := e.String(false)
			if word != "" && !strings.Contains(line, word) {
				return
			}
			s.monMu.Lock()
			w := s.monOut
			s.monMu.Unlock()
			if w != nil {
				fmt.Fprintf(w, "%s\n", line)
			}
		})
		s.monMu.Lock()
		s.monStop = cancel
		s.monMu.Unlock()
		return nil
	}
	return K("monitor", "Show new log messages in this session (no terminal monitor to stop)",
		A(KWord, "LINE", "Show only messages that contain this word").R(func(s *Session, v []string) error {
			return on(s, v[0])
		}),
	).R(func(s *Session, _ []string) error { return on(s, "") }).
		N(func(s *Session, _ []string) error {
			s.monitorOff()
			return nil
		})
}

// monitorOff removes this session's subscription to the journal.
func (s *Session) monitorOff() {
	s.monMu.Lock()
	stop := s.monStop
	s.monStop = nil
	s.monOut = nil
	s.monMu.Unlock()
	if stop != nil {
		stop()
	}
}

// Close releases session resources (the journal subscription); called when the connection drops.
func (s *Session) Close() { s.monitorOff() }
