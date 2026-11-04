// Package config describes the router configuration model and can
// render it in the router's CLI syntax (running-config / startup-config).
package config

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

const DefaultHostname = "necorouter"

type User struct {
	Name         string `json:"name"`
	Role         string `json:"role"`
	BuiltinAdmin bool   `json:"builtin_admin,omitempty"`
}

type OSPFKey struct {
	ID  int    `json:"id"`
	Key string `json:"key"`
}

type Tunnel struct {
	Src  string `json:"src"`
	Dst  string `json:"dst"`
	Mode string `json:"mode"`
}

type Iface struct {
	Name        string    `json:"name"`
	Desc        string    `json:"desc,omitempty"`
	Addrs       []string  `json:"addrs,omitempty"`
	MTU         int       `json:"mtu,omitempty"`
	NAT         string    `json:"nat,omitempty"` // inside | outside
	DHCP        int       `json:"dhcp,omitempty"`
	Shutdown    bool      `json:"shutdown,omitempty"`
	OSPFAuth    string    `json:"ospf_auth,omitempty"` // message-digest | simple
	OSPFKeys    []OSPFKey `json:"ospf_keys,omitempty"`
	OSPFSimple  string    `json:"ospf_simple,omitempty"`
	OSPFCost    int       `json:"ospf_cost,omitempty"`
	OSPFNetwork string    `json:"ospf_network,omitempty"`
	Tunnel      *Tunnel   `json:"tunnel,omitempty"`
	FMaps       []FMBind  `json:"filter_maps,omitempty"` // set filter-map in|out NAME [SEQ]
}

type SI struct {
	Name      string `json:"name"`
	Encap     string `json:"encap,omitempty"` // untagged | dot1q
	VLAN      int    `json:"vlan,omitempty"`
	Exact     bool   `json:"exact,omitempty"`
	Rewrite   string `json:"rewrite,omitempty"`
	Connect   string `json:"connect,omitempty"` // ip-interface name
	PolicyIn  string `json:"policy_in,omitempty"`
	PolicyOut string `json:"policy_out,omitempty"`
}

type ServicePolicy struct {
	Name           string `json:"name"`
	BandwidthUnit  string `json:"bandwidth_unit,omitempty"`
	BandwidthValue int    `json:"bandwidth_value,omitempty"`
}

type Port struct {
	Name string `json:"name"`
	Desc string `json:"desc,omitempty"`
	SIs  []*SI  `json:"sis,omitempty"`
}

type Route struct {
	Prefix  string `json:"prefix"`
	NextHop string `json:"nexthop"`
}

type NATPool struct {
	Name string `json:"name"`
	From string `json:"from"`
	To   string `json:"to"`
}

type NATDyn struct {
	Name     string `json:"name"` // inside-to-outside and the like
	Pool     string `json:"pool"`
	Overload string `json:"overload,omitempty"` // IP; empty = masquerade
}

type NATStatic struct {
	Proto   string `json:"proto,omitempty"` // tcp|udp|"" (1:1)
	Local   string `json:"local"`
	LPort   int    `json:"lport,omitempty"`
	Global  string `json:"global"`
	GPort   int    `json:"gport,omitempty"`
	Dest    bool   `json:"dest,omitempty"`    // ip nat destination static: external address:port (Global:GPort) → internal (Local:LPort)
	Hairpin bool   `json:"hairpin,omitempty"` // access from inside via the external address (Dest only)
}

type IPPool struct {
	Name string `json:"name"`
	From string `json:"from"`
	To   string `json:"to"`
}

type DHCPServer struct {
	ID      int      `json:"id"`
	Pool    string   `json:"pool,omitempty"`
	Mask    string   `json:"mask,omitempty"`
	Gateway string   `json:"gateway,omitempty"`
	DNS     []string `json:"dns,omitempty"`
	Domain  string   `json:"domain,omitempty"` // DHCP option 15: domain name
	Search  string   `json:"search,omitempty"` // DHCP option 119: search domain
	Lease   int      `json:"lease,omitempty"`
}

type OSPFNet struct {
	Prefix string `json:"prefix"`
	Area   string `json:"area"`
}

