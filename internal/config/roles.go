package config

// The roles of the CLI users. A command is allowed to a role by the name of the constant, so a typo
// is a compile error, not a command that nobody can use.
const (
	RoleAdmin    = "admin"    // everything
	RoleNOC      = "noc"      // auditor: status and diagnostics, no configuration, no secrets
	RoleHelpdesk = "helpdesk" // ping, traceroute and the privileged mode without show commands

	// BuiltinAdminName is the name of the built-in admin account (it cannot be removed).
	BuiltinAdminName = "admin"
)

// Roles lists every role, in the order shown to the user.
var Roles = []string{RoleAdmin, RoleNOC, RoleHelpdesk}
