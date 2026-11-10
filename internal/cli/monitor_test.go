package cli

import (
	"strings"
	"testing"

	"necorouter/internal/config"
	"necorouter/internal/logx"
)

// terminal monitor [LINE]: new journal messages are printed to the session; no terminal monitor turns it off.
func TestTerminalMonitor(t *testing.T) {
	_, s := logSetup(t)
	defer s.Close()
	var sb strings.Builder
	s.Exec("terminal monitor", &sb)
	logx.Infof("DHCP", "lease given to client")
	logx.Infof("NAT", "translation added")
	out := sb.String()
	if !strings.Contains(out, "lease given to client") || !strings.Contains(out, "translation added") {
		t.Fatalf("monitor output: %q", out)
	}
	sb.Reset()
	s.Exec("no terminal monitor", &sb)
	logx.Infof("DHCP", "after stop")
	if strings.Contains(sb.String(), "after stop") {
		t.Fatalf("still monitoring after 'no terminal monitor': %q", sb.String())
	}
}

func TestTerminalMonitorWordFilter(t *testing.T) {
	_, s := logSetup(t)
	defer s.Close()
	var sb strings.Builder
	s.Exec("terminal monitor DHCP", &sb)
	logx.Infof("DHCP", "offer sent")
	logx.Infof("NAT", "other message")
	out := sb.String()
	if !strings.Contains(out, "offer sent") || strings.Contains(out, "other message") {
		t.Fatalf("filter output: %q", out)
	}
}

func TestTerminalMonitorAdminOnly(t *testing.T) {
	r, _ := logSetup(t)
	u := NewSession(r, &config.User{Name: "noc1", Role: "noc"})
	defer u.Close()
	if out := runCmd(u, "terminal monitor"); !strings.Contains(out, "% Invalid input") {
		t.Fatalf("noc must not be offered terminal monitor: %q", out)
	}
}
