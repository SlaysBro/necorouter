package svc

import (
	"encoding/binary"
	"fmt"
	"math/bits"
	"net"
	"sort"
	"strings"

	"necorouter/internal/config"
)

// fmapSvc applies filter-map ipv4 to transit traffic in the nftables table "ip necorouter_fm".
// Two base chains on the forward hook: fm_in (iifname) and fm_out (oifname, lower priority), so that
// an accept in the inbound list does not skip the outbound check. For each interface and direction
// the rules of the bound lists go in ascending SEQUENCE order, followed by an implicit reject.
// The router's own traffic (input/output) is not affected: it is governed by security profiles.
type fmapSvc struct{ last string }

func (*fmapSvc) Name() string { return "filter-map" }

func fmWildcardExpr(kind, a string) (string, error) {
	f := strings.Fields(a)
	ip, w := net.ParseIP(f[0]).To4(), net.ParseIP(f[1]).To4()
	if ip == nil || w == nil {
		return "", fmt.Errorf("bad address %q", a)
	}
	mask := ^binary.BigEndian.Uint32(w)
	net4 := binary.BigEndian.Uint32(ip) & mask
	var nb, mb [4]byte
	binary.BigEndian.PutUint32(nb[:], net4)
	binary.BigEndian.PutUint32(mb[:], mask)
	ones := bits.OnesCount32(mask)
	if mask == 0 {
		return "", nil // inverse mask 255.255.255.255 means any address
	}
	if mask == ^uint32(0)<<(32-ones) { // contiguous mask: an ordinary prefix
		return fmt.Sprintf("ip %s %s/%d ", kind, net.IP(nb[:]), ones), nil
	}
	return fmt.Sprintf("ip %s & %s == %s ", kind, net.IP(mb[:]), net.IP(nb[:])), nil
}

func fmAddrExpr(kind, a string) (string, error) {
	switch {
	case a == "any":
		return "", nil
	case strings.HasPrefix(a, "host "):
		return fmt.Sprintf("ip %s %s ", kind, strings.TrimPrefix(a, "host ")), nil
	case strings.Contains(a, "/"):
		_, n, err := net.ParseCIDR(a)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("ip %s %s ", kind, n.String()), nil
	}
	return fmWildcardExpr(kind, a)
}

func fmPortExpr(l4, which string, p config.FMPort) string {
	switch p.Op {
	case "eq":
		return fmt.Sprintf("%s %s %d ", l4, which, p.P1)
	case "gt":
		return fmt.Sprintf("%s %s > %d ", l4, which, p.P1)
	case "lt":
		return fmt.Sprintf("%s %s < %d ", l4, which, p.P1)
	case "range":
		return fmt.Sprintf("%s %s %d-%d ", l4, which, p.P1, p.P2)
	}
	return ""
}

