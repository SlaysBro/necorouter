package cli

import (
	"fmt"
	"sort"
	"strings"

	"necorouter/internal/config"
)

// IPsec: crypto-ipsec ike enable, crypto-ipsec profile NAME ike-v1|ike-v2, crypto-map NAME SEQ,
// modes of the profile (ipprof), IKE phases (ikep1, ikep2) and the crypto map (cmap).

func ipsecProfileNames(s *Session) []string {
	var o []string
	for _, p := range s.cfg().IPsecProfiles {
		o = append(o, p.Name)
	}
	return o
}

func cryptoMapNames(s *Session) []string {
	seen := map[string]bool{}
	var o []string
	for _, m := range s.cfg().CryptoMaps {
		if !seen[m.Name] {
			seen[m.Name] = true
			o = append(o, m.Name)
		}
	}
	sort.Strings(o)
	return o
}

func ipprofCur(s *Session) (*config.IPsecProfile, error) {
	p := s.cfg().FindIPsecProfile(s.top().obj)
	if p == nil {
		return nil, fmt.Errorf("profile %s no longer exists", s.top().obj)
	}
	return p, nil
}

// pset wraps a change of the current profile.
func pset(f func(p *config.IPsecProfile, v []string) error) Handler {
	return func(s *Session, v []string) error {
		p, err := ipprofCur(s)
		if err != nil {
			return err
		}
		return f(p, v)
	}
}

func cmapCur(s *Session) (*config.CryptoMap, error) {
	f := strings.Fields(s.top().obj)
	if len(f) == 2 {
		var seq int
		fmt.Sscan(f[1], &seq)
		if m := s.cfg().FindCryptoMap(f[0], seq); m != nil {
			return m, nil
		}
	}
	return nil, fmt.Errorf("crypto-map entry no longer exists")
}

// cryptoMapUsers — filter-map rules that reference the map with that name.
func cryptoMapUsers(c *config.Config, name string) []string {
	var out []string
	for _, f := range c.FilterMaps {
		for _, r := range f.Rules {
			if r.CryptoMap == name {
				out = append(out, fmt.Sprintf("%s %d", f.Name, r.Seq))
			}
		}
	}
	return out
}

// cryptoNodes — global configuration mode commands.
func cryptoNodes() []*Node {
	enterProfile := func(version string) Handler {
		return func(s *Session, v []string) error {
			p, err := s.cfg().GetIPsecProfile(v[0], version)
			if err != nil {
				return err
			}
			s.enter("ipprof", p.Name)
			return nil
		}
	}
	delProfile := func(s *Session, v []string) error {
		c := s.cfg()
		if c.FindIPsecProfile(v[0]) == nil {
			return fmt.Errorf("profile %s not found", v[0])
		}
		for _, m := range c.CryptoMaps {
			if m.Profile == v[0] {
				return fmt.Errorf("profile %s is used by crypto-map %s %d: remove it first", v[0], m.Name, m.Seq)
			}
		}
		out := c.IPsecProfiles[:0:0]
		for _, p := range c.IPsecProfiles {
			if p.Name != v[0] {
				out = append(out, p)
			}
		}
		c.IPsecProfiles = out
		return nil
	}
	ipsec := K("crypto-ipsec", "IPsec configuration",
		K("ike", "Internet Key Exchange (IKE)",
			K("enable", "Enable the IKE daemon").
				R(func(s *Session, _ []string) error { s.cfg().IPsecIKE = true; return nil }).
				N(func(s *Session, _ []string) error { s.cfg().IPsecIKE = false; return nil })),
		K("profile", "Create or edit an IPsec profile",
			A(KWord, "NAME", "Profile name",
				K("ike-v1", "Dynamic tunnel, IKE version 1").R(enterProfile("ike-v1")).N(delProfile),
				K("ike-v2", "Dynamic tunnel, IKE version 2").R(enterProfile("ike-v2")).N(delProfile)).
				Chk(nameOK).Cmp(ipsecProfileNames).N(delProfile)))

	enterMap := func(s *Session, v []string) error {
		var seq int
		fmt.Sscan(v[1], &seq)
		s.cfg().GetCryptoMap(v[0], seq)
		s.enter("cmap", v[0]+" "+v[1])
		return nil
	}
	dropMaps := func(s *Session, name string, seq int) error { // seq < 0 — all map entries
		c := s.cfg()
		var out []*config.CryptoMap
		found, left := false, false
		for _, m := range c.CryptoMaps {
			switch {
			case m.Name == name && (seq < 0 || m.Seq == seq):
				found = true
			default:
				if m.Name == name {
					left = true
				}
				out = append(out, m)
			}
		}
		if !found {
			return fmt.Errorf("crypto-map %s not found", name)
		}
		if !left {
			if u := cryptoMapUsers(c, name); len(u) > 0 {
				return fmt.Errorf("crypto-map %s is used by filter-map %s: remove it first", name, strings.Join(u, ", "))
			}
		}
		c.CryptoMaps = out
		return nil
	}
	cmap := K("crypto-map", "Create or edit a crypto map entry",
		A(KWord, "NAME", "Crypto map name (letters, digits, _ . : / -)",
			A(KInt, "<1-65535>", "Entry number (a lower number is processed first)").Rng(1, 65535).R(enterMap).
				N(func(s *Session, v []string) error {
					var seq int
					fmt.Sscan(v[1], &seq)
					return dropMaps(s, v[0], seq)
				})).
			Chk(nameOK).Cmp(cryptoMapNames).
			N(func(s *Session, v []string) error { return dropMaps(s, v[0], -1) }))
	return []*Node{ipsec, cmap}
}

