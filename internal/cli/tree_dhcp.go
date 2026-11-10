package cli

import (
	"fmt"
	"necorouter/internal/config"
	"necorouter/internal/svc"
	"sort"
	"time"
)

func dhcpTree() []*Node {
	d := func(f func(x *config.DHCPServer, v []string) error) Handler {
		return func(s *Session, v []string) error {
			id := 0
			fmt.Sscan(s.top().obj, &id)
			x := s.cfg().FindDHCP(id)
			if x == nil {
				return fmt.Errorf("DHCP server no longer exists")
			}
			return f(x, v)
		}
	}
	dnsChain := func() *Node {
		var n *Node
		for i := 4; i >= 1; i-- {
			cur := A(KIP, "A.B.C.D", "DNS server address")
			cur.R(d(func(x *config.DHCPServer, v []string) error { x.DNS = append([]string(nil), v...); return nil }))
			if n != nil {
				cur.Kids = []*Node{n}
			}
			n = cur
		}
		return n
	}
	return []*Node{
		K("pool", "Address pool for this server",
			A(KWord, "NAME", "Pool name (see 'ip pool')",
				A(KInt, "INDEX", "Pool index").Rng(1, 255).R(d(func(x *config.DHCPServer, v []string) error { x.Pool = v[0]; return nil }))).
				Cmp(poolNames).R(d(func(x *config.DHCPServer, v []string) error { x.Pool = v[0]; return nil }))).
			N(d(func(x *config.DHCPServer, _ []string) error { x.Pool = ""; return nil })),
		K("mask", "Subnet mask",
			A(KWord, "MASK", "Prefix length (28) or dotted mask").Chk(func(v string) bool { _, ok := config.MaskLen(v); return ok }).
				R(d(func(x *config.DHCPServer, v []string) error { x.Mask = v[0]; return nil }))).
			N(d(func(x *config.DHCPServer, _ []string) error { x.Mask = ""; return nil })),
		K("gateway", "Default gateway for clients",
			A(KIP, "A.B.C.D", "Gateway address").R(d(func(x *config.DHCPServer, v []string) error { x.Gateway = v[0]; return nil }))).
			N(d(func(x *config.DHCPServer, _ []string) error { x.Gateway = ""; return nil })),
		K("dns", "DNS servers for clients", dnsChain()).
			N(d(func(x *config.DHCPServer, _ []string) error { x.DNS = nil; return nil })),
		K("domain-name", "DNS domain name for clients (DHCP option 15)",
			A(KWord, "WORD", "Domain name").R(d(func(x *config.DHCPServer, v []string) error { x.Domain = v[0]; return nil }))).
			N(d(func(x *config.DHCPServer, _ []string) error { x.Domain = ""; return nil })),
		K("domain-search", "DNS search domain list for clients (DHCP option 119)",
			A(KWord, "WORD", "Search domain").R(d(func(x *config.DHCPServer, v []string) error { x.Search = v[0]; return nil }))).
			N(d(func(x *config.DHCPServer, _ []string) error { x.Search = ""; return nil })),
		K("lease", "Lease time",
			A(KInt, "SECONDS", "Seconds").Rng(60, 31536000).R(d(func(x *config.DHCPServer, v []string) error { fmt.Sscan(v[0], &x.Lease); return nil }))).
			N(d(func(x *config.DHCPServer, _ []string) error { x.Lease = 0; return nil })),
	}
}

// cmdClearDHCPClients: clear dhcp-server clients — drop every lease; dnsmasq restarts with an empty table.
func cmdClearDHCPClients(s *Session, _ []string) error {
	n, warns, err := svc.ClearLeases(s.R.Mgr.Env, s.cfg())
	for _, w := range warns {
		s.Printf("%% warning: %s\n", w)
	}
	if err != nil {
		return err
	}
	s.journal("clear dhcp-server clients")
	s.Printf("[OK] %d lease(s) cleared\n", n)
	return nil
}

func showLeases(s *Session, _ []string) error {
	ls := svc.Leases(s.R.P)
	sort.Slice(ls, func(i, j int) bool { return ls[i].IP < ls[j].IP })
	s.Printf("%-16s %-19s %-20s %s\n", "IP address", "MAC address", "Hostname", "Expires")
	for _, l := range ls {
		var exp int64
		fmt.Sscan(l.Expires, &exp)
		e := "infinite"
		if exp > 0 {
			e = time.Unix(exp, 0).Format("2006-01-02 15:04:05")
		}
		s.Printf("%-16s %-19s %-20s %s\n", l.IP, l.MAC, l.Host, e)
	}
	return nil
}
