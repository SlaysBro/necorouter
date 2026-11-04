package svc

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"

	"necorouter/internal/config"
)

// netSvc manages links, addresses and routes through iproute2 and only removes
// resources recorded in State.
type netSvc struct{}

func (*netSvc) Name() string { return "network" }

func linkExists(dev string) bool {
	_, err := net.InterfaceByName(dev)
	return err == nil
}

func ipAddrSet(dev string) map[string]bool {
	out := map[string]bool{}
	b, err := run("ip", "-j", "address", "show", "dev", dev)
	if err != nil {
		return out
	}
	var links []struct {
		AddrInfo []struct {
			Family    string `json:"family"`
			Local     string `json:"local"`
			PrefixLen int    `json:"prefixlen"`
		} `json:"addr_info"`
	}
	if json.Unmarshal([]byte(b), &links) != nil {
		return out
	}
	for _, l := range links {
		for _, a := range l.AddrInfo {
			if a.Family == "inet" {
				out[fmt.Sprintf("%s/%d", a.Local, a.PrefixLen)] = true
			}
		}
	}
	return out
}

func (*netSvc) Apply(env *Env, c *config.Config) ([]string, error) {
	st := env.St
	if len(c.Ifaces)+len(c.Routes) == 0 && len(st.Links)+len(st.Addrs)+len(st.Routes) == 0 {
		return nil, nil
	}
	if !have("ip") {
		return nil, fmt.Errorf("iproute2 is required: ip command not found")
	}
	var warns []string
	links := map[string]string{}
	addrs := map[string][]string{}
	mtus := map[string]int{}
	down := map[string]bool{}
	up := map[string]bool{}
	for _, i := range c.Ifaces {
		dev := DevOf(env, c, i.Name)
		if dev == "" {
			continue
		}
		switch {
		case i.Tunnel != nil:
			if i.Tunnel.Mode != "gre" {
				warns = append(warns, fmt.Sprintf("interface %s: tunnel mode %q is not supported (only gre)", i.Name, i.Tunnel.Mode))
				continue
			}
			links[dev] = fmt.Sprintf("gre:%s:%s", i.Tunnel.Src, i.Tunnel.Dst)
		case strings.HasPrefix(i.Name, "loopback"):
			links[dev] = "dummy"
		default:
			p, si := c.Binding(i.Name)
			if si != nil && si.Encap == "dot1q" {
				nic := env.Ports.NIC(p.Name)
				links[dev] = fmt.Sprintf("vlan:%s:%d", nic, si.VLAN)
				up[nic] = true
			}
		}
		addrs[dev] = append(addrs[dev], i.Addrs...)
		if i.MTU > 0 {
			mtus[dev] = i.MTU
		}
		if i.Shutdown {
			down[dev] = true
		} else {
			up[dev] = true
		}
	}
	for dev, spec := range st.Links {
		if links[dev] != spec {
			_, _ = run("ip", "link", "del", "dev", dev)
			delete(st.Links, dev)
			delete(st.Addrs, dev)
		}
	}
	devs := make([]string, 0, len(links))
	for d := range links {
		devs = append(devs, d)
	}
	sort.Strings(devs)
	for _, dev := range devs {
		if linkExists(dev) {
			continue
		}
		f := strings.Split(links[dev], ":")
		var args []string
		switch f[0] {
		case "vlan":
			if f[1] == "" || !linkExists(f[1]) {
				warns = append(warns, fmt.Sprintf("port NIC %q for %s does not exist", f[1], dev))
				continue
			}
			_, _ = run("ip", "link", "set", "dev", f[1], "up")
			args = []string{"link", "add", "link", f[1], "name", dev, "type", "vlan", "id", f[2]}
		case "gre":
			args = []string{"tunnel", "add", dev, "mode", "gre", "local", f[1], "remote", f[2], "ttl", "64"}
		case "dummy":
			args = []string{"link", "add", "dev", dev, "type", "dummy"}
		}
		if len(args) > 0 {
			if _, err := run("ip", args...); err != nil {
				warns = append(warns, err.Error())
				continue
			}
			st.Links[dev] = links[dev]
			if f[0] == "gre" {
				_, _ = run("ip", "link", "set", "dev", dev, "multicast", "on")
			}
		}
	}

	known := map[string]bool{}
	for d := range addrs {
		known[d] = true
	}
	for d := range st.Addrs {
		known[d] = true
	}
	for dev := range known {
		if !linkExists(dev) {
			delete(st.Addrs, dev)
			continue
		}
		have := ipAddrSet(dev)
		want := map[string]bool{}
		for _, a := range addrs[dev] {
			want[a] = true
			if !have[a] {
				if _, err := run("ip", "address", "add", a, "dev", dev); err != nil {
					warns = append(warns, err.Error())
				}
			}
		}
		for _, a := range st.Addrs[dev] {
			if !want[a] && have[a] {
				_, _ = run("ip", "address", "del", a, "dev", dev)
			}
		}
		if len(addrs[dev]) > 0 {
			st.Addrs[dev] = append([]string(nil), addrs[dev]...)
		} else {
			delete(st.Addrs, dev)
		}
	}
	for dev, mtu := range mtus {
		if linkExists(dev) {
			_, _ = run("ip", "link", "set", "dev", dev, "mtu", fmt.Sprint(mtu))
		}
	}
	for dev := range up {
		if linkExists(dev) && !down[dev] {
			_, _ = run("ip", "link", "set", "dev", dev, "up")
		}
	}
	for dev := range down {
		if linkExists(dev) {
			_, _ = run("ip", "link", "set", "dev", dev, "down")
		}
	}
	if len(c.Ifaces) > 0 {
		_ = os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0o644)
	}

	wantR := map[string]bool{}
	for _, r := range c.Routes {
		wantR[r.Prefix+" "+r.NextHop] = true
	}
	for _, k := range st.Routes {
		if !wantR[k] {
			f := strings.Fields(k)
			if len(f) > 0 {
				args := append([]string{"route", "del", f[0]}, routeVia(f[1:])...)
				args = append(args, "proto", "static")
				_, _ = run("ip", args...)
			}
		}
	}
	var keep []string
	for _, r := range c.Routes {
		args := []string{"route", "replace", r.Prefix}
		if r.NextHop != "" {
			args = append(args, "via", r.NextHop)
		}
		args = append(args, "proto", "static")
		if _, err := run("ip", args...); err != nil {
			warns = append(warns, fmt.Sprintf("route %s via %s: %v", r.Prefix, r.NextHop, err))
			continue
		}
		keep = append(keep, r.Prefix+" "+r.NextHop)
	}
	st.Routes = keep
	return warns, nil
}

