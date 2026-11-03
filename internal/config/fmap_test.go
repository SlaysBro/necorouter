package config

import "testing"

func TestParseFMMatch(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"tcp 10.0.0.0/24 192.168.1.0/24 eq www", "tcp 10.0.0.0/24 192.168.1.0/24 eq 80"},
		{"TCP any eq 1000 any range 10 20", "tcp any eq 1000 any range 10 20"},
		{"1 host 122.168.1.15 host 172.20.100.1", "1 host 122.168.1.15 host 172.20.100.1"},
		{"ospf 192.168.32.0 0.0.7.255 any", "ospf 192.168.32.0 0.0.7.255 any"},
		{"fragments any host 20.0.0.3 host 10.0.0.2", "fragments any host 20.0.0.3 host 10.0.0.2"},
		{"tcp any any eq ssh syn not-ack", "tcp any any eq 22 syn not-ack"},
		{"udp any any eq tftp dscp 46 ttl gt 3", "udp any any eq 69 dscp 46 ttl gt 3"},
		{"udp any any range 1 2 ttl range 5 9", "udp any any range 1 2 ttl range 5 9"},
		{"any any any", "any any any"},
	} {
		m, err := ParseFMMatch(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got := m.String(); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
		// the normalized form parses back into itself
		if m2, err := ParseFMMatch(m.String()); err != nil || m2.String() != m.String() {
			t.Errorf("%q: not stable: %q (%v)", c.in, m2.String(), err)
		}
	}
}

func TestParseFMMatchErrors(t *testing.T) {
	for _, in := range []string{
		"", "tcp", "tcp any", "bogus any any", "256 any any", "tcp 10.0.0.0/33 any",
		"tcp any any eq 99999", "icmp any any eq 5", "icmp any eq 5 any", "fragments tcp any any eq 22",
		"tcp any any dscp 64", "tcp any any ttl range 9 1", "udp any any syn", "tcp any any syn not-syn",
		"tcp any any bogus", "tcp host 300.1.1.1 any", "tcp any any range 20 10",
	} {
		if _, err := ParseFMMatch(in); err == nil {
			t.Errorf("%q must be rejected", in)
		}
	}
}

func TestFilterMapHelpers(t *testing.T) {
	c := New()
	fm := c.GetFilterMap("B")
	c.GetFilterMap("A")
	if c.FilterMaps[0].Name != "A" || c.GetFilterMap("B") != fm {
		t.Fatal("maps must stay sorted and unique")
	}
	if fm.NextSeq() != 10 {
		t.Fatalf("first seq: %d", fm.NextSeq())
	}
	fm.GetRule(20)
	fm.GetRule(5)
	if fm.Rules[0].Seq != 5 || fm.NextSeq() != 30 {
		t.Fatalf("rules %v next %d", fm.Rules, fm.NextSeq())
	}
}
