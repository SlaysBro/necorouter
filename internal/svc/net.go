package svc

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strings"

	"necorouter/internal/config"
	"necorouter/internal/nl"
)

// netSvc manages links (vlan/gre/dummy), addresses, routes and
// packet forwarding via netlink. It touches only what it created itself (State).
type netSvc struct{}

func (*netSvc) Name() string { return "network" }

func linkExists(dev string) bool {
	_, err := net.InterfaceByName(dev)
	return err == nil
}

func (*netSvc) Apply(env *Env, c *config.Config) ([]string, error) {
	st := env.St
	if len(c.Ifaces)+len(c.Routes) == 0 && len(st.Links)+len(st.Addrs)+len(st.Routes) == 0 {
		return nil, nil // router not configured: do nothing and do not open sockets
	}
	n, err := nl.Open()
	if err != nil {
		return nil, err
	}
	defer n.Close()
	var warns []string

	links := map[string]string{} // dev -> spec
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
			p, s := c.Binding(i.Name)
			if s != nil && s.Encap == "dot1q" {
				nic := env.Ports.NIC(p.Name)
				links[dev] = fmt.Sprintf("vlan:%s:%d", nic, s.VLAN)
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

	// links that no longer exist or whose parameters changed
	for dev, spec := range st.Links {
		if links[dev] != spec {
			_ = n.DelLink(dev)
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
		spec := links[dev]
		if linkExists(dev) {
			continue
		}
		f := strings.Split(spec, ":")
		var err error
		switch f[0] {
		case "vlan":
			if f[1] == "" || !linkExists(f[1]) {
				warns = append(warns, fmt.Sprintf("port NIC %q for %s does not exist", f[1], dev))
				continue
			}
			_ = n.SetUp(f[1], true)
			var id int
			fmt.Sscan(f[2], &id)
			err = n.AddVLAN(dev, f[1], id)
		case "gre":
			err = n.AddGRE(dev, f[1], f[2], 64)
		case "dummy":
			err = n.AddDummy(dev)
		}
		if err != nil {
			warns = append(warns, err.Error())
			continue
		}
		st.Links[dev] = spec
	}

	// addresses
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
		have := nl.Addrs(dev)
		want := map[string]bool{}
		for _, a := range addrs[dev] {
			want[a] = true
			if !have[a] {
				if err := n.AddAddr(dev, a); err != nil {
					warns = append(warns, err.Error())
				}
			}
		}
		for _, a := range st.Addrs[dev] {
			if !want[a] && have[a] {
				_ = n.DelAddr(dev, a)
			}
		}
		if len(addrs[dev]) > 0 {
			st.Addrs[dev] = append([]string(nil), addrs[dev]...)
		} else {
			delete(st.Addrs, dev)
		}
	}
	for dev, m := range mtus {
		if linkExists(dev) {
			_ = n.SetMTU(dev, m)
		}
	}
	for dev := range up {
		if linkExists(dev) && !down[dev] {
			_ = n.SetUp(dev, true)
		}
	}
	for dev := range down {
		if linkExists(dev) {
			_ = n.SetUp(dev, false)
		}
	}
	if len(c.Ifaces) > 0 {
		_ = os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0o644)
	}

	// static routes
	wantR := map[string]bool{}
	for _, r := range c.Routes {
		wantR[r.Prefix+" "+r.NextHop] = true
	}
	for _, k := range st.Routes {
		if !wantR[k] {
			f := strings.Fields(k)
			_ = n.DelRoute(f[0], f[1])
		}
	}
	var keep []string
	for _, r := range c.Routes {
		if err := n.ReplaceRoute(r.Prefix, r.NextHop); err != nil {
			warns = append(warns, fmt.Sprintf("route %s via %s: %v", r.Prefix, r.NextHop, err))
			continue
		}
		keep = append(keep, r.Prefix+" "+r.NextHop)
	}
	st.Routes = keep
	return warns, nil
}

func (*netSvc) Stop(env *Env) error {
	st := env.St
	if len(st.Links)+len(st.Addrs)+len(st.Routes) == 0 {
		return nil
	}
	n, err := nl.Open()
	if err != nil {
		return err
	}
	defer n.Close()
	for _, k := range st.Routes {
		f := strings.Fields(k)
		_ = n.DelRoute(f[0], f[1])
	}
	st.Routes = nil
	for dev, as := range st.Addrs {
		if st.Links[dev] != "" {
			continue // removed together with the link
		}
		for _, a := range as {
			_ = n.DelAddr(dev, a)
		}
	}
	st.Addrs = map[string][]string{}
	for dev := range st.Links {
		_ = n.DelLink(dev)
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
