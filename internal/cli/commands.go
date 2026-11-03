package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"necorouter/internal/config"
	"necorouter/internal/svc"
)

// trees is built in init: command handlers (copy) call Replay, and it accesses the tree.
var trees map[string][]*Node

func init() { trees = buildTrees() }

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.:/-]*$`)
var hostRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

func nameOK(s string) bool { return nameRe.MatchString(s) }

func (s *Session) enter(kind, obj string) {
	for len(s.frames) > 1 && s.top().kind != "config" {
		s.pop()
	}
	s.push(kind, obj)
}

func (s *Session) iface() (*config.Iface, error) {
	i := s.cfg().FindIface(s.top().obj)
	if i == nil {
		return nil, fmt.Errorf("interface %s no longer exists", s.top().obj)
	}
	return i, nil
}

func ifaceNames(s *Session) []string {
	var o []string
	for _, i := range s.cfg().Ifaces {
		o = append(o, i.Name)
	}
	return o
}
func portNames(s *Session) []string {
	if s.R.Mgr == nil {
		return nil
	}
	return s.R.Mgr.Env.Ports.Names()
}
func poolNames(s *Session) []string {
	var o []string
	for _, p := range s.cfg().IPPools {
		o = append(o, p.Name)
	}
	return o
}
func natPoolNames(s *Session) []string {
	var o []string
	for _, p := range s.cfg().NATPools {
		o = append(o, p.Name)
	}
	return o
}
func userNames(s *Session) []string {
	var o []string
	for _, u := range s.cfg().Users {
		o = append(o, u.Name)
	}
	return o
}

func rangeOf(v string) (string, string) {
	i := strings.IndexByte(v, '-')
	if i < 0 {
		return v, v
	}
	return v[:i], v[i+1:]
}

func buildTrees() map[string][]*Node {
	t := map[string][]*Node{}

	exit := func(s *Session, _ []string) error {
		k := s.top().kind
		if k == "exec" || k == "priv" {
			return errQuit
		}
		s.pop()
		return nil
	}
	exitN := K("exit", "Exit from the current mode").R(exit)
	logout := K("logout", "Exit from the EXEC").R(func(*Session, []string) error { return errQuit })
	quit := K("quit", "Exit from the EXEC").R(func(*Session, []string) error { return errQuit }).Hide()
	end := K("end", "Return to privileged EXEC mode").R(func(s *Session, _ []string) error {
		s.frames = s.frames[:1]
		for len(s.frames) > 0 && s.top().kind != "priv" {
			if len(s.frames) == 1 {
				s.frames[0] = frame{kind: "priv"}
				break
			}
			s.pop()
		}
		return nil
	})
	do := K("do", "Run an EXEC command from configuration mode", A(KRest, "COMMAND", "EXEC command")).R(
		func(*Session, []string) error { return fmt.Errorf("incomplete command") })

	show := showTree()
	show.Kids = append(show.Kids, showConfigNodes()...)
	show.Kids = append(show.Kids, showBackupsNode(), showSecurityNode(), showFeaturesNode(), showFilterMapNode(), showBannerNode(), showRouteMapNode())
	for _, k := range show.Kids {
		if k.Word == "ip" {
			k.Kids = append(k.Kids, showPrefixListNode())
		}
	}
	ping := K("ping", "Send echo messages", A(KWord, "HOST", "Destination address or name").Chk(hostRe.MatchString).R(cmdPing))
	trace := K("traceroute", "Trace route to destination", A(KWord, "HOST", "Destination address or name").Chk(hostRe.MatchString).R(cmdTrace))

	enable := K("enable", "Turn on privileged commands").R(func(s *Session, _ []string) error {
		if err := s.needAdmin(); err != nil { // without a role, or with the noc/helpdesk role, privileged mode is closed
			return err
		}
		// The enable password is asked of everyone except local root (who can modify the files anyway).
		if h := s.cfg().EnablePass; h != "" && s.User != "root" {
			s.askSecret("Password: ", func(s *Session, in string) error {
				if !checkEnablePass(s.cfg().EnablePass, in) {
					return fmt.Errorf("Access denied")
				}
				s.frames = []frame{{kind: "priv"}}
				return nil
			})
			return nil
		}
		s.frames = []frame{{kind: "priv"}}
		return nil
	})
	disable := K("disable", "Turn off privileged commands").R(func(s *Session, _ []string) error {
		s.frames = []frame{{kind: "exec"}}
		return nil
	})
	configure := K("configure", "Enter configuration mode",
		K("terminal", "Configure from the terminal").R(func(s *Session, _ []string) error {
			s.push("config", "")
			return nil
		}))
	configure.R(func(s *Session, _ []string) error { // "configure" without an argument = configure terminal
		s.push("config", "")
		return nil
	})
	save := func(s *Session, _ []string) error {
		s.Printf("Building configuration...\n")
		if err := s.R.Save(); err != nil {
			return err
		}
		s.journal("write")
		s.Printf("[OK]\n")
		return nil
	}
	write := K("write", "Write running configuration to memory", K("memory", "Write to NV memory").R(save)).R(save)
	write.Kids = append(write.Kids, writeKids()...)
	cp := K("copy", "Copy from one file to another",
		K("running-config", "Copy from current system configuration",
			K("startup-config", "Copy to startup configuration").R(save)))
	cp.Kids = append(cp.Kids, K("empty-config", "Reset configuration to factory defaults (applied after reload)",
		K("startup-config", "Write factory defaults to startup-config").R(cmdEmptyConfig)))
	cp.Kids = append(cp.Kids, copyKids()...)

	clearN := K("clear", "Clear the terminal screen").R(func(s *Session, _ []string) error {
		if s.TTY { // clear screen and scrollback; outside a terminal, print nothing
			s.Printf("\x1b[H\x1b[2J\x1b[3J")
		}
		return nil
	})
	terminal := K("terminal", "Set terminal line parameters",
		monitorNode(),
		K("length", "Set number of lines on a screen",
			A(KInt, "<0-512>", "Lines per page (0 disables paging, default: window height)").Rng(0, 512).R(func(s *Session, a []string) error {
				n, _ := strconv.Atoi(a[len(a)-1])
				if n == 0 {
					n = -1
				}
				s.PageLen = n
				return nil
			})))
	mge := K("mge", "Choo-choo! An animated ASCII steam train (Ctrl-C to stop)",
		A(KRest, "TEXT", `Text shown after the train passes, in matching double or single quotes, 1-64 characters (default: Have a nice day.)`).R(cmdTrain),
	).R(cmdTrain)
	t["exec"] = []*Node{enable, ping, trace, show, clearN, terminal, mge, exitN, logout, quit}
	t["priv"] = []*Node{disable, configure, write, cp, deleteNode(), reloadNode(), poweroffNode(), updateNode(), restartNode(), ping, trace, show, clearN, terminal, mge, exitN, logout, quit}

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
				A(KRange, "A.B.C.D[-A.B.C.D]", "Address range (a single address is allowed)").R(func(s *Session, v []string) error {
					f, to := rangeOf(v[1])
					c := s.cfg()
					if p := c.FindPool(v[0]); p != nil {
						p.From, p.To = f, to
						return nil
					}
					c.IPPools = append(c.IPPools, &config.IPPool{Name: v[0], From: f, To: to})
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

	// username NAME role ROLE password PW — a single line, without entering user mode
	ensureUser := func(s *Session, name string) *config.User {
		c := s.cfg()
		if u := c.FindUser(name); u != nil {
			return u
		}
		u := &config.User{Name: name}
		c.Users = append(c.Users, u)
		return u
	}
	// v: [name] or [name, role], the last element is the password
	uPass := K("password", "Set the account password (kept only in the Linux account, not in the configuration)",
		A(KRest, "PASSWORD", "Password (any text up to the end of the line)").R(func(s *Session, v []string) error {
			if len(v) == 3 {
				if !roleOK(v[1]) {
					return fmt.Errorf("role must be admin, noc or helpdesk")
				}
				ensureUser(s, v[0]).Role = v[1]
			}
			ensureUser(s, v[0])
			if s.noApply {
				return nil
			}
			return s.R.Mgr.SetPassword(v[0], v[len(v)-1])
		}))
	uRole := K("role", "Set the user role",
		A(KWord, "ROLE", "Role: admin (full privileges), noc or helpdesk", uPass).Cmp(func(*Session) []string { return []string{"admin", "noc", "helpdesk"} }).
			R(func(s *Session, v []string) error {
				if !roleOK(v[1]) {
					return fmt.Errorf("role must be admin, noc or helpdesk")
				}
				ensureUser(s, v[0]).Role = v[1]
				return nil
			}))
	username := K("username", "Establish a user name",
		A(KWord, "NAME", "User name (letters, digits, _ . : / -)", uRole, uPass).Chk(nameOK).Cmp(userNames).R(func(s *Session, v []string) error {
			c := s.cfg()
			if c.FindUser(v[0]) == nil {
				c.Users = append(c.Users, &config.User{Name: v[0]})
			}
			s.enter("user", v[0])
			return nil
		}).N(func(s *Session, v []string) error {
			c := s.cfg()
			if v[0] == "admin" {
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
			s.enter("if", v[0])
			return nil
		}).N(func(s *Session, v []string) error {
			if !s.cfg().RemoveIface(v[0]) {
				return fmt.Errorf("interface %s not found", v[0])
			}
			return nil
		}))

	port := K("port", "Select a port to configure",
		A(KWord, "NAME", "Port name (te0, te1, ...)").Chk(nameOK).Cmp(portNames).R(func(s *Session, v []string) error {
			if s.R.Mgr != nil && !s.R.Mgr.Env.Ports.Has(v[0]) {
				return fmt.Errorf("port %s not found (available: %s)", v[0], strings.Join(s.R.Mgr.Env.Ports.Names(), ", "))
			}
			c := s.cfg()
			if c.FindPort(v[0]) == nil {
				c.Ports = append(c.Ports, &config.Port{Name: v[0]})
			}
			s.enter("port", v[0])
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
				s.enter("ospf", v[0])
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
			s.enter("dhcp", v[0])
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
	switchers := []*Node{iface, port, router, username, do, clearN, exitN, end}
	historyCfg := historyCfgNodes()
	filterMap := fmapConfigNode()
	t["config"] = append([]*Node{hostname, ipCfg, ntp, serviceNode(), featureNode(), username, iface, port, router, dhcpSrv, filterMap, bannerConfigNode(), aaaNode(), do, clearN, exitN, end}, historyCfg...)
	t["config"] = append(t["config"], secCfg...)
	t["config"] = append(t["config"], epw...)
	t["config"] = append(t["config"], cryptoNodes()...)
	t["config"] = append(t["config"], policyConfigNodes()...)
	ipCfg.Kids = append(ipCfg.Kids, prefixListNode())
	// IPsec modes: their own commands plus all global-mode commands (as in the examples: crypto-map comes right after the profile)
	t["ipprof"] = append(ipsecProfileTree(), t["config"]...)
	t["ikep1"] = append(ikePhase1Tree(), t["config"]...)
	t["ikep2"] = append(ikePhase2Tree(), t["config"]...)
	t["cmap"] = append(cryptoMapTree(), t["config"]...)
	t["rmap"] = append(rmapTree(), t["config"]...)
	t["secprof"] = append(secProfileTree(), []*Node{iface, port, router, username, do, clearN, exitN, end}...)
	t["fmap"] = append(fmapModeTree(), []*Node{filterMap, iface, port, router, username, do, clearN, exitN, end}...)

	t["if"] = append(append(ifaceTree(), fmapIfaceNode()), switchers...)
	t["port"] = append(portTree(), switchers...)
	t["si"] = append(append(siTree(), portTree()[1]), switchers...)
	t["ospf"] = append(ospfTree(), switchers...)
	t["user"] = append(userTree(), switchers...)
	t["dhcp"] = append(dhcpTree(), []*Node{iface, port, router, username, do, clearN, exitN, end}...)
	return t
}

func removeStr(l []string, v string) []string {
	var out []string
	for _, x := range l {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func delRoute(s *Session, prefix, nh string) error {
	p, err := config.Canon(prefix)
	if err != nil {
		return err
	}
	c := s.cfg()
	var out []config.Route
	for _, r := range c.Routes {
		if r.Prefix == p && (nh == "" || r.NextHop == nh) {
			continue
		}
		out = append(out, r)
	}
	c.Routes = out
	return nil
}

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
				A(KRange, "A.B.C.D[-A.B.C.D]", "Address range (a single address is allowed)").R(func(s *Session, v []string) error {
					f, to := rangeOf(v[1])
					c := s.cfg()
					if p := c.FindNATPool(v[0]); p != nil {
						p.From, p.To = f, to
						return nil
					}
					c.NATPools = append(c.NATPools, &config.NATPool{Name: v[0], From: f, To: to})
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
				// compatibility: an arbitrary rule name (hidden from hints)
				A(KWord, "RULE", "Rule name", dynTail(1, setDyn)).N(delDyn).Hide()),
			K("static", "Static translation (port forwarding)",
				withPorts("tcp"), withPorts("udp"),
				A(KIP, "LOCAL-IP", "Inside local address",
					A(KIP, "GLOBAL-IP", "Inside global address").R(addStatic("")).N(delStatic("")))))).
		N(nil)
}

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
				if s.top().kind == "si" { // neighboring service-instance without exit
					s.pop()
				}
				p, err := cur(s)
				if err != nil {
					return err
				}
				if p.FindSI(v[0]) == nil {
					p.SIs = append(p.SIs, &config.SI{Name: v[0]})
				}
				s.push("si", v[0])
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

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}
func addOnce(l *[]string, v string) {
	if !contains(*l, v) {
		*l = append(*l, v)
	}
}

func userTree() []*Node {
	u := func(f func(u *config.User, v []string) error) Handler {
		return func(s *Session, v []string) error {
			x := s.cfg().FindUser(s.top().obj)
			if x == nil {
				return fmt.Errorf("user no longer exists")
			}
			return f(x, v)
		}
	}
	return []*Node{
		K("password", "Set the account password (kept only in the Linux account, not in the configuration)",
			A(KRest, "PASSWORD", "Password (any text up to the end of the line)").R(func(s *Session, v []string) error {
				name := s.top().obj
				if s.cfg().FindUser(name) == nil {
					return fmt.Errorf("user no longer exists")
				}
				if s.noApply { // old startup-configs could contain a password: passwords are now only in Linux
					return nil
				}
				return s.R.Mgr.SetPassword(name, v[0])
			})).Sec(),
		K("role", "Set the user role",
			A(KWord, "ROLE", "Role: admin (full privileges), noc or helpdesk").Cmp(func(*Session) []string { return []string{"admin", "noc", "helpdesk"} }).
				R(u(func(x *config.User, v []string) error { x.Role = v[0]; return nil }))).
			N(u(func(x *config.User, _ []string) error { x.Role = ""; return nil })),
	}
}

func dhcpTree() []*Node {
	d := func(f func(x *config.DHCPServer, v []string) error) Handler {
		return func(s *Session, v []string) error {
			id := 0
			fmt.Sscan(s.top().obj, &id)
			x := s.cfg().FindDHCP(id)
			if x == nil {
				return fmt.Errorf("DHCP server no longer exists")
			}
			return f(x, v)
		}
	}
	dnsChain := func() *Node {
		var n *Node
		for i := 4; i >= 1; i-- {
			cur := A(KIP, "A.B.C.D", "DNS server address")
			cur.R(d(func(x *config.DHCPServer, v []string) error { x.DNS = append([]string(nil), v...); return nil }))
			if n != nil {
				cur.Kids = []*Node{n}
			}
			n = cur
		}
		return n
	}
	return []*Node{
		K("pool", "Address pool for this server",
			A(KWord, "NAME", "Pool name (see 'ip pool')",
				A(KInt, "INDEX", "Pool index").Rng(1, 255).R(d(func(x *config.DHCPServer, v []string) error { x.Pool = v[0]; return nil }))).
				Cmp(poolNames).R(d(func(x *config.DHCPServer, v []string) error { x.Pool = v[0]; return nil }))).
			N(d(func(x *config.DHCPServer, _ []string) error { x.Pool = ""; return nil })),
		K("mask", "Subnet mask",
			A(KWord, "MASK", "Prefix length (28) or dotted mask").Chk(func(v string) bool { _, ok := config.MaskLen(v); return ok }).
				R(d(func(x *config.DHCPServer, v []string) error { x.Mask = v[0]; return nil }))).
			N(d(func(x *config.DHCPServer, _ []string) error { x.Mask = ""; return nil })),
		K("gateway", "Default gateway for clients",
			A(KIP, "A.B.C.D", "Gateway address").R(d(func(x *config.DHCPServer, v []string) error { x.Gateway = v[0]; return nil }))).
			N(d(func(x *config.DHCPServer, _ []string) error { x.Gateway = ""; return nil })),
		K("dns", "DNS servers for clients", dnsChain()).
			N(d(func(x *config.DHCPServer, _ []string) error { x.DNS = nil; return nil })),
		K("domain-name", "DNS domain name for clients (DHCP option 15)",
			A(KWord, "WORD", "Domain name").R(d(func(x *config.DHCPServer, v []string) error { x.Domain = v[0]; return nil }))).
			N(d(func(x *config.DHCPServer, _ []string) error { x.Domain = ""; return nil })),
		K("domain-search", "DNS search domain list for clients (DHCP option 119)",
			A(KWord, "WORD", "Search domain").R(d(func(x *config.DHCPServer, v []string) error { x.Search = v[0]; return nil }))).
			N(d(func(x *config.DHCPServer, _ []string) error { x.Search = ""; return nil })),
		K("lease", "Lease time",
			A(KInt, "SECONDS", "Seconds").Rng(60, 31536000).R(d(func(x *config.DHCPServer, v []string) error { fmt.Sscan(v[0], &x.Lease); return nil }))).
			N(d(func(x *config.DHCPServer, _ []string) error { x.Lease = 0; return nil })),
	}
}

// ---------------- ping / traceroute ----------------

// external runs an external utility and passes its output to the session. The command
// lives until it finishes on its own or the user interrupts it (Ctrl-C
// → Session.Interrupt). The router lock is released for that time, so
// other sessions and the CLI itself do not hang.
func external(s *Session, bin string, args ...string) error {
	p, err := exec.LookPath(bin)
	if err != nil {
		return fmt.Errorf("%s is not installed", bin)
	}
	s.cmu.Lock()
	ctx := s.ctx
	s.cmu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.Command(p, args...)
	cmd.Stdout, cmd.Stderr = s.W, s.W
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // the session's Ctrl-C must not hit the daemon
	if err := cmd.Start(); err != nil {
		return err
	}
	s.R.mu.Unlock()
	defer s.R.mu.Lock()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			// SIGINT: ping/traceroute print final statistics and exit by themselves
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-done:
		}
	}()
	_ = cmd.Wait()
	close(done)
	return nil
}

// pingArgs — ping arguments: WITHOUT -c, i.e. continuous until interrupted by the user.
func pingArgs(host string) []string { return []string{"-W", "2", host} }

func cmdPing(s *Session, v []string) error {
	if strings.HasPrefix(v[0], "-") {
		return fmt.Errorf("invalid host")
	}
	return external(s, "ping", pingArgs(v[0])...)
}
func cmdTrace(s *Session, v []string) error {
	if strings.HasPrefix(v[0], "-") {
		return fmt.Errorf("invalid host")
	}
	return external(s, "traceroute", "-n", "-w", "2", "-m", "20", v[0])
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
			if err := s.needAdmin(); err != nil {
				return err
			}
			s.Printf("%s", s.cfg().Render())
			return nil
		}),
		K("startup-config", "Contents of startup configuration").R(func(s *Session, _ []string) error {
			if err := s.needAdmin(); err != nil {
				return err
			}
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
		K("dhcp-server", "DHCP server information",
			K("clients", "Leased clients").R(showLeases)).R(func(s *Session, _ []string) error {
			for _, d := range s.cfg().DHCP {
				s.Printf("dhcp-server %d: pool=%s mask=%s gateway=%s dns=%s domain=%s search=%s\n", d.ID, d.Pool, d.Mask, d.Gateway, strings.Join(d.DNS, ","), d.Domain, d.Search)
			}
			return nil
		}),
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

func showLeases(s *Session, _ []string) error {
	ls := svc.Leases(s.R.P)
	sort.Slice(ls, func(i, j int) bool { return ls[i].IP < ls[j].IP })
	s.Printf("%-16s %-19s %-20s %s\n", "IP address", "MAC address", "Hostname", "Expires")
	for _, l := range ls {
		var exp int64
		fmt.Sscan(l.Expires, &exp)
		e := "infinite"
		if exp > 0 {
			e = time.Unix(exp, 0).Format("2006-01-02 15:04:05")
		}
		s.Printf("%-16s %-19s %-20s %s\n", l.IP, l.MAC, l.Host, e)
	}
	return nil
}

func filterPort(all, name string) string {
	lines := strings.Split(strings.TrimRight(all, "\n"), "\n")
	var out []string
	for n, l := range lines {
		if n == 0 || strings.HasPrefix(l, name+" ") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n") + "\n"
}

// roleOK — valid user roles.
func roleOK(r string) bool { return r == "admin" || r == "noc" || r == "helpdesk" }
