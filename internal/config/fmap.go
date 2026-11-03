package config

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// filter-map ipv4: access lists for transit IPv4 traffic.
//   filter-map ipv4 NAME [SEQ]   (filter-map-ipv4 mode: match ..., set accept|discard|reject)
//   interface: set filter-map in|out NAME [SEQ]
// One list holds several rules (SEQUENCE). A rule has one or more match lines (any of them may match)
// and one action. After all lists bound to an interface and direction there is an implicit reject.

// FMRule is a filter-map rule: Seq is the check order, Matches is the normalized text of the match lines.
type FMRule struct {
	Seq     int      `json:"seq"`
	Matches []string `json:"matches,omitempty"`
	Action  string   `json:"action,omitempty"` // accept | discard | reject
	// set crypto-map NAME peer ADDR: traffic is handled by IPsec (accepted in nftables); Action is empty in this case
	CryptoMap  string `json:"crypto_map,omitempty"`
	CryptoPeer string `json:"crypto_peer,omitempty"`
}

type FilterMap struct {
	Name  string    `json:"name"`
	Rules []*FMRule `json:"rules,omitempty"`
}

// FMBind binds a filter-map to an interface: Dir is in | out, Seq is the order among the interface's lists.
type FMBind struct {
	Dir  string `json:"dir"`
	Name string `json:"name"`
	Seq  int    `json:"seq"`
}

