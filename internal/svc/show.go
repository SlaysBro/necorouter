package svc

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strings"

	"necorouter/internal/config"
)

func sysfs(dev, file string) string {
	b, err := os.ReadFile("/sys/class/net/" + dev + "/" + file)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func stat(dev, name string) string {
	if v := sysfs(dev, "statistics/"+name); v != "" {
		return v
	}
	return "0"
}

func devUp(dev string) bool {
	i, err := net.InterfaceByName(dev)
	return err == nil && i.Flags&net.FlagUp != 0
}

func ifStatus(env *Env, c *config.Config, i *config.Iface) (dev, st string) {
	dev = DevOf(env, c, i.Name)
	switch {
	case i.Shutdown:
		st = "admin-down"
	case dev == "" || !linkExists(dev):
		st = "down"
	case devUp(dev) && sysfs(dev, "carrier") != "0":
		st = "up"
	default:
		st = "down"
	}
	return
}

// IfaceBrief implements "show ip interface brief".
func IfaceBrief(env *Env, c *config.Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-20s %-20s %-11s %s\n", "Interface", "IP-Address", "Status", "Port")
	for _, i := range c.Ifaces {
		_, st := ifStatus(env, c, i)
		addr := "unassigned"
		if len(i.Addrs) > 0 {
			addr = i.Addrs[0]
		}
		port := "-"
		if p, _ := c.Binding(i.Name); p != nil {
			port = p.Name
		} else if i.Tunnel != nil {
			port = "tunnel"
		}
		fmt.Fprintf(&b, "%-20s %-20s %-11s %s\n", i.Name, addr, st, port)
	}
	return b.String()
}

// IfaceDetail implements "show interface NAME".
func IfaceDetail(env *Env, c *config.Config, name string) string {
	i := c.FindIface(name)
	if i == nil {
		return ""
	}
	dev, st := ifStatus(env, c, i)
	var b strings.Builder
	fmt.Fprintf(&b, "Interface %s is %s\n", i.Name, st)
	if i.Desc != "" {
		fmt.Fprintf(&b, "  Description: %s\n", i.Desc)
	}
	if dev != "" {
		fmt.Fprintf(&b, "  Linux device: %s\n", dev)
		if ni, err := net.InterfaceByName(dev); err == nil {
			if len(ni.HardwareAddr) > 0 {
				fmt.Fprintf(&b, "  Hardware address: %s\n", ni.HardwareAddr)
			}
			fmt.Fprintf(&b, "  MTU: %d\n", ni.MTU)
		}
	}
	for _, a := range i.Addrs {
		fmt.Fprintf(&b, "  Internet address: %s\n", a)
	}
	if i.Tunnel != nil {
		fmt.Fprintf(&b, "  Tunnel: mode %s, source %s, destination %s\n", i.Tunnel.Mode, i.Tunnel.Src, i.Tunnel.Dst)
	}
	if i.NAT != "" {
		fmt.Fprintf(&b, "  NAT: %s\n", i.NAT)
	}
	if dev != "" {
		rp, rb := stat(dev, "rx_packets"), stat(dev, "rx_bytes")
		tp, tb := stat(dev, "tx_packets"), stat(dev, "tx_bytes")
		fmt.Fprintf(&b, "  Input: %s packets, %s bytes\n  Output: %s packets, %s bytes\n", rp, rb, tp, tb)
	}
	return b.String()
}

// PortList implements "show port".
func PortList(env *Env, c *config.Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-8s %-10s %-8s %-19s %s\n", "Port", "Device", "State", "MAC", "Service-instances")
	for _, p := range env.Ports.Names() {
		nic := env.Ports.NIC(p)
		st := "down"
		if _, err := net.InterfaceByName(nic); err != nil {
			st = "absent" // the card is disabled or renamed and the MAC is not found
		} else if devUp(nic) && sysfs(nic, "carrier") != "0" {
			st = "up"
		}
		var sis []string
		if cp := c.FindPort(p); cp != nil {
			for _, s := range cp.SIs {
				sis = append(sis, s.Name)
			}
		}
		mac := ""
		if ni, err := net.InterfaceByName(nic); err == nil {
			mac = ni.HardwareAddr.String()
		}
		fmt.Fprintf(&b, "%-8s %-10s %-8s %-19s %s\n", p, nic, st, mac, strings.Join(sis, ","))
	}
	return b.String()
}

// Routes implements "show ip route".
func Routes(env *Env, c *config.Config) string {
	rs, err := systemRoutes()
	if err != nil {
		return "% " + err.Error() + "\n"
	}
	type row struct{ key, line string }
	var rows []row
	for _, r := range rs {
		name := IfaceByDev(env, c, r.Dev)
		if name == "" {
			name = r.Dev
		}
		code, ad := "K", "0"
		switch r.Protocol {
		case "kernel", "connected", "link":
			code = "C"
		case "static", "boot":
			code, ad = "S", "1"
		case "ospf":
			code, ad = "O", "110"
		}
		var l string
		if r.Gateway == "" {
			l = fmt.Sprintf("%s    %s is directly connected, %s", code, r.Dst, name)
		} else {
			l = fmt.Sprintf("%s    %s [%s/%d] via %s, %s", code, r.Dst, ad, r.Metric, r.Gateway, name)
		}
		rows = append(rows, row{r.Dst, l})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].key < rows[j].key })
	var b strings.Builder
	b.WriteString("Codes: C - connected, S - static, O - OSPF, K - kernel\n\n")
	for _, r := range rows {
		b.WriteString(r.line + "\n")
	}
	return b.String()
}

// NATTable implements "show ip nat translations" from conntrack.
func NATTable(env *Env) string {
	b, err := os.ReadFile("/proc/net/nf_conntrack")
	if err != nil {
		return "No translations (conntrack is not active)\n"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-5s %-24s %-24s %s\n", "Pro", "Inside global", "Inside local", "Outside")
	n := 0
	for _, ln := range strings.Split(string(b), "\n") {
		f := strings.Fields(ln)
		if len(f) < 8 {
			continue
		}
		kv := func(skip int, key string) string {
			c := 0
			for _, w := range f {
				if strings.HasPrefix(w, key+"=") {
					if c == skip {
						return strings.TrimPrefix(w, key+"=")
					}
					c++
				}
			}
			return ""
		}
		src, dst, sp, dp := kv(0, "src"), kv(0, "dst"), kv(0, "sport"), kv(0, "dport")
		rsrc, rdst, rdp := kv(1, "src"), kv(1, "dst"), kv(1, "dport")
		if src == "" || (src == rdst && dst == rsrc) {
			continue // not translated
		}
		pro := f[2]
		g, l := rdst, src
		if sp != "" {
			g += ":" + rdp
			l += ":" + sp
		}
		o := dst
		if dp != "" {
			o += ":" + dp
		}
		fmt.Fprintf(&sb, "%-5s %-24s %-24s %s\n", pro, g, l, o)
		n++
	}
	if n == 0 {
		return "No translations\n"
	}
	return sb.String()
}

func Arp() string {
	b, err := os.ReadFile("/proc/net/arp")
	if err != nil {
		return "% " + err.Error() + "\n"
	}
	return string(b)
}
