package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"necorouter/internal/config"
)

// Routing policies: policy-filter-list, ip prefix-list, route-map (rmap mode), show route-map,
// show ip prefix-list.

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func prefixListNames(s *Session) []string {
	var o []string
	for _, l := range s.cfg().PrefixLists {
		o = append(o, l.Name)
	}
	return o
}

func routeMapNames(s *Session) []string { return s.cfg().RouteMapNames() }

const maxDesc = 80

func descOK(t string) (string, error) {
	t = strings.TrimSpace(t)
	switch {
	case t == "":
		return "", fmt.Errorf("the text must not be empty")
	case len(t) > maxDesc:
		return "", fmt.Errorf("the text is too long (up to %d characters)", maxDesc)
	}
	return t, nil
}

// policyConfigNodes — global mode commands: policy-filter-list and route-map.
func policyConfigNodes() []*Node {
	pflNum := func(v string) (int, error) {
		n := atoi(v)
		if !config.ValidPFLNum(n) {
			return 0, fmt.Errorf("policy-filter-list number must be 1-99 or 1300-1999")
		}
		return n, nil
	}
	add := func(action string, anyForm bool) Handler {
		return func(s *Session, v []string) error {
			n, err := pflNum(v[0])
			if err != nil {
				return err
			}
			if anyForm {
				return s.cfg().AddPFLRule(n, action, "any", "")
			}
			return s.cfg().AddPFLRule(n, action, v[1], v[2])
		}
	}
	del := func(action string, anyForm bool) Handler {
		return func(s *Session, v []string) error {
			n, err := pflNum(v[0])
			if err != nil {
				return err
			}
			if anyForm {
				return s.cfg().DelPFLRule(n, action, "any", "")
			}
			return s.cfg().DelPFLRule(n, action, v[1], v[2])
		}
	}
	rule := func(action, help string) *Node {
		return K(action, help,
			K("any", "Any address").R(add(action, true)).N(del(action, true)),
			A(KIP, "A.B.C.D", "Network address",
				A(KIP, "WILDCARD", "Wildcard (inverse) mask A.B.C.D").R(add(action, false)).N(del(action, false))))
	}
	pfl := K("policy-filter-list", "Create a policy filter list (for routing policies, not for transit traffic)",
		A(KInt, "<1-1999>", "List number (1-99, 1300-1999)",
			rule("permit", "Permit the route update"),
			rule("deny", "Deny the route update"),
			K("remark", "Description of the list",
				A(KRest, "TEXT", "Description").R(func(s *Session, v []string) error {
					n, err := pflNum(v[0])
					if err != nil {
						return err
					}
					t, err := descOK(v[1])
					if err != nil {
						return err
					}
					s.cfg().GetPFL(n).Remark = t
					return nil
				})).N(func(s *Session, v []string) error {
				n, err := pflNum(v[0])
				if err != nil {
					return err
				}
				return s.cfg().ClearPFLRemark(n)
			})).Rng(1, 1999).N(func(s *Session, v []string) error {
			n, err := pflNum(v[0])
			if err != nil {
				return err
			}
			return s.cfg().DelPFL(n)
		}))

	enterRM := func(action string) Handler {
		return func(s *Session, v []string) error {
			seq := config.RouteMapDefaultSeq
			if len(v) > 1 {
				seq = atoi(v[1])
			}
			s.cfg().GetRouteMapEntry(v[0], seq, action)
			s.enter(modeRMap, fmt.Sprintf("%s %d", v[0], seq))
			return nil
		}
	}
	delRM := func(action string) Handler {
		return func(s *Session, v []string) error {
			seq := config.RouteMapDefaultSeq
			if len(v) > 1 {
				seq = atoi(v[1])
			}
			return s.cfg().DelRouteMap(v[0], seq, action)
		}
	}
	act := func(action, help string) *Node {
		return K(action, help,
			A(KInt, "<1-65535>", "Entry number (a lower number is checked first; default 10)").Rng(1, 65535).
				R(enterRM(action)).N(delRM(action))).R(enterRM(action)).N(delRM(action))
	}
	rm := K("route-map", "Create or edit a route map entry",
		A(KWord, "NAME", "Route map name",
			act("permit", "Routes matching the entry are permitted"),
			act("deny", "Routes matching the entry are denied")).
			Chk(nameOK).Cmp(routeMapNames).
			N(func(s *Session, v []string) error { return s.cfg().DelRouteMap(v[0], -1, "") }))
	return []*Node{pfl, rm}
}

