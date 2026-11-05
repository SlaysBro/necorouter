package cli

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"necorouter/internal/config"
	"necorouter/internal/svc"
)

// newTestRouter creates a router without services: touches nothing in the system.
func newTestRouter(t *testing.T) *Router {
	t.Helper()
	return newRouterWith(t, "te0 eth0\nte1 eth1\nte2 eth2\n", false)
}

// newRouterWith: the port map is set explicitly; withServices keeps services
// (needed for checks on a kernel, see addr_test.go).
func newRouterWith(t *testing.T, portmap string, withServices bool) *Router {
	t.Helper()
	root := t.TempDir()
	t.Setenv("NECOROUTER_ROOT", root)
	// fixed port map: the test does not depend on the machine's network cards
	_ = os.MkdirAll(svc.DefaultPaths().Etc, 0o755)
	_ = os.WriteFile(svc.DefaultPaths().PortMap(), []byte(portmap), 0o644)
	mgr := svc.NewManager(svc.DefaultPaths(), log.New(io.Discard, "", 0))
	if !withServices {
		mgr.Services = nil
	}
	return NewRouter(mgr, svc.DefaultPaths())
}

func replay(t *testing.T, text string) (*Router, string) {
	t.Helper()
	r := newTestRouter(t)
	if errs := r.Replay(text); len(errs) > 0 {
		t.Fatalf("replay errors: %v", errs)
	}
	return r, r.Cfg.Render()
}

func mustContain(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}
func mustNotContain(t *testing.T, got string, bad ...string) {
	t.Helper()
	for _, w := range bad {
		if strings.Contains(got, w) {
			t.Errorf("unexpected %q in:\n%s", w, got)
		}
	}
}

// Regression: encapsulation untagged must not disappear from port/service-instance.
func TestRenderUntaggedServiceInstance(t *testing.T) {
	_, out := replay(t, `
port te1
 service-instance te1/isp
  encapsulation untagged
 service-instance te1/vl100
  encapsulation dot1q 100 exact
  rewrite pop 1
`)
	mustContain(t, out,
		" service-instance te1/isp\n  encapsulation untagged\n",
		" service-instance te1/vl100\n  encapsulation dot1q 100 exact\n  rewrite pop 1\n")
}

// Regression: ip nat source dynamic preserves the rule name, pool name, and overload target.
func TestRenderNATDynamicAndStatic(t *testing.T) {
	_, out := replay(t, `
ip nat pool server-pool 192.168.100.1-192.168.100.30
ip nat pool inside-pool 192.168.3.1-192.168.3.30
ip nat source dynamic inside-to-outside pool server-pool overload 172.16.1.2
interface isp
 ip address 172.16.4.2/28
ip nat source dynamic lan pool inside-pool overload interface isp
ip nat source static tcp 192.168.100.2 8080 172.16.1.2 8080
ip nat source static 10.0.0.1 10.0.0.2
`)
	mustContain(t, out,
		"ip nat pool server-pool 192.168.100.1-192.168.100.30\n",
		"ip nat source dynamic inside-to-outside pool server-pool overload 172.16.1.2\n",
		"ip nat source dynamic lan pool inside-pool overload interface isp\n",
		"ip nat source static tcp 192.168.100.2 8080 172.16.1.2 8080\n",
		"ip nat source static 10.0.0.1 10.0.0.2\n")
	mustNotContain(t, out, "overload \n", "pool  ")
}

// An incomplete NAT command must be rejected by the parser, not produce a "truncated" line in the config.
func TestIncompleteNATDynamicRejected(t *testing.T) {
	r := newTestRouter(t)
	s := &Session{R: r, User: "root", Role: "admin", noApply: true, frames: []frame{{kind: "config"}}}
	var sb strings.Builder
	s.Exec("ip nat source dynamic inside-to-outside pool X overload", &sb)
	s.Exec("ip nat source dynamic inside-to-outside pool X", &sb)
	if len(r.Cfg.NATDyn) != 0 {
		t.Fatalf("incomplete command stored: %+v", r.Cfg.NATDyn[0])
	}
	if !strings.Contains(sb.String(), "%") {
		t.Fatalf("no error reported: %q", sb.String())
	}
}

// Regression: without DHCP in the configuration there is no ip pool / dhcp-server; addresses and routes remain.
func TestNoDHCPCommandsWithoutDHCP(t *testing.T) {
	_, out := replay(t, `
interface isp
 ip address 172.16.1.2/28
interface tunnel.0
 ip address 10.10.10.1/30
 ip tunnel 172.16.1.2 172.16.2.2 mode gre
ip route 0.0.0.0/0 172.16.1.1
`)
	mustContain(t, out, " ip address 172.16.1.2/28\n", " ip address 10.10.10.1/30\n",
		" ip tunnel 172.16.1.2 172.16.2.2 mode gre\n", "ip route 0.0.0.0/0 172.16.1.1\n")
	mustNotContain(t, out, "ip pool", "dhcp-server")
	// and in the factory configuration too
	mustNotContain(t, config.New().Render(), "ip pool", "dhcp-server")
}