type OSPF struct {
	ID             int               `json:"id"`
	RouterID       string            `json:"router_id,omitempty"`
	Nets           []OSPFNet         `json:"nets,omitempty"`
	PassiveDefault bool              `json:"passive_default,omitempty"`
	Passive        []string          `json:"passive,omitempty"`
	NoPassive      []string          `json:"no_passive,omitempty"`
	AreaAuth       map[string]string `json:"area_auth,omitempty"`
	Redistribute   []string          `json:"redistribute,omitempty"`
	DefaultOrig    bool              `json:"default_orig,omitempty"`
	RedistRM       map[string]string `json:"redist_route_map,omitempty"`  // redistribute PROTOCOL route-map NAME
	DefaultRM      string            `json:"default_route_map,omitempty"` // default-information originate route-map NAME
	RedistMetric   map[string]int    `json:"redist_metric,omitempty"`     // metric for redistribute PROTOCOL and DefaultKey
	RedistType     map[string]int    `json:"redist_type,omitempty"`       // metric-type 1|2 for redistribute PROTOCOL and DefaultKey
	DefaultAlways  bool              `json:"default_always,omitempty"`    // default-information originate always
}

// SecRule is a security profile rule. Src and Dst: "any", "host A.B.C.D" or "A.B.C.D/M".
// Op: "", "eq", "gt", "lt", "range" (ports only for tcp and udp).
type SecRule struct {
	Num    int    `json:"num"`
	Action string `json:"action"` // permit | deny
	Proto  string `json:"proto"`
	Src    string `json:"src"`
	Dst    string `json:"dst"`
	Op     string `json:"op,omitempty"`
	P1     int    `json:"p1,omitempty"`
	P2     int    `json:"p2,omitempty"`
}

type SecProfile struct {
	ID    int       `json:"id"`
	Rules []SecRule `json:"rules,omitempty"`
}

func (r SecRule) String() string {
	s := fmt.Sprintf("%s %s %s %s", r.Action, r.Proto, r.Src, r.Dst)
	switch r.Op {
	case "range":
		s += fmt.Sprintf(" range %d %d", r.P1, r.P2)
	case "":
	default:
		s += fmt.Sprintf(" %s %d", r.Op, r.P1)
	}
	return s
}

// DefaultSecurity is the hard-coded default profile: SSH, Telnet and SNMP on line ports are closed.
var DefaultSecurity = []SecRule{
	{0, "deny", "tcp", "any", "any", "eq", 22, 0}, {1, "deny", "tcp", "any", "any", "eq", 23, 0},
	{2, "deny", "tcp", "any", "any", "eq", 161, 0}, {3, "deny", "udp", "any", "any", "eq", 22, 0},
	{4, "deny", "udp", "any", "any", "eq", 23, 0}, {5, "deny", "udp", "any", "any", "eq", 161, 0},
}

