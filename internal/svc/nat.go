package svc

import (
	"fmt"
	"sort"
	"strings"

	"necorouter/internal/config"
)

// natSvc builds the nftables ruleset. Until NAT is configured, there is no table.
type natSvc struct{ last string }

func (*natSvc) Name() string { return "nat" }

func (n *natSvc) Apply(env *Env, c *config.Config) ([]string, error) {
	if len(c.NATDyn)+len(c.NATStatic) == 0 {
		return nil, n.Stop(env)
	}
	if !have("nft") {
		return nil, fmt.Errorf("nftables (nft) is not installed")
	}
	var warns []string
	var inside, outside []string
	for _, i := range c.Ifaces {
		d := DevOf(env, c, i.Name)
		if d == "" {
			continue
		}
		switch i.NAT {
		case "inside":
			inside = append(inside, d)
		case "outside":
			outside = append(outside, d)
		}
	}
	sort.Strings(inside)
	sort.Strings(outside)
	set := func(l []string) string {
		q := make([]string, len(l))
		for i, v := range l {
			q[i] = fmt.Sprintf("%q", v)
		}
		return "{ " + strings.Join(q, ", ") + " }"
	}

	var pre, post []string
	for _, s := range c.NATStatic {
		oif := ""
		if len(outside) > 0 {
			oif = "iifname " + set(outside) + " "
		}
		if s.Dest {
			pre = append(pre, fmt.Sprintf("%sip daddr %s %s dport %d dnat to %s:%d", oif, s.Global, s.Proto, s.GPort, s.Local, s.LPort))
			if s.Hairpin {
				if len(inside) == 0 {
					warns = append(warns, "nat: hairpin needs an interface with 'ip nat inside'")
				} else {
					post = append(post, fmt.Sprintf("iifname %s ip daddr %s %s dport %d ct status dnat snat to %s", set(inside), s.Local, s.Proto, s.LPort, s.Global))
				}
			}
		} else if s.Proto != "" {
			pre = append(pre, fmt.Sprintf("%sip daddr %s %s dport %d dnat to %s:%d", oif, s.Global, s.Proto, s.GPort, s.Local, s.LPort))
		} else {
			pre = append(pre, fmt.Sprintf("%sip daddr %s dnat to %s", oif, s.Global, s.Local))
			post = append(post, fmt.Sprintf("ip saddr %s snat to %s", s.Local, s.Global))
		}
	}
	for _, d := range c.NATDyn {
		p := c.FindNATPool(d.Pool)
		if p == nil {
			warns = append(warns, fmt.Sprintf("nat: pool %s is not defined", d.Pool))
			continue
		}
		if len(outside) == 0 {
			warns = append(warns, "nat: no interface has 'ip nat outside'")
			continue
		}
		act := "masquerade"
		if d.Overload != "" && !strings.HasPrefix(d.Overload, "interface") {
			act = "snat to " + d.Overload
		}
		in := ""
		if len(inside) > 0 {
			in = "iifname " + set(inside) + " "
		}
		post = append(post, fmt.Sprintf("%soifname %s ip saddr %s %s", in, set(outside), nftAddressRanges(p.AddressRanges()), act))
	}

	var b strings.Builder
	b.WriteString("table ip necorouter\ndelete table ip necorouter\ntable ip necorouter {\n")
	b.WriteString("  chain prerouting { type nat hook prerouting priority dstnat; policy accept;\n")
	for _, r := range pre {
		b.WriteString("    " + r + "\n")
	}
	b.WriteString("  }\n  chain postrouting { type nat hook postrouting priority srcnat; policy accept;\n")
	for _, r := range post {
		b.WriteString("    " + r + "\n")
	}
	b.WriteString("  }\n}\n")
	if b.String() == n.last && env.St.NFT {
		return warns, nil
	}
	if _, err := runIn(b.String(), "nft", "-f", "-"); err != nil {
		return warns, err
	}
	n.last = b.String()
	env.St.NFT = true
	return warns, nil
}

func (n *natSvc) Stop(env *Env) error {
	if env.St.NFT && have("nft") {
		_, _ = run("nft", "delete", "table", "ip", "necorouter")
	}
	env.St.NFT = false
	n.last = ""
	return nil
}
func (*natSvc) Status(env *Env) string {
	if env.St.NFT {
		return "active (nftables table ip necorouter)"
	}
	return "idle"
}

func nftAddressRanges(ranges []string) string {
	if len(ranges) == 1 {
		return ranges[0]
	}
	return "{ " + strings.Join(ranges, ", ") + " }"
}
