package config

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// Routing policies (manual, sections 21.1, 21.2 and 22):
//
//	policy-filter-list N permit|deny ADDRESS|any [WILDCARD]    (N: 1-99, 1300-1999)
//	policy-filter-list N remark TEXT
//	ip prefix-list NAME [seq N] permit|deny PREFIX/LEN [ge N] [le N] | [eq N] | any
//	ip prefix-list NAME description TEXT
//	route-map NAME permit|deny [SEQ]    (match interface | ip address N | ip address prefix-list NAME | metric | tag;
//	                                     set tag | metric | metric-type)
//
// Applied in router ospf: redistribute ... route-map NAME, default-information originate route-map NAME.

// PFRule is one policy-filter-list rule. Addr: "any" or A.B.C.D (then Wild is the inverse mask).
type PFRule struct {
	Action string `json:"action"` // permit | deny
	Addr   string `json:"addr"`
	Wild   string `json:"wild,omitempty"`
}

// PolicyFilterList is a numbered list; rules are checked in the order they were added.
type PolicyFilterList struct {
	Num    int      `json:"num"`
	Remark string   `json:"remark,omitempty"`
	Rules  []PFRule `json:"rules,omitempty"`
}

// PrefixEntry is a prefix-list entry. Prefix: "any" or A.B.C.D/M. GE, LE, EQ: 0 means not set.
type PrefixEntry struct {
	Seq    int    `json:"seq"`
	Action string `json:"action"`
	Prefix string `json:"prefix"`
	GE     int    `json:"ge,omitempty"`
	LE     int    `json:"le,omitempty"`
	EQ     int    `json:"eq,omitempty"`
}

type PrefixList struct {
	Name    string         `json:"name"`
	Desc    string         `json:"desc,omitempty"`
	Entries []*PrefixEntry `json:"entries,omitempty"` // in ascending Seq order
}

// RouteMapEntry is a route-map entry: name + sequence number. Pointer numbers: nil means the condition or action is not set.
type RouteMapEntry struct {
	Name          string `json:"name"`
	Seq           int    `json:"seq"`
	Action        string `json:"action"` // permit | deny
	MatchIface    string `json:"match_iface,omitempty"`
	MatchPFL      int    `json:"match_pfl,omitempty"`    // match ip address N
	MatchPrefix   string `json:"match_prefix,omitempty"` // match ip address prefix-list NAME
	MatchMetric   *int   `json:"match_metric,omitempty"`
	MatchTag      *int   `json:"match_tag,omitempty"`
	SetTag        *int   `json:"set_tag,omitempty"`
	SetMetric     *int   `json:"set_metric,omitempty"`
	SetMetricType int    `json:"set_metric_type,omitempty"` // 1 | 2
}

// ValidPFLNum validates a policy-filter-list number: 1-99 or 1300-1999.
func ValidPFLNum(n int) bool { return (n >= 1 && n <= 99) || (n >= 1300 && n <= 1999) }

func (c *Config) FindPFL(n int) *PolicyFilterList {
	for _, l := range c.PFLists {
		if l.Num == n {
			return l
		}
	}
	return nil
}

// GetPFL returns the list, creating it if necessary (lists are stored in ascending number order).
func (c *Config) GetPFL(n int) *PolicyFilterList {
	if l := c.FindPFL(n); l != nil {
		return l
	}
	l := &PolicyFilterList{Num: n}
	c.PFLists = append(c.PFLists, l)
	sort.Slice(c.PFLists, func(i, j int) bool { return c.PFLists[i].Num < c.PFLists[j].Num })
	return l
}

func (c *Config) dropPFLIfEmpty(l *PolicyFilterList) {
	if len(l.Rules) > 0 || l.Remark != "" {
		return
	}
	out := c.PFLists[:0:0]
	for _, x := range c.PFLists {
		if x != l {
			out = append(out, x)
		}
	}
	c.PFLists = out
}

// AddPFLRule appends a rule to the end of the list (an identical rule is not added again).
func (c *Config) AddPFLRule(n int, action, addr, wild string) error {
	if !ValidPFLNum(n) {
		return fmt.Errorf("policy-filter-list number must be 1-99 or 1300-1999")
	}
	r := PFRule{Action: action}
	if strings.EqualFold(addr, "any") {
		r.Addr = "any"
	} else {
		a, err := netip.ParseAddr(addr)
		if err != nil || !a.Is4() {
			return fmt.Errorf("invalid address %s", addr)
		}
		w, err := netip.ParseAddr(wild)
		if err != nil || !w.Is4() {
			return fmt.Errorf("invalid wildcard mask %s", wild)
		}
		r.Addr, r.Wild = a.String(), w.String()
	}
	l := c.GetPFL(n)
	for _, x := range l.Rules {
		if x == r {
			return nil
		}
	}
	l.Rules = append(l.Rules, r)
	return nil
}

