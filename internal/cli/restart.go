package cli

// restart PROCESS: restart one router service with the current configuration.
// The list of processes is the services that can be restarted individually.
var restartProcs = []struct{ name, help string }{
	{"dhcp", "DHCP server"},
	{"dns", "DNS resolver"},
	{"filter-map", "Filter-map lists (nftables)"},
	{"ipsec", "IPsec (IKE daemon)"},
	{"nat", "NAT translation"},
	{"ntp", "NTP client"},
	{"ospf", "OSPF routing process"},
	{"security", "Security profiles (nftables)"},
}

// restartNode: restart PROCESS — restart a service (exec mode with privileges).
func restartNode() *Node {
	kids := make([]*Node, 0, len(restartProcs))
	for _, p := range restartProcs {
		name := p.name
		kids = append(kids, K(name, p.help).R(func(s *Session, _ []string) error {
			warns, err := s.R.Mgr.Restart(name, s.cfg())
			for _, w := range warns {
				s.Printf("%% %s\n", w)
			}
			if err != nil {
				return err
			}
			s.journal("restart %s", name)
			return nil
		}))
	}
	return K("restart", "Restart process", kids...)
}