// fmMatchExpr translates a match line into an nftables expression (without interface and action).
func fmMatchExpr(text string) (string, error) {
	m, err := config.ParseFMMatch(text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if m.Frag {
		b.WriteString("ip frag-off & 0x1fff != 0 ")
	}
	l4 := m.L4()
	needL4 := l4 != "" && (m.SPort.Op != "" || m.DPort.Op != "" || len(m.Flags) > 0)
	if n, ok := m.ProtoNumber(); ok && !needL4 {
		fmt.Fprintf(&b, "ip protocol %d ", n)
	}
	src, err := fmAddrExpr("saddr", m.Src)
	if err != nil {
		return "", err
	}
	dst, err := fmAddrExpr("daddr", m.Dst)
	if err != nil {
		return "", err
	}
	b.WriteString(src + dst)
	b.WriteString(fmPortExpr(l4, "sport", m.SPort))
	b.WriteString(fmPortExpr(l4, "dport", m.DPort))
	if m.DSCP >= 0 {
		fmt.Fprintf(&b, "ip dscp %d ", m.DSCP)
	}
	switch m.TTLOp {
	case "eq":
		fmt.Fprintf(&b, "ip ttl %d ", m.TTL1)
	case "gt":
		fmt.Fprintf(&b, "ip ttl > %d ", m.TTL1)
	case "lt":
		fmt.Fprintf(&b, "ip ttl < %d ", m.TTL1)
	case "range":
		fmt.Fprintf(&b, "ip ttl %d-%d ", m.TTL1, m.TTL2)
	}
	if len(m.Flags) > 0 {
		mask, val := 0, 0
		for _, f := range m.Flags {
			name := strings.TrimPrefix(f, "not-")
			bit := config.FMTCPFlag[name]
			mask |= bit
			if name == f {
				val |= bit
			}
		}
		fmt.Fprintf(&b, "tcp flags & 0x%02x == 0x%02x ", mask, val)
	}
	return strings.TrimSpace(b.String()), nil
}

var fmVerdict = map[string]string{"accept": "accept", "discard": "drop", "reject": "reject"}

// fmapRules builds the rules of the chain for direction dir: for each interface, lists by SEQUENCE and an implicit reject.
func fmapRules(env *Env, c *config.Config, dir string) ([]string, []string, error) {
	var out, warns []string
	for _, i := range c.Ifaces {
		dev := DevOf(env, c, i.Name)
		var binds []config.FMBind
		for _, b := range i.FMaps {
			if b.Dir == dir {
				binds = append(binds, b)
			}
		}
		if dev == "" || len(binds) == 0 {
			if len(binds) > 0 {
				warns = append(warns, fmt.Sprintf("filter-map: interface %s has no device yet, its lists are not applied", i.Name))
			}
			continue
		}
		sort.SliceStable(binds, func(a, b int) bool { return binds[a].Seq < binds[b].Seq })
		sel := "iifname"
		if dir == "out" {
			sel = "oifname"
		}
		pre := fmt.Sprintf("%s %q ", sel, dev)
		for _, b := range binds {
			fm := c.FindFilterMap(b.Name)
			if fm == nil {
				warns = append(warns, fmt.Sprintf("filter-map: list %s of interface %s does not exist", b.Name, i.Name))
				continue
			}
			for _, r := range fm.Rules {
				verdict, ok := fmVerdict[r.Action]
				if r.CryptoMap != "" { // set crypto-map: traffic is accepted, and the kernel encrypts it per the IPsec policies
					verdict, ok = "accept", true
					if !c.CryptoMapNamed(r.CryptoMap) {
						warns = append(warns, fmt.Sprintf("filter-map %s %d: crypto-map %s does not exist", fm.Name, r.Seq, r.CryptoMap))
					}
				}
				if !ok || len(r.Matches) == 0 {
					warns = append(warns, fmt.Sprintf("filter-map %s %d: needs match and set, the rule is skipped", fm.Name, r.Seq))
					continue
				}
				for _, mt := range r.Matches {
					e, err := fmMatchExpr(mt)
					if err != nil {
						return nil, nil, fmt.Errorf("filter-map %s %d: %v", fm.Name, r.Seq, err)
					}
					out = append(out, strings.TrimSpace(pre+e)+" "+verdict)
				}
			}
		}
		out = append(out, pre+"reject")
	}
	return out, warns, nil
}

func (s *fmapSvc) Apply(env *Env, c *config.Config) ([]string, error) {
	in, w1, err := fmapRules(env, c, "in")
	if err != nil {
		return nil, err
	}
	out, w2, err := fmapRules(env, c, "out")
	if err != nil {
		return nil, err
	}
	warns := append(w1, w2...)
	if len(in) == 0 && len(out) == 0 {
		return warns, s.Stop(env)
	}
	if !have("nft") {
		return append(warns, "filter-map: nftables (nft) is not installed, the lists are not applied"), nil
	}
	var b strings.Builder
	b.WriteString("table ip necorouter_fm\ndelete table ip necorouter_fm\ntable ip necorouter_fm {\n")
	b.WriteString("  chain fm_in { type filter hook forward priority filter; policy accept;\n")
	for _, r := range in {
		b.WriteString("    " + r + "\n")
	}
	b.WriteString("  }\n  chain fm_out { type filter hook forward priority filter + 10; policy accept;\n")
	for _, r := range out {
		b.WriteString("    " + r + "\n")
	}
	b.WriteString("  }\n}\n")
	if b.String() == s.last && env.St.FM {
		return warns, nil
	}
	if _, err := runIn(b.String(), "nft", "-f", "-"); err != nil {
		return warns, err
	}
	s.last = b.String()
	env.St.FM = true
	return warns, nil
}

func (s *fmapSvc) Stop(env *Env) error {
	if env.St.FM && have("nft") {
		_, _ = run("nft", "delete", "table", "ip", "necorouter_fm")
	}
	env.St.FM = false
	s.last = ""
	return nil
}

func (*fmapSvc) Status(env *Env) string {
	if env.St.FM {
		return "active (nftables table ip necorouter_fm)"
	}
	return "idle"
}