// DelPFLRule deletes a rule; an empty list disappears together with its description.
func (c *Config) DelPFLRule(n int, action, addr, wild string) error {
	l := c.FindPFL(n)
	if l == nil {
		return fmt.Errorf("policy-filter-list %d not found", n)
	}
	if strings.EqualFold(addr, "any") {
		addr, wild = "any", ""
	}
	out := l.Rules[:0:0]
	found := false
	for _, x := range l.Rules {
		if x.Action == action && x.Addr == addr && x.Wild == wild {
			found = true
			continue
		}
		out = append(out, x)
	}
	if !found {
		return fmt.Errorf("rule not found in policy-filter-list %d", n)
	}
	l.Rules = out
	c.dropPFLIfEmpty(l)
	return nil
}

func (c *Config) DelPFL(n int) error {
	l := c.FindPFL(n)
	if l == nil {
		return fmt.Errorf("policy-filter-list %d not found", n)
	}
	l.Rules, l.Remark = nil, ""
	c.dropPFLIfEmpty(l)
	return nil
}

func (c *Config) FindPrefixList(name string) *PrefixList {
	for _, l := range c.PrefixLists {
		if l.Name == name {
			return l
		}
	}
	return nil
}

func (c *Config) GetPrefixList(name string) *PrefixList {
	if l := c.FindPrefixList(name); l != nil {
		return l
	}
	l := &PrefixList{Name: name}
	c.PrefixLists = append(c.PrefixLists, l)
	sort.Slice(c.PrefixLists, func(i, j int) bool { return c.PrefixLists[i].Name < c.PrefixLists[j].Name })
	return l
}

func (c *Config) dropPrefixListIfEmpty(l *PrefixList) {
	if len(l.Entries) > 0 || l.Desc != "" {
		return
	}
	out := c.PrefixLists[:0:0]
	for _, x := range c.PrefixLists {
		if x != l {
			out = append(out, x)
		}
	}
	c.PrefixLists = out
}

// SetPrefixEntry adds an entry (seq = 0 means the number is chosen automatically: 5, then maximum + 5).
// An entry with the same number is replaced. Entries are stored in ascending number order.
func (c *Config) SetPrefixEntry(name string, seq int, action, prefix string, ge, le, eq int) error {
	e := &PrefixEntry{Action: action, GE: ge, LE: le, EQ: eq}
	if strings.EqualFold(prefix, "any") || strings.EqualFold(prefix, "all") {
		if ge|le|eq != 0 {
			return fmt.Errorf("ge, le and eq cannot be used with any")
		}
		e.Prefix = "any"
	} else {
		p, err := netip.ParsePrefix(prefix)
		if err != nil || !p.Addr().Is4() {
			return fmt.Errorf("invalid prefix %s", prefix)
		}
		e.Prefix = p.Masked().String()
		n := p.Bits()
		switch {
		case eq != 0 && (ge != 0 || le != 0):
			return fmt.Errorf("eq cannot be combined with ge or le")
		case eq != 0 && (eq < n || eq > 32):
			return fmt.Errorf("eq must be in the range %d-32", n)
		case ge != 0 && (ge <= n || ge > 32):
			return fmt.Errorf("ge must be greater than the prefix length %d and not above 32", n)
		case le != 0 && (le <= n || le > 32):
			return fmt.Errorf("le must be greater than the prefix length %d and not above 32", n)
		case ge != 0 && le != 0 && ge > le:
			return fmt.Errorf("ge must not be greater than le")
		}
	}
	l := c.FindPrefixList(name)
	if seq == 0 {
		if c.PrefixNoSeq {
			return fmt.Errorf("sequence number is required (automatic numbering is disabled)")
		}
		seq = 5
		if l != nil && len(l.Entries) > 0 {
			seq = l.Entries[len(l.Entries)-1].Seq + 5
		}
	}
	e.Seq = seq
	l = c.GetPrefixList(name)
	for i, x := range l.Entries {
		if x.Seq == seq {
			l.Entries[i] = e
			return nil
		}
	}
	l.Entries = append(l.Entries, e)
	sort.SliceStable(l.Entries, func(i, j int) bool { return l.Entries[i].Seq < l.Entries[j].Seq })
	return nil
}

