package svc

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"

	"necorouter/internal/config"
)

// ClearLeases empties the lease table and leaves dnsmasq running with the same configuration.
func TestClearLeasesRestartsDnsmasq(t *testing.T) {
	bin := t.TempDir()
	_ = os.WriteFile(filepath.Join(bin, "dnsmasq"), []byte("#!/bin/sh\nsleep 30\n"), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("NECOROUTER_ROOT", t.TempDir())
	_ = os.MkdirAll(DefaultPaths().Etc, 0o755)
	_ = os.WriteFile(DefaultPaths().PortMap(), []byte("te0 eth0\n"), 0o644)
	m := NewManager(DefaultPaths(), log.New(io.Discard, "", 0))
	t.Cleanup(m.Env.Sup.StopAll)

	c := config.New()
	c.IPPools = []*config.IPPool{{Name: "p", Ranges: []string{"192.168.200.10-192.168.200.20"}}}
	c.DHCP = []*config.DHCPServer{{ID: 1, Pool: "p", Mask: "28", Gateway: "192.168.200.1"}}
	c.Ifaces = []*config.Iface{{Name: "vl200", Addrs: []string{"192.168.200.1/28"}, DHCP: 1}}
	c.Ports = []*config.Port{{Name: "te0", SIs: []*config.SI{{Name: "te0/vl200", Encap: "untagged", Connect: "vl200"}}}}
	if _, err := (&dhcpSvc{}).Apply(m.Env, c); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LeaseFile(m.Env.P), []byte("1900000000 00:11:22:33:44:55 192.168.200.10 h *\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, _, err := ClearLeases(m.Env, c)
	if err != nil || n != 1 {
		t.Fatalf("ClearLeases: n=%d err=%v", n, err)
	}
	if b, _ := os.ReadFile(LeaseFile(m.Env.P)); len(b) != 0 {
		t.Fatalf("leases left: %q", b)
	}
	if !m.Env.Sup.Running("dnsmasq") {
		t.Fatal("dnsmasq must be running again after clearing")
	}
}
