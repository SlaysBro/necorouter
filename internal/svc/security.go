package svc

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"necorouter/internal/config"
)

// secSvc applies the line-port security profile in the nftables table "ip necorouter_sec":
// the input chain filters only packets addressed to the router itself and arriving on the router's
// interfaces; established connections are accepted so as not to break current sessions.
// Other interfaces (for example, the management NIC) and transit traffic are not affected.
type secSvc struct{ last string }

func (*secSvc) Name() string { return "security" }

var secProtoNum = map[string]string{
	"gre": "47", "icmp": "1", "igmp": "2", "ipcomp": "108", "ospf": "89", "pim": "103", "rsvp": "46",
	"tcp": "6", "udp": "17", "vrrp": "112",
}

func secAddr(kind, a string) (string, error) {
	switch {
	case a == "any":
		return "", nil
	case strings.HasPrefix(a, "host "):
		return fmt.Sprintf("ip %s %s ", kind, strings.TrimPrefix(a, "host ")), nil
	}
	_, n, err := net.ParseCIDR(a)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("ip %s %s ", kind, n.String()), nil
}

// secRuleExpr translates a rule into an nftables expression (without the interface condition).
func secRuleExpr(r config.SecRule) (string, error) {
	var b strings.Builder
	proto := r.Proto
	switch {
	case proto == "any" || proto == "ip":
	case r.Op == "" || (proto != "tcp" && proto != "udp"):
		n := proto
		if v, ok := secProtoNum[proto]; ok {
			n = v
		}
		fmt.Fprintf(&b, "ip protocol %s ", n)
	}
	src, err := secAddr("saddr", r.Src)
	if err != nil {
		return "", err
	}
	dst, err := secAddr("daddr", r.Dst)
	if err != nil {
		return "", err
	}
	b.WriteString(src + dst)
	if r.Op != "" {
		switch r.Op {
		case "eq":
			fmt.Fprintf(&b, "%s dport %d ", proto, r.P1)
		case "gt":
			fmt.Fprintf(&b, "%s dport > %d ", proto, r.P1)
		case "lt":
			fmt.Fprintf(&b, "%s dport < %d ", proto, r.P1)
		case "range":
			fmt.Fprintf(&b, "%s dport %d-%d ", proto, r.P1, r.P2)
		}
	}
	if r.Action == "permit" {
		b.WriteString("accept")
	} else {
		b.WriteString("drop")
	}
	return b.String(), nil
}

func (s *secSvc) Apply(env *Env, c *config.Config) ([]string, error) {
	name, rules := c.EffectiveSecurity()
	if c.Security != "" && c.Security != "none" && name == "none" {
		return []string{fmt.Sprintf("security: profile %s does not exist, no restrictions applied", c.Security)}, s.Stop(env)
	}
	var devs []string
	for _, i := range c.Ifaces {
		if d := DevOf(env, c, i.Name); d != "" && !strings.HasPrefix(i.Name, "loopback") {
			devs = append(devs, d)
		}
	}
	sort.Strings(devs)
	if len(rules) == 0 || len(devs) == 0 {
		return nil, s.Stop(env)
	}
	if !have("nft") {
		return []string{"security: nftables (nft) is not installed, the security profile is not applied"}, nil
	}
	q := make([]string, len(devs))
	for i, d := range devs {
		q[i] = fmt.Sprintf("%q", d)
	}
	iif := "iifname { " + strings.Join(q, ", ") + " } "
	rs := append([]config.SecRule(nil), rules...)
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Num < rs[j].Num })
	var b strings.Builder
	b.WriteString("table ip necorouter_sec\ndelete table ip necorouter_sec\ntable ip necorouter_sec {\n")
	b.WriteString("  chain input { type filter hook input priority filter; policy accept;\n")
	b.WriteString("    ct state established,related accept\n")
	for _, r := range rs {
		e, err := secRuleExpr(r)
		if err != nil {
			return nil, fmt.Errorf("rule %d: %v", r.Num, err)
		}
		b.WriteString("    " + iif + e + "\n")
	}
	b.WriteString("  }\n}\n")
	if b.String() == s.last && env.St.Sec {
		return nil, nil
	}
	if _, err := runIn(b.String(), "nft", "-f", "-"); err != nil {
		return nil, err
	}
	s.last = b.String()
	env.St.Sec = true
	return nil, nil
}

func (s *secSvc) Stop(env *Env) error {
	if env.St.Sec && have("nft") {
		_, _ = run("nft", "delete", "table", "ip", "necorouter_sec")
	}
	env.St.Sec = false
	s.last = ""
	return nil
}

func (*secSvc) Status(env *Env) string {
	if env.St.Sec {
		return "active (nftables table ip necorouter_sec)"
	}
	return "idle"
}
