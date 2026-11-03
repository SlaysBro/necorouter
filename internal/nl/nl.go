// Package nl is a minimal rtnetlink client using only the standard library:
// VLAN/GRE/dummy interfaces, addresses, routes. It replaces calls to the ip utility,
// so iproute2 is not needed on the router, and it is faster.
package nl

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"syscall"
)

var ne = binary.NativeEndian

const (
	iflaIfname   = 3
	iflaMTU      = 4
	iflaLink     = 5
	iflaLinkinfo = 18
	infoKind     = 1
	infoData     = 2
	vlanID       = 1
	greLink      = 1
	greLocal     = 6
	greRemote    = 7
	greTTL       = 8
	grePMTUDisc  = 10
	ifaAddress   = 1
	ifaLocal     = 2
	ifaBroadcast = 4
	rtaDst       = 1
	rtaOif       = 4
	rtaGateway   = 5
	rtaPriority  = 6
	rtaPrefsrc   = 7
	iffUp        = 0x1
	iffMulticast = 0x1000

	RTProtoStatic = 4
	RTProtoOSPF   = 188
)

func align(n int) int { return (n + 3) &^ 3 }

func attr(typ uint16, data []byte) []byte {
	l := 4 + len(data)
	b := make([]byte, align(l))
	ne.PutUint16(b[0:], uint16(l))
	ne.PutUint16(b[2:], typ)
	copy(b[4:], data)
	return b
}
func attrStr(typ uint16, s string) []byte { return attr(typ, append([]byte(s), 0)) }
func attrU32(typ uint16, v uint32) []byte {
	b := make([]byte, 4)
	ne.PutUint32(b, v)
	return attr(typ, b)
}
func attrU16(typ uint16, v uint16) []byte {
	b := make([]byte, 2)
	ne.PutUint16(b, v)
	return attr(typ, b)
}
func attrU8(typ uint16, v uint8) []byte { return attr(typ, []byte{v}) }
func cat(parts ...[]byte) []byte {
	var o []byte
	for _, p := range parts {
		o = append(o, p...)
	}
	return o
}

type Conn struct {
	fd  int
	seq uint32
}

func Open() (*Conn, error) {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_ROUTE)
	if err != nil {
		return nil, fmt.Errorf("netlink: %w", err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("netlink bind: %w", err)
	}
	return &Conn{fd: fd}, nil
}

func (c *Conn) Close() { syscall.Close(c.fd) }

// do sends a request with ACK and waits for kernel confirmation.
func (c *Conn) do(typ, flags uint16, body []byte) error {
	c.seq++
	b := make([]byte, 16, 16+len(body))
	ne.PutUint32(b[0:], uint32(16+len(body)))
	ne.PutUint16(b[4:], typ)
	ne.PutUint16(b[6:], flags|syscall.NLM_F_REQUEST|syscall.NLM_F_ACK)
	ne.PutUint32(b[8:], c.seq)
	b = append(b, body...)
	if err := syscall.Sendto(c.fd, b, 0, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return err
	}
	buf := make([]byte, 1<<16)
	for {
		n, _, err := syscall.Recvfrom(c.fd, buf, 0)
		if err != nil {
			return err
		}
		for off := 0; off+16 <= n; {
			l := int(ne.Uint32(buf[off:]))
			if l < 16 || off+l > n {
				return fmt.Errorf("netlink: bad message")
			}
			t := ne.Uint16(buf[off+4:])
			if ne.Uint32(buf[off+8:]) == c.seq && t == syscall.NLMSG_ERROR && l >= 20 {
				if e := int32(ne.Uint32(buf[off+16:])); e != 0 {
					return syscall.Errno(-e)
				}
				return nil
			}
			off += align(l)
		}
	}
}

func ifinfo(index int32, flags, change uint32) []byte {
	b := make([]byte, 16)
	ne.PutUint32(b[4:], uint32(index))
	ne.PutUint32(b[8:], flags)
	ne.PutUint32(b[12:], change)
	return b
}

