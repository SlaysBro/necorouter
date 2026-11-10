package svc

import (
	"io"
	"log"
	"sort"
	"strings"
	"testing"

	"necorouter/internal/config"
)

func ipsecTestConfig() *config.Config {
	c := config.New()
	c.IPsecIKE = true
	c.IPsecProfiles = []*config.IPsecProfile{
		{Name: "CIPROFILE12", Version: "ike-v1", Mode: "tunnel", NATT: true, P1Proposal: "aes256-sha256-modp2048", PSK: `Example"Pass#12`,
			Protocol: "esp", P2Proposal: "aes256-sha256", LocalTS: "192.168.123.1", RemoteTS: "192.168.123.2"},
		{Name: "CIPROFILE13", Version: "ike-v2", Mode: "tunnel", P1Proposal: "aes256-sha256-modp2048", PSK: "ExamplePassword13",
			Protocol: "esp", P2Proposal: "aes256-sha256", LocalTS: "192.168.123.1,10.0.0.0/24", RemoteTS: "192.168.123.3", Rekey: 3600},
	}
	c.CryptoMaps = []*config.CryptoMap{
		{Name: "CMAP12", Seq: 10, Peer: "192.168.123.2", Profile: "CIPROFILE12"},
		{Name: "CMAP13", Seq: 20, Peer: "192.168.123.3", Profile: "CIPROFILE13"},
	}
	return c
}

func TestIPsecSwanctl(t *testing.T) {
	conf, warns, n := ipsecSwanctl(ipsecTestConfig())
	if n != 2 || len(warns) != 0 {
		t.Fatalf("conns=%d warns=%v", n, warns)
	}
	for _, want := range []string{
		"CIPROFILE12 {", "version = 1", "remote_addrs = 192.168.123.2", "encap = yes",
		`proposals = "aes256-sha256-modp2048"`, "local_ts = 192.168.123.1/32", "remote_ts = 192.168.123.2/32",
		`esp_proposals = "aes256-sha256"`, "start_action = trap",
		"CIPROFILE13 {", "version = 2", "local_ts = 192.168.123.1/32,10.0.0.0/24", "rekey_time = 3600s",
		`secret = "Example\"Pass#12"`, `id-1 = "192.168.123.3"`,
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q in:\n%s", want, conf)
		}
	}
	if strings.Count(conf, "encap = yes") != 1 {
		t.Errorf("encap only for ike-v1 with nat-traversal:\n%s", conf)
	}
}

func TestIPsecIncompleteEntriesSkipped(t *testing.T) {
	c := ipsecTestConfig()
	c.CryptoMaps = append(c.CryptoMaps,
		&config.CryptoMap{Name: "X", Seq: 1, Peer: "10.1.1.1"},                    // no profile
		&config.CryptoMap{Name: "X", Seq: 2, Profile: "CIPROFILE12"},              // no peer
		&config.CryptoMap{Name: "X", Seq: 3, Peer: "10.1.1.2", Profile: "NOSUCH"}) // no such profile
	c.IPsecProfiles = append(c.IPsecProfiles, &config.IPsecProfile{Name: "EMPTY", Version: "ike-v2"})
	c.CryptoMaps = append(c.CryptoMaps, &config.CryptoMap{Name: "X", Seq: 4, Peer: "10.1.1.3", Profile: "EMPTY"})
	_, warns, n := ipsecSwanctl(c)
	if n != 2 || len(warns) != 4 {
		t.Fatalf("conns=%d warns=%v", n, warns)
	}
}

func TestIPsecSharedProfileGetsUniqueNames(t *testing.T) {
	c := ipsecTestConfig()
	c.CryptoMaps = append(c.CryptoMaps, &config.CryptoMap{Name: "CMAP12", Seq: 30, Peer: "192.168.123.9", Profile: "CIPROFILE12"})
	names := ipsecNames(c)
	if len(names) != 3 || names[0] == names[1] {
		t.Fatalf("names: %v", names)
	}
}

func TestIPsecLocalRemotePSK(t *testing.T) {
	c := ipsecTestConfig()
	p := c.IPsecProfiles[1]
	p.PSK = ""
	p.LocalPSK = &config.PSKID{ID: "r1.example", Data: "LocalKey"}
	p.RemotePSK = &config.PSKID{ID: "r3.example", Data: "RemoteKey"}
	conf, _, _ := ipsecSwanctl(c)
	for _, want := range []string{`id = "r1.example"`, `id = "r3.example"`, `secret = "LocalKey"`, `secret = "RemoteKey"`} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q in:\n%s", want, conf)
		}
	}
}

func TestIPsecServiceWithoutStrongSwan(t *testing.T) {
	if find("charon", "/usr/lib/ipsec", "/usr/libexec/ipsec", "/usr/lib/strongswan", "/usr/libexec/strongswan") != "" {
		t.Skip("strongSwan is installed here")
	}
	env := &Env{P: Paths{Root: t.TempDir(), Run: t.TempDir()}, Sup: NewSupervisor(log.New(io.Discard, "", 0))}
	s := &ipsecSvc{}
	_, err := s.Apply(env, ipsecTestConfig())
	if err == nil || !strings.Contains(err.Error(), "strongSwan is not installed") {
		t.Fatalf("expected a hint about strongSwan, got %v", err)
	}
	c := ipsecTestConfig()
	c.IPsecIKE = false
	warns, err := s.Apply(env, c)
	if err != nil || len(warns) != 0 {
		t.Fatalf("ike disabled: warns=%v err=%v", warns, err)
	}
	if _, err := IPsecShow(env, "sas"); err == nil {
		t.Fatal("show must fail while IKE is not running")
	}
}

func TestFmapCryptoMapRuleIsAccepted(t *testing.T) {
	c := config.New()
	c.CryptoMaps = []*config.CryptoMap{{Name: "CM", Seq: 1}}
	c.Ifaces = []*config.Iface{{Name: "tunnel.12", Tunnel: &config.Tunnel{Src: "1.1.1.1", Dst: "2.2.2.2", Mode: "gre"},
		FMaps: []config.FMBind{{Dir: "in", Name: "F", Seq: 10}}}}
	c.FilterMaps = []*config.FilterMap{{Name: "F", Rules: []*config.FMRule{
		{Seq: 5, Matches: []string{"gre host 1.1.1.1 host 2.2.2.2"}, CryptoMap: "CM", CryptoPeer: "2.2.2.2"},
		{Seq: 10, Matches: []string{"any any any"}, CryptoMap: "NOSUCH", CryptoPeer: "3.3.3.3"},
	}}}
	rules, warns, err := fmapRules(&Env{}, c, "in")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(rules, "\n")
	if !strings.Contains(joined, `iifname "tunnel.12" ip protocol 47 ip saddr 1.1.1.1 ip daddr 2.2.2.2 accept`) {
		t.Fatalf("rules:\n%s", joined)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "crypto-map NOSUCH does not exist") {
		t.Fatalf("warns: %v", warns)
	}
}

// ipsecNames returns the connection names (for tests and diagnostics).
func ipsecNames(c *config.Config) []string {
	conns, _ := ipsecConns(c)
	var out []string
	for _, cn := range conns {
		out = append(out, cn.name)
	}
	sort.Strings(out)
	return out
}
