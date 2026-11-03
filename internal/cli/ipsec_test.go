package cli

import (
	"strings"
	"testing"
)

// Configuration of R1 from the GRE over IPsec example (hub-and-spoke): entered line by line, as from a console.
var ipsecR1 = []string{
	"configure terminal",
	"hostname R1",
	"interface loopback.0", "ip address 1.1.1.1/24",
	"interface 2ge0", "ip address 192.168.123.1/24",
	"port ge0", "service-instance 4ge0", "encapsulation untagged", "connect ip interface 2ge0",
	"interface tunnel.12", "ip address 192.168.12.1/24", "ip mtu 1400", "ip tunnel 192.168.123.1 192.168.123.2 mode gre",
	"interface tunnel.13", "ip address 192.168.13.1/24", "ip mtu 1400", "ip tunnel 192.168.123.1 192.168.123.3 mode gre",
	"router ospf 1", "network 1.1.1.0 0.0.0.255 area 0", "network 192.168.12.0 0.0.0.255 area 0", "exit",
	"crypto-ipsec ike enable",
	"crypto-ipsec profile CIPROFILE12 ike-v1",
	"nat-traversal", "mode tunnel",
	"ike-phase1", "proposal aes256-sha256-modp2048", "auth pre-shared-key ExamplePassword12",
	"ike-phase2", "protocol esp", "proposal aes256-sha256", "local-ts 192.168.123.1", "remote-ts 192.168.123.2",
	"crypto-ipsec profile CIPROFILE13 ike-v2",
	"mode tunnel",
	"ike-phase1", "proposal aes256-sha256-modp2048", "auth pre-shared-key ExamplePassword13",
	"ike-phase2", "protocol esp", "proposal aes256-sha256", "local-ts 192.168.123.1", "remote-ts 192.168.123.3", "rekey-time 3600",
	"crypto-map CMAP12 10", "match peer 192.168.123.2", "set crypto-ipsec profile CIPROFILE12",
	"crypto-map CMAP13 20", "match peer 192.168.123.3", "set crypto-ipsec profile CIPROFILE13",
	"filter-map ipv4 FMAP123 5", "match gre host 192.168.123.1 host 192.168.123.2", "set crypto-map CMAP12 peer 192.168.123.2",
	"filter-map ipv4 FMAP123 10", "match udp host 192.168.123.2 eq 4500 host 192.168.123.1 eq 4500", "set crypto-map CMAP12 peer 192.168.123.2",
	"filter-map ipv4 FMAP123 15", "match gre host 192.168.123.1 host 192.168.123.3", "set crypto-map CMAP13 peer 192.168.123.3",
	"filter-map ipv4 FMAP123 20", "match udp host 192.168.123.3 eq 4500 host 192.168.123.1 eq 4500", "set crypto-map CMAP13 peer 192.168.123.3",
	"filter-map ipv4 FMAP123 30", "match any any any", "set accept",
	"interface 2ge0", "set filter-map in FMAP123 10",
	"interface tunnel.12", "set filter-map in FMAP123 10",
	"interface tunnel.13", "set filter-map in FMAP123 10",
	"end",
}

func TestIPsecGuideExample(t *testing.T) {
	r := newRouterWith(t, "ge0 eth0\nge1 eth1\n", false)
	s := sessionOn(r, "root")
	for _, l := range ipsecR1 {
		if out := runCmd(s, l); strings.Contains(out, "%") {
			t.Fatalf("%q: %s", l, out)
		}
	}
	c := r.Cfg
	if !c.IPsecIKE || len(c.IPsecProfiles) != 2 || len(c.CryptoMaps) != 2 {
		t.Fatalf("ike=%v profiles=%d maps=%d", c.IPsecIKE, len(c.IPsecProfiles), len(c.CryptoMaps))
	}
	p := c.FindIPsecProfile("CIPROFILE13")
	if p == nil || p.Version != "ike-v2" || p.Rekey != 3600 || p.LocalTS != "192.168.123.1" || p.PSK != "ExamplePassword13" {
		t.Fatalf("profile: %+v", p)
	}
	if m := c.FindCryptoMap("CMAP12", 10); m == nil || m.Peer != "192.168.123.2" || m.Profile != "CIPROFILE12" {
		t.Fatalf("crypto-map: %+v", m)
	}
	rule := c.FindFilterMap("FMAP123").Rule(5)
	if rule.CryptoMap != "CMAP12" || rule.CryptoPeer != "192.168.123.2" || rule.Action != "" {
		t.Fatalf("filter-map rule: %+v", rule)
	}
	// Render → Replay → Render gives the same result
	text := c.Render()
	mustContain(t, text, "crypto-ipsec ike enable", "crypto-ipsec profile CIPROFILE12 ike-v1", " nat-traversal",
		"  auth pre-shared-key ExamplePassword12", "crypto-map CMAP13 20", " match peer 192.168.123.3",
		" set crypto-ipsec profile CIPROFILE13", " set crypto-map CMAP12 peer 192.168.123.2", "  rekey-time 3600")
	r2 := newRouterWith(t, "ge0 eth0\nge1 eth1\n", false)
	if errs := r2.Replay(text); len(errs) > 0 {
		t.Fatalf("replay: %v", errs)
	}
	if got := r2.Cfg.Render(); got != text {
		t.Fatalf("round trip differs:\n--- first\n%s\n--- second\n%s", text, got)
	}
}

