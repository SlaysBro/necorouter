package cli

import (
	"strings"
	"testing"
)

func TestPolicyFilterList(t *testing.T) {
	r, out := replay(t, `
policy-filter-list 99 remark Allow 172.168.1.0
policy-filter-list 99 permit 172.168.1.0 0.0.0.255
policy-filter-list 99 deny 10.0.0.0 0.255.255.255
policy-filter-list 1300 permit any
policy-filter-list 5 deny 192.168.0.0 0.0.255.255
`)
	i5, i99, i1300 := strings.Index(out, "policy-filter-list 5 "), strings.Index(out, "policy-filter-list 99 remark"), strings.Index(out, "policy-filter-list 1300")
	if !(i5 >= 0 && i5 < i99 && i99 < i1300) {
		t.Fatalf("lists not ordered by number:\n%s", out)
	}
	mustContain(t, out,
		"policy-filter-list 99 remark Allow 172.168.1.0\npolicy-filter-list 99 permit 172.168.1.0 0.0.0.255\npolicy-filter-list 99 deny 10.0.0.0 0.255.255.255\n",
		"policy-filter-list 1300 permit any\n")
	s := sessionOn(r, "root")
	runCmd(s, "configure terminal")
	for _, bad := range []string{"policy-filter-list 100 permit any", "policy-filter-list 0 permit any", "policy-filter-list 2000 permit any", "policy-filter-list 5 permit 10.0.0.300 0.0.0.1"} {
		if o := runCmd(s, bad); !strings.Contains(o, "%") {
			t.Errorf("%q accepted: %q", bad, o)
		}
	}
	runCmd(s, "no policy-filter-list 99 deny 10.0.0.0 0.255.255.255")
	runCmd(s, "no policy-filter-list 1300")
	runCmd(s, "no policy-filter-list 5 deny 192.168.0.0 0.0.255.255")
	runCmd(s, "no policy-filter-list 99 remark")
	if len(r.Cfg.PFLists) != 1 || len(r.Cfg.PFLists[0].Rules) != 1 || r.Cfg.PFLists[0].Remark != "" {
		t.Fatalf("no forms: %+v", r.Cfg.PFLists)
	}
	runCmd(s, "no policy-filter-list 99")
	if len(r.Cfg.PFLists) != 0 {
		t.Fatalf("list not removed: %+v", r.Cfg.PFLists)
	}
}

func TestPrefixList(t *testing.T) {
	r, out := replay(t, `
ip prefix-list TEST description test list
ip prefix-list TEST seq 10 deny any
ip prefix-list TEST seq 5 permit 10.0.0.5/8 ge 10 le 20
ip prefix-list TEST seq 7 permit 11.0.0.0/8 eq 24
ip prefix-list AUTO permit 10.1.0.0/16
ip prefix-list AUTO permit 10.2.0.0/16 le 24
ip prefix-list AUTO deny all
`)
	mustContain(t, out,
		"ip prefix-list TEST description test list\nip prefix-list TEST seq 5 permit 10.0.0.0/8 ge 10 le 20\nip prefix-list TEST seq 7 permit 11.0.0.0/8 eq 24\nip prefix-list TEST seq 10 deny any\n",
		"ip prefix-list AUTO seq 5 permit 10.1.0.0/16\nip prefix-list AUTO seq 10 permit 10.2.0.0/16 le 24\nip prefix-list AUTO seq 15 deny any\n")
	s := sessionOn(r, "root")
	runCmd(s, "configure terminal")
	for _, bad := range []string{
		"ip prefix-list B seq 1 permit 10.0.0.0/8 ge 8", "ip prefix-list B seq 1 permit 10.0.0.0/8 le 33", "ip prefix-list B seq 1 permit 10.0.0.0/8 ge 20 le 10",
		"ip prefix-list B seq 1 permit 10.0.0.0/33", "ip prefix-list B seq 1 permit 10.0.0.0/8 eq 7", "ip prefix-list B seq 0 permit any", "ip prefix-list B seq 1 permit any ge 9"} {
		if o := runCmd(s, bad); !strings.Contains(o, "%") {
			t.Errorf("%q accepted: %q", bad, o)
		}
	}
	if r.Cfg.FindPrefixList("B") != nil {
		t.Fatal("invalid entry stored")
	}
	runCmd(s, "ip prefix-list TEST seq 5 deny 10.0.0.0/8") // replacing the entry with the same number
	mustContain(t, r.Cfg.Render(), "ip prefix-list TEST seq 5 deny 10.0.0.0/8\n")
	mustNotContain(t, r.Cfg.Render(), "ge 10 le 20")
	runCmd(s, "no ip prefix-list TEST seq 7")
	runCmd(s, "no ip prefix-list TEST description")
	if l := r.Cfg.FindPrefixList("TEST"); l == nil || len(l.Entries) != 2 || l.Desc != "" {
		t.Fatalf("no seq/description: %+v", l)
	}
	runCmd(s, "no ip prefix-list TEST")
	if r.Cfg.FindPrefixList("TEST") != nil {
		t.Fatal("list not removed")
	}
	// disabling auto-numbering
	runCmd(s, "no ip prefix-list sequence-number")
	if o := runCmd(s, "ip prefix-list C permit 10.0.0.0/8"); !strings.Contains(o, "%") {
		t.Errorf("auto number with sequence-number disabled: %q", o)
	}
	runCmd(s, "ip prefix-list C seq 1 permit 10.0.0.0/8")
	mustContain(t, r.Cfg.Render(), "no ip prefix-list sequence-number\n")
}

