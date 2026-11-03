package svc

import (
	"os/exec"
	"strings"
	"testing"
)

var fmCases = []struct{ in, want string }{
	{"tcp 10.0.0.0/24 192.168.1.0/24 eq www", "ip saddr 10.0.0.0/24 ip daddr 192.168.1.0/24 tcp dport 80"},
	{"1 host 122.168.1.15 host 172.20.100.1", "ip protocol 1 ip saddr 122.168.1.15 ip daddr 172.20.100.1"},
	{"ospf 192.168.32.0 0.0.7.255 any", "ip protocol 89 ip saddr 192.168.32.0/21"},
	{"any 10.0.0.1 0.255.0.0 any", "ip saddr & 255.0.255.255 == 10.0.0.1"},
	{"fragments any host 20.0.0.3 host 10.0.0.2", "ip frag-off & 0x1fff != 0 ip saddr 20.0.0.3 ip daddr 10.0.0.2"},
	{"tcp any any eq ssh syn not-ack", "tcp dport 22 tcp flags & 0x12 == 0x02"},
	{"udp any eq 5000 any range 10 20 dscp 46 ttl range 1 5", "udp sport 5000 udp dport 10-20 ip dscp 46 ip ttl 1-5"},
	{"tcp any gt 1023 any lt 100 ttl eq 64", "tcp sport > 1023 tcp dport < 100 ip ttl 64"},
	{"tcp any any", "ip protocol 6"},
	{"gre host 1.1.1.1 host 2.2.2.2", "ip protocol 47 ip saddr 1.1.1.1 ip daddr 2.2.2.2"},
	{"any any any", ""},
}

func TestFmMatchExpr(t *testing.T) {
	for _, c := range fmCases {
		got, err := fmMatchExpr(c.in)
		if err != nil || got != c.want {
			t.Errorf("%q: got %q (%v), want %q", c.in, got, err, c.want)
		}
	}
}

// If nft -c works in the environment, check the syntax of all expressions with nftables itself.
func TestFmExprAcceptedByNft(t *testing.T) {
	nft, err := exec.LookPath("nft")
	if err != nil {
		t.Skip("nft is not installed")
	}
	check := func(rules string) error {
		cmd := exec.Command(nft, "-c", "-f", "-")
		cmd.Stdin = strings.NewReader("table ip fmtest {\n chain c { type filter hook forward priority filter; policy accept;\n" + rules + " }\n}\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return &exec.ExitError{Stderr: out}
		}
		return nil
	}
	if err := check("accept\n"); err != nil {
		t.Skipf("nft -c is not usable here: %v", err)
	}
	for _, verdict := range []string{"accept", "drop", "reject"} {
		for _, c := range fmCases {
			e, _ := fmMatchExpr(c.in)
			line := strings.TrimSpace(`iifname "eth0" `+e) + " " + verdict + "\n"
			if err := check(line); err != nil {
				t.Errorf("nft rejects %q: %s", line, err.(*exec.ExitError).Stderr)
			}
		}
	}
}
