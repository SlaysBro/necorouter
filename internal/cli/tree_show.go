package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"necorouter/internal/config"
	"necorouter/internal/svc"
)

// buildShow returns the show tree with the visibility of every command (the EcoRouter table 128 for the
// modes, extensions in the privileged mode only). Running configuration, saved files and features hold
// secrets, so they stay with admin; see needAdmin.
func buildShow() *Node {
	show := showTree()
	show.Kids = append(show.Kids, showConfigNodes()...)
	show.Kids = append(show.Kids, showSecurityNode(), showFeaturesNode(), showFilterMapNode(), showBannerNode(), showRouteMapNode())
	for _, k := range show.Kids {
		if k.Word == "ip" {
			k.Kids = append(k.Kids, showPrefixListNode().For(modePriv, config.RoleAdmin))
		}
	}
	admin, noc := []string{config.RoleAdmin}, []string{config.RoleAdmin, config.RoleNOC}
	both := func(word string, roles []string) { show.kid(word).For(modeExec, roles...).For(modePriv, roles...) }
	show.For(modeExec, noc...).For(modePriv, noc...) // helpdesk has no show commands
	both("hostname", noc)
	both("version", noc)
	both("port", noc)
	both("interface", noc)
	both("ip", noc)
	both("filter-map", noc)
	both("security-profile", noc)
	both("banner", noc)
	both("dhcp-profile", noc)
	show.kid("running-config").For(modeExec, noc...).For(modePriv, noc...)
	show.kid("users").For(modeExec, admin...).For(modePriv, noc...)
	show.kid("log").For(modeExec, admin...).For(modePriv, admin...) // the log may hold command lines: admin only
	show.kid("startup-config").For(modePriv, admin...)
	for _, w := range []string{"route-map", "arp", "ntp", "clock", "service-policy", "crypto-ipsec", "services", "dhcp-server"} {
		show.kid(w).For(modePriv, noc...)
	}
	for _, w := range []string{"files", "config", "features"} {
		show.kid(w).For(modePriv, admin...)
	}
	return show
}

// ---------------- show ----------------

