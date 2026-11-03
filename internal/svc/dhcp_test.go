package svc

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"necorouter/internal/config"
)

// A single-address pool gives a dhcp-range with equal start and end.
func TestDHCPSingleAddressPool(t *testing.T) {
	bin := t.TempDir()
	_ = os.WriteFile(filepath.Join(bin, "dnsmasq"), []byte("#!/bin/sh\nsleep 30\n"), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	root := t.TempDir()
	t.Setenv("NECOROUTER_ROOT", root)
	_ = os.MkdirAll(DefaultPaths().Etc, 0o755)
	_ = os.WriteFile(DefaultPaths().PortMap(), []byte("te0 eth0\n"), 0o644)
	m := NewManager(DefaultPaths(), log.New(io.Discard, "", 0))
	t.Cleanup(m.Env.Sup.StopAll)

	c := config.New()
	c.IPPools = []*config.IPPool{{Name: "one", From: "192.168.200.5", To: "192.168.200.5"}}
	c.DHCP = []*config.DHCPServer{{ID: 1, Pool: "one", Mask: "28", Gateway: "192.168.200.1"}}
	c.Ifaces = []*config.Iface{{Name: "vl200", Addrs: []string{"192.168.200.1/28"}, DHCP: 1}}
	c.Ports = []*config.Port{{Name: "te0", SIs: []*config.SI{{Name: "te0/vl200", Encap: "untagged", Connect: "vl200"}}}}
	if _, err := (&dhcpSvc{}).Apply(m.Env, c); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(m.Env.P.Run + "/dnsmasq.conf")
	if !strings.Contains(string(b), "dhcp-range=set:s1,192.168.200.5,192.168.200.5,255.255.255.240,") {
		t.Fatalf("dnsmasq.conf:\n%s", b)
	}
}
