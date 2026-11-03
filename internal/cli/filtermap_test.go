package cli

import (
	"strings"
	"testing"
)

func TestFilterMapFlow(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	runCmd(s, "configure terminal")
	runCmd(s, "filter-map ipv4 ALLOW 10")
	if !strings.HasSuffix(s.Prompt(), "(filter-map-ipv4)#") {
		t.Fatalf("prompt: %q", s.Prompt())
	}
	for _, l := range []string{
		"match tcp 10.0.0.0/24 192.168.1.0/24 eq www",
		"match 1 host 122.168.1.15 host 172.20.100.1",
		"match ospf 192.168.32.0 0.0.7.255 any",
		"set accept",
	} {
		if out := runCmd(s, l); strings.Contains(out, "%") {
			t.Fatalf("%q rejected: %q", l, out)
		}
	}
	runCmd(s, "exit")
	runCmd(s, "filter-map ipv4 ALLOW") // without SEQ: the next free number (20)
	runCmd(s, "match any any any")
	runCmd(s, "set discard")
	runCmd(s, "end")

	out := runCmd(s, "show filter-map ipv4")
	for _, want := range []string{" Filter map ALLOW", " Filter 10", " match tcp 10.0.0.0/24 192.168.1.0/24 eq 80", " set accept", " Filter 20", " set discard"} {
		if !strings.Contains(out, want) {
			t.Fatalf("show lacks %q:\n%s", want, out)
		}
	}
	if out := runCmd(s, "show filter-map ipv4 NOPE"); !strings.Contains(out, "%") {
		t.Fatalf("unknown list must be an error: %q", out)
	}

	runCmd(s, "configure terminal")
	runCmd(s, "interface lan")
	if out := runCmd(s, "set filter-map in NOPE"); !strings.Contains(out, "%") {
		t.Fatalf("binding a missing list must fail: %q", out)
	}
	runCmd(s, "set filter-map in ALLOW")
	runCmd(s, "set filter-map out ALLOW 5")
	runCmd(s, "end")

	rc := r.Cfg.Render()
	for _, want := range []string{"filter-map ipv4 ALLOW 10\n match tcp 10.0.0.0/24 192.168.1.0/24 eq 80", " set filter-map in ALLOW 10", " set filter-map out ALLOW 5"} {
		if !strings.Contains(rc, want) {
			t.Fatalf("running-config lacks %q:\n%s", want, rc)
		}
	}
	if strings.Index(rc, "filter-map ipv4") > strings.Index(rc, "interface lan") {
		t.Fatal("filter-maps must be written before the interfaces that use them")
	}
	// the configuration replays without errors and yields the same text
	cfg, errs := r.validateConfig(rc)
	if len(errs) > 0 || cfg.Render() != rc {
		t.Fatalf("replay differs: %v", errs)
	}

	// a list with a binding is not deleted
	runCmd(s, "configure terminal")
	if out := runCmd(s, "no filter-map ipv4 ALLOW"); !strings.Contains(out, "attached") {
		t.Fatalf("attached list must not be deleted: %q", out)
	}
	runCmd(s, "interface lan")
	runCmd(s, "no set filter-map in ALLOW")
	runCmd(s, "no set filter-map out ALLOW")
	runCmd(s, "exit")
	runCmd(s, "no filter-map ipv4 ALLOW 20")
	if r.Cfg.FindFilterMap("ALLOW") == nil || r.Cfg.FindFilterMap("ALLOW").Rule(20) != nil {
		t.Fatal("rule 20 must be removed, the list kept")
	}
	runCmd(s, "no filter-map ipv4 ALLOW")
	if r.Cfg.FindFilterMap("ALLOW") != nil {
		t.Fatal("list must be removed")
	}
}

func TestFilterMapRejectsBadInput(t *testing.T) {
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	runCmd(s, "configure terminal")
	runCmd(s, "filter-map ipv4 X 10")
	for _, l := range []string{
		"match tcp any any eq 99999", "match icmp any any eq 5", "match fragments tcp any any eq 22",
		"match bogus any any", "match tcp any", "set nexthop 1.1.1.1",
	} {
		if out := runCmd(s, l); !strings.Contains(out, "%") {
			t.Errorf("%q must be rejected: %q", l, out)
		}
	}
	if out := runCmd(s, "filter-map ipv4 bad!name 10"); !strings.Contains(out, "%") {
		t.Errorf("bad name must be rejected: %q", out)
	}
}