func TestIPsecNegationAndGuards(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	runCmd(s, "configure terminal")
	runCmd(s, "crypto-ipsec profile P1 ike-v2")
	runCmd(s, "crypto-map M 1")
	runCmd(s, "set crypto-ipsec profile P1")
	runCmd(s, "exit")
	if out := runCmd(s, "crypto-ipsec profile P1 ike-v1"); !strings.Contains(out, "ike-v2") {
		t.Fatalf("version change must be refused: %q", out)
	}
	if out := runCmd(s, "no crypto-ipsec profile P1"); !strings.Contains(out, "used by crypto-map") {
		t.Fatalf("profile in use must not be removed: %q", out)
	}
	runCmd(s, "filter-map ipv4 F 10")
	runCmd(s, "match any any any")
	runCmd(s, "set crypto-map M peer 10.0.0.2")
	runCmd(s, "exit")
	if out := runCmd(s, "no crypto-map M"); !strings.Contains(out, "used by filter-map") {
		t.Fatalf("crypto-map in use must not be removed: %q", out)
	}
	// set accept replaces set crypto-map and vice versa
	runCmd(s, "filter-map ipv4 F 10")
	runCmd(s, "set accept")
	if rule := r.Cfg.FindFilterMap("F").Rule(10); rule.CryptoMap != "" || rule.Action != "accept" {
		t.Fatalf("rule: %+v", rule)
	}
	runCmd(s, "exit")
	if out := runCmd(s, "no crypto-map M"); strings.Contains(out, "%") {
		t.Fatalf("no crypto-map: %q", out)
	}
	if out := runCmd(s, "no crypto-ipsec profile P1"); strings.Contains(out, "%") {
		t.Fatalf("no crypto-ipsec profile: %q", out)
	}
	if len(r.Cfg.IPsecProfiles) != 0 || len(r.Cfg.CryptoMaps) != 0 {
		t.Fatal("not removed")
	}
	// invalid values
	runCmd(s, "crypto-ipsec profile P2 ike-v1")
	runCmd(s, "ike-phase2")
	for _, l := range []string{"local-ts 300.1.1.1", "local-ts 10.0.0.0/33", "proposal a b", "rekey-time 80000"} {
		if out := runCmd(s, l); !strings.Contains(out, "%") {
			t.Errorf("%q must be rejected, got %q", l, out)
		}
	}
	if out := runCmd(s, "local-ts 10.0.0.1,192.168.1.0/24"); strings.Contains(out, "%") {
		t.Errorf("list of selectors rejected: %q", out)
	}
}

func TestIPsecHelpAndAudit(t *testing.T) {
	_, s := logSetup(t)
	runCmd(s, "configure terminal")
	if h := s.Help("crypto-ipsec profile P "); !strings.Contains(h, "ike-v1") || !strings.Contains(h, "ike-v2") {
		t.Fatalf("help: %q", h)
	}
	runCmd(s, "crypto-ipsec profile P ike-v1")
	runCmd(s, "ike-phase1")
	if !strings.HasSuffix(s.Prompt(), "(config-ike-phase1)#") {
		t.Fatalf("prompt %q", s.Prompt())
	}
	runCmd(s, "auth pre-shared-key TopSecretKey123")
	runCmd(s, "local-psk id r1 data AnotherSecret456")
	runCmd(s, "end")
	out := runCmd(s, "show log | include AUDIT")
	mustNotContain(t, out, "TopSecretKey123", "AnotherSecret456")
	mustContain(t, out, "auth pre-shared-key ***", "local-psk id r1 data ***")
}

func TestIPsecShowNoDaemon(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	out := runCmd(s, "show crypto-ipsec ike connections")
	if out == "" {
		t.Fatal("empty output")
	}
	runCmd(s, "show crypto-ipsec ike security-associations")
}
