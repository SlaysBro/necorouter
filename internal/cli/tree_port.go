package cli

import (
	"fmt"
	"necorouter/internal/config"
)

func portTree() []*Node {
	cur := func(s *Session) (*config.Port, error) {
		p := s.cfg().FindPort(s.top().obj)
		if p == nil {
			return nil, fmt.Errorf("port no longer exists")
		}
		return p, nil
	}
	return []*Node{
		K("description", "Port description",
			A(KRest, "LINE", "Description text").R(func(s *Session, v []string) error {
				p, err := cur(s)
				if err != nil {
					return err
				}
				p.Desc = v[0]
				return nil
			})),
		K("service-instance", "Service instance (logical sub-port)",
			A(KWord, "NAME", "Service instance name (letters, digits, _ . : / -)").Chk(nameOK).Cmp(func(s *Session) []string {
				var o []string
				if p, err := cur(s); err == nil {
					for _, x := range p.SIs {
						o = append(o, x.Name)
					}
				}
				return o
			}).R(func(s *Session, v []string) error {
				if s.top().kind == modeSI { // neighboring service-instance without exit
					s.pop()
				}
				p, err := cur(s)
				if err != nil {
					return err
				}
				if p.FindSI(v[0]) == nil {
					p.SIs = append(p.SIs, &config.SI{Name: v[0]})
				}
				s.push(modeSI, v[0])
				s.frames[len(s.frames)-1].obj2 = p.Name
				return nil
			}).N(func(s *Session, v []string) error {
				p, err := cur(s)
				if err != nil {
					return err
				}
				var out []*config.SI
				for _, x := range p.SIs {
					if x.Name != v[0] {
						out = append(out, x)
					}
				}
				p.SIs = out
				return nil
			})),
	}
}

func siTree() []*Node {
	cur := func(s *Session) (*config.SI, error) {
		if p := s.cfg().FindPort(s.top().obj2); p != nil {
			if si := p.FindSI(s.top().obj); si != nil {
				return si, nil
			}
		}
		return nil, fmt.Errorf("service-instance no longer exists")
	}
	do := func(f func(si *config.SI, v []string) error) Handler {
		return func(s *Session, v []string) error {
			si, err := cur(s)
			if err != nil {
				return err
			}
			return f(si, v)
		}
	}
	rw := func(op string) *Node {
		return K(op, "Rewrite action",
			A(KInt, "TAGS", "Number of tags").Rng(1, 2).R(do(func(si *config.SI, v []string) error {
				si.Rewrite = op + " " + v[0]
				return nil
			})))
	}
	return []*Node{
		K("encapsulation", "Configure ethernet frame matching criteria",
			K("untagged", "Match untagged frames").R(do(func(si *config.SI, _ []string) error {
				si.Encap, si.VLAN, si.Exact = "untagged", 0, false
				return nil
			})),
			K("dot1q", "Match 802.1Q tagged frames",
				A(KInt, "VLAN-ID", "VLAN ID", K("exact", "Match only frames with exactly this tag").R(do(func(si *config.SI, v []string) error {
					si.Encap = "dot1q"
					fmt.Sscan(v[0], &si.VLAN)
					si.Exact = true
					return nil
				}))).Rng(1, 4094).R(do(func(si *config.SI, v []string) error {
					si.Encap, si.Exact = "dot1q", false
					fmt.Sscan(v[0], &si.VLAN)
					return nil
				})))).
			N(do(func(si *config.SI, _ []string) error { si.Encap, si.VLAN, si.Exact = "", 0, false; return nil })),
		K("rewrite", "Ethernet frame tag rewrite", rw("pop"), rw("push")).
			N(do(func(si *config.SI, _ []string) error { si.Rewrite = ""; return nil })),
		K("connect", "Connect the service instance",
			K("ip", "Connect to an IP interface",
				K("interface", "IP interface",
					A(KWord, "NAME", "Interface name (letters, digits, _ . : / -)").Cmp(ifaceNames).R(func(s *Session, v []string) error {
						si, err := cur(s)
						if err != nil {
							return err
						}
						if s.cfg().FindIface(v[0]) == nil {
							return fmt.Errorf("interface %s does not exist (create it with 'interface %s')", v[0], v[0])
						}
						if p, o := s.cfg().Binding(v[0]); o != nil && o != si {
							return fmt.Errorf("interface %s is already connected to %s/%s", v[0], p.Name, o.Name)
						}
						si.Connect = v[0]
						return nil
					})))).
			N(do(func(si *config.SI, _ []string) error { si.Connect = ""; return nil })),
	}
}

func portNames(s *Session) []string {
	if s.R.Mgr == nil {
		return nil
	}
	return s.R.Mgr.Env.Ports.Names()
}
