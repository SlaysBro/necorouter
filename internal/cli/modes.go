package cli

// The modes of the command tree. A mode is named by one of these constants; a misspelt mode name is a
// compile error, not a silently empty mode. exec, priv and config are the modes a user moves between with
// enable, disable and configure; every other mode is a sub-mode of config that is entered by naming an
// object (interface, port, router, ...), and its commands are the ones of the object plus the switchers.
const (
	modeExec   = "exec"   // user mode (>)
	modePriv   = "priv"   // privileged mode (#)
	modeConfig = "config" // configuration mode ((config)#)

	modeIf        = "if"        // interface NAME
	modePort      = "port"      // port NAME
	modeSI        = "si"        // service-instance NAME (inside a port)
	modeQoSPolicy = "qospolicy" // service-policy NAME
	modeOSPF      = "ospf"      // router ospf N
	modeUser      = "user"      // username NAME
	modeDHCP      = "dhcp"      // dhcp-server N
	modeSecProf   = "secprof"   // security-profile NAME
	modeFMap      = "fmap"      // filter-map ipv4 NAME SEQ
	modeIPProf    = "ipprof"    // crypto-ipsec profile NAME
	modeIKEP1     = "ikep1"     // crypto-ike profile phase 1
	modeIKEP2     = "ikep2"     // crypto-ike profile phase 2
	modeCMap      = "cmap"      // crypto-map NAME SEQ
	modeRMap      = "rmap"      // route-map NAME SEQ
)
