package cli

import (
	"strings"
	"testing"
)

func TestOSPFNetworkForms(t *testing.T) {
	_, out := replay(t, `
router ospf 1
 ospf router-id 1.1.1.1
 network 10.10.10.0/30 area 0
 network 192.168.100.0 255.255.255.224 area 0
 network 192.168.200.0 0.0.0.15 area 0.0.0.0
 network 192.168.99.5/29 area 10
`)
	mustContain(t, out,
		" network 10.10.10.0/30 area 0\n",
		" network 192.168.100.0/27 area 0\n",
		" network 192.168.200.0/28 area 0.0.0.0\n",
		" network 192.168.99.0/29 area 10\n") // host bits dropped
	mustNotContain(t, out, "192.168.99.5")
}

func TestOSPFNetworkNoAndBadInput(t *testing.T) {
	r := newTestRouter(t)
	s := &Session{R: r, User: "root", Role: "admin", noApply: true, frames: []frame{{kind: modeConfig}}}
	run := func(l string) string { var sb strings.Builder; s.Exec(l, &sb); return sb.String() }
	run("router ospf 1")
	run("network 10.0.0.0/24 area 0")
	run("network 10.1.0.0 0.0.0.255 area 0")
	if len(r.Cfg.OSPF.Nets) != 2 {
		t.Fatalf("nets: %+v", r.Cfg.OSPF.Nets)
	}
	run("no network 10.0.0.0/24")
	run("no network 10.1.0.0 255.255.255.0")
	if len(r.Cfg.OSPF.Nets) != 0 {
		t.Fatalf("no network failed: %+v", r.Cfg.OSPF.Nets)
	}
	for _, bad := range []string{"network 10.0.0.0/33 area 0", "network 10.0.0.0/243 area 0", "network 10.0.0.0 255.0.255.0 area 0", "network 10.0.0.0/24 area abc", "network 10.0.0.0/24 area 99999999999", "network 10.0.0.0/24"} {
		if o := run(bad); !strings.Contains(o, "%") {
			t.Errorf("%q accepted: %q", bad, o)
		}
	}
	if len(r.Cfg.OSPF.Nets) != 0 {
		t.Fatalf("invalid input stored: %+v", r.Cfg.OSPF.Nets)
	}
}

func TestOSPFHelp(t *testing.T) {
	r := newTestRouter(t)
	s := &Session{R: r, User: "root", Role: "admin", noApply: true, frames: []frame{{kind: modeConfig}}}
	s.Exec("router ospf 1", nil)
	for line, want := range map[string][]string{
		"network ":                         {"A.B.C.D/M", "A.B.C.D"},
		"network 10.0.0.0/30 ":             {"area"},
		"network 10.0.0.0/30 area ":        {"AREA"},
		"network 10.0.0.0 ":                {"MASK"},
		"network 10.0.0.0 0.0.0.3 ":        {"area"},
		"network 10.0.0.0 0.0.0.3 area 0 ": {"<cr>"},
	} {
		out := s.Help(line)
		for _, w := range want {
			if !strings.Contains(out, w) {
				t.Errorf("Help(%q) lacks %q: %s", line, w, out)
			}
		}
	}
}

// A DHCP/NAT pool of a single address is accepted and stored as a single address.
func TestSingleAddressPools(t *testing.T) {
	_, out := replay(t, `
ip pool one 192.168.200.5
ip pool many 192.168.200.10-192.168.200.20
ip nat pool S 10.0.0.7
ip nat pool R 10.0.0.1-10.0.0.9
`)
	mustContain(t, out, "ip pool one 192.168.200.5\n", "ip pool many 192.168.200.10-192.168.200.20\n",
		"ip nat pool S 10.0.0.7\n", "ip nat pool R 10.0.0.1-10.0.0.9\n")
	// the reverse form is reproducible
	_, again := replay(t, out)
	if again != out {
		t.Fatalf("not idempotent:\n%s\n---\n%s", out, again)
	}
	// invalid values are still rejected
	r := newTestRouter(t)
	s := &Session{R: r, User: "root", Role: "admin", noApply: true, frames: []frame{{kind: modeConfig}}}
	for _, bad := range []string{"ip pool x 10.0.0.9-10.0.0.1", "ip pool x 10.0.0.256", "ip pool x abc"} {
		var sb strings.Builder
		s.Exec(bad, &sb)
		if !strings.Contains(sb.String(), "%") || len(r.Cfg.IPPools) != 0 {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestInterfaceAddressForms(t *testing.T) {
	r, out := replay(t, `
interface a
 ip address 10.1.1.1/24
interface b
 ip address 10.2.2.2 255.255.255.252
interface c
 ip address 10.3.3.3/24
 ip address 10.4.4.4 255.255.255.0
`)
	mustContain(t, out, " ip address 10.1.1.1/24\n", " ip address 10.2.2.2/30\n", " ip address 10.4.4.4/24\n")
	mustNotContain(t, out, "10.3.3.3")
	s := &Session{R: r, User: "root", Role: "admin", noApply: true, frames: []frame{{kind: modeConfig}}}
	s.Exec("interface a", nil)
	for _, c := range []struct{ line, hint string }{
		{"ip address 10.1.1.1/243", "prefix length '243'"},
		{"ip address 10.1.1.1/33", "prefix length '33'"},
		{"ip address 999.1.1.1/24", "Invalid address"},
		{"ip address 10.1.1.1 255.0.255.0", "Invalid mask"},
	} {
		var sb strings.Builder
		s.Exec(c.line, &sb)
		if !strings.Contains(sb.String(), c.hint) {
			t.Errorf("%q: want hint %q, got %q", c.line, c.hint, sb.String())
		}
		if got := r.Cfg.FindIface("a").Addrs; len(got) != 1 || got[0] != "10.1.1.1/24" {
			t.Errorf("%q changed address: %v", c.line, got)
		}
	}
	// standard prefixes
	for _, p := range []string{"0", "8", "16", "24", "30", "31", "32"} {
		var sb strings.Builder
		s.Exec("ip address 10.9.9.9/"+p, &sb)
		if strings.Contains(sb.String(), "%") {
			t.Errorf("/%s rejected: %s", p, sb.String())
		}
	}
}
