package cli

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"necorouter/internal/config"
)

// Security profiles: security-profile N / rule / security N / show security-profile.
// A profile filters traffic addressed to the router itself on the router's interfaces (line ports).
// The default profile (the default) closes SSH, Telnet and SNMP; to remove: no security default.
// Interfaces not described in the router configuration (for example, a management network card) are not affected.

var hostRe32 = regexp.MustCompile(`host (\S+)`)

var secProtoNames = map[string]int{
	"gre": 47, "icmp": 1, "igmp": 2, "ip": -1, "ipcomp": 108, "ospf": 89, "pim": 103, "rsvp": 46,
	"tcp": 6, "udp": 17, "vrrp": 112, "any": -1,
}

var secPortNames = map[string]map[string]int{
	"tcp": {"ftp": 21, "ssh": 22, "telnet": 23, "www": 80},
	"udp": {"bootp": 67, "tftp": 69},
}

func normProto(p string) (string, error) {
	p = strings.ToLower(p)
	if _, ok := secProtoNames[p]; ok {
		return p, nil
	}
	if n, err := strconv.Atoi(p); err == nil && n >= 0 && n <= 255 {
		return p, nil
	}
	return "", fmt.Errorf("unknown protocol %q (any, gre, icmp, igmp, ip, ipcomp, ospf, pim, rsvp, tcp, udp, vrrp or 0-255)", p)
}

