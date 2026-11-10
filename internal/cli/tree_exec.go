package cli

import (
	"fmt"
	"strconv"

	"necorouter/internal/config"
)

// buildUserModes fills the user mode (>) and the privileged mode (#). Who may use a command is declared on
// the command itself (For); show is built once and offers each command in the modes it belongs to.
func buildUserModes(t map[string][]*Node, nav nav) {
	exitN, logout, quit := nav.exitN, nav.logout, nav.quit
	// disable in the user mode does nothing; help describes the '?' and Tab help (offered to every role)
	disableUser := K("disable", "Turn off privileged mode command").R(func(*Session, []string) error { return nil })
	helpN := K("help", "Description of the interactive help system").R(cmdHelp)
	// the show word itself is offered only to the roles that have some show command (not helpdesk)
	show := buildShow()

	enable := K("enable", "Turn on privileged commands").R(func(s *Session, _ []string) error {
		if err := s.needRole(config.RoleAdmin, config.RoleNOC, config.RoleHelpdesk); err != nil { // without a role, privileged mode is closed
			return err
		}
		// The enable password is asked of everyone except local root (who can modify the files anyway).
		if h := s.cfg().EnablePass; h != "" && s.User != "root" {
			s.askSecret("Password: ", func(s *Session, in string) error {
				if !checkEnablePass(s.cfg().EnablePass, in) {
					return fmt.Errorf("Access denied")
				}
				s.frames = []frame{{kind: modePriv}}
				return nil
			})
			return nil
		}
		s.frames = []frame{{kind: modePriv}}
		return nil
	})
	disable := K("disable", "Turn off privileged commands").R(func(s *Session, _ []string) error {
		s.frames = []frame{{kind: modeExec}}
		return nil
	})
	configure := K("configure", "Enter configuration mode",
		K("terminal", "Configure from the terminal").R(func(s *Session, _ []string) error {
			s.push(modeConfig, "")
			return nil
		}))
	configure.R(func(s *Session, _ []string) error { // "configure" without an argument = configure terminal
		s.push(modeConfig, "")
		return nil
	})
	save := func(s *Session, _ []string) error {
		s.Printf("Building configuration...\n")
		if err := s.R.Save(); err != nil {
			return err
		}
		s.journal("write")
		s.Printf("[OK]\n")
		return nil
	}
	write := K("write", "Write running configuration to memory", K("memory", "Write to NV memory").R(save)).R(save)
	write.Kids = append(write.Kids, writeKids()...)
	cp := K("copy", "Copy from one file to another",
		K("running-config", "Copy from current system configuration",
			K("startup-config", "Copy to startup configuration").R(save)))
	cp.Kids = append(cp.Kids, K("empty-config", "Reset configuration to factory defaults (applied after reload)",
		K("startup-config", "Write factory defaults to startup-config").R(cmdEmptyConfig),
		K("running-config", "Replace the running configuration with factory defaults and apply it now").R(cmdEmptyConfigRunning)))
	cp.Kids = append(cp.Kids, copyKids()...)

	clearN := K("clear", "Reset functions",
		K("dhcp-server", "Clear DHCP server state",
			K("clients", "Forget all leased addresses; clients get new ones on their next request").R(cmdClearDHCPClients))).For(modePriv, config.RoleAdmin)
	terminal := K("terminal", "Set terminal line parameters",
		monitorNode().For(modeExec, config.RoleAdmin).For(modePriv, config.RoleAdmin), // journal lines on the screen: admin only
		K("length", "Set number of lines on a screen",
			A(KInt, "<0-512>", "Lines per page (0 disables paging, default: window height)").Rng(0, 512).R(func(s *Session, a []string) error {
				n, _ := strconv.Atoi(a[len(a)-1])
				if n == 0 {
					n = -1
				}
				s.PageLen = n
				return nil
			})))
	mge := K("mge", "Choo-choo! An animated ASCII steam train (Ctrl-C to stop)",
		A(KRest, "TEXT", `Text shown after the train passes, in matching double or single quotes, 1-64 characters (default: Have a nice day.)`).R(cmdTrain),
	).R(cmdTrain)
	t[modeExec] = []*Node{enable, pingNode(modeExec, config.RoleAdmin), show, terminal, mge, disableUser, helpN, exitN, logout, quit}
	t[modePriv] = []*Node{adminOnly(disable), adminOnly(configure), adminOnly(write), adminOnly(cp), adminOnly(deleteNode()), adminOnly(reloadNode()), adminOnly(poweroffNode()), adminOnly(updateNode()), adminOnly(restartNode()), pingNode(modePriv, config.RoleAdmin, config.RoleNOC, config.RoleHelpdesk), traceNode(), show, clearN, terminal, mge, helpN, exitN, logout, quit}
}