func (c *Config) FindFilterMap(name string) *FilterMap {
	for _, f := range c.FilterMaps {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// GetFilterMap returns the list, creating it if necessary (lists are stored by name).
func (c *Config) GetFilterMap(name string) *FilterMap {
	if f := c.FindFilterMap(name); f != nil {
		return f
	}
	f := &FilterMap{Name: name}
	c.FilterMaps = append(c.FilterMaps, f)
	sort.Slice(c.FilterMaps, func(i, j int) bool { return c.FilterMaps[i].Name < c.FilterMaps[j].Name })
	return f
}

func (f *FilterMap) Rule(seq int) *FMRule {
	for _, r := range f.Rules {
		if r.Seq == seq {
			return r
		}
	}
	return nil
}

// GetRule returns the rule with number seq, creating it if necessary.
func (f *FilterMap) GetRule(seq int) *FMRule {
	if r := f.Rule(seq); r != nil {
		return r
	}
	r := &FMRule{Seq: seq}
	f.Rules = append(f.Rules, r)
	sort.Slice(f.Rules, func(i, j int) bool { return f.Rules[i].Seq < f.Rules[j].Seq })
	return r
}

// NextSeq is the next free rule number, in steps of 10.
func (f *FilterMap) NextSeq() int {
	max := 0
	for _, r := range f.Rules {
		if r.Seq > max {
			max = r.Seq
		}
	}
	if len(f.Rules) == 0 {
		return 10
	}
	return (max/10 + 1) * 10
}

// FMProtoNum holds the filter-map protocol names (any means no condition).
var FMProtoNum = map[string]int{
	"ipinip": 4, "icmp": 1, "gre": 47, "igmp": 2, "pim": 103, "rsvp": 46, "ospf": 89, "vrrp": 112, "ipcomp": 108,
	"udp": 17, "tcp": 6,
}

var fmPortNames = map[string]map[string]int{
	"tcp": {"ftp": 21, "ssh": 22, "telnet": 23, "www": 80},
	"udp": {"tftp": 69, "bootp": 67},
}

// FMTCPFlag holds the values of the TCP flag bits.
var FMTCPFlag = map[string]int{"fin": 1, "syn": 2, "rst": 4, "psh": 8, "ack": 16, "urg": 32}

// FMPort is a port condition: Op "", eq, gt, lt or range.
type FMPort struct {
	Op     string
	P1, P2 int
}

func (p FMPort) String() string {
	switch p.Op {
	case "":
		return ""
	case "range":
		return fmt.Sprintf("range %d %d", p.P1, p.P2)
	}
	return fmt.Sprintf("%s %d", p.Op, p.P1)
}

// FMMatch is a parsed match line.
// Src and Dst: "any", "host A.B.C.D", "A.B.C.D/M" or "A.B.C.D K.L.M.N" (address with an inverse mask).
type FMMatch struct {
	Frag         bool
	Proto        string // any | name | number
	Src, Dst     string
	SPort, DPort FMPort
	DSCP         int // -1 means not set
	TTLOp        string
	TTL1, TTL2   int
	Flags        []string // ack, not-syn, ...
}

// L4 returns "tcp" or "udp" (including for numbers 6 and 17), otherwise "".
func (m FMMatch) L4() string {
	switch m.Proto {
	case "tcp", "6":
		return "tcp"
	case "udp", "17":
		return "udp"
	}
	return ""
}

// ProtoNumber returns the protocol number; ok=false for any.
func (m FMMatch) ProtoNumber() (int, bool) {
	if m.Proto == "any" {
		return 0, false
	}
	if n, ok := FMProtoNum[m.Proto]; ok {
		return n, true
	}
	n, _ := strconv.Atoi(m.Proto)
	return n, true
}

func isPortOp(s string) bool { return s == "eq" || s == "gt" || s == "lt" || s == "range" }

func fmIPv4(s string) net.IP {
	if ip := net.ParseIP(s); ip != nil {
		return ip.To4()
	}
	return nil
}

func fmAddr(t []string) (string, int, error) {
	const usage = "address expected: any, host A.B.C.D, A.B.C.D/M or A.B.C.D K.L.M.N"
	if len(t) == 0 {
		return "", 0, fmt.Errorf(usage)
	}
	switch t[0] {
	case "any":
		return "any", 1, nil
	case "host":
		if len(t) < 2 || fmIPv4(t[1]) == nil {
			return "", 0, fmt.Errorf("host needs an IPv4 address")
		}
		return "host " + fmIPv4(t[1]).String(), 2, nil
	}
	if strings.Contains(t[0], "/") {
		ip, n, err := net.ParseCIDR(t[0])
		if err != nil || ip.To4() == nil {
			return "", 0, fmt.Errorf("invalid address %q: %s", t[0], usage)
		}
		ones, _ := n.Mask.Size()
		return fmt.Sprintf("%s/%d", ip.To4(), ones), 1, nil
	}
	if ip := fmIPv4(t[0]); ip != nil && len(t) >= 2 {
		if w := fmIPv4(t[1]); w != nil {
			return ip.String() + " " + w.String(), 2, nil
		}
	}
	return "", 0, fmt.Errorf("invalid address %q: %s", t[0], usage)
}

func fmPort(l4 string, t []string) (FMPort, int, error) {
	if l4 == "" {
		return FMPort{}, 0, fmt.Errorf("ports can be given for tcp and udp only")
	}
	val := func(s string) (int, error) {
		if n, ok := fmPortNames[l4][s]; ok {
			return n, nil
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > 65535 {
			return 0, fmt.Errorf("invalid port %q (0-65535)", s)
		}
		return n, nil
	}
	p := FMPort{Op: t[0]}
	var err error
	switch p.Op {
	case "range":
		if len(t) < 3 {
			return p, 0, fmt.Errorf("usage: range LOW HIGH")
		}
		if p.P1, err = val(t[1]); err != nil {
			return p, 0, err
		}
		if p.P2, err = val(t[2]); err != nil {
			return p, 0, err
		}
		if p.P2 < p.P1 {
			return p, 0, fmt.Errorf("range: the upper port is below the lower one")
		}
		return p, 3, nil
	default:
		if len(t) < 2 {
			return p, 0, fmt.Errorf("usage: %s PORT", p.Op)
		}
		if p.P1, err = val(t[1]); err != nil {
			return p, 0, err
		}
		return p, 2, nil
	}
}

// ParseFMMatch parses a line
//
//	[fragments] PROTOCOL SRC [PORT_CONDITION] DST [PORT_CONDITION] [dscp N] [ttl eq|gt|lt N | ttl range A B] [FLAGS]
func ParseFMMatch(text string) (FMMatch, error) {
	m := FMMatch{DSCP: -1}
	t := strings.Fields(strings.ToLower(text))
	const usage = "usage: match [fragments] PROTOCOL SOURCE [PORT] DESTINATION [PORT] [dscp N] [ttl ...] [FLAGS]"
	if len(t) > 0 && t[0] == "fragments" {
		m.Frag = true
		t = t[1:]
	}
	if len(t) == 0 {
		return m, fmt.Errorf(usage)
	}
	proto := t[0]
	t = t[1:]
	if _, ok := FMProtoNum[proto]; ok || proto == "any" {
		m.Proto = proto
	} else if n, err := strconv.Atoi(proto); err == nil && n >= 0 && n <= 255 {
		m.Proto = strconv.Itoa(n)
	} else {
		return m, fmt.Errorf("unknown protocol %q (any, ipinip, icmp, gre, igmp, pim, rsvp, ospf, vrrp, ipcomp, udp, tcp or 0-255)", proto)
	}
	var err error
	var n int
	if m.Src, n, err = fmAddr(t); err != nil {
		return m, fmt.Errorf("source: %v", err)
	}
	t = t[n:]
	if len(t) > 0 && isPortOp(t[0]) {
		if m.Frag {
			return m, fmt.Errorf("fragments: a port cannot be given for secondary fragments")
		}
		if m.SPort, n, err = fmPort(m.L4(), t); err != nil {
			return m, err
		}
		t = t[n:]
	}
	if m.Dst, n, err = fmAddr(t); err != nil {
		return m, fmt.Errorf("destination: %v", err)
	}
	t = t[n:]
	if len(t) > 0 && isPortOp(t[0]) {
		if m.Frag {
			return m, fmt.Errorf("fragments: a port cannot be given for secondary fragments")
		}
		if m.DPort, n, err = fmPort(m.L4(), t); err != nil {
			return m, err
		}
		t = t[n:]
	}
	seen := map[string]string{} // flag -> "set" | "not"
	for len(t) > 0 {
		switch w := t[0]; {
		case w == "dscp":
			if len(t) < 2 {
				return m, fmt.Errorf("usage: dscp 0-63")
			}
			v, err := strconv.Atoi(t[1])
			if err != nil || v < 0 || v > 63 {
				return m, fmt.Errorf("invalid dscp %q (0-63)", t[1])
			}
			m.DSCP = v
			t = t[2:]
		case w == "ttl":
			if len(t) < 3 {
				return m, fmt.Errorf("usage: ttl eq|gt|lt 0-255 | ttl range 0-255 0-255")
			}
			m.TTLOp = t[1]
			switch m.TTLOp {
			case "eq", "gt", "lt":
				if m.TTL1, err = strconv.Atoi(t[2]); err != nil || m.TTL1 < 0 || m.TTL1 > 255 {
					return m, fmt.Errorf("invalid ttl %q (0-255)", t[2])
				}
				t = t[3:]
			case "range":
				if len(t) < 4 {
					return m, fmt.Errorf("usage: ttl range LOW HIGH")
				}
				a, e1 := strconv.Atoi(t[2])
				b, e2 := strconv.Atoi(t[3])
				if e1 != nil || e2 != nil || a < 0 || b > 255 || b < a {
					return m, fmt.Errorf("invalid ttl range (0-255, LOW <= HIGH)")
				}
				m.TTL1, m.TTL2 = a, b
				t = t[4:]
			default:
				return m, fmt.Errorf("unexpected %q: eq, gt, lt or range expected", t[1])
			}
		default:
			name, kind := strings.TrimPrefix(w, "not-"), "set"
			if strings.HasPrefix(w, "not-") {
				kind = "not"
			}
			if _, ok := FMTCPFlag[name]; !ok {
				return m, fmt.Errorf("unexpected %q: dscp, ttl or a TCP flag (ack, fin, psh, rst, syn, urg, not-...) expected", w)
			}
			if m.L4() != "tcp" {
				return m, fmt.Errorf("flags can be given for tcp only")
			}
			if prev, dup := seen[name]; dup && prev != kind {
				return m, fmt.Errorf("flags %s and not-%s contradict each other", name, name)
			} else if !dup {
				m.Flags = append(m.Flags, w)
			}
			seen[name] = kind
			t = t[1:]
		}
	}
	return m, nil
}

// String is the normalized text of the match line (for configuration and show).
func (m FMMatch) String() string {
	var b strings.Builder
	if m.Frag {
		b.WriteString("fragments ")
	}
	b.WriteString(m.Proto + " " + m.Src)
	if s := m.SPort.String(); s != "" {
		b.WriteString(" " + s)
	}
	b.WriteString(" " + m.Dst)
	if s := m.DPort.String(); s != "" {
		b.WriteString(" " + s)
	}
	if m.DSCP >= 0 {
		fmt.Fprintf(&b, " dscp %d", m.DSCP)
	}
	switch m.TTLOp {
	case "":
	case "range":
		fmt.Fprintf(&b, " ttl range %d %d", m.TTL1, m.TTL2)
	default:
		fmt.Fprintf(&b, " ttl %s %d", m.TTLOp, m.TTL1)
	}
	for _, f := range m.Flags {
		b.WriteString(" " + f)
	}
	return b.String()
}