func (c *Config) DelPrefixEntry(name string, seq int) error {
	l := c.FindPrefixList(name)
	if l == nil {
		return fmt.Errorf("ip prefix-list %s not found", name)
	}
	out := l.Entries[:0:0]
	for _, x := range l.Entries {
		if x.Seq != seq {
			out = append(out, x)
		}
	}
	if len(out) == len(l.Entries) {
		return fmt.Errorf("entry %d not found in ip prefix-list %s", seq, name)
	}
	l.Entries = out
	c.dropPrefixListIfEmpty(l)
	return nil
}

func (c *Config) DelPrefixList(name string) error {
	l := c.FindPrefixList(name)
	if l == nil {
		return fmt.Errorf("ip prefix-list %s not found", name)
	}
	l.Entries, l.Desc = nil, ""
	c.dropPrefixListIfEmpty(l)
	return nil
}

// String is the entry in a form suitable for running-config and show: "seq 5 permit 10.0.0.0/8 ge 10 le 20".
func (e *PrefixEntry) String() string {
	s := fmt.Sprintf("seq %d %s %s", e.Seq, e.Action, e.Prefix)
	if e.GE > 0 {
		s += fmt.Sprintf(" ge %d", e.GE)
	}
	if e.LE > 0 {
		s += fmt.Sprintf(" le %d", e.LE)
	}
	if e.EQ > 0 {
		s += fmt.Sprintf(" eq %d", e.EQ)
	}
	return s
}

func (c *Config) FindRouteMap(name string) []*RouteMapEntry {
	var out []*RouteMapEntry
	for _, m := range c.RouteMaps {
		if m.Name == name {
			out = append(out, m)
		}
	}
	return out
}

func (c *Config) FindRouteMapEntry(name string, seq int) *RouteMapEntry {
	for _, m := range c.RouteMaps {
		if m.Name == name && m.Seq == seq {
			return m
		}
	}
	return nil
}

