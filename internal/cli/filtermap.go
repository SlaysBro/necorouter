package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"necorouter/internal/config"
)

// filter-map ipv4: access lists for transit traffic (see internal/config/fmap.go).
//   filter-map ipv4 NAME [SEQ]   — create a rule and enter filter-map-ipv4 mode
//     match [fragments] PROTOCOL SRC [PORT] DST [PORT] [dscp N] [ttl ...] [FLAGS]
//     set accept | discard | reject
//   interface: set filter-map in|out NAME [SEQ]
//   show filter-map ipv4 [NAME]

func fmapNames(s *Session) []string {
	var o []string
	for _, f := range s.cfg().FilterMaps {
		o = append(o, f.Name)
	}
	return o
}

// fmapUsers — interfaces to which list name is bound.
func fmapUsers(c *config.Config, name string) []string {
	var o []string
	for _, i := range c.Ifaces {
		for _, b := range i.FMaps {
			if b.Name == name {
				o = append(o, i.Name)
				break
			}
		}
	}
	return o
}

func fmapEnter(s *Session, v []string) error {
	fm := s.cfg().GetFilterMap(v[0])
	seq := 0
	if len(v) > 1 {
		seq, _ = strconv.Atoi(v[1])
	} else {
		seq = fm.NextSeq()
	}
	fm.GetRule(seq)
	s.enter("fmap", v[0]+" "+strconv.Itoa(seq))
	return nil
}

func fmapDelete(s *Session, v []string) error {
	c := s.cfg()
	fm := c.FindFilterMap(v[0])
	if fm == nil {
		return fmt.Errorf("filter-map %s not found", v[0])
	}
	removeMap := len(v) == 1
	if !removeMap {
		seq, _ := strconv.Atoi(v[1])
		if fm.Rule(seq) == nil {
			return fmt.Errorf("filter-map %s has no rule %d", fm.Name, seq)
		}
		removeMap = len(fm.Rules) == 1
	}
	if removeMap {
		if u := fmapUsers(c, fm.Name); len(u) > 0 {
			return fmt.Errorf("filter-map %s is attached to interface %s: remove it first (no set filter-map ...)", fm.Name, strings.Join(u, ", "))
		}
		out := c.FilterMaps[:0:0]
		for _, f := range c.FilterMaps {
			if f != fm {
				out = append(out, f)
			}
		}
		c.FilterMaps = out
		return nil
	}
	seq, _ := strconv.Atoi(v[1])
	rules := fm.Rules[:0:0]
	for _, r := range fm.Rules {
		if r.Seq != seq {
			rules = append(rules, r)
		}
	}
	fm.Rules = rules
	return nil
}

// fmapConfigNode: filter-map ipv4 NAME [SEQ] / no filter-map ipv4 NAME [SEQ].
func fmapConfigNode() *Node {
	seq := A(KInt, "<0-65535>", "Rule sequence number (default: next free, step 10)").Rng(0, 65535).R(fmapEnter).N(fmapDelete)
	name := A(KWord, "FILTER_MAP_NAME", "Filter-map name (letters, digits, _ . : / -)", seq).Chk(nameOK).Cmp(fmapNames).R(fmapEnter).N(fmapDelete)
	return K("filter-map", "Create or edit a filter-map (packet filtering list)",
		K("ipv4", "L3 (IPv4) filter-map for transit traffic", name))
}

func fmapCur(s *Session) (*config.FilterMap, *config.FMRule, error) {
	f := strings.Fields(s.top().obj)
	if len(f) == 2 {
		if fm := s.cfg().FindFilterMap(f[0]); fm != nil {
			seq, _ := strconv.Atoi(f[1])
			if r := fm.Rule(seq); r != nil {
				return fm, r, nil
			}
		}
	}
	return nil, nil, fmt.Errorf("filter-map rule no longer exists")
}

func fmapModeTree() []*Node {
	match := K("match", "Set the match condition of the rule",
		A(KRest, "[fragments] PROTOCOL SOURCE [PORT] DESTINATION [PORT] [dscp N] [ttl ...] [FLAGS]",
			"Protocol (any, tcp, udp, icmp, gre, ospf, vrrp ... or 0-255); address: any | host A.B.C.D | A.B.C.D/M | A.B.C.D WILDCARD; "+
				"port: eq|gt|lt PORT | range LOW HIGH (tcp, udp)").
			R(func(s *Session, v []string) error {
				_, r, err := fmapCur(s)
				if err != nil {
					return err
				}
				m, err := config.ParseFMMatch(v[0])
				if err != nil {
					return err
				}
				line := m.String()
				for _, x := range r.Matches {
					if x == line {
						return nil
					}
				}
				r.Matches = append(r.Matches, line)
				return nil
			}).
			N(func(s *Session, v []string) error {
				_, r, err := fmapCur(s)
				if err != nil {
					return err
				}
				m, err := config.ParseFMMatch(v[0])
				if err != nil {
					return err
				}
				line := m.String()
				for i, x := range r.Matches {
					if x == line {
						r.Matches = append(r.Matches[:i], r.Matches[i+1:]...)
						return nil
					}
				}
				return fmt.Errorf("no such match line in this rule")
			}))
	act := func(a string) Handler {
		return func(s *Session, _ []string) error {
			_, r, err := fmapCur(s)
			if err != nil {
				return err
			}
			r.Action, r.CryptoMap, r.CryptoPeer = a, "", ""
			return nil
		}
	}
	set := K("set", "Set the action for matching packets",
		K("accept", "Permit the packets").R(act("accept")),
		K("discard", "Drop the packets without an ICMP notification").R(act("discard")),
		K("reject", "Drop the packets and send an ICMP notification").R(act("reject")),
		fmapCryptoNode(),
	).N(func(s *Session, _ []string) error {
		_, r, err := fmapCur(s)
		if err != nil {
			return err
		}
		r.Action, r.CryptoMap, r.CryptoPeer = "", "", ""
		return nil
	})
	return []*Node{match, set}
}