// ipsecProfileTree — profile mode: crypto-ipsec profile NAME ike-v1|ike-v2.
func ipsecProfileTree() []*Node {
	return []*Node{
		K("mode", "Tunnel or transport mode",
			K("tunnel", "The whole packet is protected").R(pset(func(p *config.IPsecProfile, _ []string) error { p.Mode = "tunnel"; return nil })),
			K("transport", "Only the payload is protected").R(pset(func(p *config.IPsecProfile, _ []string) error { p.Mode = "transport"; return nil }))).
			N(pset(func(p *config.IPsecProfile, _ []string) error { p.Mode = ""; return nil })),
		K("nat-traversal", "Enable NAT traversal (ESP in UDP, port 4500)").
			R(pset(func(p *config.IPsecProfile, _ []string) error { p.NATT = true; return nil })).
			N(pset(func(p *config.IPsecProfile, _ []string) error { p.NATT = false; return nil })),
		ikePhase1Node(),
		ikePhase2Node(),
	}
}

func ikePhase1Node() *Node {
	return K("ike-phase1", "IKE phase 1 parameters (secure connection to the peer)").R(func(s *Session, _ []string) error {
		p, err := ipprofCur(s)
		if err != nil {
			return err
		}
		s.enter("ikep1", p.Name)
		return nil
	})
}

func ikePhase2Node() *Node {
	return K("ike-phase2", "IKE phase 2 parameters (protected traffic)").R(func(s *Session, _ []string) error {
		p, err := ipprofCur(s)
		if err != nil {
			return err
		}
		s.enter("ikep2", p.Name)
		return nil
	})
}

// ikePhase1Tree — ike-phase1 mode.
func ikePhase1Tree() []*Node {
	psk := func(local bool) Handler {
		return pset(func(p *config.IPsecProfile, v []string) error {
			if local {
				p.LocalPSK = &config.PSKID{ID: v[0], Data: v[1]}
			} else {
				p.RemotePSK = &config.PSKID{ID: v[0], Data: v[1]}
			}
			return nil
		})
	}
	pskNode := func(word, help string, local bool) *Node {
		return K(word, help,
			K("id", "Identifier of the router (IP address, domain name, e-mail or any string)",
				A(KWord, "ID", "IP address, domain name, e-mail or any string without spaces",
					K("data", "Key",
						A(KWord, "KEY", "Pre-shared key").R(psk(local))).Sec()))).
			N(pset(func(p *config.IPsecProfile, _ []string) error {
				if local {
					p.LocalPSK = nil
				} else {
					p.RemotePSK = nil
				}
				return nil
			}))
	}
	return []*Node{
		K("proposal", "Proposal: encryption-integrity-dh group, e.g. aes256-sha256-modp2048",
			A(KWord, "PROPOSAL", "Algorithms joined by '-' (letters, digits, '_'; up to 128 characters), e.g. aes256-sha256-modp2048").Chk(config.ValidProposal).
				R(pset(func(p *config.IPsecProfile, v []string) error { p.P1Proposal = v[0]; return nil }))).
			N(pset(func(p *config.IPsecProfile, _ []string) error { p.P1Proposal = ""; return nil })),
		K("auth", "Authentication of the peer",
			K("pre-shared-key", "Pre-shared key (at least 20 random characters are recommended)",
				A(KWord, "KEY", "Pre-shared key").R(pset(func(p *config.IPsecProfile, v []string) error { p.PSK = v[0]; return nil }))).Sec().
				N(pset(func(p *config.IPsecProfile, _ []string) error { p.PSK = ""; return nil }))),
		pskNode("local-psk", "Pre-shared key with the identifier of the local router", true),
		pskNode("remote-psk", "Pre-shared key with the identifier of the remote router", false),
		ikePhase2Node(),
	}
}