type Config struct {
	Hostname        string              `json:"hostname"`
	EnablePass      string              `json:"enable_pass,omitempty"` // hash of the enable-mode password (the plaintext password is not stored)
	BannerMotd      string              `json:"banner_motd,omitempty"` // banner motd TEXT: greeting after CLI login
	BannerOff       bool                `json:"banner_off,omitempty"`  // no banner motd: no greeting (by default, a line with the version)
	SecProfiles     []*SecProfile       `json:"sec_profiles,omitempty"`
	FilterMaps      []*FilterMap        `json:"filter_maps,omitempty"` // filter-map ipv4 (access lists for transit traffic)
	Security        string              `json:"security,omitempty"`    // "" means the default profile, "none" or a profile number
	Domain          string              `json:"domain,omitempty"`
	Timezone        string              `json:"timezone,omitempty"`
	AutosaveMin     int                 `json:"autosave_min,omitempty"`  // autosave period in minutes (0 means AutosaveDefaultMin)
	AutosaveKeep    int                 `json:"autosave_keep,omitempty"` // how many autosave copies to keep (0 means default)
	AutosaveOn      bool                `json:"autosave_on,omitempty"`   // autosave enabled by the autosave interval command (disabled by default)
	PwEncrypt       bool                `json:"pw_encrypt,omitempty"`    // service password-encryption (passwords are stored hashed anyway)
	Features        map[string]bool     `json:"features,omitempty"`      // enabled optional features (feature NAME)
	AAALevel        int                 `json:"aaa_log_level,omitempty"` // aaa logging level 1-6 (0 means default, 3)
	StartupKeep     int                 `json:"startup_keep,omitempty"`  // how many startup copies to keep (config-history feature)
	NameServers     []string            `json:"name_servers,omitempty"`
	NTPServers      []string            `json:"ntp_servers,omitempty"`
	Users           []*User             `json:"users,omitempty"`
	Ifaces          []*Iface            `json:"ifaces,omitempty"`
	Ports           []*Port             `json:"ports,omitempty"`
	Routes          []Route             `json:"routes,omitempty"`
	ServicePolicies []*ServicePolicy    `json:"service_policies,omitempty"`
	NATPools        []*NATPool          `json:"nat_pools,omitempty"`
	NATDyn          []*NATDyn           `json:"nat_dyn,omitempty"`
	NATStatic       []*NATStatic        `json:"nat_static,omitempty"`
	IPPools         []*IPPool           `json:"ip_pools,omitempty"`
	DHCP            []*DHCPServer       `json:"dhcp,omitempty"`
	OSPF            *OSPF               `json:"ospf,omitempty"`
	IPsecIKE        bool                `json:"ipsec_ike,omitempty"`           // crypto-ipsec ike enable
	IPsecProfiles   []*IPsecProfile     `json:"ipsec_profiles,omitempty"`      // crypto-ipsec profile NAME ike-v1|ike-v2
	CryptoMaps      []*CryptoMap        `json:"crypto_maps,omitempty"`         // crypto-map NAME SEQ
	PFLists         []*PolicyFilterList `json:"policy_filter_lists,omitempty"` // policy-filter-list N ...
	PrefixLists     []*PrefixList       `json:"prefix_lists,omitempty"`        // ip prefix-list NAME ...
	PrefixNoSeq     bool                `json:"prefix_no_seq,omitempty"`       // no ip prefix-list sequence-number
	RouteMaps       []*RouteMapEntry    `json:"route_maps,omitempty"`          // route-map NAME permit|deny SEQ
}

// AutosaveDefaultMin is the autosave period if none is set (autosave is off until enabled by the autosave interval command).
const AutosaveDefaultMin = 5

// Features are optional features (non-vanilla syntax) enabled by the feature NAME command.
// Output order in the configuration.
var Features = []string{"config-history", "config-manage", "config-journal", "ssh-copy"}

// New returns the factory configuration: name necorouter and the built-in admin/admin.
func New() *Config {
	return &Config{
		Hostname: DefaultHostname,
		Users:    []*User{{Name: "admin", Role: "admin", BuiltinAdmin: true}},
	}
}

// Fingerprint is for a quick check of "has the configuration changed".
func (c *Config) Fingerprint() string {
	b, _ := json.Marshal(c)
	return string(b)
}

