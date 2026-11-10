package cli

// buildSubModes fills the sub-modes of the configuration (interface, port, router, ...). Each one lists its own
// commands, then the commands that select another object (switchers); the IPsec, route-map and similar modes
// also list every global configuration command, as in the examples.
func buildSubModes(t map[string][]*Node, nav nav, g cfgObjects) {
	// IPsec modes: their own commands plus all global-mode commands (as in the examples: crypto-map comes right after the profile)
	t[modeIPProf] = append(ipsecProfileTree(), t[modeConfig]...)
	t[modeIKEP1] = append(ikePhase1Tree(), t[modeConfig]...)
	t[modeIKEP2] = append(ikePhase2Tree(), t[modeConfig]...)
	t[modeCMap] = append(cryptoMapTree(), t[modeConfig]...)
	t[modeRMap] = append(rmapTree(), t[modeConfig]...)
	t[modeSecProf] = append(secProfileTree(), g.switchers...)
	t[modeFMap] = append(fmapModeTree(), append([]*Node{g.filterMap}, g.switchers...)...)

	t[modeIf] = append(append(ifaceTree(), fmapIfaceNode()), g.switchers...)
	t[modePort] = append(portTree(), g.switchers...)
	t[modeSI] = append(append(append(siTree(), portTree()[1]), bindServicePolicyNode()), g.switchers...)
	t[modeQoSPolicy] = append(servicePolicyTree(), g.switchers...)
	t[modeOSPF] = append(ospfTree(), g.switchers...)
	t[modeUser] = append(userTree(), g.switchers...)
	t[modeDHCP] = append(dhcpTree(), g.switchers...)
}
