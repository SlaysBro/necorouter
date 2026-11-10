package cli

import (
	"fmt"
	"necorouter/internal/config"
)

func ospfTree() []*Node {
	o := func(f func(o *config.OSPF, v []string) error) Handler {
		return func(s *Session, v []string) error {
			if s.cfg().OSPF == nil {
				return fmt.Errorf("OSPF process no longer exists")
			}
			return f(s.cfg().OSPF, v)
		}
	}
	addNet := func(prefix func([]string) (string, error), areaIdx int) func(*config.OSPF, []string) error {
		return func(p *config.OSPF, v []string) error {
			c, err := prefix(v)
			if err != nil {
				return err
			}
			for n := range p.Nets {
				if p.Nets[n].Prefix == c {
					p.Nets[n].Area = v[areaIdx]
					return nil
				}
			}
			p.Nets = append(p.Nets, config.OSPFNet{Prefix: c, Area: v[areaIdx]})
			return nil
		}
	}
	delNet := func(prefix func([]string) (string, error)) func(*config.OSPF, []string) error {
		return func(p *config.OSPF, v []string) error {
			c, err := prefix(v)
			if err != nil {
				return err
			}
			var out []config.OSPFNet
			for _, n := range p.Nets {
				if n.Prefix != c {
					out = append(out, n)
				}
			}
			p.Nets = out
			return nil
		}
	}
	byCIDR := func(v []string) (string, error) { return config.CanonNet(v[0]) }
	byMask := func(v []string) (string, error) {
		p, err := config.PrefixFromMask(v[0], v[1], true)
		if err != nil {
			return "", err
		}
		return config.CanonNet(p)
	}
	areaNode := func(h Handler) *Node {
		return A(KWord, "AREA", "OSPF area ID (A.B.C.D or decimal)").Chk(config.ValidArea).R(h)
	}
	rid := o(func(p *config.OSPF, v []string) error { p.RouterID = v[0]; return nil })
	ridN := o(func(p *config.OSPF, _ []string) error { p.RouterID = ""; return nil })
	return []*Node{
		K("ospf", "OSPF specific commands",
			K("router-id", "Router-ID for the OSPF process",
				A(KIP, "A.B.C.D", "OSPF router-id").R(rid))).N(ridN),
		K("router-id", "Router-ID for the OSPF process", A(KIP, "A.B.C.D", "OSPF router-id").R(rid)).N(ridN).Hide(),
		K("network", "Enable routing on an IP network",
			// network A.B.C.D/M area AREA
			A(KCIDR, "A.B.C.D/M", "Network prefix",
				K("area", "Set the OSPF area ID", areaNode(o(addNet(byCIDR, 1))))).
				N(o(delNet(byCIDR))),
			// network A.B.C.D MASK area AREA (mask 255.255.255.252 or wildcard 0.0.0.3)
			A(KIP, "A.B.C.D", "Network address",
				A(KWord, "MASK", "Network mask (255.255.255.0) or wildcard (0.0.0.255)",
					K("area", "Set the OSPF area ID", areaNode(o(addNet(byMask, 2))))).
					Chk(func(m string) bool { _, e := config.PrefixFromMask("0.0.0.0", m, true); return e == nil }).
					N(o(delNet(byMask)))),
		),
		K("passive-interface", "Suppress routing updates on an interface",
			K("default", "Suppress routing updates on all interfaces").R(o(func(p *config.OSPF, _ []string) error { p.PassiveDefault = true; return nil })).
				N(o(func(p *config.OSPF, _ []string) error { p.PassiveDefault = false; return nil })),
			A(KWord, "NAME", "Interface name (letters, digits, _ . : / -)").Cmp(ifaceNames).R(o(func(p *config.OSPF, v []string) error {
				p.NoPassive = removeStr(p.NoPassive, v[0])
				if !contains(p.Passive, v[0]) {
					p.Passive = append(p.Passive, v[0])
				}
				return nil
			})).N(o(func(p *config.OSPF, v []string) error {
				p.Passive = removeStr(p.Passive, v[0])
				if !contains(p.NoPassive, v[0]) {
					p.NoPassive = append(p.NoPassive, v[0])
				}
				return nil
			}))),
		K("area", "OSPF area parameters",
			A(KWord, "AREA", "OSPF area ID (A.B.C.D or decimal)",
				K("authentication", "Enable authentication",
					K("message-digest", "Use message-digest authentication").R(o(func(p *config.OSPF, v []string) error {
						if p.AreaAuth == nil {
							p.AreaAuth = map[string]string{}
						}
						p.AreaAuth[v[0]] = "message-digest"
						return nil
					}))).R(o(func(p *config.OSPF, v []string) error {
					if p.AreaAuth == nil {
						p.AreaAuth = map[string]string{}
					}
					p.AreaAuth[v[0]] = "simple"
					return nil
				})).N(o(func(p *config.OSPF, v []string) error { delete(p.AreaAuth, v[0]); return nil })))),
		K("redistribute", "Redistribute information from another routing protocol",
			redistNode("connected", "Connected routes", o),
			redistNode("static", "Static routes", o)),
		K("default-information", "Control distribution of default information",
			K("originate", "Distribute a default route",
				optNodes(o, optDefaultCtx, nil, 0)...).
				R(o(func(p *config.OSPF, _ []string) error {
					p.DefaultOrig, p.DefaultRM = true, ""
					delete(p.RedistMetric, config.DefaultKey)
					delete(p.RedistType, config.DefaultKey)
					p.DefaultAlways = false
					return nil
				})).
				N(o(func(p *config.OSPF, _ []string) error {
					p.DefaultOrig, p.DefaultRM, p.DefaultAlways = false, "", false
					delete(p.RedistMetric, config.DefaultKey)
					delete(p.RedistType, config.DefaultKey)
					return nil
				}))),
	}
}

// redistNode — redistribute PROTOCOL [metric N] [metric-type T] [route-map NAME].
func redistNode(proto, help string, o func(func(*config.OSPF, []string) error) Handler) *Node {
	drop := func(p *config.OSPF) {
		delete(p.RedistRM, proto)
		delete(p.RedistMetric, proto)
		delete(p.RedistType, proto)
	}
	return K(proto, help, optNodes(o, optCtx{key: proto}, nil, 0)...).
		R(o(func(p *config.OSPF, _ []string) error { addOnce(&p.Redistribute, proto); drop(p); return nil })).
		N(o(func(p *config.OSPF, _ []string) error {
			p.Redistribute = removeStr(p.Redistribute, proto)
			drop(p)
			return nil
		}))
}
