package cli

import "necorouter/internal/config"

// adminOnly: the command is offered in the privileged mode to admin only.
func adminOnly(n *Node) *Node { return n.For(modePriv, config.RoleAdmin) }

// pingNode: ping in the user mode is for admin only, in the privileged mode for all roles (EcoRouter table 128).
func pingNode(mode string, roles ...string) *Node {
	return K("ping", "Send echo messages", A(KWord, "HOST", "Destination address or name").Chk(hostRe.MatchString).R(cmdPing)).For(mode, roles...)
}

// traceNode: traceroute exists only in the privileged mode, for all roles (EcoRouter table 128).
func traceNode() *Node {
	return K("traceroute", "Trace route to destination", A(KWord, "HOST", "Destination address or name").Chk(hostRe.MatchString).R(cmdTrace)).For(modePriv)
}
