package cli

import (
	"fmt"
	"necorouter/internal/config"
)

func ifaceTree() []*Node {
	need := func(f func(i *config.Iface, v []string) error) Handler {
		return func(s *Session, v []string) error {
			i, err := s.iface()
			if err != nil {
				return err
			}
			return f(i, v)
		}
	}
	return []*Node{
		K("description", "Interface specific description",
			A(KRest, "LINE", "Description text").R(need(func(i *config.Iface, v []string) error { i.Desc = v[0]; return nil }))).
			N(need(func(i *config.Iface, _ []string) error { i.Desc = ""; return nil })),
		K("shutdown", "Shutdown the selected interface").R(need(func(i *config.Iface, _ []string) error { i.Shutdown = true; return nil })).
			N(need(func(i *config.Iface, _ []string) error { i.Shutdown = false; return nil })),
		K("dhcp-server", "Attach a DHCP server instance",
			A(KInt, "ID", "Server number").Rng(1, 255).R(need(func(i *config.Iface, v []string) error { fmt.Sscan(v[0], &i.DHCP); return nil }))).
			N(need(func(i *config.Iface, _ []string) error { i.DHCP = 0; return nil })),
		K("ip", "Interface Internet Protocol config commands",
			K("address", "Set the IP address of an interface",
				A(KCIDR, "A.B.C.D/M", "IP address with prefix length").R(need(func(i *config.Iface, v []string) error {
					a, err := config.Canon(v[0])
					if err != nil {
						return err
					}
					// a repeated command REPLACES the interface address (as in IOS):
					// the new address becomes the only one, the old one is removed from the kernel on reconcile
					i.Addrs = []string{a}
					return nil
				})).N(need(func(i *config.Iface, v []string) error {
					a, err := config.Canon(v[0])
					if err != nil {
						return err
					}
					i.Addrs = removeStr(i.Addrs, a)
					return nil
				})),
				// ip address A.B.C.D 255.255.255.0 — the same with a mask
				A(KIP, "A.B.C.D", "IP address",
					A(KWord, "MASK", "Subnet mask (255.255.255.0)").Chk(func(m string) bool {
						_, e := config.PrefixFromMask("0.0.0.0", m, false)
						return e == nil
					}).R(need(func(i *config.Iface, v []string) error {
						a, err := config.PrefixFromMask(v[0], v[1], false)
						if err != nil {
							return err
						}
						i.Addrs = []string{a}
						return nil
					})).N(need(func(i *config.Iface, v []string) error {
						a, err := config.PrefixFromMask(v[0], v[1], false)
						if err != nil {
							return err
						}
						i.Addrs = removeStr(i.Addrs, a)
						return nil
					}))),
			).N(need(func(i *config.Iface, _ []string) error { i.Addrs = nil; return nil })),
			K("mtu", "Set the interface MTU",
				A(KInt, "MTU", "Bytes").Rng(68, 9216).R(need(func(i *config.Iface, v []string) error { fmt.Sscan(v[0], &i.MTU); return nil }))).
				N(need(func(i *config.Iface, _ []string) error { i.MTU = 0; return nil })),
			K("nat", "Network address translation role",
				K("inside", "Inside (local) network").R(need(func(i *config.Iface, _ []string) error { i.NAT = "inside"; return nil })).
					N(need(func(i *config.Iface, _ []string) error { i.NAT = ""; return nil })),
				K("outside", "Outside (global) network").R(need(func(i *config.Iface, _ []string) error { i.NAT = "outside"; return nil })).
					N(need(func(i *config.Iface, _ []string) error { i.NAT = ""; return nil }))),
			K("tunnel", "Configure an IP tunnel",
				A(KIP, "SOURCE", "Tunnel source address",
					A(KIP, "DESTINATION", "Tunnel destination address",
						K("mode", "Tunnel encapsulation mode",
							K("gre", "Generic Routing Encapsulation (RFC 2784)").R(need(func(i *config.Iface, v []string) error {
								i.Tunnel = &config.Tunnel{Src: v[0], Dst: v[1], Mode: "gre"}
								return nil
							}))).N(nil)).R(need(func(i *config.Iface, v []string) error {
						i.Tunnel = &config.Tunnel{Src: v[0], Dst: v[1], Mode: "gre"}
						return nil
					})))).N(need(func(i *config.Iface, _ []string) error { i.Tunnel = nil; return nil })),
			K("ospf", "OSPF interface commands",
				K("authentication", "Enable authentication",
					K("message-digest", "Use message-digest (MD5) authentication").R(need(func(i *config.Iface, _ []string) error { i.OSPFAuth = "message-digest"; return nil })),
					K("null", "Use no authentication").R(need(func(i *config.Iface, _ []string) error { i.OSPFAuth = ""; return nil }))).
					R(need(func(i *config.Iface, _ []string) error { i.OSPFAuth = "simple"; return nil })).
					N(need(func(i *config.Iface, _ []string) error { i.OSPFAuth = ""; return nil })),
				K("authentication-key", "Simple authentication password",
					A(KWord, "KEY", "Password").R(need(func(i *config.Iface, v []string) error { i.OSPFSimple = v[0]; return nil }))).
					N(need(func(i *config.Iface, _ []string) error { i.OSPFSimple = ""; return nil })).Sec(),
				K("message-digest-key", "Message digest authentication password (key)",
					A(KInt, "KEY-ID", "Key ID",
						K("md5", "Use MD5 algorithm",
							A(KWord, "KEY", "Password (up to 16 characters)").R(need(func(i *config.Iface, v []string) error {
								id := 0
								fmt.Sscan(v[0], &id)
								for n := range i.OSPFKeys {
									if i.OSPFKeys[n].ID == id {
										i.OSPFKeys[n].Key = v[1]
										return nil
									}
								}
								i.OSPFKeys = append(i.OSPFKeys, config.OSPFKey{ID: id, Key: v[1]})
								return nil
							}))).Sec()).Rng(1, 255).N(need(func(i *config.Iface, v []string) error {
						var out []config.OSPFKey
						for _, k := range i.OSPFKeys {
							if fmt.Sprint(k.ID) != v[0] {
								out = append(out, k)
							}
						}
						i.OSPFKeys = out
						return nil
					}))),
				K("cost", "Interface cost",
					A(KInt, "COST", "Cost").Rng(1, 65535).R(need(func(i *config.Iface, v []string) error { fmt.Sscan(v[0], &i.OSPFCost); return nil }))).
					N(need(func(i *config.Iface, _ []string) error { i.OSPFCost = 0; return nil })),
				K("network", "Network type",
					K("point-to-point", "Point-to-point network").R(need(func(i *config.Iface, _ []string) error { i.OSPFNetwork = "point-to-point"; return nil })),
					K("broadcast", "Broadcast network").R(need(func(i *config.Iface, _ []string) error { i.OSPFNetwork = "broadcast"; return nil }))).
					N(need(func(i *config.Iface, _ []string) error { i.OSPFNetwork = ""; return nil })))),
	}
}

func ifaceNames(s *Session) []string {
	var o []string
	for _, i := range s.cfg().Ifaces {
		o = append(o, i.Name)
	}
	return o
}

func (s *Session) iface() (*config.Iface, error) {
	i := s.cfg().FindIface(s.top().obj)
	if i == nil {
		return nil, fmt.Errorf("interface %s no longer exists", s.top().obj)
	}
	return i, nil
}