// ikePhase2Tree — ike-phase2 mode.
func ikePhase2Tree() []*Node {
	ts := func(word, help string, local bool) *Node {
		set := func(p *config.IPsecProfile, v string) {
			if local {
				p.LocalTS = v
			} else {
				p.RemoteTS = v
			}
		}
		return K(word, help,
			A(KWord, "ADDRESS[,ADDRESS...]", "Host addresses or subnets A.B.C.D/M, comma separated").Chk(config.ValidTS).
				R(pset(func(p *config.IPsecProfile, v []string) error { set(p, v[0]); return nil }))).
			N(pset(func(p *config.IPsecProfile, _ []string) error { set(p, ""); return nil }))
	}
	return []*Node{
		K("protocol", "Protection protocol",
			K("esp", "Encapsulating Security Payload").R(pset(func(p *config.IPsecProfile, _ []string) error { p.Protocol = "esp"; return nil }))).
			N(pset(func(p *config.IPsecProfile, _ []string) error { p.Protocol = ""; return nil })),
		K("proposal", "Proposal: encryption-integrity, e.g. aes256-sha256",
			A(KWord, "PROPOSAL", "Algorithms joined by '-' (letters, digits, '_'; up to 128 characters), e.g. aes256-sha256").Chk(config.ValidProposal).
				R(pset(func(p *config.IPsecProfile, v []string) error { p.P2Proposal = v[0]; return nil }))).
			N(pset(func(p *config.IPsecProfile, _ []string) error { p.P2Proposal = ""; return nil })),
		ts("local-ts", "Traffic selector: local addresses protected by the tunnel", true),
		ts("remote-ts", "Traffic selector: remote addresses protected by the tunnel", false),
		K("rekey-time", "Time in seconds after which the keys are renewed",
			A(KInt, "<0-75600>", "Seconds (0 — default)").Rng(0, 75600).
				R(pset(func(p *config.IPsecProfile, v []string) error { fmt.Sscan(v[0], &p.Rekey); return nil }))).
			N(pset(func(p *config.IPsecProfile, _ []string) error { p.Rekey = 0; return nil })),
		ikePhase1Node(),
	}
}

// cryptoMapTree — crypto-map entry mode: match peer, set crypto-ipsec profile.
func cryptoMapTree() []*Node {
	cur := func(f func(m *config.CryptoMap, v []string)) Handler {
		return func(s *Session, v []string) error {
			m, err := cmapCur(s)
			if err != nil {
				return err
			}
			f(m, v)
			return nil
		}
	}
	return []*Node{
		K("match", "Match condition of the entry",
			K("peer", "IPv4 address of the remote router",
				A(KIP, "A.B.C.D", "Peer address").R(cur(func(m *config.CryptoMap, v []string) { m.Peer = v[0] })))).
			N(cur(func(m *config.CryptoMap, _ []string) { m.Peer = "" })),
		K("set", "Action of the entry",
			K("crypto-ipsec", "IPsec parameters",
				K("profile", "IPsec profile to apply to the peer",
					A(KWord, "NAME", "Profile name (letters, digits, _ . : / -)").Chk(nameOK).Cmp(ipsecProfileNames).
						R(cur(func(m *config.CryptoMap, v []string) { m.Profile = v[0] }))))).
			N(cur(func(m *config.CryptoMap, _ []string) { m.Profile = "" })),
	}
}

// fmapCryptoNode: set crypto-map NAME peer ADDRESS in filter-map mode.
func fmapCryptoNode() *Node {
	return K("crypto-map", "Process matching packets with IPsec (crypto-map)",
		A(KWord, "NAME", "Crypto map name (letters, digits, _ . : / -)",
			K("peer", "IPv4 address of the peer in the tunnel",
				A(KIP, "A.B.C.D", "Peer address").R(func(s *Session, v []string) error {
					_, r, err := fmapCur(s)
					if err != nil {
						return err
					}
					r.Action, r.CryptoMap, r.CryptoPeer = "", v[0], v[1]
					return nil
				}))).Chk(nameOK).Cmp(cryptoMapNames))
}
