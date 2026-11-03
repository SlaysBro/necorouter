package cli

// Power management (privileged mode; via do — also from configuration mode):
//   reload                 reboot (asks for confirmation, warns about unsaved configuration)
//   reload in <1-600>      reboot in N minutes (one confirmation when scheduling)
//   reload at <HH:MM>      reboot at the given time (the nearest one; one confirmation)
//   reload cancel          cancel a scheduled reboot
//   poweroff               system shutdown (confirmation)
//   copy empty-config startup-config   factory configuration into startup; applied after reload,
//                                      accounts created by the router are removed when it is applied.
//
// Reboot and shutdown are performed by a system command: the daemon receives SIGTERM and shuts down
// cleanly (MarkClean), so no crash-config is created on a planned reboot.

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"necorouter/internal/config"
)

var hhmmRe = regexp.MustCompile(`^([01]?\d|2[0-3]):([0-5]\d)$`)

// powerCall performs a system reboot or shutdown. Replaced in tests.
func (r *Router) powerCall(kind string) error {
	if r.power != nil {
		return r.power(kind)
	}
	return systemPower(kind)
}

// systemPower runs reboot/poweroff and does not wait for completion: the daemon stops by itself on SIGTERM.
func systemPower(kind string) error {
	if p, err := exec.LookPath("systemctl"); err == nil {
		return exec.Command(p, "--no-block", kind).Start()
	}
	p, err := exec.LookPath(kind)
	if err != nil {
		return fmt.Errorf("%s: command not found", kind)
	}
	return exec.Command(p).Start()
}

// unsavedChanges: running-config differs from startup-config (the description from write description is ignored).
func (r *Router) unsavedChanges() bool {
	b, err := os.ReadFile(r.P.Startup())
	if err != nil {
		return true
	}
	var keep []string
	for _, ln := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(ln, "! description:") {
			keep = append(keep, ln)
		}
	}
	return strings.TrimRight(strings.Join(keep, "\n"), "\n") != strings.TrimRight(r.Cfg.Render(), "\n")
}

// saveWarning: a warning if there are unsaved changes.
func (s *Session) saveWarning() string {
	if s.R.unsavedChanges() {
		return "% Unsaved changes in running-config. Please save config and reload.\n"
	}
	return ""
}

// scheduleReload puts a reboot into daemon memory (the schedule is not preserved after a reboot).
func (r *Router) scheduleReload(at time.Time) {
	r.pmu.Lock()
	defer r.pmu.Unlock()
	if r.reloadTimer != nil {
		r.reloadTimer.Stop()
	}
	r.reloadAt = at
	r.reloadTimer = time.AfterFunc(time.Until(at), func() {
		r.pmu.Lock()
		r.reloadTimer = nil
		r.pmu.Unlock()
		_ = r.powerCall("reboot")
	})
}

// cancelReload cancels a scheduled reboot; reports whether there was one.
func (r *Router) cancelReload() bool {
	r.pmu.Lock()
	defer r.pmu.Unlock()
	if r.reloadTimer == nil {
		return false
	}
	r.reloadTimer.Stop()
	r.reloadTimer = nil
	return true
}

func cmdReload(s *Session, _ []string) error {
	if s.noApply { // replaying a configuration never reboots the machine
		return nil
	}
	return s.ask(s.saveWarning(), "reboot system?", func(s *Session) error {
		s.Printf("...reboot...\n")
		return s.R.powerCall("reboot")
	})
}

func cmdReloadIn(s *Session, v []string) error {
	if s.noApply {
		return nil
	}
	n, err := strconv.Atoi(v[0])
	if err != nil || n < 1 || n > 600 {
		return fmt.Errorf("value must be 1-600 minutes")
	}
	at := time.Now().Add(time.Duration(n) * time.Minute)
	return s.ask(s.saveWarning(), fmt.Sprintf("reboot system in %d minute(s)?", n), func(s *Session) error {
		s.R.scheduleReload(at)
		s.Printf("Reload scheduled at %s\n", at.Format("15:04"))
		return nil
	})
}

func cmdReloadAt(s *Session, v []string) error {
	if s.noApply {
		return nil
	}
	m := hhmmRe.FindStringSubmatch(v[0])
	if m == nil {
		return fmt.Errorf("time must be HH:MM (00-23, 00-59)")
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	now := time.Now()
	at := time.Date(now.Year(), now.Month(), now.Day(), h, mi, 0, 0, now.Location())
	if !at.After(now) {
		at = at.Add(24 * time.Hour)
	}
	return s.ask(s.saveWarning(), "reboot system at "+at.Format("15:04")+"?", func(s *Session) error {
		s.R.scheduleReload(at)
		s.Printf("Reload scheduled at %s\n", at.Format("15:04"))
		return nil
	})
}

func cmdReloadCancel(s *Session, _ []string) error {
	if s.noApply {
		return nil
	}
	if !s.R.cancelReload() {
		s.Printf("%% No reload is scheduled\n")
		return nil
	}
	s.Printf("Scheduled reload canceled\n")
	return nil
}

func cmdPoweroff(s *Session, _ []string) error {
	if s.noApply {
		return nil
	}
	return s.ask(s.saveWarning(), "poweroff system?", func(s *Session) error {
		s.Printf("...poweroff...\n")
		return s.R.powerCall("poweroff")
	})
}

func cmdEmptyConfig(s *Session, _ []string) error {
	if s.noApply {
		return nil
	}
	warn := "% Configuration will be reset to factory defaults after reload.\n" +
		"% User accounts created on this router will be deleted when it is applied.\n"
	return s.ask(warn, "Continue?", func(s *Session) error {
		if err := s.R.writeStartup(config.New().Render()); err != nil {
			return err
		}
		s.journal("copy empty-config startup-config")
		s.Printf("[OK]\n")
		return nil
	})
}

// reloadNode: reload and its variants.
func reloadNode() *Node {
	return K("reload", "Halt and perform a cold restart",
		K("in", "Reboot device automatically after a certain timespan (in minutes)",
			A(KInt, "<1-600>", "Minutes until reboot").Rng(1, 600).R(cmdReloadIn)),
		K("at", "Reboot device automatically at a certain time (within 24 hours)",
			A(KWord, "<HH:MM>", "Time of reboot, HH=00 to 23; MM=00 to 59").Chk(hhmmRe.MatchString).R(cmdReloadAt)),
		K("cancel", "Cancel the scheduled reboot").R(cmdReloadCancel),
	).R(cmdReload)
}

// poweroffNode: poweroff — system shutdown.
func poweroffNode() *Node {
	return K("poweroff", "Turn system off").R(cmdPoweroff)
}