// fmapIfaceNode: set filter-map in|out NAME [SEQ] in interface mode.
func fmapIfaceNode() *Node {
	bind := func(dir string) Handler {
		return func(s *Session, v []string) error {
			i, err := s.iface()
			if err != nil {
				return err
			}
			name := v[0]
			if s.cfg().FindFilterMap(name) == nil {
				return fmt.Errorf("filter-map %s not found", name)
			}
			seq, max := 0, 0
			var keep []config.FMBind
			for _, b := range i.FMaps {
				if b.Dir == dir && b.Name == name {
					continue // a repeated binding replaces the previous one
				}
				if b.Dir == dir && b.Seq > max {
					max = b.Seq
				}
				keep = append(keep, b)
			}
			if len(v) > 1 {
				seq, _ = strconv.Atoi(v[1])
				for _, b := range keep {
					if b.Dir == dir && b.Seq == seq {
						return fmt.Errorf("sequence %d is already used by filter-map %s", seq, b.Name)
					}
				}
			} else {
				seq = (max/10 + 1) * 10
			}
			i.FMaps = append(keep, config.FMBind{Dir: dir, Name: name, Seq: seq})
			sort.SliceStable(i.FMaps, func(a, b int) bool {
				if i.FMaps[a].Dir != i.FMaps[b].Dir {
					return i.FMaps[a].Dir < i.FMaps[b].Dir
				}
				return i.FMaps[a].Seq < i.FMaps[b].Seq
			})
			return nil
		}
	}
	unbind := func(dir string) Handler {
		return func(s *Session, v []string) error {
			i, err := s.iface()
			if err != nil {
				return err
			}
			for k, b := range i.FMaps {
				if b.Dir == dir && b.Name == v[0] {
					i.FMaps = append(i.FMaps[:k], i.FMaps[k+1:]...)
					return nil
				}
			}
			return fmt.Errorf("filter-map %s is not attached in direction %s", v[0], dir)
		}
	}
	dirNode := func(dir, help string) *Node {
		seq := A(KInt, "SEQUENCE", "Order of the list among the lists of this interface (default: next free, step 10)").Rng(0, 65535).R(bind(dir))
		name := A(KWord, "FILTER_MAP_NAME", "Filter-map name (letters, digits, _ . : / -)", seq).Chk(nameOK).Cmp(fmapNames).R(bind(dir)).N(unbind(dir))
		return K(dir, help, name)
	}
	return K("set", "Set interface parameters",
		K("filter-map", "Attach a filter-map to the interface",
			dirNode("in", "Filter packets entering the interface"),
			dirNode("out", "Filter packets leaving the interface")))
}

func showFilterMapNode() *Node {
	show := func(s *Session, v []string) error {
		if err := s.needAdmin(); err != nil {
			return err
		}
		found := false
		for _, fm := range s.cfg().FilterMaps {
			if len(v) > 0 && fm.Name != v[0] {
				continue
			}
			found = true
			s.Printf(" Filter map %s\n", fm.Name)
			for _, r := range fm.Rules {
				s.Printf(" Filter %d\n", r.Seq)
				for _, m := range r.Matches {
					s.Printf(" match %s\n", m)
				}
				if r.Action != "" {
					s.Printf(" set %s\n", r.Action)
				}
				if r.CryptoMap != "" {
					s.Printf(" set crypto-map %s peer %s\n", r.CryptoMap, r.CryptoPeer)
				}
			}
		}
		if len(v) > 0 && !found {
			return fmt.Errorf("filter-map %s not found", v[0])
		}
		return nil
	}
	return K("filter-map", "Show filter-map lists",
		K("ipv4", "Show L3 (IPv4) filter-maps",
			A(KWord, "FILTER_MAP_NAME", "Filter-map name (letters, digits, _ . : / -)").Cmp(fmapNames).R(show)).R(show))
}