// prefixListNode — ip prefix-list ... (nested in ip).
func prefixListNode() *Node {
	// entry: v = NAME [SEQ] PREFIX [BOUNDS]; the action and keywords are not in v
	spec := func(action string, seqGiven bool) []*Node {
		add := func(form string) Handler {
			return func(s *Session, v []string) error {
				i, seq := 1, 0
				if seqGiven {
					seq = atoi(v[1])
					i = 2
				}
				pfx := v[i]
				var ge, le, eq int
				switch form {
				case "ge":
					ge = atoi(v[i+1])
				case "le":
					le = atoi(v[i+1])
				case "eq":
					eq = atoi(v[i+1])
				case "gele":
					ge, le = atoi(v[i+1]), atoi(v[i+2])
				}
				return s.cfg().SetPrefixEntry(v[0], seq, action, pfx, ge, le, eq)
			}
		}
		anyH := func(s *Session, v []string) error {
			seq := 0
			if seqGiven {
				seq = atoi(v[1])
			}
			return s.cfg().SetPrefixEntry(v[0], seq, action, "any", 0, 0, 0)
		}
		bound := func(name string) *Node { return A(KInt, "<1-32>", "Prefix length"+name).Rng(1, 32) }
		geN := bound(" (minimum)").R(add("ge"))
		geN.Kids = []*Node{K("le", "Match prefix lengths less than or equal to the value", bound(" (maximum)").R(add("gele")))}
		return []*Node{
			K("any", "Any prefix").R(anyH),
			K("all", "Any prefix (same as any)").R(anyH).Hide(),
			A(KCIDR, "A.B.C.D/M", "Network prefix",
				K("ge", "Match prefix lengths greater than or equal to the value", geN),
				K("le", "Match prefix lengths less than or equal to the value", bound(" (maximum)").R(add("le"))),
				K("eq", "Match exactly this prefix length", bound("").R(add("eq")))).R(add("")),
		}
	}
	act := func(action string, seqGiven bool, help string) *Node {
		return K(action, help, spec(action, seqGiven)...)
	}
	return K("prefix-list", "Build a prefix list",
		K("sequence-number", "Automatic numbering of entries").
			R(func(s *Session, _ []string) error { s.cfg().PrefixNoSeq = false; return nil }).
			N(func(s *Session, _ []string) error { s.cfg().PrefixNoSeq = true; return nil }),
		A(KWord, "NAME", "Prefix list name",
			K("seq", "Entry number (a lower number is checked first)",
				A(KInt, "<1-4294967295>", "Entry number",
					act("permit", true, "Permit matching prefixes"),
					act("deny", true, "Deny matching prefixes")).Rng(1, 4294967295).
					N(func(s *Session, v []string) error { return s.cfg().DelPrefixEntry(v[0], atoi(v[1])) })),
			K("description", "Description of the list (up to 80 characters)",
				A(KRest, "TEXT", "Description").R(func(s *Session, v []string) error {
					t, err := descOK(v[1])
					if err != nil {
						return err
					}
					s.cfg().GetPrefixList(v[0]).Desc = t
					return nil
				})).N(func(s *Session, v []string) error {
				return s.cfg().ClearPrefixDesc(v[0])
			}),
			act("permit", false, "Permit matching prefixes"),
			act("deny", false, "Deny matching prefixes")).
			Chk(nameOK).Cmp(prefixListNames).
			N(func(s *Session, v []string) error { return s.cfg().DelPrefixList(v[0]) }))
}

// ---- route-map mode ----