func showTree() *Node {
	env := func(s *Session) *svc.Env {
		if s.R.Mgr == nil {
			return nil
		}
		return s.R.Mgr.Env
	}
	ospf := func(cmd string) Handler {
		return func(s *Session, _ []string) error {
			out, err := svc.OSPFShow(env(s), s.cfg(), cmd)
			if err != nil {
				return err
			}
			s.Printf("%s\n", out)
			return nil
		}
	}
	return K("show", "Show running system information",
		K("running-config", "Current operating configuration").R(func(s *Session, _ []string) error {
			if s.Role == config.RoleAdmin {
				s.Printf("%s", s.cfg().Render())
				return nil
			}
			s.Printf("%s", s.cfg().RenderHidingSecrets())
			return nil
		}),
		K("startup-config", "Contents of startup configuration").R(func(s *Session, _ []string) error {
			b, err := os.ReadFile(s.R.P.Startup())
			if err != nil {
				return fmt.Errorf("startup-config is not present (use 'write' to create it)")
			}
			s.Printf("%s", b)
			return nil
		}),
		logNode(),
		K("version", "System hardware and software status").R(func(s *Session, _ []string) error {
			s.Printf("Software router, necorouter %s\n", VersionString())
			s.Printf("Hostname: %s\nUptime: %s\n", s.cfg().Hostname, time.Since(s.R.Started).Round(time.Second))
			s.Printf("Configuration: %s\n", s.R.P.Startup())
			return nil
		}),
		K("clock", "Display the system clock").R(func(s *Session, _ []string) error {
			s.Printf("%s\n", time.Now().Format("15:04:05 MST Mon Jan 2 2006"))
			return nil
		}),
		K("hostname", "Display the system hostname").R(func(s *Session, _ []string) error {
			s.Printf("%s\n", s.cfg().Hostname)
			return nil
		}),
		K("users", "Display configured users").R(func(s *Session, _ []string) error {
			s.Printf("%-20s %s\n", "Username", "Role")
			for _, u := range s.cfg().Users {
				r := u.Role
				if r == "" {
					r = "-"
				}
				s.Printf("%-20s %s\n", u.Name, r)
			}
			return nil
		}),
		K("port", "Port information",
			A(KWord, "NAME", "Port name").Cmp(portNames).R(func(s *Session, v []string) error {
				if e := env(s); e != nil {
					s.Printf("%s", filterPort(svc.PortList(e, s.cfg()), v[0]))
				}
				return nil
			})).R(func(s *Session, _ []string) error {
			if e := env(s); e != nil {
				s.Printf("%s", svc.PortList(e, s.cfg()))
			}
			return nil
		}),
		K("interface", "Interface status and configuration",
			A(KWord, "NAME", "Interface name (letters, digits, _ . : / -)").Cmp(ifaceNames).R(func(s *Session, v []string) error {
				o := svc.IfaceDetail(env(s), s.cfg(), v[0])
				if o == "" {
					return fmt.Errorf("interface %s not found", v[0])
				}
				s.Printf("%s", o)
				return nil
			})).R(func(s *Session, _ []string) error {
			for _, i := range s.cfg().Ifaces {
				s.Printf("%s", svc.IfaceDetail(env(s), s.cfg(), i.Name))
			}
			return nil
		}),
		K("service-policy", "Show configured traffic service-policies").R(func(s *Session, _ []string) error { return showServicePolicy(s) }),
		K("ip", "IP information",
			K("interface", "IP interface status",
				K("brief", "Brief summary of IP status").R(func(s *Session, _ []string) error {
					s.Printf("%s", svc.IfaceBrief(env(s), s.cfg()))
					return nil
				})),
			K("route", "IP routing table").R(func(s *Session, _ []string) error {
				s.Printf("%s", svc.Routes(env(s), s.cfg()))
				return nil
			}),
			K("ospf", "OSPF information",
				K("neighbor", "Neighbor list").R(ospf("show ip ospf neighbor")),
				K("interface", "Interface information").R(ospf("show ip ospf interface")),
				K("database", "Database summary").R(ospf("show ip ospf database")),
				K("route", "OSPF routing table").R(ospf("show ip ospf route"))).R(ospf("show ip ospf")),
			K("nat", "NAT information",
				K("translations", "Active translations").R(func(s *Session, _ []string) error {
					s.Printf("%s", svc.NATTable(env(s)))
					return nil
				}))),
		K("dhcp-profile", "DHCP profile configuration").R(func(s *Session, _ []string) error {
			for _, d := range s.cfg().DHCP {
				s.Printf("dhcp-profile %d: pool=%s mask=%s gateway=%s dns=%s domain=%s search=%s\n", d.ID, d.Pool, d.Mask, d.Gateway, strings.Join(d.DNS, ","), d.Domain, d.Search)
			}
			return nil
		}),
		K("dhcp-server", "DHCP server leases",
			K("clients", "Leased clients").R(showLeases)),
		K("crypto-ipsec", "IPsec information",
			K("ike", "IKE (Internet Key Exchange) information",
				K("connections", "Configured IKE connections").R(func(s *Session, _ []string) error {
					o, err := svc.IPsecShow(env(s), "connections")
					if err != nil {
						return err
					}
					s.Printf("%s\n", o)
					return nil
				}),
				K("security-associations", "Established IKE and IPsec security associations").R(func(s *Session, _ []string) error {
					o, err := svc.IPsecShow(env(s), "sas")
					if err != nil {
						return err
					}
					s.Printf("%s\n", o)
					return nil
				}))),
		K("ntp", "NTP information",
			K("date", "Current date and time").R(func(s *Session, _ []string) error {
				o, err := svc.NTPDate()
				if err != nil {
					return err
				}
				s.Printf("%s\n", strings.TrimSpace(o))
				return nil
			}),
			K("status", "NTP synchronisation status").R(func(s *Session, _ []string) error {
				o, err := svc.NTPStatus(env(s))
				if err != nil {
					return err
				}
				s.Printf("%s\n", o)
				return nil
			})),
		K("arp", "ARP table").R(func(s *Session, _ []string) error { s.Printf("%s", svc.Arp()); return nil }),
		K("services", "Background services of the router").R(func(s *Session, _ []string) error {
			if e := s.R.Mgr; e != nil {
				s.Printf("%s", e.Status())
			}
			return nil
		}),
	)
}