func routeVia(fields []string) []string {
	if len(fields) >= 2 && fields[0] != "" {
		return []string{"via", fields[0]}
	}
	return nil
}

func (*netSvc) Stop(env *Env) error {
	st := env.St
	if len(st.Links)+len(st.Addrs)+len(st.Routes) == 0 {
		return nil
	}
	if !have("ip") {
		return fmt.Errorf("iproute2 is required: ip command not found")
	}
	for _, k := range st.Routes {
		f := strings.Fields(k)
		if len(f) > 0 {
			args := append([]string{"route", "del", f[0]}, routeVia(f[1:])...)
			args = append(args, "proto", "static")
			_, _ = run("ip", args...)
		}
	}
	st.Routes = nil
	for dev, as := range st.Addrs {
		if st.Links[dev] != "" {
			continue
		}
		for _, a := range as {
			_, _ = run("ip", "address", "del", a, "dev", dev)
		}
	}
	st.Addrs = map[string][]string{}
	for dev := range st.Links {
		_, _ = run("ip", "link", "del", "dev", dev)
	}
	st.Links = map[string]string{}
	return nil
}

func (*netSvc) Status(env *Env) string {
	a := 0
	for _, x := range env.St.Addrs {
		a += len(x)
	}
	if a+len(env.St.Links)+len(env.St.Routes) == 0 {
		return "idle"
	}
	return fmt.Sprintf("%d address(es), %d link(s), %d route(s)", a, len(env.St.Links), len(env.St.Routes))
}

// ipRoute is one main-table route returned by iproute2 JSON output.
type ipRoute struct {
	Dst      string `json:"dst"`
	Gateway  string `json:"gateway"`
	Dev      string `json:"dev"`
	Protocol string `json:"protocol"`
	Metric   uint32 `json:"metric"`
}

func systemRoutes() ([]ipRoute, error) {
	out, err := run("ip", "-j", "-4", "route", "show", "table", "main")
	if err != nil {
		return nil, err
	}
	var routes []ipRoute
	if err := json.Unmarshal([]byte(out), &routes); err != nil {
		return nil, fmt.Errorf("decode ip route output: %w", err)
	}
	return routes, nil
}