func rmapCur(s *Session) (*config.RouteMapEntry, error) {
	f := strings.Fields(s.top().obj)
	if len(f) == 2 {
		if m := s.cfg().FindRouteMapEntry(f[0], atoi(f[1])); m != nil {
			return m, nil
		}
	}
	return nil, fmt.Errorf("route-map entry no longer exists")
}

func rmapTree() []*Node {
	cur := func(f func(m *config.RouteMapEntry, v []string)) Handler {
		return func(s *Session, v []string) error {
			m, err := rmapCur(s)
			if err != nil {
				return err
			}
			f(m, v)
			return nil
		}
	}
	pint := func(v string) *int { n := atoi(v); return &n }
	clearAddr := cur(func(m *config.RouteMapEntry, _ []string) { m.MatchPFL, m.MatchPrefix = 0, "" })
	setPFL := func(s *Session, v []string) error {
		n := atoi(v[0])
		if !config.ValidPFLNum(n) {
			return fmt.Errorf("policy-filter-list number must be 1-99 or 1300-1999")
		}
		return cur(func(m *config.RouteMapEntry, _ []string) { m.MatchPFL, m.MatchPrefix = n, "" })(s, v)
	}
	const u32 = 4294967295
	return []*Node{
		K("match", "Match condition of the entry",
			K("interface", "Match the outgoing interface of the route",
				A(KWord, "NAME", "Interface name (letters, digits, _ . : / -)").Chk(nameOK).Cmp(ifaceNames).
					R(cur(func(m *config.RouteMapEntry, v []string) { m.MatchIface = v[0] })).
					N(cur(func(m *config.RouteMapEntry, _ []string) { m.MatchIface = "" }))).
				N(cur(func(m *config.RouteMapEntry, _ []string) { m.MatchIface = "" })),
			K("ip", "IP information",
				K("address", "Match the prefix against a list",
					A(KInt, "<1-1999>", "Policy-filter-list number").Rng(1, 1999).R(setPFL).N(clearAddr),
					K("prefix-list", "Match the prefix against a prefix list",
						A(KWord, "NAME", "Prefix list name (letters, digits, _ . : / -)").Chk(nameOK).Cmp(prefixListNames).
							R(cur(func(m *config.RouteMapEntry, v []string) { m.MatchPrefix, m.MatchPFL = v[0], 0 })).N(clearAddr))).
					N(clearAddr)),
			K("metric", "Match the metric of the route",
				A(KInt, "<0-4294967295>", "Metric").Rng(0, u32).
					R(cur(func(m *config.RouteMapEntry, v []string) { m.MatchMetric = pint(v[0]) })).
					N(cur(func(m *config.RouteMapEntry, _ []string) { m.MatchMetric = nil }))).
				N(cur(func(m *config.RouteMapEntry, _ []string) { m.MatchMetric = nil })),
			K("tag", "Match the tag of the route",
				A(KInt, "<1-4294967295>", "Tag").Rng(1, u32).
					R(cur(func(m *config.RouteMapEntry, v []string) { m.MatchTag = pint(v[0]) })).
					N(cur(func(m *config.RouteMapEntry, _ []string) { m.MatchTag = nil }))).
				N(cur(func(m *config.RouteMapEntry, _ []string) { m.MatchTag = nil }))),
		K("set", "Action of the entry",
			K("metric", "Metric of the route",
				A(KInt, "<1-4294967295>", "Metric").Rng(1, u32).
					R(cur(func(m *config.RouteMapEntry, v []string) { m.SetMetric = pint(v[0]) })).
					N(cur(func(m *config.RouteMapEntry, _ []string) { m.SetMetric = nil }))).
				N(cur(func(m *config.RouteMapEntry, _ []string) { m.SetMetric = nil })),
			K("metric-type", "Type of the OSPF external metric",
				A(KInt, "<1-2>", "Metric type (1 or 2)").Rng(1, 2).
					R(cur(func(m *config.RouteMapEntry, v []string) { m.SetMetricType = atoi(v[0]) })).
					N(cur(func(m *config.RouteMapEntry, _ []string) { m.SetMetricType = 0 }))).
				N(cur(func(m *config.RouteMapEntry, _ []string) { m.SetMetricType = 0 })),
			K("tag", "Tag of the route",
				A(KInt, "<1-4294967295>", "Tag").Rng(1, u32).
					R(cur(func(m *config.RouteMapEntry, v []string) { m.SetTag = pint(v[0]) })).
					N(cur(func(m *config.RouteMapEntry, _ []string) { m.SetTag = nil }))).
				N(cur(func(m *config.RouteMapEntry, _ []string) { m.SetTag = nil }))),
	}
}