// GetRouteMapEntry returns the entry, creating it if necessary (by name, then in ascending number order:
// an entry with a lower number is checked earlier). The action of an existing entry is updated.
func (c *Config) GetRouteMapEntry(name string, seq int, action string) *RouteMapEntry {
	if m := c.FindRouteMapEntry(name, seq); m != nil {
		m.Action = action
		return m
	}
	m := &RouteMapEntry{Name: name, Seq: seq, Action: action}
	c.RouteMaps = append(c.RouteMaps, m)
	sort.SliceStable(c.RouteMaps, func(i, j int) bool {
		a, b := c.RouteMaps[i], c.RouteMaps[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Seq < b.Seq
	})
	return m
}

// DelRouteMap deletes an entry (seq >= 0; if action is not empty, it must match) or the whole map (seq < 0).
func (c *Config) DelRouteMap(name string, seq int, action string) error {
	var out []*RouteMapEntry
	found := false
	for _, m := range c.RouteMaps {
		if m.Name == name && (seq < 0 || m.Seq == seq) {
			if seq >= 0 && action != "" && m.Action != action {
				return fmt.Errorf("route-map %s %d is %s", name, seq, m.Action)
			}
			found = true
			continue
		}
		out = append(out, m)
	}
	if !found {
		if seq < 0 {
			return fmt.Errorf("route-map %s not found", name)
		}
		return fmt.Errorf("route-map %s %d not found", name, seq)
	}
	c.RouteMaps = out
	return nil
}

// RouteMapDefaultSeq is the entry number if none is given (manual, 22.1).
const RouteMapDefaultSeq = 10

// Clauses returns the conditions (match) and actions (set) of the entry in CLI syntax, in output order.
func (m *RouteMapEntry) Clauses() (match, set []string) {
	if m.MatchIface != "" {
		match = append(match, "interface "+m.MatchIface)
	}
	if m.MatchPFL > 0 {
		match = append(match, fmt.Sprintf("ip address %d", m.MatchPFL))
	}
	if m.MatchPrefix != "" {
		match = append(match, "ip address prefix-list "+m.MatchPrefix)
	}
	if m.MatchMetric != nil {
		match = append(match, fmt.Sprintf("metric %d", *m.MatchMetric))
	}
	if m.MatchTag != nil {
		match = append(match, fmt.Sprintf("tag %d", *m.MatchTag))
	}
	if m.SetMetric != nil {
		set = append(set, fmt.Sprintf("metric %d", *m.SetMetric))
	}
	if m.SetMetricType > 0 {
		set = append(set, fmt.Sprintf("metric-type %d", m.SetMetricType))
	}
	if m.SetTag != nil {
		set = append(set, fmt.Sprintf("tag %d", *m.SetTag))
	}
	return
}

// RouteMapNames lists the route-map names (alphabetically).
func (c *Config) RouteMapNames() []string {
	seen := map[string]bool{}
	var o []string
	for _, m := range c.RouteMaps {
		if !seen[m.Name] {
			seen[m.Name] = true
			o = append(o, m.Name)
		}
	}
	sort.Strings(o)
	return o
}

// PrefixListRefs counts how many route-map entries reference the prefix list (refcount).
func (c *Config) PrefixListRefs(name string) int {
	n := 0
	for _, m := range c.RouteMaps {
		if m.MatchPrefix == name {
			n++
		}
	}
	return n
}

// PolicyWarnings lists references to nonexistent lists and maps (checked on apply, not on input:
// the order of commands in the configuration does not matter).
func (c *Config) PolicyWarnings() []string {
	var w []string
	for _, m := range c.RouteMaps {
		if m.MatchPFL > 0 && c.FindPFL(m.MatchPFL) == nil {
			w = append(w, fmt.Sprintf("route-map %s %d: policy-filter-list %d does not exist", m.Name, m.Seq, m.MatchPFL))
		}
		if m.MatchPrefix != "" && c.FindPrefixList(m.MatchPrefix) == nil {
			w = append(w, fmt.Sprintf("route-map %s %d: ip prefix-list %s does not exist", m.Name, m.Seq, m.MatchPrefix))
		}
		if m.MatchIface != "" && c.FindIface(m.MatchIface) == nil {
			w = append(w, fmt.Sprintf("route-map %s %d: interface %s does not exist", m.Name, m.Seq, m.MatchIface))
		}
	}
	if o := c.OSPF; o != nil {
		for _, r := range o.Redistribute {
			if n := o.RedistRM[r]; n != "" && len(c.FindRouteMap(n)) == 0 {
				w = append(w, fmt.Sprintf("router ospf: redistribute %s: route-map %s does not exist", r, n))
			}
		}
		if n := o.DefaultRM; n != "" && len(c.FindRouteMap(n)) == 0 {
			w = append(w, fmt.Sprintf("router ospf: default-information originate: route-map %s does not exist", n))
		}
	}
	return w
}

// renderPolicy prints policy-filter-list, ip prefix-list and route-map into running-config.
func (c *Config) renderPolicy(w func(string, ...any), sep func()) {
	for _, l := range c.PFLists {
		if l.Remark != "" {
			w("policy-filter-list %d remark %s", l.Num, l.Remark)
		}
		for _, r := range l.Rules {
			if r.Addr == "any" {
				w("policy-filter-list %d %s any", l.Num, r.Action)
			} else {
				w("policy-filter-list %d %s %s %s", l.Num, r.Action, r.Addr, r.Wild)
			}
		}
		sep()
	}
	if c.PrefixNoSeq {
		w("no ip prefix-list sequence-number")
		sep()
	}
	for _, l := range c.PrefixLists {
		if l.Desc != "" {
			w("ip prefix-list %s description %s", l.Name, l.Desc)
		}
		for _, e := range l.Entries {
			w("ip prefix-list %s %s", l.Name, e.String())
		}
		sep()
	}
	for _, m := range c.RouteMaps {
		w("route-map %s %s %d", m.Name, m.Action, m.Seq)
		match, set := m.Clauses()
		for _, s := range match {
			w(" match %s", s)
		}
		for _, s := range set {
			w(" set %s", s)
		}
		sep()
	}
}

// ClearPFLRemark removes the list description (an empty list disappears).
func (c *Config) ClearPFLRemark(n int) error {
	l := c.FindPFL(n)
	if l == nil {
		return fmt.Errorf("policy-filter-list %d not found", n)
	}
	l.Remark = ""
	c.dropPFLIfEmpty(l)
	return nil
}

// ClearPrefixDesc removes the prefix-list description (an empty list disappears).
func (c *Config) ClearPrefixDesc(name string) error {
	l := c.FindPrefixList(name)
	if l == nil {
		return fmt.Errorf("ip prefix-list %s not found", name)
	}
	l.Desc = ""
	c.dropPrefixListIfEmpty(l)
	return nil
}
