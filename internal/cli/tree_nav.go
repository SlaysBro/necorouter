package cli

import "fmt"

// nav holds the commands that leave a mode or return to the top; they are in the lists of many modes.
type nav struct{ exitN, logout, quit, end, do *Node }

func newNav() nav {
	exit := func(s *Session, _ []string) error {
		k := s.top().kind
		if k == modeExec || k == modePriv {
			return errQuit
		}
		s.pop()
		return nil
	}
	exitN := K("exit", "Exit from the current mode").R(exit)
	logout := K("logout", "Exit from the EXEC").R(func(*Session, []string) error { return errQuit })
	quit := K("quit", "Exit from the EXEC").R(func(*Session, []string) error { return errQuit }).Hide()
	end := K("end", "Return to privileged EXEC mode").R(func(s *Session, _ []string) error {
		s.frames = s.frames[:1]
		for len(s.frames) > 0 && s.top().kind != modePriv {
			if len(s.frames) == 1 {
				s.frames[0] = frame{kind: modePriv}
				break
			}
			s.pop()
		}
		return nil
	})
	do := K("do", "Run an EXEC command from configuration mode", A(KRest, "COMMAND", "EXEC command")).R(
		func(*Session, []string) error { return fmt.Errorf("incomplete command") })
	return nav{exitN: exitN, logout: logout, quit: quit, end: end, do: do}
}