// ---- show ----

func showRouteMapNode() *Node {
	show := func(s *Session, v []string) error {
		names := s.cfg().RouteMapNames()
		if len(v) > 0 {
			if len(s.cfg().FindRouteMap(v[0])) == 0 {
				return fmt.Errorf("route-map %s not found", v[0])
			}
			names = []string{v[0]}
		}
		for _, n := range names {
			s.Printf("route-map: %s\n", n)
			for _, m := range s.cfg().FindRouteMap(n) {
				s.Printf(" %s, sequence %d\n", m.Action, m.Seq)
				match, set := m.Clauses()
				s.Printf("  Match clauses:\n")
				for _, c := range match {
					s.Printf("    %s\n", c)
				}
				s.Printf("  Set clauses:\n")
				for _, c := range set {
					s.Printf("    %s\n", c)
				}
			}
		}
		return nil
	}
	return K("route-map", "Show route maps",
		A(KWord, "NAME", "Route map name (letters, digits, _ . : / -)").Cmp(routeMapNames).R(show)).R(show)
}

func showPrefixListNode() *Node {
	body := func(s *Session, l *config.PrefixList, detail bool) {
		s.Printf("ip prefix-list %s: %d entries\n", l.Name, len(l.Entries))
		if l.Desc != "" {
			s.Printf("   Description: %s\n", l.Desc)
		}
		for _, e := range l.Entries {
			s.Printf("   %s\n", e.String())
		}
		if detail {
			s.Printf("   refcount: %d\n", s.cfg().PrefixListRefs(l.Name))
		}
	}
	pick := func(s *Session, name string) ([]*config.PrefixList, error) {
		if name == "" {
			return s.cfg().PrefixLists, nil
		}
		l := s.cfg().FindPrefixList(name)
		if l == nil {
			return nil, fmt.Errorf("ip prefix-list %s not found", name)
		}
		return []*config.PrefixList{l}, nil
	}
	list := func(detail bool) Handler {
		return func(s *Session, v []string) error {
			name := ""
			if len(v) > 0 {
				name = v[0]
			}
			ls, err := pick(s, name)
			if err != nil {
				return err
			}
			for _, l := range ls {
				body(s, l, detail)
			}
			return nil
		}
	}
	summary := func(s *Session, _ []string) error {
		ls := append([]*config.PrefixList(nil), s.cfg().PrefixLists...)
		sort.Slice(ls, func(i, j int) bool { return ls[i].Name < ls[j].Name })
		for _, l := range ls {
			rng := ""
			if n := len(l.Entries); n > 0 {
				rng = fmt.Sprintf(", sequences: %d - %d", l.Entries[0].Seq, l.Entries[n-1].Seq)
			}
			s.Printf("ip prefix-list %s: count: %d%s, refcount: %d\n", l.Name, len(l.Entries), rng, s.cfg().PrefixListRefs(l.Name))
		}
		return nil
	}
	return K("prefix-list", "Show prefix lists",
		K("summary", "Summary of all prefix lists").R(summary),
		K("detail", "Entries and the number of references from route maps",
			A(KWord, "NAME", "Prefix list name (letters, digits, _ . : / -)").Cmp(prefixListNames).R(list(true))).R(list(true)),
		A(KWord, "NAME", "Prefix list name (letters, digits, _ . : / -)").Cmp(prefixListNames).R(list(false))).R(list(false))
}
