//go:build iproute2test

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"testing"
)

// A repeated ip address command replaces the address rather than adding a second one.
func TestIPAddressReplaces(t *testing.T) {
	r, _ := replay(t, "interface isp\n ip address 10.1.1.1/24\n ip address 10.2.2.2/24\n")
	i := r.Cfg.FindIface("isp")
	if i == nil || !reflect.DeepEqual(i.Addrs, []string{"10.2.2.2/24"}) {
		t.Fatalf("after second ip address: %v (want only 10.2.2.2/24)", i.Addrs)
	}
}

// no ip address A/M removes only the given address; no ip address — all of them.
func TestIPAddressNoForms(t *testing.T) {
	r := newTestRouter(t)
	s := &Session{R: r, User: "root", Role: "admin", noApply: true, frames: []frame{{kind: modeConfig}}}
	s.Exec("interface isp", nil)
	s.Exec("ip address 10.1.1.1/24", nil)
	s.Exec("ip address 10.2.2.2/24", nil)
	if i := r.Cfg.FindIface("isp"); len(i.Addrs) != 1 {
		t.Fatalf("addrs: %v", i.Addrs)
	}
	s.Exec("no ip address 10.2.2.2/24", nil)
	if i := r.Cfg.FindIface("isp"); len(i.Addrs) != 0 {
		t.Fatalf("no ip address A did not remove: %v", i.Addrs)
	}
	s.Exec("ip address 10.3.3.3/24", nil)
	s.Exec("no ip address", nil)
	if i := r.Cfg.FindIface("isp"); len(i.Addrs) != 0 {
		t.Fatalf("no ip address did not clear: %v", i.Addrs)
	}
}

// On a kernel: the second command removes the old address from the interface and sets the new one.
// Runs only in an isolated network (NR_NETNS=1), see test/addr_replace.sh.
func TestKernelAddressReplace(t *testing.T) {
	if os.Getenv("NR_NETNS") != "1" {
		t.Skip("needs NR_NETNS=1 (isolated network namespace, root)")
	}
	if out, err := exec.Command("ip", "link", "add", "nrv0", "type", "veth", "peer", "name", "nrv1").CombinedOutput(); err != nil {
		t.Fatalf("ip link add veth: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("ip", "link", "del", "dev", "nrv0").Run() })
	if err := exec.Command("ip", "link", "set", "dev", "nrv0", "up").Run(); err != nil {
		t.Fatal(err)
	}
	r := newRouterWith(t, "te0 nrv0\n", true) // port te0 -> veth nrv0, services enabled (netSvc)
	setup := `
interface isp
 ip address 10.1.1.1/24
exit
port te0
 service-instance te0/isp
  encapsulation untagged
  connect ip interface isp
 exit
exit
`
	if errs := r.Replay(setup); len(errs) > 0 {
		t.Fatal(errs)
	}
	if got := testIPAddrs(t, "nrv0"); !got["10.1.1.1/24"] {
		t.Fatalf("first address not on kernel: %v", got)
	}
	if errs := r.Replay("interface isp\n ip address 10.2.2.2/24\n"); len(errs) > 0 {
		t.Fatal(errs)
	}
	got := testIPAddrs(t, "nrv0")
	if got["10.1.1.1/24"] || !got["10.2.2.2/24"] {
		t.Fatalf("kernel addresses after replace: %v (want only 10.2.2.2/24)", got)
	}
}

func testIPAddrs(t *testing.T, dev string) map[string]bool {
	t.Helper()
	b, err := exec.Command("ip", "-j", "address", "show", "dev", dev).Output()
	if err != nil {
		t.Fatal(err)
	}
	var links []struct {
		AddrInfo []struct {
			Family    string `json:"family"`
			Local     string `json:"local"`
			PrefixLen int    `json:"prefixlen"`
		} `json:"addr_info"`
	}
	if err := json.Unmarshal(b, &links); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, link := range links {
		for _, a := range link.AddrInfo {
			if a.Family == "inet" {
				out[fmt.Sprintf("%s/%d", a.Local, a.PrefixLen)] = true
			}
		}
	}
	return out
}