func index(name string) (int, error) {
	i, err := net.InterfaceByName(name)
	if err != nil {
		return 0, fmt.Errorf("interface %s: not found", name)
	}
	return i.Index, nil
}

const create = syscall.NLM_F_CREATE | syscall.NLM_F_EXCL

func (c *Conn) newLink(name string, parent int, kind string, data []byte) error {
	li := attrStr(infoKind, kind)
	if data != nil {
		li = cat(li, attr(infoData|0x8000, data))
	}
	parts := [][]byte{ifinfo(0, 0, 0), attrStr(iflaIfname, name)}
	if parent > 0 {
		parts = append(parts, attrU32(iflaLink, uint32(parent)))
	}
	parts = append(parts, attr(iflaLinkinfo|0x8000, li))
	if err := c.do(syscall.RTM_NEWLINK, create, cat(parts...)); err != nil {
		return fmt.Errorf("create %s %s: %w", kind, name, err)
	}
	return nil
}

func (c *Conn) AddVLAN(name, parent string, id int) error {
	pi, err := index(parent)
	if err != nil {
		return err
	}
	return c.newLink(name, pi, "vlan", attrU16(vlanID, uint16(id)))
}

func (c *Conn) AddGRE(name, local, remote string, ttl int) error {
	l, e1 := netip.ParseAddr(local)
	r, e2 := netip.ParseAddr(remote)
	if e1 != nil || e2 != nil || !l.Is4() || !r.Is4() {
		return fmt.Errorf("gre %s: bad tunnel addresses", name)
	}
	l4, r4 := l.As4(), r.As4()
	d := cat(attr(greLocal, l4[:]), attr(greRemote, r4[:]), attrU8(greTTL, uint8(ttl)), attrU8(grePMTUDisc, 1))
	if err := c.newLink(name, 0, "gre", d); err != nil {
		return err
	}
	return c.SetMulticast(name, true)
}

func (c *Conn) AddDummy(name string) error { return c.newLink(name, 0, "dummy", nil) }

func (c *Conn) DelLink(name string) error {
	i, err := index(name)
	if err != nil {
		return nil
	}
	return c.do(syscall.RTM_DELLINK, 0, ifinfo(int32(i), 0, 0))
}

func (c *Conn) SetUp(name string, up bool) error {
	i, err := index(name)
	if err != nil {
		return err
	}
	var fl uint32
	if up {
		fl = iffUp
	}
	return c.do(syscall.RTM_NEWLINK, 0, ifinfo(int32(i), fl, iffUp))
}

// SetMulticast enables/disables multicast (a GRE tunnel is brought up with it).
func (c *Conn) SetMulticast(name string, on bool) error {
	i, err := index(name)
	if err != nil {
		return err
	}
	var fl uint32
	if on {
		fl = iffMulticast
	}
	return c.do(syscall.RTM_NEWLINK, 0, ifinfo(int32(i), fl, iffMulticast))
}

func (c *Conn) SetMTU(name string, mtu int) error {
	i, err := index(name)
	if err != nil {
		return err
	}
	return c.do(syscall.RTM_NEWLINK, 0, cat(ifinfo(int32(i), 0, 0), attrU32(iflaMTU, uint32(mtu))))
}

func ifaddr(i int, p netip.Prefix) []byte {
	b := make([]byte, 8)
	b[0], b[1] = syscall.AF_INET, uint8(p.Bits())
	b[3] = 0 // scope universe
	ne.PutUint32(b[4:], uint32(i))
	a := p.Addr().As4()
	out := cat(b, attr(ifaLocal, a[:]), attr(ifaAddress, a[:]))
	if p.Bits() < 31 {
		bc := a
		host := 32 - p.Bits()
		v := uint32(bc[0])<<24 | uint32(bc[1])<<16 | uint32(bc[2])<<8 | uint32(bc[3])
		v |= (1 << uint(host)) - 1
		bc = [4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
		out = cat(out, attr(ifaBroadcast, bc[:]))
	}
	return out
}

func (c *Conn) AddAddr(name, cidr string) error {
	i, err := index(name)
	if err != nil {
		return err
	}
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return err
	}
	if err := c.do(syscall.RTM_NEWADDR, create, ifaddr(i, p)); err != nil && err != syscall.EEXIST {
		return fmt.Errorf("add address %s to %s: %w", cidr, name, err)
	}
	return nil
}