func TestRouteMap(t *testing.T) {
	r, out := replay(t, `
ip prefix-list 1 permit 10.0.0.0/8
ip prefix-list 2 permit 11.0.0.0/8 le 24
route-map TEST permit 3
route-map TEST deny 2
 match ip address prefix-list 2
route-map TEST permit 1
 match ip address prefix-list 1
 set tag 7
route-map M2 permit
 match ip address 99
 match metric 20
 match tag 5
 set metric 100
 set metric-type 1
`)
	i1, i2, i3 := strings.Index(out, "route-map TEST permit 1\n"), strings.Index(out, "route-map TEST deny 2\n"), strings.Index(out, "route-map TEST permit 3\n")
	if !(i1 >= 0 && i1 < i2 && i2 < i3) {
		t.Fatalf("entries not ordered by seq:\n%s", out)
	}
	mustContain(t, out,
		"route-map TEST permit 1\n match ip address prefix-list 1\n set tag 7\n!\n",
		"route-map TEST deny 2\n match ip address prefix-list 2\n!\n",
		"route-map TEST permit 3\n!\n",
		"route-map M2 permit 10\n match ip address 99\n match metric 20\n match tag 5\n set metric 100\n set metric-type 1\n!\n")
	s := sessionOn(r, "root")
	runCmd(s, "configure terminal")
	runCmd(s, "route-map TEST permit 1")
	if !strings.HasSuffix(s.Prompt(), "(config-route-map)#") {
		t.Fatalf("prompt %q", s.Prompt())
	}
	runCmd(s, "no set tag")
	runCmd(s, "no match ip address")
	runCmd(s, "match interface isp")
	runCmd(s, "match ip address 1300")
	runCmd(s, "match ip address prefix-list 2") // replaces the previous condition
	if e := r.Cfg.FindRouteMapEntry("TEST", 1); e.SetTag != nil || e.MatchPFL != 0 || e.MatchPrefix != "2" || e.MatchIface != "isp" {
		t.Fatalf("entry: %+v", e)
	}
	for _, bad := range []string{"match ip address 100", "set metric-type 3", "set tag 0", "match bogus 1"} {
		if o := runCmd(s, bad); !strings.Contains(o, "%") {
			t.Errorf("%q accepted: %q", bad, o)
		}
	}
	runCmd(s, "exit")
	runCmd(s, "no route-map TEST permit 3")
	runCmd(s, "no route-map TEST permit 2") // the action does not match the entry
	if len(r.Cfg.FindRouteMap("TEST")) != 2 {
		t.Fatalf("no entry: %+v", r.Cfg.FindRouteMap("TEST"))
	}
	o := runCmd(s, "do show route-map TEST")
	mustContain(t, o, "route-map: TEST", " permit, sequence 1", " deny, sequence 2", "ip address prefix-list 2", "interface isp")
	if o := runCmd(s, "do show route-map NOPE"); !strings.Contains(o, "not found") {
		t.Errorf("show of an unknown map: %q", o)
	}
	runCmd(s, "no route-map TEST")
	if len(r.Cfg.FindRouteMap("TEST")) != 0 {
		t.Fatal("route-map not removed")
	}
}

