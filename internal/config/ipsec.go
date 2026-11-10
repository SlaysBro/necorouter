package config

import (
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

// IPsec: profiles (crypto-ipsec profile NAME ike-v1|ike-v2), crypto maps (crypto-map NAME SEQ)
// and the binding of filter-map rules to maps (set crypto-map NAME peer ADDR).
//
//	crypto-ipsec ike enable
//	crypto-ipsec profile NAME ike-v1|ike-v2   (mode, nat-traversal, ike-phase1, ike-phase2)
//	crypto-map NAME SEQ                       (match peer ADDR, set crypto-ipsec profile NAME)

// PSKID is a pre-shared key with an identifier (local-psk / remote-psk).
type PSKID struct {
	ID   string `json:"id"`
	Data string `json:"data"`
}

// IPsecProfile is an IPsec profile. Version: ike-v1 | ike-v2.
type IPsecProfile struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Mode    string `json:"mode,omitempty"` // tunnel | transport (empty means tunnel)
	NATT    bool   `json:"natt,omitempty"` // nat-traversal

	P1Proposal string `json:"p1_proposal,omitempty"`
	PSK        string `json:"psk,omitempty"` // auth pre-shared-key
	LocalPSK   *PSKID `json:"local_psk,omitempty"`
	RemotePSK  *PSKID `json:"remote_psk,omitempty"`

	Protocol   string `json:"protocol,omitempty"` // esp
	P2Proposal string `json:"p2_proposal,omitempty"`
	LocalTS    string `json:"local_ts,omitempty"`
	RemoteTS   string `json:"remote_ts,omitempty"`
	Rekey      int    `json:"rekey,omitempty"` // seconds; 0 means default
}

// CryptoMap is one crypto-map entry: name + sequence number.
type CryptoMap struct {
	Name    string `json:"name"`
	Seq     int    `json:"seq"`
	Peer    string `json:"peer,omitempty"`
	Profile string `json:"profile,omitempty"`
}

func (c *Config) FindIPsecProfile(name string) *IPsecProfile {
	for _, p := range c.IPsecProfiles {
		if p.Name == name {
			return p
		}
	}
	return nil
}

func (c *Config) FindCryptoMap(name string, seq int) *CryptoMap {
	for _, m := range c.CryptoMaps {
		if m.Name == name && m.Seq == seq {
			return m
		}
	}
	return nil
}

// CryptoMapNamed reports whether there is at least one map entry with this name.
func (c *Config) CryptoMapNamed(name string) bool {
	for _, m := range c.CryptoMaps {
		if m.Name == name {
			return true
		}
	}
	return false
}

// GetCryptoMap returns the map entry, creating it if necessary.
func (c *Config) GetCryptoMap(name string, seq int) *CryptoMap {
	if m := c.FindCryptoMap(name, seq); m != nil {
		return m
	}
	m := &CryptoMap{Name: name, Seq: seq}
	c.CryptoMaps = append(c.CryptoMaps, m)
	sort.SliceStable(c.CryptoMaps, func(i, j int) bool {
		a, b := c.CryptoMaps[i], c.CryptoMaps[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Seq < b.Seq
	})
	return m
}

// GetIPsecProfile returns the profile, creating it if necessary. The IKE version of an existing profile cannot be changed.
func (c *Config) GetIPsecProfile(name, version string) (*IPsecProfile, error) {
	if p := c.FindIPsecProfile(name); p != nil {
		if p.Version != version {
			return nil, fmt.Errorf("profile %s is %s: remove it first (no crypto-ipsec profile %s)", name, p.Version, name)
		}
		return p, nil
	}
	p := &IPsecProfile{Name: name, Version: version}
	c.IPsecProfiles = append(c.IPsecProfiles, p)
	sort.Slice(c.IPsecProfiles, func(i, j int) bool { return c.IPsecProfiles[i].Name < c.IPsecProfiles[j].Name })
	return p, nil
}

// proposalRe: a strongSwan proposal string, for example aes256-sha256-modp2048 or aes256-sha256.
var proposalRe = regexp.MustCompile(`(?i)^[a-z0-9_]+(-[a-z0-9_]+)*$`)

// ValidProposal reports whether the proposal string has a valid form (the set of algorithms is checked by the daemon itself on load).
func ValidProposal(s string) bool { return len(s) <= 128 && proposalRe.MatchString(s) }

// ValidTS checks a traffic selector: one or more (comma-separated) host or subnet addresses.
func ValidTS(s string) bool {
	if s == "" {
		return false
	}
	for _, part := range strings.Split(s, ",") {
		if _, err := NormTS1(part); err != nil {
			return false
		}
	}
	return true
}

// NormTS1 normalizes one selector element to A.B.C.D/M (a host becomes /32).
func NormTS1(p string) (string, error) {
	p = strings.TrimSpace(p)
	if strings.Contains(p, "/") {
		pf, err := netip.ParsePrefix(p)
		if err != nil || !pf.Addr().Is4() {
			return "", fmt.Errorf("invalid traffic selector %q", p)
		}
		return pf.Masked().String(), nil
	}
	a, err := netip.ParseAddr(p)
	if err != nil || !a.Is4() {
		return "", fmt.Errorf("invalid traffic selector %q", p)
	}
	return a.String() + "/32", nil
}

// TSList converts a selector from the configuration into a list of A.B.C.D/M elements.
func TSList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if n, err := NormTS1(part); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// renderIPsec prints the IPsec part of running-config (before filter-map: the rules refer to the maps).
func (c *Config) renderIPsec(w func(string, ...any), sep func(), sec func(string) string) {
	if c.IPsecIKE {
		w("crypto-ipsec ike enable")
		sep()
	}
	for _, p := range c.IPsecProfiles {
		w("crypto-ipsec profile %s %s", p.Name, p.Version)
		if p.NATT {
			w(" nat-traversal")
		}
		if p.Mode != "" {
			w(" mode %s", p.Mode)
		}
		if p.P1Proposal != "" || p.PSK != "" || p.LocalPSK != nil || p.RemotePSK != nil {
			w(" ike-phase1")
			if p.P1Proposal != "" {
				w("  proposal %s", p.P1Proposal)
			}
			if p.PSK != "" {
				w("  auth pre-shared-key %s", sec(p.PSK))
			}
			if p.LocalPSK != nil {
				w("  local-psk id %s data %s", p.LocalPSK.ID, sec(p.LocalPSK.Data))
			}
			if p.RemotePSK != nil {
				w("  remote-psk id %s data %s", p.RemotePSK.ID, sec(p.RemotePSK.Data))
			}
		}
		if p.Protocol != "" || p.P2Proposal != "" || p.LocalTS != "" || p.RemoteTS != "" || p.Rekey > 0 {
			w(" ike-phase2")
			if p.Protocol != "" {
				w("  protocol %s", p.Protocol)
			}
			if p.P2Proposal != "" {
				w("  proposal %s", p.P2Proposal)
			}
			if p.LocalTS != "" {
				w("  local-ts %s", p.LocalTS)
			}
			if p.RemoteTS != "" {
				w("  remote-ts %s", p.RemoteTS)
			}
			if p.Rekey > 0 {
				w("  rekey-time %d", p.Rekey)
			}
		}
		sep()
	}
	for _, m := range c.CryptoMaps {
		w("crypto-map %s %d", m.Name, m.Seq)
		if m.Peer != "" {
			w(" match peer %s", m.Peer)
		}
		if m.Profile != "" {
			w(" set crypto-ipsec profile %s", m.Profile)
		}
		sep()
	}
}