// DHCP, conversely, is present when it is configured.
func TestDHCPRenderedWhenConfigured(t *testing.T) {
	_, out := replay(t, `
ip pool lan 192.168.200.2-192.168.200.14
dhcp-server 1
 pool lan 1
 mask 255.255.255.240
 gateway 192.168.200.1
interface vl200
 ip address 192.168.200.1/28
 dhcp-server 1
`)
	mustContain(t, out, "ip pool lan 192.168.200.2-192.168.200.14\n", "dhcp-server 1\n pool lan 1\n", " dhcp-server 1\n")
}

// Fixtures from test/fixtures/ must replay without errors and give a stable (idempotent) result.
func TestExamplesRoundTrip(t *testing.T) {
	files, _ := filepath.Glob("../../test/fixtures/*.conf")
	if len(files) == 0 {
		t.Skip("no fixtures")
	}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		_, first := replay(t, string(b))
		_, second := replay(t, first)
		if first != second {
			t.Errorf("%s: render is not stable\n--- first\n%s\n--- second\n%s", f, first, second)
		}
		if strings.Contains(string(b), "encapsulation untagged") {
			mustContain(t, first, "encapsulation untagged")
		}
	}
}

// ---- ping ----

func fakeBin(t *testing.T, name, script string) string {
	t.Helper()
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", d+":"+os.Getenv("PATH"))
	return d
}

func TestPingHasNoCountLimit(t *testing.T) {
	for _, a := range pingArgs("1.1.1.1") {
		if a == "-c" || strings.HasPrefix(a, "-c") {
			t.Fatalf("ping must not limit packet count: %v", pingArgs("1.1.1.1"))
		}
	}
	if pingArgs("h")[len(pingArgs("h"))-1] != "h" {
		t.Fatal("host must be last")
	}
}

type syncBuf struct {
	mu sync.Mutex
	sb strings.Builder
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.Write(p)
}
func (b *syncBuf) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.sb.String() }

// Ping runs until it is stopped; other sessions are not blocked while ping runs;
// Interrupt terminates the process (with statistics), and the session stays usable.
func TestPingContinuousInterruptibleNonBlocking(t *testing.T) {
	// simulates iputils ping: without -c it prints forever, on SIGINT it prints statistics
	fakeBin(t, "ping", `
for a in "$@"; do [ "$a" = "-c" ] && { echo "unexpected -c" ; exit 3; }; done
trap 'echo "--- statistics ---"; exit 0' INT
i=0; while :; do i=$((i+1)); echo "64 bytes from $3: icmp_seq=$i"; sleep 0.05; done
`)
	r := newTestRouter(t)
	u := &config.User{Name: "admin", Role: "admin"}
	s1, s2 := NewSession(r, u), NewSession(r, u)
	s1.Exec("enable", io.Discard)
	s2.Exec("enable", io.Discard)

	out := &syncBuf{}
	done := make(chan struct{})
	go func() { s1.Exec("ping 10.0.0.1", out); close(done) }()

	// 1) runs longer than ping -c 5 could, and does not finish by itself
	time.Sleep(700 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("ping finished by itself")
	default:
	}
	if !strings.Contains(out.String(), "icmp_seq=5") || strings.Contains(out.String(), "unexpected") {
		t.Fatalf("ping output: %q", out.String())
	}

	// 2) another session works while ping is running
	fin := make(chan string, 1)
	go func() { var sb strings.Builder; s2.Exec("show hostname", &sb); fin <- sb.String() }()
	select {
	case o := <-fin:
		if !strings.Contains(o, "necorouter") {
			t.Fatalf("show hostname: %q", o)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CLI blocked while ping is running")
	}

	// 3) manual stop
	s1.Interrupt()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ping was not stopped by Interrupt")
	}
	if !strings.Contains(out.String(), "--- statistics ---") {
		t.Errorf("ping did not get SIGINT (no statistics): %q", out.String())
	}

	// 4) the session is usable after the stop, the next ping is not "cancelled in advance"
	var sb strings.Builder
	s1.Exec("show hostname", &sb)
	if !strings.Contains(sb.String(), "necorouter") {
		t.Fatalf("session broken after interrupt: %q", sb.String())
	}
	out2 := &syncBuf{}
	done2 := make(chan struct{})
	go func() { s1.Exec("ping 10.0.0.2", out2); close(done2) }()
	time.Sleep(300 * time.Millisecond)
	select {
	case <-done2:
		t.Fatal("second ping was cancelled by the stale interrupt")
	default:
	}
	s1.Interrupt()
	<-done2
}
