package svc

import (
	"testing"

	"necorouter/internal/config"
)

func TestSecRuleExpr(t *testing.T) {
	for _, c := range []struct {
		r    config.SecRule
		want string
	}{
		{config.SecRule{Num: 0, Action: "deny", Proto: "tcp", Src: "any", Dst: "any", Op: "eq", P1: 22}, "tcp dport 22 drop"},
		{config.SecRule{Action: "permit", Proto: "udp", Src: "any", Dst: "any", Op: "range", P1: 10, P2: 20}, "udp dport 10-20 accept"},
		{config.SecRule{Action: "deny", Proto: "ospf", Src: "host 127.0.0.12", Dst: "any"}, "ip protocol 89 ip saddr 127.0.0.12 drop"},
		{config.SecRule{Action: "deny", Proto: "tcp", Src: "any", Dst: "192.168.10.2/24", Op: "gt", P1: 5}, "ip daddr 192.168.10.0/24 tcp dport > 5 drop"},
		{config.SecRule{Action: "permit", Proto: "any", Src: "any", Dst: "any"}, "accept"},
		{config.SecRule{Action: "deny", Proto: "112", Src: "any", Dst: "any"}, "ip protocol 112 drop"},
		{config.SecRule{Action: "deny", Proto: "tcp", Src: "any", Dst: "any"}, "ip protocol 6 drop"},
	} {
		got, err := secRuleExpr(c.r)
		if err != nil || got != c.want {
			t.Errorf("%+v: got %q (%v), want %q", c.r, got, err, c.want)
		}
	}
}
