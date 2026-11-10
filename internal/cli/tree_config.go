package cli

import (
	"fmt"
	"strings"

	"necorouter/internal/config"
	"necorouter/internal/svc"
)

// cfgObjects are the commands that select an object to configure; the sub-modes list them as switchers,
// so that the next object can be selected without leaving the current one.
type cfgObjects struct {
	iface, port, router, username, filterMap *Node
	switchers                                []*Node
}

// buildConfig fills the configuration mode (t[modeConfig]); it must run before buildSubModes, which copies
// the global commands into the sub-modes. The order is checked by the tests (every global command is reachable
// from every sub-mode).
func buildConfig(t map[string][]*Node, nav nav) cfgObjects {
	do, exitN, end := nav.do, nav.exitN, nav.end
	// ---- configuration mode ----
	hostname := K("hostname", "Set the system's network name",
		A(KWord, "WORD", "Hostname (letters, digits, _ . : / -)").Chk(func(v string) bool { return nameOK(v) }).R(func(s *Session, v []string) error {
			s.cfg().Hostname = v[0]
			return nil
		})).N(func(s *Session, _ []string) error { s.cfg().Hostname = config.DefaultHostname; return nil })

	ipCfg := K("ip", "Global IP configuration",
		K("domain-name", "Set the default domain name",
			A(KWord, "WORD", "Domain name").R(func(s *Session, v []string) error { s.cfg().Domain = v[0]; return nil })).
			N(func(s *Session, _ []string) error { s.cfg().Domain = ""; return nil }),
		K("name-server", "Specify a DNS server",
			A(KIP, "A.B.C.D", "DNS server address").R(func(s *Session, v []string) error {
				for _, n := range s.cfg().NameServers {
					if n == v[0] {
						return nil
					}
				}
				s.cfg().NameServers = append(s.cfg().NameServers, v[0])
				return nil
			}).N(func(s *Session, v []string) error {
				s.cfg().NameServers = removeStr(s.cfg().NameServers, v[0])
				return nil
			})).
			N(func(s *Session, _ []string) error { s.cfg().NameServers = nil; return nil }),
		K("route", "Establish static routes",
			A(KCIDR, "PREFIX", "Destination prefix",
				A(KIP, "A.B.C.D", "Next hop address").R(func(s *Session, v []string) error {
					p, err := config.Canon(v[0])
					if err != nil {
						return err
					}
					for _, r := range s.cfg().Routes {
						if r.Prefix == p && r.NextHop == v[1] {
							return nil
						}
					}
					s.cfg().Routes = append(s.cfg().Routes, config.Route{Prefix: p, NextHop: v[1]})
					return nil
				}).N(func(s *Session, v []string) error { return delRoute(s, v[0], v[1]) })).
				N(func(s *Session, v []string) error { return delRoute(s, v[0], "") })),
		K("pool", "Define an address pool",
			A(KWord, "NAME", "Pool name",
				A(KRange, "A.B.C.D[-A.B.C.D][,...]", "Comma-separated IPv4 addresses or ranges").R(func(s *Session, v []string) error {
					ranges, err := config.ParseIPv4Ranges(v[1])
					if err != nil {
						return err
					}
					c := s.cfg()
					if p := c.FindPool(v[0]); p != nil {
						p.Ranges = ranges
						return nil
					}
					c.IPPools = append(c.IPPools, &config.IPPool{Name: v[0], Ranges: ranges})
					return nil
				})).N(func(s *Session, v []string) error {
				c := s.cfg()
				out := c.IPPools[:0:0]
				for _, p := range c.IPPools {
					if p.Name != v[0] {
						out = append(out, p)
					}
				}
				c.IPPools = out
				return nil
			}).Cmp(poolNames)),
		natTree())

	ntp := K("ntp", "Configure NTP",
		K("server", "Use an NTP server",
			A(KWord, "ADDRESS", "Server address or name").Chk(hostRe.MatchString).R(func(s *Session, v []string) error {
				for _, n := range s.cfg().NTPServers {
					if n == v[0] {
						return nil
					}
				}
				s.cfg().NTPServers = append(s.cfg().NTPServers, v[0])
				return nil
			}).N(func(s *Session, v []string) error {
				s.cfg().NTPServers = removeStr(s.cfg().NTPServers, v[0])
				return nil
			})),
		K("synchronize", "Step the system clock to the NTP servers now (needs ntp server)").R(func(s *Session, _ []string) error {
			if s.noApply {
				return nil
			}
			_, err := svc.NTPSync(s.R.Mgr.Env)
			return err
		}),
		K("timezone", "Set the system time zone (utc+N or utc-N in whole hours, up to 14; utc; or Region/City)",
			A(KWord, "ZONE", "utc+3 is UTC+3, utc-5 is UTC-5, utc is UTC, Europe/Moscow is a region and city").Chk(func(v string) bool {
				_, err := svc.ZoneFile(v)
				return err == nil
			}).R(func(s *Session, v []string) error { s.cfg().Timezone = strings.ToLower(v[0]); return nil })).
			N(func(s *Session, _ []string) error { s.cfg().Timezone = ""; return nil }))

	username := K("username", "Establish a user name",
		A(KWord, "NAME", "User name (letters, digits, _ . : / -)").Chk(nameOK).Cmp(userNames).TabOnly().R(func(s *Session, v []string) error {
			c := s.cfg()
			if c.FindUser(v[0]) == nil {
				c.Users = append(c.Users, &config.User{Name: v[0]})
			}
			s.enter(modeUser, v[0])
			return nil
		}).N(func(s *Session, v []string) error {
			c := s.cfg()
			if v[0] == config.BuiltinAdminName {
				return fmt.Errorf("the built-in admin account cannot be removed")
			}
			out := c.Users[:0:0]
			for _, u := range c.Users {
				if u.Name != v[0] {
					out = append(out, u)
				}
			}
			c.Users = out
			return nil
		}))

	iface := K("interface", "Select an interface to configure",
		A(KWord, "NAME", "Interface name (letters, digits, _ . : / -)").Chk(nameOK).Cmp(ifaceNames).R(func(s *Session, v []string) error {
			c := s.cfg()
			if c.FindIface(v[0]) == nil {
				c.Ifaces = append(c.Ifaces, &config.Iface{Name: v[0]})
			}
			s.enter(modeIf, v[0])
			return nil
		}).N(func(s *Session, v []string) error {
			if !s.cfg().RemoveIface(v[0]) {
				return fmt.Errorf("interface %s not found", v[0])
			}
			return nil
		}))

	port := K("port", "Select a port to configure",
		A(KWord, "NAME", "Port name (e0, ge0, te0, ...)").Chk(nameOK).Cmp(portNames).R(func(s *Session, v []string) error {
			if s.R.Mgr != nil && !s.R.Mgr.Env.Ports.Has(v[0]) {
				return fmt.Errorf("port %s not found (available: %s)", v[0], strings.Join(s.R.Mgr.Env.Ports.Names(), ", "))
			}
			c := s.cfg()
			if c.FindPort(v[0]) == nil {
				c.Ports = append(c.Ports, &config.Port{Name: v[0]})
			}
			s.enter(modePort, v[0])
			return nil
		}).N(func(s *Session, v []string) error {
			c := s.cfg()
			out := c.Ports[:0:0]
			for _, p := range c.Ports {
				if p.Name != v[0] {
					out = append(out, p)
				}
			}
			c.Ports = out
			return nil
		}))

	router := K("router", "Enable a routing process",
		K("ospf", "Open Shortest Path First (OSPF)",
			A(KInt, "PROCESS-ID", "Process ID").Rng(1, 65535).R(func(s *Session, v []string) error {
				c := s.cfg()
				if c.OSPF == nil {
					id := 0
					fmt.Sscan(v[0], &id)
					c.OSPF = &config.OSPF{ID: id, AreaAuth: map[string]string{}}
				}
				s.enter(modeOSPF, v[0])
				return nil
			}).N(func(s *Session, _ []string) error { s.cfg().OSPF = nil; return nil })).
			N(func(s *Session, _ []string) error { s.cfg().OSPF = nil; return nil }))

	dhcpSrv := K("dhcp-server", "Configure a DHCP server instance",
		A(KInt, "ID", "Server number").Rng(1, 255).R(func(s *Session, v []string) error {
			id := 0
			fmt.Sscan(v[0], &id)
			c := s.cfg()
			if c.FindDHCP(id) == nil {
				c.DHCP = append(c.DHCP, &config.DHCPServer{ID: id})
			}
			s.enter(modeDHCP, v[0])
			return nil
		}).N(func(s *Session, v []string) error {
			id := 0
			fmt.Sscan(v[0], &id)
			c := s.cfg()
			out := c.DHCP[:0:0]
			for _, d := range c.DHCP {
				if d.ID != id {
					out = append(out, d)
				}
			}
			c.DHCP = out
			for _, i := range c.Ifaces {
				if i.DHCP == id {
					i.DHCP = 0
				}
			}
			return nil
		}))

	secCfg := secConfigNodes()
	epw := enablePassNodes()
	switchers := []*Node{iface, port, router, username, do, exitN, end}
	historyCfg := historyCfgNodes()
	filterMap := fmapConfigNode()
	t[modeConfig] = append([]*Node{hostname, ipCfg, ntp, serviceNode(), featureNode(), username, iface, port, router, dhcpSrv, filterMap, bannerConfigNode(), aaaNode(), servicePolicyNode(), do, exitN, end}, historyCfg...)
	t[modeConfig] = append(t[modeConfig], secCfg...)
	t[modeConfig] = append(t[modeConfig], epw...)
	t[modeConfig] = append(t[modeConfig], cryptoNodes()...)
	t[modeConfig] = append(t[modeConfig], policyConfigNodes()...)
	ipCfg.Kids = append(ipCfg.Kids, prefixListNode())
	return cfgObjects{iface: iface, port: port, router: router, username: username, filterMap: filterMap, switchers: switchers}
}
