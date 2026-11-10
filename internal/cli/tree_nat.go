package cli

import (
	"fmt"
	"necorouter/internal/config"
	"strings"
)

func natTree() *Node {
	addStatic := func(proto string) Handler {
		return func(s *Session, v []string) error {
			e := &config.NATStatic{Proto: proto, Local: v[0]}
			if proto != "" {
				fmt.Sscan(v[1], &e.LPort)
				e.Global = v[2]
				fmt.Sscan(v[3], &e.GPort)
			} else {
				e.Global = v[1]
			}
			c := s.cfg()
			for i, x := range c.NATStatic {
				if x.Proto == e.Proto && x.Global == e.Global && x.GPort == e.GPort {
					c.NATStatic[i] = e
					return nil
				}
			}
			c.NATStatic = append(c.NATStatic, e)
			return nil
		}
	}
	delStatic := func(proto string) Handler {
		return func(s *Session, v []string) error {
			c := s.cfg()
			var out []*config.NATStatic
			for _, x := range c.NATStatic {
				match := x.Proto == proto && x.Local == v[0]
				if proto != "" {
					match = match && fmt.Sprint(x.LPort) == v[1] && x.Global == v[2] && fmt.Sprint(x.GPort) == v[3]
				} else {
					match = match && x.Global == v[1]
				}
				if !match {
					out = append(out, x)
				}
			}
			c.NATStatic = out
			return nil
		}
	}
	withPorts := func(proto string) *Node {
		return K(proto, strings.ToUpper(proto)+" port translation",
			A(KIP, "LOCAL-IP", "Inside local address",
				A(KInt, "LOCAL-PORT", "Inside local port",
					A(KIP, "GLOBAL-IP", "Inside global address",
						A(KInt, "GLOBAL-PORT", "Inside global port").Rng(1, 65535).R(addStatic(proto)).N(delStatic(proto))).
						Rng(1, 65535)).Rng(1, 65535)))
	}
	// withDest: ip nat destination static tcp|udp GLOBAL-IP PORT LOCAL-IP PORT [hairpin]
	withDest := func(proto string) *Node {
		add := func(hp bool) Handler {
			return func(s *Session, v []string) error {
				e := &config.NATStatic{Proto: proto, Dest: true, Hairpin: hp, Global: v[0], Local: v[2]}
				fmt.Sscan(v[1], &e.GPort)
				fmt.Sscan(v[3], &e.LPort)
				c := s.cfg()
				for i, x := range c.NATStatic {
					if x.Dest && x.Proto == proto && x.Global == e.Global && x.GPort == e.GPort {
						c.NATStatic[i] = e
						return nil
					}
				}
				c.NATStatic = append(c.NATStatic, e)
				return nil
			}
		}
		del := func(s *Session, v []string) error {
			c := s.cfg()
			var out []*config.NATStatic
			for _, x := range c.NATStatic {
				if x.Dest && x.Proto == proto && x.Global == v[0] && fmt.Sprint(x.GPort) == v[1] {
					continue
				}
				out = append(out, x)
			}
			c.NATStatic = out
			return nil
		}
		return K(proto, strings.ToUpper(proto)+" port translation",
			A(KIP, "GLOBAL-IP", "External (outside) address",
				A(KInt, "GLOBAL-PORT", "External port",
					A(KIP, "LOCAL-IP", "Inside local address",
						A(KInt, "LOCAL-PORT", "Inside local port",
							K("hairpin", "Allow access from inside by the external address").R(add(true))).
							Rng(1, 65535).R(add(false)).N(del)).
						Rng(1, 65535)).Rng(1, 65535)))
	}
	// setDyn(off)(over): off=0 — the rule name is inside-to-outside (a keyword, not in v);
	// off=1 — the rule name is the first argument.
	setDyn := func(off int, over string) Handler {
		return func(s *Session, v []string) error {
			name := "inside-to-outside"
			if off == 1 {
				name = v[0]
			}
			pool := v[off]
			o := over
			if o == "" {
				o = v[off+1]
			} else if o == "interface" {
				o = "interface " + v[off+1]
			}
			c := s.cfg()
			for _, d := range c.NATDyn {
				if d.Name == name {
					d.Pool, d.Overload = pool, o
					return nil
				}
			}
			c.NATDyn = append(c.NATDyn, &config.NATDyn{Name: name, Pool: pool, Overload: o})
			return nil
		}
	}
	delDyn := func(s *Session, v []string) error {
		c := s.cfg()
		name := "inside-to-outside"
		if len(v) > 0 {
			name = v[0]
		}
		out := c.NATDyn[:0:0]
		for _, d := range c.NATDyn {
			if d.Name != name {
				out = append(out, d)
			}
		}
		c.NATDyn = out
		return nil
	}
	dynTail := func(off int, mk func(int, string) Handler) *Node {
		return K("pool", "Inside local pool",
			A(KWord, "POOL", "Pool name",
				K("overload", "Translate many to one (PAT)",
					A(KIP, "A.B.C.D", "Inside global address").R(mk(off, "")),
					K("interface", "Use an interface address",
						A(KWord, "NAME", "Interface name (letters, digits, _ . : / -)").Cmp(ifaceNames).R(mk(off, "interface"))))).Cmp(natPoolNames))
	}
	return K("nat", "NAT configuration commands",
		K("pool", "Define a pool of inside local addresses",
			A(KWord, "NAME", "Pool name",
				A(KRange, "A.B.C.D[-A.B.C.D][,...]", "Comma-separated IPv4 addresses or ranges").R(func(s *Session, v []string) error {
					ranges, err := config.ParseIPv4Ranges(v[1])
					if err != nil {
						return err
					}
					c := s.cfg()
					if p := c.FindNATPool(v[0]); p != nil {
						p.Ranges = ranges
						return nil
					}
					c.NATPools = append(c.NATPools, &config.NATPool{Name: v[0], Ranges: ranges})
					return nil
				})).N(func(s *Session, v []string) error {
				c := s.cfg()
				out := c.NATPools[:0:0]
				for _, p := range c.NATPools {
					if p.Name != v[0] {
						out = append(out, p)
					}
				}
				c.NATPools = out
				return nil
			}).Cmp(natPoolNames)),
		K("destination", "Destination address translation (inbound access to an inside server)",
			K("static", "Static translation of an external address:port to an inside server",
				withDest("tcp"), withDest("udp"))),
		K("source", "Source address translation",
			K("dynamic", "Dynamic translation",
				// standard form: inside-to-outside is the direction keyword
				K("inside-to-outside", "Translate inside local addresses to the outside", dynTail(0, setDyn)).N(delDyn),
				// the arbitrary rule name is the form the configuration is written in (hidden from hints)
				A(KWord, "RULE", "Rule name", dynTail(1, setDyn)).N(delDyn).Hide()),
			K("static", "Static translation (port forwarding)",
				withPorts("tcp"), withPorts("udp"),
				A(KIP, "LOCAL-IP", "Inside local address",
					A(KIP, "GLOBAL-IP", "Inside global address").R(addStatic("")).N(delStatic("")))))).
		N(nil)
}

func natPoolNames(s *Session) []string {
	var o []string
	for _, p := range s.cfg().NATPools {
		o = append(o, p.Name)
	}
	return o
}

func poolNames(s *Session) []string {
	var o []string
	for _, p := range s.cfg().IPPools {
		o = append(o, p.Name)
	}
	return o
}
