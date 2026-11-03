package cli

import (
	"strings"
	"testing"
)

// ip nat destination static (NAT table) and show ntp date.
func TestNATDestinationAndNTPDate(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	runCmd(s, "configure terminal")
	if out := runCmd(s, "ip nat destination static tcp 172.16.1.2 8080 192.168.0.2 8080 hairpin"); strings.Contains(out, "%") {
		t.Fatalf("ip nat destination rejected: %q", out)
	}
	if !strings.Contains(r.Cfg.Render(), "ip nat destination static tcp 172.16.1.2 8080 192.168.0.2 8080 hairpin") {
		t.Fatalf("destination rule not rendered:\n%s", r.Cfg.Render())
	}
	if out := runCmd(s, "no ip nat destination static tcp 172.16.1.2 8080 192.168.0.2 8080"); strings.Contains(out, "%") {
		t.Fatalf("no ip nat destination rejected: %q", out)
	}
	if strings.Contains(r.Cfg.Render(), "ip nat destination") {
		t.Fatal("rule not removed")
	}
	runCmd(s, "end")
	if out := runCmd(s, "show ntp date"); strings.Contains(out, "%") || strings.TrimSpace(out) == "" {
		t.Fatalf("show ntp date failed: %q", out)
	}
}
