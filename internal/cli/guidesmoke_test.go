package cli

import (
	"strings"
	"testing"
)

func TestGuideConfigLinesAccepted(t *testing.T) {
	r := newRouterWith(t, "ge0 eth0\nge1 eth1\nge2 eth2\n", false)
	s := sessionOn(r, "root")
	lines := []string{
		"configure terminal",
		"interface wan", "ip address 172.16.1.2/28", "ip nat outside", "port ge0",
		"service-instance wan", "encapsulation untagged", "connect ip interface wan", "exit", "exit",
		"ip route 0.0.0.0/0 172.16.1.1",
		"interface v100", "ip address 192.168.0.1/29", "ip nat inside", "exit",
		"port ge1", "service-instance v100", "encapsulation dot1q 100 exact", "rewrite pop 1", "connect ip interface v100", "exit", "exit",
		"ip pool lan 192.168.0.10-192.168.0.20",
		"ip nat pool nat 192.168.0.1-192.168.0.23",
		"ip nat source dynamic inside-to-outside pool nat overload interface wan",
		"ip nat source static tcp 192.168.0.2 2027 172.16.1.2 2027",
		"ip nat destination static tcp 172.16.1.2 8080 192.168.0.2 8080 hairpin",
		"interface tunnel.0", "ip address 10.10.10.1/30", "ip tunnel 172.16.1.2 172.16.2.2 mode gre",
		"ip ospf authentication message-digest", "ip ospf message-digest-key 1 md5 P@ssw0rd", "exit",
		"router ospf 1", "network 10.10.10.0/30 area 0", "network 192.168.0.0/24 area 0",
		"passive-interface default", "no passive-interface tunnel.0", "exit",
		"dhcp-server 1", "pool lan 1", "mask 29", "gateway 192.168.0.9", "dns 192.168.0.2", "domain-search example.local", "exit",
		"interface v100", "dhcp-server 1", "exit",
		"ntp timezone utc+3", "ntp server 172.16.1.1", "ntp synchronize",
		"username net_admin", "role admin",
	}
	for _, l := range lines {
		if out := runCmd(s, l); strings.Contains(out, "% ") && !strings.Contains(out, "NTP client is not running") && !strings.Contains(out, "access denied") {
			// apply errors (no kernel/services) are acceptable, syntax errors are not
			if strings.Contains(out, "Invalid") || strings.Contains(out, "Unrecognized") || strings.Contains(out, "Incomplete") || strings.Contains(out, "Ambiguous") {
				t.Errorf("%q: %s", l, out)
			}
		}
	}
	if u := r.Cfg.FindUser("net_admin"); u == nil || u.Role != "admin" {
		t.Errorf("net_admin not created as admin")
	}
	if len(r.Cfg.Routes) == 0 || r.Cfg.OSPF == nil {
		t.Errorf("routes or ospf missing")
	}
}
