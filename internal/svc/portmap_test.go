package svc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSys creates a fake /sys/class/net.
type fakeSys struct{ dir string }

func (f fakeSys) nic(t *testing.T, name, mac string, phys bool, extra ...string) {
	t.Helper()
	d := filepath.Join(f.dir, name)
	_ = os.MkdirAll(d, 0o755)
	_ = os.WriteFile(d+"/type", []byte("1\n"), 0o644)
	_ = os.WriteFile(d+"/address", []byte(mac+"\n"), 0o644)
	if phys {
		_ = os.MkdirAll(d+"/device", 0o755)
	}
	for _, e := range extra {
		_ = os.MkdirAll(d+"/"+e, 0o755)
	}
}
func (f fakeSys) rm(name string) { _ = os.RemoveAll(filepath.Join(f.dir, name)) }

func TestPortMapAutodiscovery(t *testing.T) {
	f := fakeSys{t.TempDir()}
	f.nic(t, "eth0", "52:54:00:00:00:01", true)
	f.nic(t, "eth1", "52:54:00:00:00:02", true)
	f.nic(t, "docker0", "02:42:00:00:00:01", false, "bridge")
	f.nic(t, "br-x", "02:42:00:00:00:02", false, "bridge")
	f.nic(t, "veth9", "aa:00:00:00:00:09", false) // virtual: not taken while there is hardware
	_ = os.WriteFile(f.dir+"/lo", nil, 0o644)     // not a directory: ignored
	pf := filepath.Join(t.TempDir(), "ports.map")

	pm := loadPortMap(pf, f.dir)
	if got := strings.Join(pm.Names(), ","); got != "te0,te1" {
		t.Fatalf("initial ports: %s", got)
	}
	if pm.NIC("te0") != "eth0" || pm.NIC("te1") != "eth1" {
		t.Fatalf("mapping: %v", pm.m)
	}
	b, _ := os.ReadFile(pf)
	if !strings.Contains(string(b), "te0 eth0 52:54:00:00:00:01") {
		t.Fatalf("ports.map not written with MAC:\n%s", b)
	}

	// a new network card appeared later → te2 without human involvement
	f.nic(t, "eth2", "52:54:00:00:00:03", true)
	pm.Refresh(true)
	if pm.NIC("te2") != "eth2" {
		t.Fatalf("hot-plugged NIC not added: %v", pm.m)
	}
	// rename (eth1 → ens4, same MAC): port te1 stays with the same card
	f.rm("eth1")
	f.nic(t, "ens4", "52:54:00:00:00:02", true)
	pm.Refresh(true)
	if pm.NIC("te1") != "ens4" || len(pm.Names()) != 3 {
		t.Fatalf("rename not followed: %v names=%v", pm.m, pm.Names())
	}
	// daemon restart: the map is read from the file and matches
	pm2 := loadPortMap(pf, f.dir)
	if pm2.NIC("te0") != "eth0" || pm2.NIC("te1") != "ens4" || pm2.NIC("te2") != "eth2" {
		t.Fatalf("reload: %v", pm2.m)
	}
}

// An entry without a MAC is pinned manually: auto-discovery leaves it alone and does not duplicate the NIC.
func TestPortMapPinnedEntriesUntouched(t *testing.T) {
	f := fakeSys{t.TempDir()}
	f.nic(t, "eth0", "52:54:00:00:00:01", true)
	f.nic(t, "eth1", "52:54:00:00:00:02", true)
	pf := filepath.Join(t.TempDir(), "ports.map")
	_ = os.WriteFile(pf, []byte("te0 eth1\n"), 0o644) // manual: te0 = eth1
	pm := loadPortMap(pf, f.dir)
	if pm.NIC("te0") != "eth1" {
		t.Fatalf("pinned entry changed: %v", pm.m)
	}
	if pm.NIC("te1") != "eth0" { // eth0 is free → gets the next free port
		t.Fatalf("free NIC not added: %v", pm.m)
	}
}

// Without hardware (container), virtual Ethernet interfaces are taken, but only for an empty map.
func TestPortMapVirtualFallbackOnlyWhenEmpty(t *testing.T) {
	f := fakeSys{t.TempDir()}
	f.nic(t, "veth0", "aa:00:00:00:00:01", false)
	f.nic(t, "veth1", "aa:00:00:00:00:02", false)
	pf := filepath.Join(t.TempDir(), "ports.map")
	pm := loadPortMap(pf, f.dir)
	if len(pm.Names()) != 2 {
		t.Fatalf("fallback: %v", pm.m)
	}
	pf2 := filepath.Join(t.TempDir(), "ports.map")
	_ = os.WriteFile(pf2, []byte("te0 veth0\n"), 0o644)
	pm2 := loadPortMap(pf2, f.dir)
	if len(pm2.Names()) != 1 {
		t.Fatalf("virtual NIC added to a non-empty map: %v", pm2.m)
	}
}