func TestShowPrefixList(t *testing.T) {
	r, _ := replay(t, `
ip prefix-list A description first
ip prefix-list A seq 5 permit 10.0.0.0/8 ge 10 le 20
ip prefix-list A seq 10 deny any
ip prefix-list B seq 20 permit 1.1.1.0/24
route-map RM permit 1
 match ip address prefix-list A
`)
	s := sessionOn(r, "root")
	o := runCmd(s, "show ip prefix-list A")
	mustContain(t, o, "ip prefix-list A: 2 entries", "Description: first", "seq 5 permit 10.0.0.0/8 ge 10 le 20", "seq 10 deny any")
	mustContain(t, runCmd(s, "show ip prefix-list summary"), "ip prefix-list A: count: 2, sequences: 5 - 10, refcount: 1", "ip prefix-list B: count: 1")
	mustContain(t, runCmd(s, "show ip prefix-list detail A"), "refcount: 1")
	mustContain(t, runCmd(s, "show ip prefix-list"), "ip prefix-list B: 1 entries")
	if o := runCmd(s, "show ip prefix-list ZZ"); !strings.Contains(o, "not found") {
		t.Errorf("%q", o)
	}
}

func TestOSPFRouteMapApply(t *testing.T) {
	r, out := replay(t, `
route-map RM permit 10
route-map DEF permit 10
router ospf 1
 redistribute connected route-map RM
 redistribute static
 default-information originate route-map DEF
`)
	mustContain(t, out, " redistribute connected route-map RM\n", " redistribute static\n", " default-information originate route-map DEF\n")
	s := sessionOn(r, "root")
	runCmd(s, "configure terminal")
	runCmd(s, "router ospf 1")
	runCmd(s, "no redistribute connected route-map")
	runCmd(s, "redistribute static route-map RM")
	runCmd(s, "no default-information originate route-map")
	o := r.Cfg.Render()
	mustContain(t, o, " redistribute connected\n", " redistribute static route-map RM\n", " default-information originate\n")
	mustNotContain(t, o, "route-map DEF\n router", "originate route-map")
	runCmd(s, "no redistribute static")
	runCmd(s, "redistribute static")
	mustContain(t, r.Cfg.Render(), " redistribute static\n")
	mustNotContain(t, r.Cfg.Render(), "static route-map")
}

// Render → Replay → Render gives the same text.
func TestPolicyRoundTrip(t *testing.T) {
	r, out := replay(t, `
policy-filter-list 99 remark r
policy-filter-list 99 permit 172.168.1.0 0.0.0.255
policy-filter-list 1300 deny any
no ip prefix-list sequence-number
ip prefix-list P description d
ip prefix-list P seq 5 permit 10.0.0.0/8 ge 10 le 20
ip prefix-list P seq 6 permit 10.0.0.0/8 eq 24
ip prefix-list P seq 10 deny any
route-map RM permit 10
 match interface isp
 match ip address 99
 match metric 3
 match tag 4
 set metric 5
 set metric-type 2
 set tag 6
route-map RM deny 20
 match ip address prefix-list P
route-map RM permit 30
router ospf 1
 redistribute connected route-map RM
 default-information originate route-map RM
`)
	r2 := newTestRouter(t)
	if errs := r2.Replay(out); len(errs) > 0 {
		t.Fatalf("replay of the rendered config: %v\n%s", errs, out)
	}
	if out2 := r2.Cfg.Render(); out2 != out {
		t.Fatalf("Render differs after Replay:\n--- first\n%s\n--- second\n%s", out, out2)
	}
	_ = r
}
