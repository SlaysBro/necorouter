// necorouter — a software router with a CLI in the style of industrial routers.
//
//	necorouter daemon              background process (started from systemd/OpenRC)
//	necorouter shell               CLI for a user already logged in via login/sshd (PAM)
//	necorouter issue sync|restore  banner in /etc/issue (before login)
//	necorouter cleanup             remove the router's accounts and restore the shells (uninstall)
//	necorouter version
//
// The binary is assigned as the login shell (symlink necorouter-shell) to the Linux accounts
// that the router creates; passwords are checked by PAM, permissions are determined by the daemon.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"necorouter/internal/cli"
	"necorouter/internal/daemon"
	"necorouter/internal/svc"
)

func main() {
	name := strings.TrimPrefix(filepath.Base(os.Args[0]), "-")
	args := os.Args[1:]
	mode := ""
	if name == "necorouter-shell" {
		mode = "shell"
	} else if len(args) > 0 {
		mode, args = args[0], args[1:]
	}
	p := svc.DefaultPaths()
	switch mode {
	case "daemon":
		l := log.New(os.Stderr, "necorouter: ", log.LstdFlags)
		if os.Geteuid() != 0 {
			l.Fatal("the daemon must run as root")
		}
		if err := daemon.NewServer(p, l).Run(); err != nil {
			l.Fatal(err)
		}
	case "shell":
		o := daemon.Options{Sock: p.Sock()}
		// ssh host "command": sshd invokes the shell as `shell -c "command"`
		for i := 0; i < len(args); i++ {
			if args[i] == "-c" && i+1 < len(args) {
				for _, ln := range strings.FieldsFunc(args[i+1], func(r rune) bool { return r == ';' || r == '\n' }) {
					o.Cmds = append(o.Cmds, ln)
				}
			}
		}
		if err := daemon.Run(o, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "cleanup":
		if os.Geteuid() != 0 {
			fmt.Fprintln(os.Stderr, "necorouter cleanup: must run as root")
			os.Exit(1)
		}
		for _, l := range svc.Cleanup(p) {
			fmt.Println(l)
		}
	case "issue":
		if os.Geteuid() != 0 {
			fmt.Fprintln(os.Stderr, "necorouter issue: must run as root")
			os.Exit(1)
		}
		var err error
		switch {
		case len(args) == 1 && args[0] == "sync":
			err = svc.SyncIssue(p)
		case len(args) == 1 && args[0] == "restore":
			err = svc.RestoreIssue(p)
		default:
			fmt.Fprintln(os.Stderr, "usage: necorouter issue sync|restore")
			os.Exit(2)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "necorouter issue:", err)
			os.Exit(1)
		}
	case "version", "--version", "-v":
		fmt.Println("necorouter", cli.VersionString())
	default:
		fmt.Fprintf(os.Stderr, "usage: necorouter daemon|shell|issue|cleanup|version\n")
		os.Exit(2)
	}
}
