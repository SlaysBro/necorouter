package svc

import (
	"fmt"
	"net/netip"
	"os"
	"strings"

	"necorouter/internal/config"
)

// dhcpSvc runs dnsmasq (DHCP only, DNS disabled), and only when
// there is an interface with "dhcp-server N".
type dhcpSvc struct{}

func (*dhcpSvc) Name() string { return "dhcp" }

func LeaseFile(p Paths) string { return p.Var + "/dnsmasq.leases" }

func (*dhcpSvc) Apply(env *Env, c *config.Config) ([]string, error) {
	var warns []string
	var b strings.Builder
	b.WriteString("port=0\nbind-interfaces\ndhcp-authoritative\n")
	fmt.Fprintf(&b, "dhcp-leasefile=%s\n", LeaseFile(env.P))
	n := 0
	for _, i := range c.Ifaces {
		if i.DHCP == 0 {
			continue
		}
		d := c.FindDHCP(i.DHCP)
		dev := DevOf(env, c, i.Name)
		if d == nil || dev == "" {
			warns = append(warns, fmt.Sprintf("dhcp: interface %s refers to a missing server or is unbound", i.Name))
			continue
		}
		pool := c.FindPool(d.Pool)
		if pool == nil {
			warns = append(warns, fmt.Sprintf("dhcp-server %d: pool %q is not defined", d.ID, d.Pool))
			continue
		}
		mask := ""
		if d.Mask != "" {
			if l, ok := config.MaskLen(d.Mask); ok {
				mask = config.MaskString(l)
			}
		}
		var self netip.Addr
		if len(i.Addrs) > 0 {
			p, _ := netip.ParsePrefix(i.Addrs[0])
			self = p.Addr()
			if mask == "" {
				mask = config.MaskString(p.Bits())
			}
		}
		if mask == "" {
			warns = append(warns, fmt.Sprintf("dhcp-server %d: no mask", d.ID))
			continue
		}
		gw := d.Gateway
		if gw == "" && self.IsValid() {
			gw = self.String()
		}
		tag := fmt.Sprintf("s%d", d.ID)
		lease := "12h"
		if d.Lease > 0 {
			lease = fmt.Sprintf("%ds", d.Lease)
		}
		fmt.Fprintf(&b, "interface=%s\n", dev)
		for _, r := range pool.AddressRanges() {
			from, to, _ := strings.Cut(r, "-")
			if to == "" {
				to = from
			}
			fmt.Fprintf(&b, "dhcp-range=set:%s,%s,%s,%s,%s\n", tag, from, to, mask, lease)
		}
		if gw != "" {
			fmt.Fprintf(&b, "dhcp-option=tag:%s,option:router,%s\n", tag, gw)
		}
		if len(d.DNS) > 0 {
			fmt.Fprintf(&b, "dhcp-option=tag:%s,option:dns-server,%s\n", tag, strings.Join(d.DNS, ","))
		}
		if d.Domain != "" { // option 15
			fmt.Fprintf(&b, "dhcp-option=tag:%s,option:domain-name,%s\n", tag, d.Domain)
		}
		if d.Search != "" { // option 119
			fmt.Fprintf(&b, "dhcp-option=tag:%s,option:domain-search,%s\n", tag, d.Search)
		}
		n++
	}
	if n == 0 {
		env.Sup.Stop("dnsmasq")
		return warns, nil
	}
	bin := find("dnsmasq")
	if bin == "" {
		return warns, fmt.Errorf("dnsmasq is not installed (needed for dhcp-server)")
	}
	conf := env.P.Run + "/dnsmasq.conf"
	if err := writeFileAtomic(conf, []byte(b.String()), 0o644); err != nil {
		return warns, err
	}
	err := env.Sup.Ensure("dnsmasq", b.String(), []string{bin, "-k", "--conf-file=" + conf, "--pid-file=" + env.P.Run + "/dnsmasq.pid"})
	return warns, err
}

func (*dhcpSvc) Stop(env *Env) error { env.Sup.Stop("dnsmasq"); return nil }
func (*dhcpSvc) Status(env *Env) string {
	if env.Sup.Running("dnsmasq") {
		return "running (dnsmasq)"
	}
	return "idle"
}

type Lease struct{ Expires, MAC, IP, Host string }

func Leases(p Paths) []Lease {
	b, err := os.ReadFile(LeaseFile(p))
	if err != nil {
		return nil
	}
	var out []Lease
	for _, ln := range strings.Split(string(b), "\n") {
		f := strings.Fields(ln)
		if len(f) >= 4 {
			out = append(out, Lease{f[0], f[1], f[2], f[3]})
		}
	}
	return out
}

// ClearLeases forgets all DHCP leases (clear dhcp-server clients). dnsmasq is stopped first, because it
// writes its lease table on exit; then the lease file is emptied and the DHCP service is applied again
// with the current configuration. It returns the number of leases that were dropped.
func ClearLeases(env *Env, c *config.Config) (int, []string, error) {
	n := len(Leases(env.P))
	env.Sup.Stop("dnsmasq")
	if err := writeFileAtomic(LeaseFile(env.P), nil, 0o644); err != nil {
		return n, nil, err
	}
	warns, err := (&dhcpSvc{}).Apply(env, c)
	return n, warns, err
}