func (c *Conn) DelAddr(name, cidr string) error {
	i, err := index(name)
	if err != nil {
		return nil
	}
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return err
	}
	if err := c.do(syscall.RTM_DELADDR, 0, ifaddr(i, p)); err != nil && err != syscall.EADDRNOTAVAIL {
		return err
	}
	return nil
}

// Addrs returns the IPv4 addresses of an interface as "a.b.c.d/m".
func Addrs(name string) map[string]bool {
	m := map[string]bool{}
	ifc, err := net.InterfaceByName(name)
	if err != nil {
		return m
	}
	as, _ := ifc.Addrs()
	for _, a := range as {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
			m[n.String()] = true
		}
	}
	return m
}

func rtmsg(dstLen uint8, proto uint8) []byte {
	b := make([]byte, 12)
	b[0], b[1] = syscall.AF_INET, dstLen
	b[4], b[5], b[6], b[7] = syscall.RT_TABLE_MAIN, proto, syscall.RT_SCOPE_UNIVERSE, syscall.RTN_UNICAST
	return b
}

func routeBody(prefix, gw string, proto uint8) ([]byte, error) {
	p, err := netip.ParsePrefix(prefix)
	if err != nil {
		return nil, err
	}
	a := p.Masked().Addr().As4()
	parts := [][]byte{rtmsg(uint8(p.Bits()), proto), attr(rtaDst, a[:])}
	if gw != "" {
		g, err := netip.ParseAddr(gw)
		if err != nil {
			return nil, err
		}
		g4 := g.As4()
		parts = append(parts, attr(rtaGateway, g4[:]))
	}
	return cat(parts...), nil
}

// ReplaceRoute adds or replaces a static route.
func (c *Conn) ReplaceRoute(prefix, gw string) error {
	b, err := routeBody(prefix, gw, RTProtoStatic)
	if err != nil {
		return err
	}
	return c.do(syscall.RTM_NEWROUTE, syscall.NLM_F_CREATE|syscall.NLM_F_REPLACE, b)
}

func (c *Conn) DelRoute(prefix, gw string) error {
	b, err := routeBody(prefix, gw, RTProtoStatic)
	if err != nil {
		return err
	}
	if err := c.do(syscall.RTM_DELROUTE, 0, b); err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}

type Route struct {
	Dst    string
	Gw     string
	Dev    string
	Proto  uint8
	Scope  uint8
	Metric uint32
}

// Routes returns the IPv4 routes of the main table.
func Routes() ([]Route, error) {
	rib, err := syscall.NetlinkRIB(syscall.RTM_GETROUTE, syscall.AF_INET)
	if err != nil {
		return nil, err
	}
	msgs, err := syscall.ParseNetlinkMessage(rib)
	if err != nil {
		return nil, err
	}
	var out []Route
	for _, m := range msgs {
		if m.Header.Type != syscall.RTM_NEWROUTE || len(m.Data) < 12 {
			continue
		}
		if m.Data[4] != syscall.RT_TABLE_MAIN || m.Data[7] != syscall.RTN_UNICAST {
			continue
		}
		r := Route{Proto: m.Data[5], Scope: m.Data[6]}
		dl := int(m.Data[1])
		attrs, _ := syscall.ParseNetlinkRouteAttr(&m)
		dst := "0.0.0.0"
		for _, a := range attrs {
			switch a.Attr.Type {
			case rtaDst:
				dst = net.IP(a.Value).String()
			case rtaGateway:
				r.Gw = net.IP(a.Value).String()
			case rtaOif:
				if i, err := net.InterfaceByIndex(int(ne.Uint32(a.Value))); err == nil {
					r.Dev = i.Name
				}
			case rtaPriority:
				r.Metric = ne.Uint32(a.Value)
			}
		}
		r.Dst = fmt.Sprintf("%s/%d", dst, dl)
		out = append(out, r)
	}
	return out, nil
}
