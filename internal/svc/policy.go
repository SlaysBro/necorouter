package svc

import (
	"fmt"
	"sort"
	"strings"

	"necorouter/internal/config"
)

// frrPolicy translates the routing policies referenced by router ospf (redistribute ... route-map,
// default-information originate route-map) into FRR configuration:
//
//	policy-filter-list N  -> access-list N permit|deny ADDRESS WILDCARD   (FRR accepts the number as the list name)
//	ip prefix-list        -> ip prefix-list (eq is translated to ge = le)
//	route-map             -> route-map (match interface, ip address, metric, tag; set metric, metric-type, tag)
//
// Only objects reachable from OSPF are emitted. dev translates an ip-interface name into a device name ("" means no device).
func frrPolicy(c *config.Config, dev func(string) string) (string, []string) {
	o := c.OSPF
	if o == nil {
		return "", nil
	}
	var warns []string
	var names []string
	for _, r := range o.Redistribute {
		if n := o.RedistRM[r]; n != "" {
			names = append(names, n)
		}
	}
	if o.DefaultRM != "" {
		names = append(names, o.DefaultRM)
	}
	sort.Strings(names)
	var maps []string
	seen := map[string]bool{}
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			maps = append(maps, n)
		}
	}
	pfls := map[int]bool{}
	plists := map[string]bool{}
	var b strings.Builder
	for _, n := range maps {
		for _, m := range c.FindRouteMap(n) {
			fmt.Fprintf(&b, "route-map %s %s %d\n", m.Name, m.Action, m.Seq)
			if m.MatchIface != "" {
				d := dev(m.MatchIface)
				if d == "" {
					d = m.MatchIface
					warns = append(warns, fmt.Sprintf("route-map %s %d: interface %s has no device yet", m.Name, m.Seq, m.MatchIface))
				}
				fmt.Fprintf(&b, " match interface %s\n", d)
			}
			if m.MatchPFL > 0 {
				pfls[m.MatchPFL] = true
				fmt.Fprintf(&b, " match ip address %d\n", m.MatchPFL)
			}
			if m.MatchPrefix != "" {
				plists[m.MatchPrefix] = true
				fmt.Fprintf(&b, " match ip address prefix-list %s\n", m.MatchPrefix)
			}
			if m.MatchMetric != nil {
				fmt.Fprintf(&b, " match metric %d\n", *m.MatchMetric)
			}
			if m.MatchTag != nil {
				fmt.Fprintf(&b, " match tag %d\n", *m.MatchTag)
			}
			if m.SetMetric != nil {
				fmt.Fprintf(&b, " set metric %d\n", *m.SetMetric)
			}
			if m.SetMetricType > 0 {
				fmt.Fprintf(&b, " set metric-type type-%d\n", m.SetMetricType)
			}
			if m.SetTag != nil {
				fmt.Fprintf(&b, " set tag %d\n", *m.SetTag)
			}
			b.WriteString("!\n")
		}
	}
	var acl strings.Builder
	for _, l := range c.PFLists {
		if !pfls[l.Num] {
			continue
		}
		if l.Remark != "" {
			fmt.Fprintf(&acl, "access-list %d remark %s\n", l.Num, l.Remark)
		}
		for _, r := range l.Rules {
			if r.Addr == "any" {
				fmt.Fprintf(&acl, "access-list %d %s any\n", l.Num, r.Action)
			} else {
				fmt.Fprintf(&acl, "access-list %d %s %s %s\n", l.Num, r.Action, r.Addr, r.Wild)
			}
		}
	}
	for _, l := range c.PrefixLists {
		if !plists[l.Name] {
			continue
		}
		if l.Desc != "" {
			fmt.Fprintf(&acl, "ip prefix-list %s description %s\n", l.Name, l.Desc)
		}
		for _, e := range l.Entries {
			fmt.Fprintf(&acl, "ip prefix-list %s seq %d %s %s", l.Name, e.Seq, e.Action, e.Prefix)
			ge, le := e.GE, e.LE
			if e.EQ > 0 {
				ge, le = e.EQ, e.EQ
				if pl := prefixBits(e.Prefix); pl == e.EQ {
					ge, le = 0, 0 // eq equals the prefix length: an exact match
				}
			}
			if ge > 0 {
				fmt.Fprintf(&acl, " ge %d", ge)
			}
			if le > 0 {
				fmt.Fprintf(&acl, " le %d", le)
			}
			acl.WriteString("\n")
		}
	}
	out := ""
	if acl.Len() > 0 {
		out = acl.String() + "!\n"
	}
	return out + b.String(), warns
}

func prefixBits(p string) int {
	if i := strings.IndexByte(p, '/'); i >= 0 {
		var n int
		fmt.Sscan(p[i+1:], &n)
		return n
	}
	return 0
}