func parsePort(proto, s string) (int, error) {
	if n, ok := secPortNames[proto][strings.ToLower(s)]; ok {
		return n, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 65535 {
		return 0, fmt.Errorf("invalid port %q (0-65535)", s)
	}
	return n, nil
}

// parseAddr parses "any", "host A.B.C.D" or "A.B.C.D/M" and returns the canonical form and the number of words consumed.
func parseAddr(t []string) (string, int, error) {
	if len(t) == 0 {
		return "", 0, fmt.Errorf("address expected: any, host A.B.C.D or A.B.C.D/M")
	}
	switch strings.ToLower(t[0]) {
	case "any":
		return "any", 1, nil
	case "host":
		if len(t) < 2 || net.ParseIP(t[1]) == nil || net.ParseIP(t[1]).To4() == nil {
			return "", 0, fmt.Errorf("host needs an IPv4 address")
		}
		return "host " + t[1], 2, nil
	}
	if ip, _, err := net.ParseCIDR(t[0]); err != nil || ip.To4() == nil {
		return "", 0, fmt.Errorf("invalid address %q: any, host A.B.C.D or A.B.C.D/M", t[0])
	}
	return t[0], 1, nil
}

// parseRule parses "PROTOCOL SOURCE DESTINATION [eq|gt|lt|range PORT [PORT]]".
func parseRule(num int, action, rest string) (config.SecRule, error) {
	t := strings.Fields(rest)
	r := config.SecRule{Num: num, Action: action}
	if len(t) == 0 {
		return r, fmt.Errorf("usage: rule %d %s PROTOCOL SOURCE DESTINATION [eq|gt|lt|range PORT [PORT]]", num, action)
	}
	var err error
	if r.Proto, err = normProto(t[0]); err != nil {
		return r, err
	}
	t = t[1:]
	var n int
	if r.Src, n, err = parseAddr(t); err != nil {
		return r, fmt.Errorf("source: %v", err)
	}
	t = t[n:]
	if r.Dst, n, err = parseAddr(t); err != nil {
		return r, fmt.Errorf("destination: %v", err)
	}
	t = t[n:]
	if len(t) == 0 {
		return r, nil
	}
	if r.Proto != "tcp" && r.Proto != "udp" {
		return r, fmt.Errorf("ports can be given for tcp and udp only")
	}
	op := strings.ToLower(t[0])
	switch op {
	case "eq", "gt", "lt":
		if len(t) != 2 {
			return r, fmt.Errorf("usage: %s PORT", op)
		}
	case "range":
		if len(t) != 3 {
			return r, fmt.Errorf("usage: range LOW HIGH")
		}
	default:
		return r, fmt.Errorf("unexpected %q: eq, gt, lt or range expected", t[0])
	}
	r.Op = op
	if r.P1, err = parsePort(r.Proto, t[1]); err != nil {
		return r, err
	}
	if op == "range" {
		if r.P2, err = parsePort(r.Proto, t[2]); err != nil {
			return r, err
		}
		if r.P2 < r.P1 {
			return r, fmt.Errorf("range: the upper port is below the lower one")
		}
	}
	return r, nil
}

func secProfile(s *Session) (*config.SecProfile, error) {
	id, _ := strconv.Atoi(s.top().obj)
	p := s.cfg().FindSecProfile(id)
	if p == nil {
		return nil, fmt.Errorf("security profile no longer exists")
	}
	return p, nil
}

func secLocked(s *Session, id int) error {
	if s.cfg().Security == strconv.Itoa(id) {
		return fmt.Errorf("Profile is set on 1 namespaces. Unset profile prior to change it.")
	}
	return nil
}

func secRuleNode(action string) *Node {
	return K(action, "Rule action",
		A(KRest, "PROTOCOL SOURCE DESTINATION [eq|gt|lt|range PORT [PORT]]",
			"Protocol (any, tcp, udp, icmp, gre, ospf, vrrp ... or 0-255), source and destination (any | host A.B.C.D | A.B.C.D/M), optional port").
			R(func(s *Session, v []string) error {
				p, err := secProfile(s)
				if err != nil {
					return err
				}
				if err := secLocked(s, p.ID); err != nil {
					return err
				}
				num, _ := strconv.Atoi(v[0])
				r, err := parseRule(num, action, v[1])
				if err != nil {
					return err
				}
				for i := range p.Rules {
					if p.Rules[i].Num == num {
						p.Rules[i] = r
						return nil
					}
				}
				p.Rules = append(p.Rules, r)
				sort.Slice(p.Rules, func(i, j int) bool { return p.Rules[i].Num < p.Rules[j].Num })
				return nil
			}))
}

func secProfileTree() []*Node {
	num := A(KInt, "NUMBER", "Rule number 0-1023 (rules are applied in this order)", secRuleNode("permit"), secRuleNode("deny")).Rng(0, 1023)
	num.N(func(s *Session, v []string) error {
		p, err := secProfile(s)
		if err != nil {
			return err
		}
		if err := secLocked(s, p.ID); err != nil {
			return err
		}
		n, _ := strconv.Atoi(v[0])
		for i := range p.Rules {
			if p.Rules[i].Num == n {
				p.Rules = append(p.Rules[:i], p.Rules[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("rule %d not found", n)
	})
	return []*Node{K("rule", "Add a rule to the security profile", num)}
}

// secConfigNodes: security-profile N, security (N|none|default).
func secConfigNodes() []*Node {
	prof := K("security-profile", "Create or edit a security profile",
		A(KInt, "NUMBER", "Profile number").Rng(1, 255).
			R(func(s *Session, v []string) error {
				id, _ := strconv.Atoi(v[0])
				if s.cfg().FindSecProfile(id) == nil {
					s.cfg().SecProfiles = append(s.cfg().SecProfiles, &config.SecProfile{ID: id})
				}
				s.enter(modeSecProf, v[0])
				return nil
			}).
			N(func(s *Session, v []string) error {
				id, _ := strconv.Atoi(v[0])
				if s.cfg().FindSecProfile(id) == nil {
					return fmt.Errorf("security profile %d not found", id)
				}
				if err := secLocked(s, id); err != nil {
					return err
				}
				out := s.cfg().SecProfiles[:0:0]
				for _, p := range s.cfg().SecProfiles {
					if p.ID != id {
						out = append(out, p)
					}
				}
				s.cfg().SecProfiles = out
				return nil
			}))
	unset := func(s *Session, _ []string) error { s.cfg().Security = "none"; return nil }
	sec := K("security", "Set the security profile for line ports",
		K("default", "Built-in profile: SSH, Telnet and SNMP are denied").R(func(s *Session, _ []string) error {
			s.cfg().Security = ""
			return nil
		}).N(unset),
		K("none", "Empty profile: everything is permitted").R(unset),
		A(KInt, "NUMBER", "Profile number").Rng(1, 255).R(func(s *Session, v []string) error {
			id, _ := strconv.Atoi(v[0])
			if s.cfg().FindSecProfile(id) == nil {
				return fmt.Errorf("security profile %d not found", id)
			}
			s.cfg().Security = strconv.Itoa(id)
			return nil
		}).N(unset)).N(unset)
	return []*Node{prof, sec}
}

func showSecurityNode() *Node {
	return K("security-profile", "Show security profiles").R(func(s *Session, _ []string) error {
		c := s.cfg() // the rules hold no secrets: every role that may see the command reads them
		show := func(name string, rules []config.SecRule) {
			s.Printf("Security profile %s\n", name)
			for _, r := range rules {
				s.Printf(" %d: %s\n", r.Num, hostRe32.ReplaceAllString(r.String(), "$1/32"))
			}
			s.Printf(" permit any any any\n")
		}
		show("default", config.DefaultSecurity)
		sps := append([]*config.SecProfile(nil), c.SecProfiles...)
		sort.Slice(sps, func(i, j int) bool { return sps[i].ID < sps[j].ID })
		for _, p := range sps {
			show(strconv.Itoa(p.ID), p.Rules)
		}
		name, _ := c.EffectiveSecurity()
		s.Printf("Applied to line ports: %s\n", name)
		return nil
	})
}
