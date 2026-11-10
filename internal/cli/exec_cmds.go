package cli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// external runs an external utility and passes its output to the session. The command
// lives until it finishes on its own or the user interrupts it (Ctrl-C
// → Session.Interrupt). The router lock is released for that time, so
// other sessions and the CLI itself do not hang.
func external(s *Session, bin string, args ...string) error {
	p, err := exec.LookPath(bin)
	if err != nil {
		return fmt.Errorf("%s is not installed", bin)
	}
	s.cmu.Lock()
	ctx := s.ctx
	s.cmu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.Command(p, args...)
	cmd.Stdout, cmd.Stderr = s.W, s.W
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // the session's Ctrl-C must not hit the daemon
	if err := cmd.Start(); err != nil {
		return err
	}
	s.R.mu.Unlock()
	defer s.R.mu.Lock()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			// SIGINT: ping/traceroute print final statistics and exit by themselves
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-done:
		}
	}()
	_ = cmd.Wait()
	close(done)
	return nil
}

// pingArgs — ping arguments: WITHOUT -c, i.e. continuous until interrupted by the user.
func pingArgs(host string) []string { return []string{"-W", "2", host} }

// ---------------- ping / traceroute ----------------

func cmdPing(s *Session, v []string) error {
	if strings.HasPrefix(v[0], "-") {
		return fmt.Errorf("invalid host")
	}
	return external(s, "ping", pingArgs(v[0])...)
}

func cmdTrace(s *Session, v []string) error {
	if strings.HasPrefix(v[0], "-") {
		return fmt.Errorf("invalid host")
	}
	return external(s, "traceroute", "-n", "-w", "2", "-m", "20", v[0])
}

func filterPort(all, name string) string {
	lines := strings.Split(strings.TrimRight(all, "\n"), "\n")
	var out []string
	for n, l := range lines {
		if n == 0 || strings.HasPrefix(l, name+" ") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n") + "\n"
}

// cmdHelp: the help system is '?' (the list of commands of the mode), Tab (completion) and the keys of a command.
func cmdHelp(s *Session, _ []string) error {
	s.Printf("Type ? for the commands of the current mode.\nTab completes a word.\nCtrl-C stops a running command; Ctrl-L clears the screen.\n")
	return nil
}
