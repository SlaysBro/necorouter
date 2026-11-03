package svc

import (
	"strings"
	"testing"

	"necorouter/internal/config"
)

func pint(n int) *int { return &n }

func TestFRRPolicy(t *testing.T) {
	c := config.New()
	c.OSPF = &config.OSPF{ID: 1, Redistribute: []string{"connected", "static"}, RedistRM: map[string]string{"connected": "RM"}, DefaultOrig: true, DefaultRM: "GONE"}
	c.PFLists = []*config.PolicyFilterList{
		{Num: 99, Remark: "lan", Rules: []config.PFRule{{Action: "permit", Addr: "172.168.1.0", Wild: "0.0.0.255"}, {Action: "deny", Addr: "any"}}},
		{Num: 5, Rules: []config.PFRule{{Action: "permit", Addr: "any"}}}, // unused: does not end up in FRR
	}
	c.PrefixLists = []*config.PrefixList{{Name: "P", Desc: "d", Entries: []*config.PrefixEntry{
		{Seq: 5, Action: "permit", Prefix: "10.0.0.0/8", GE: 10, LE: 20},
		{Seq: 6, Action: "permit", Prefix: "11.0.0.0/8", EQ: 24},
		{Seq: 7, Action: "permit", Prefix: "12.0.0.0/8", EQ: 8},
		{Seq: 10, Action: "deny", Prefix: "any"}}}}
	c.RouteMaps = []*config.RouteMapEntry{
		{Name: "RM", Seq: 10, Action: "permit", MatchIface: "isp", MatchPFL: 99, MatchMetric: pint(3), MatchTag: pint(4), SetMetric: pint(5), SetMetricType: 2, SetTag: pint(6)},
		{Name: "RM", Seq: 20, Action: "deny", MatchPrefix: "P"},
		{Name: "RM", Seq: 30, Action: "permit"},
	}
	got, warns := frrPolicy(c, func(n string) string { return "eth1" })
	for _, w := range []string{
		"access-list 99 remark lan\n", "access-list 99 permit 172.168.1.0 0.0.0.255\n", "access-list 99 deny any\n",
		"ip prefix-list P description d\n", "ip prefix-list P seq 5 permit 10.0.0.0/8 ge 10 le 20\n",
		"ip prefix-list P seq 6 permit 11.0.0.0/8 ge 24 le 24\n", "ip prefix-list P seq 7 permit 12.0.0.0/8\n", "ip prefix-list P seq 10 deny any\n",
		"route-map RM permit 10\n match interface eth1\n match ip address 99\n match metric 3\n match tag 4\n set metric 5\n set metric-type type-2\n set tag 6\n!\n",
		"route-map RM deny 20\n match ip address prefix-list P\n!\n", "route-map RM permit 30\n!\n",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("FRR config lacks %q:\n%s", w, got)
		}
	}
	if strings.Contains(got, "access-list 5 ") {
		t.Errorf("unused list rendered:\n%s", got)
	}
	// route-map GONE does not exist: a warning on apply
	all := strings.Join(c.PolicyWarnings(), "\n")
	if !strings.Contains(all, "route-map GONE does not exist") {
		t.Errorf("no warning for an unknown route-map: %v", all)
	}
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
	c.RouteMaps[1].MatchPrefix = "NOPE"
	c.RouteMaps[0].MatchPFL = 98
	all = strings.Join(c.PolicyWarnings(), "\n")
	for _, w := range []string{"ip prefix-list NOPE does not exist", "policy-filter-list 98 does not exist"} {
		if !strings.Contains(all, w) {
			t.Errorf("no warning %q: %v", w, all)
		}
	}
}