func (c *Config) FindUser(n string) *User {
	for _, u := range c.Users {
		if u.Name == n {
			return u
		}
	}
	return nil
}
func (c *Config) FindIface(n string) *Iface {
	for _, i := range c.Ifaces {
		if i.Name == n {
			return i
		}
	}
	return nil
}
func (c *Config) FindPort(n string) *Port {
	for _, p := range c.Ports {
		if p.Name == n {
			return p
		}
	}
	return nil
}
func (c *Config) FindServicePolicy(n string) *ServicePolicy {
	for _, p := range c.ServicePolicies {
		if p.Name == n {
			return p
		}
	}
	return nil
}
func (p *Port) FindSI(n string) *SI {
	for _, s := range p.SIs {
		if s.Name == n {
			return s
		}
	}
	return nil
}
func (c *Config) FindPool(n string) *IPPool {
	for _, p := range c.IPPools {
		if p.Name == n {
			return p
		}
	}
	return nil
}
func (c *Config) FindNATPool(n string) *NATPool {
	for _, p := range c.NATPools {
		if p.Name == n {
			return p
		}
	}
	return nil
}
func (c *Config) FindSecProfile(id int) *SecProfile {
	for _, p := range c.SecProfiles {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// EffectiveSecurity returns the name and rules of the active line-port profile.
func (c *Config) EffectiveSecurity() (string, []SecRule) {
	switch c.Security {
	case "":
		return "default", DefaultSecurity
	case "none":
		return "none", nil
	}
	var id int
	fmt.Sscan(c.Security, &id)
	if p := c.FindSecProfile(id); p != nil {
		return c.Security, p.Rules
	}
	return "none", nil
}

func (c *Config) FindDHCP(id int) *DHCPServer {
	for _, d := range c.DHCP {
		if d.ID == id {
			return d
		}
	}
	return nil
}

// Binding returns the port and service-instance an ip-interface is attached to.
func (c *Config) Binding(iface string) (*Port, *SI) {
	for _, p := range c.Ports {
		for _, s := range p.SIs {
			if s.Connect == iface {
				return p, s
			}
		}
	}
	return nil, nil
}

// RemoveIface removes an interface and all references to it.
func (c *Config) RemoveIface(name string) bool {
	out := c.Ifaces[:0:0]
	found := false
	for _, i := range c.Ifaces {
		if i.Name == name {
			found = true
			continue
		}
		out = append(out, i)
	}
	c.Ifaces = out
	for _, p := range c.Ports {
		for _, s := range p.SIs {
			if s.Connect == name {
				s.Connect = ""
			}
		}
	}
	return found
}

func Canon(cidr string) (string, error) {
	p, err := netip.ParsePrefix(cidr)
	if err != nil || !p.Addr().Is4() {
		return "", fmt.Errorf("bad prefix")
	}
	return p.String(), nil
}

// MaskLen converts "28" or "255.255.255.240" into a prefix length.
func MaskLen(m string) (int, bool) {
	var n int
	if _, err := fmt.Sscanf(m, "%d", &n); err == nil && fmt.Sprint(n) == m && n >= 0 && n <= 32 {
		return n, true
	}
	a, err := netip.ParseAddr(m)
	if err != nil || !a.Is4() {
		return 0, false
	}
	b := a.As4()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	ones := 0
	for i := 31; i >= 0 && v&(1<<uint(i)) != 0; i-- {
		ones++
	}
	if ones < 32 && v<<uint(ones) != 0 {
		return 0, false
	}
	return ones, true
}

func MaskString(n int) string {
	v := uint32(0xffffffff) << uint(32-n)
	if n == 0 {
		v = 0
	}
	return fmt.Sprintf("%d.%d.%d.%d", v>>24, v>>16&255, v>>8&255, v&255)
}

// Render prints the configuration in a form suitable both for reading and for replaying
// through the CLI.
func (c *Config) Render() string {
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	sep := func() { b.WriteString("!\n") }

	sep()
	w("hostname %s", c.Hostname)
	if c.Domain != "" {
		w("ip domain-name %s", c.Domain)
	}
	sep()
	if len(c.NameServers) > 0 || len(c.NTPServers) > 0 || c.Timezone != "" {
		for _, n := range c.NameServers {
			w("ip name-server %s", n)
		}
		for _, n := range c.NTPServers {
			w("ntp server %s", n)
		}
		if c.Timezone != "" {
			w("ntp timezone %s", c.Timezone)
		}
		sep()
	}
	if c.BannerOff {
		w("no banner motd")
		sep()
	} else if c.BannerMotd != "" {
		w("banner motd %s", c.BannerMotd)
		sep()
	}
	if c.PwEncrypt {
		w("service password-encryption")
		sep()
	}
	if c.EnablePass != "" {
		w("enable password 8 %s", c.EnablePass)
		sep()
	}
	feats := false
	for _, f := range Features {
		if c.Features[f] {
			w("feature %s", f)
			feats = true
		}
	}
	if feats {
		sep()
	}
	if c.AAALevel != 0 && c.AAALevel != 3 {
		w("aaa logging level %d", c.AAALevel)
		sep()
	}
	if c.StartupKeep > 0 {
		w("startup-history keep %d", c.StartupKeep)
		sep()
	}
	if c.AutosaveOn {
		mins := c.AutosaveMin
		if mins == 0 {
			mins = AutosaveDefaultMin
		}
		if c.AutosaveKeep > 0 {
			w("autosave interval %d keep %d", mins, c.AutosaveKeep)
		} else {
			w("autosave interval %d", mins)
		}
		sep()
	} else {
		if c.AutosaveMin > 0 { // autosave was enabled and then disabled by the no autosave command
			w("no autosave")
			sep()
		}
		if c.AutosaveKeep > 0 {
			w("autosave keep %d", c.AutosaveKeep)
			sep()
		}
	}
	for _, u := range c.Users {
		if u.BuiltinAdmin && u.Role == "admin" {
			continue
		}
		w("username %s", u.Name)
		if u.Role != "" {
			w(" role %s", u.Role)
		}
		sep()
	}
	sps := append([]*SecProfile(nil), c.SecProfiles...)
	sort.Slice(sps, func(i, j int) bool { return sps[i].ID < sps[j].ID })
	for _, p := range sps {
		w("security-profile %d", p.ID)
		for _, r := range p.Rules {
			w(" rule %d %s", r.Num, r.String())
		}
		sep()
	}
	if c.Security != "" {
		w("security %s", c.Security)
		sep()
	}
	c.renderIPsec(w, sep)
	for _, f := range c.FilterMaps {
		for _, r := range f.Rules {
			w("filter-map ipv4 %s %d", f.Name, r.Seq)
			for _, m := range r.Matches {
				w(" match %s", m)
			}
			if r.Action != "" {
				w(" set %s", r.Action)
			}
			if r.CryptoMap != "" {
				w(" set crypto-map %s peer %s", r.CryptoMap, r.CryptoPeer)
			}
			sep()
		}
	}
	c.renderPolicy(w, sep)
	for _, p := range c.IPPools {
		w("ip pool %s %s", p.Name, rangeStr(p.From, p.To))
	}
	if len(c.IPPools) > 0 {
		sep()
	}
	ds := append([]*DHCPServer(nil), c.DHCP...)
	sort.Slice(ds, func(i, j int) bool { return ds[i].ID < ds[j].ID })
	for _, d := range ds {
		w("dhcp-server %d", d.ID)
		if d.Pool != "" {
			w(" pool %s 1", d.Pool)
		}
		if d.Mask != "" {
			w(" mask %s", d.Mask)
		}
		if d.Gateway != "" {
			w(" gateway %s", d.Gateway)
		}
		if len(d.DNS) > 0 {
			w(" dns %s", strings.Join(d.DNS, " "))
		}
		if d.Domain != "" {
			w(" domain-name %s", d.Domain)
		}
		if d.Search != "" {
			w(" domain-search %s", d.Search)
		}
		if d.Lease > 0 {
			w(" lease %d", d.Lease)
		}
		sep()
	}
	for _, p := range c.ServicePolicies {
		w("service-policy %s", p.Name)
		if p.BandwidthUnit != "" && p.BandwidthValue > 0 {
			w(" bandwidth %s %d", p.BandwidthUnit, p.BandwidthValue)
		}
		w("exit")
		sep()
	}
	for _, i := range c.Ifaces {
		w("interface %s", i.Name)
		if i.Desc != "" {
			w(" description %s", i.Desc)
		}
		for _, a := range i.Addrs {
			w(" ip address %s", a)
		}
		if i.MTU > 0 {
			w(" ip mtu %d", i.MTU)
		}
		if i.NAT != "" {
			w(" ip nat %s", i.NAT)
		}
		if i.Tunnel != nil {
			w(" ip tunnel %s %s mode %s", i.Tunnel.Src, i.Tunnel.Dst, i.Tunnel.Mode)
		}
		if i.OSPFAuth != "" {
			w(" ip ospf authentication %s", i.OSPFAuth)
		}
		for _, k := range i.OSPFKeys {
			w(" ip ospf message-digest-key %d md5 %s", k.ID, k.Key)
		}
		if i.OSPFSimple != "" {
			w(" ip ospf authentication-key %s", i.OSPFSimple)
		}
		if i.OSPFCost > 0 {
			w(" ip ospf cost %d", i.OSPFCost)
		}
		if i.OSPFNetwork != "" {
			w(" ip ospf network %s", i.OSPFNetwork)
		}
		if i.DHCP > 0 {
			w(" dhcp-server %d", i.DHCP)
		}
		for _, b := range i.FMaps {
			w(" set filter-map %s %s %d", b.Dir, b.Name, b.Seq)
		}
		if i.Shutdown {
			w(" shutdown")
		}
		sep()
	}
	for _, p := range c.Ports {
		w("port %s", p.Name)
		if p.Desc != "" {
			w(" description %s", p.Desc)
		}
		for _, s := range p.SIs {
			w(" service-instance %s", s.Name)
			switch s.Encap {
			case "untagged":
				w("  encapsulation untagged")
			case "dot1q":
				if s.Exact {
					w("  encapsulation dot1q %d exact", s.VLAN)
				} else {
					w("  encapsulation dot1q %d", s.VLAN)
				}
			}
			if s.Rewrite != "" {
				w("  rewrite %s", s.Rewrite)
			}
			if s.Connect != "" {
				w("  connect ip interface %s", s.Connect)
			}
			if s.PolicyIn != "" {
				w("  service-policy %s in", s.PolicyIn)
			}
			if s.PolicyOut != "" {
				w("  service-policy %s out", s.PolicyOut)
			}
		}
		sep()
	}
	if o := c.OSPF; o != nil {
		w("router ospf %d", o.ID)
		if o.RouterID != "" {
			w(" ospf router-id %s", o.RouterID)
		}
		if o.PassiveDefault {
			w(" passive-interface default")
		}
		for _, p := range o.Passive {
			w(" passive-interface %s", p)
		}
		for _, p := range o.NoPassive {
			w(" no passive-interface %s", p)
		}
		for _, n := range o.Nets {
			w(" network %s area %s", n.Prefix, n.Area)
		}
		areas := make([]string, 0, len(o.AreaAuth))
		for a := range o.AreaAuth {
			areas = append(areas, a)
		}
		sort.Strings(areas)
		for _, a := range areas {
			if o.AreaAuth[a] == "message-digest" {
				w(" area %s authentication message-digest", a)
			} else {
				w(" area %s authentication", a)
			}
		}
		for _, r := range o.Redistribute {
			w("%s", o.RedistLine(r))
		}
		if o.DefaultOrig {
			w("%s", o.DefaultLine())
		}
		sep()
	}
	for _, p := range c.NATPools {
		w("ip nat pool %s %s", p.Name, rangeStr(p.From, p.To))
	}
	for _, d := range c.NATDyn {
		w("ip nat source dynamic %s pool %s overload %s", d.Name, d.Pool, d.Overload)
	}
	for _, s := range c.NATStatic {
		if s.Dest {
			hp := ""
			if s.Hairpin {
				hp = " hairpin"
			}
			w("ip nat destination static %s %s %d %s %d%s", s.Proto, s.Global, s.GPort, s.Local, s.LPort, hp)
		} else if s.Proto != "" {
			w("ip nat source static %s %s %d %s %d", s.Proto, s.Local, s.LPort, s.Global, s.GPort)
		} else {
			w("ip nat source static %s %s", s.Local, s.Global)
		}
	}
	if len(c.NATPools)+len(c.NATDyn)+len(c.NATStatic) > 0 {
		sep()
	}
	for _, r := range c.Routes {
		w("ip route %s %s", r.Prefix, r.NextHop)
	}
	if len(c.Routes) > 0 {
		sep()
	}
	b.WriteString("end\n")
	return b.String()
}

// rangeStr prints a range; a single-address pool is just the address.
func rangeStr(from, to string) string {
	if from == to {
		return from
	}
	return from + "-" + to
}

// CanonNet is a network prefix without host bits ("192.168.1.5/24" → "192.168.1.0/24").
func CanonNet(cidr string) (string, error) {
	p, err := netip.ParsePrefix(cidr)
	if err != nil || !p.Addr().Is4() {
		return "", fmt.Errorf("bad prefix")
	}
	return p.Masked().String(), nil
}

// PrefixFromMask builds "A.B.C.D/M" from an address and a mask like 255.255.255.0 or a length "24".
// If wildcard=true, an inverse mask (0.0.0.255) is also allowed, as in "network … area".
func PrefixFromMask(addr, mask string, wildcard bool) (string, error) {
	a, err := netip.ParseAddr(addr)
	if err != nil || !a.Is4() {
		return "", fmt.Errorf("bad address")
	}
	n, ok := MaskLen(mask)
	if !ok && wildcard {
		if w, err := netip.ParseAddr(mask); err == nil && w.Is4() {
			b := w.As4()
			inv := fmt.Sprintf("%d.%d.%d.%d", 255-b[0], 255-b[1], 255-b[2], 255-b[3])
			n, ok = MaskLen(inv)
		}
	}
	if !ok {
		return "", fmt.Errorf("bad mask")
	}
	return netip.PrefixFrom(a, n).String(), nil
}

// ValidArea is an OSPF area identifier: a decimal number or A.B.C.D.
func ValidArea(s string) bool {
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Is4()
	}
	var n uint64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || fmt.Sprint(n) != s || n > 4294967295 {
		return false
	}
	return true
}
