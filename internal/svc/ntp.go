package svc

import (
	"fmt"
	"strings"

	"necorouter/internal/config"
)

// ntpSvc runs chronyd as an NTP client, only if "ntp server" is set.
type ntpSvc struct{}

func (*ntpSvc) Name() string { return "ntp" }

func (*ntpSvc) Apply(env *Env, c *config.Config) ([]string, error) {
	if len(c.NTPServers) == 0 {
		env.Sup.Stop("chronyd")
		return nil, nil
	}
	bin := find("chronyd")
	if bin == "" {
		return nil, fmt.Errorf("chrony is not installed (needed for ntp server)")
	}
	var b strings.Builder
	for _, s := range c.NTPServers {
		fmt.Fprintf(&b, "server %s iburst\n", s)
	}
	fmt.Fprintf(&b, "makestep 1.0 3\ndriftfile %s/chrony.drift\npidfile %s/chronyd.pid\n", env.P.Var, env.P.Run)
	b.WriteString("bindcmdaddress 127.0.0.1\ncmdport 323\nrtcsync\n")
	conf := env.P.Run + "/chrony.conf"
	if err := writeFileAtomic(conf, []byte(b.String()), 0o644); err != nil {
		return nil, err
	}
	return nil, env.Sup.Ensure("chronyd", b.String(), []string{bin, "-n", "-f", conf, "-u", "root"})
}

func (*ntpSvc) Stop(env *Env) error { env.Sup.Stop("chronyd"); return nil }
func (*ntpSvc) Status(env *Env) string {
	if env.Sup.Running("chronyd") {
		return "running (chronyd)"
	}
	return "idle"
}

// NTPDate is the current system date and time (show ntp date).
func NTPDate() (string, error) { return run("date") }

func NTPStatus(env *Env) (string, error) {
	if !env.Sup.Running("chronyd") {
		return "", fmt.Errorf("NTP client is not running")
	}
	c := find("chronyc")
	if c == "" {
		return "", fmt.Errorf("chronyc is not installed")
	}
	return run(c, "-h", "127.0.0.1", "tracking")
}

// NTPSync immediately sets the clock to the servers' time (ntp synchronize).
func NTPSync(env *Env) (string, error) {
	if !env.Sup.Running("chronyd") {
		return "", fmt.Errorf("NTP client is not running (set 'ntp server' first)")
	}
	c := find("chronyc")
	if c == "" {
		return "", fmt.Errorf("chronyc is not installed")
	}
	// chronyc without -h talks to chronyd over its Unix socket, where root may issue commands.
	// Over UDP (-h 127.0.0.1, cmdport 323) chronyd answers write commands with "501 Not authorised".
	return run(c, "makestep")
}
