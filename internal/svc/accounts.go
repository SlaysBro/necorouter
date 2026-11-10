package svc

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"necorouter/internal/config"
)

// Router accounts in Linux: shell necorouter-shell, group necorouter.
// Passwords are checked by PAM (getty/login, sshd); CLI privileges are determined by the daemon from the uid.
// root and accounts with uid < 1000 are not touched.

// ResolveUID determines the user name from the uid of the socket owner. The account must
// actually have the router shell and uid ≥ 1000; root is always admin.
func ResolveUID(uid int) (string, error) {
	if uid == 0 {
		return "root", nil
	}
	name, ok := nameByUID(uid)
	if !ok {
		return "", fmt.Errorf("unknown account")
	}
	a, _ := lookupAccount(name)
	if uid < 1000 || a.Shell != ShellPath {
		return "", fmt.Errorf("this account does not use the router shell")
	}
	return name, nil
}

// SetAccountPassword changes a router account password (not other accounts).
func SetAccountPassword(env *Env, name, pw string) error {
	a, ok := lookupAccount(name)
	if !ok || (!inList(env.St.Users, name) && a.Shell != ShellPath) {
		return fmt.Errorf("no router account %q yet (it is created when the user is configured)", name)
	}
	if err := setPassword(name, pw); err != nil {
		return err
	}
	env.St.DefPw = dropFrom(env.St.DefPw, name)
	return nil
}

// SyncAccounts brings Linux accounts to the configuration's user list.
func SyncAccounts(env *Env, c *config.Config) ([]string, error) {
	var warns []string
	st := env.St
	owned := map[string]bool{}
	for _, n := range st.Users {
		owned[n] = true
	}
	ensureShell()
	want := map[string]bool{}
	for _, u := range c.Users {
		if !userRe.MatchString(u.Name) {
			warns = append(warns, "user "+u.Name+": name not usable as a login (use lowercase letters, digits, _ or -)")
			continue
		}
		acc, exists := lookupAccount(u.Name)
		if exists && !owned[u.Name] && acc.UID < 1000 {
			warns = append(warns, "user "+u.Name+": refusing to take over a system account")
			continue
		}
		want[u.Name] = true
		switch {
		case !exists:
			var err error
			if have("useradd") {
				_, _ = run("groupadd", "-f", routerGroup)
				_, err = run("useradd", "-m", "-s", ShellPath, "-g", routerGroup, u.Name)
			} else {
				_, _ = run("addgroup", "-S", routerGroup)
				_, err = run("adduser", "-D", "-s", ShellPath, "-G", routerGroup, u.Name)
			}
			if err != nil {
				warns = append(warns, err.Error())
				continue
			}
			owned[u.Name] = true
			pw := ""
			if u.BuiltinAdmin {
				pw = DefaultAdminPassword
				st.DefPw = append(dropFrom(st.DefPw, u.Name), u.Name)
			}
			if pw == "" {
				warns = append(warns, "user "+u.Name+" has no password yet: set it with 'password' under 'username "+u.Name+"'")
			} else if err := setPassword(u.Name, pw); err != nil {
				warns = append(warns, "user "+u.Name+": "+err.Error())
			}
		case !owned[u.Name] && acc.Shell != ShellPath && have("usermod"):
			// existing account: give it the router CLI as shell and the group;
			// remember the previous shell to restore it when removed from the configuration
			if _, ok := st.Taken[u.Name]; !ok {
				st.Taken[u.Name] = acc.Shell
			}
			_, _ = run("groupadd", "-f", routerGroup)
			_, _ = run("usermod", "-s", ShellPath, u.Name)
			joinRouterGroup(u.Name)
		}
	}
	for n := range owned {
		if !want[n] {
			deleteAccount(n)
			delete(owned, n)
			st.DefPw = dropFrom(st.DefPw, n)
		}
	}
	st.Users = st.Users[:0]
	for n := range owned {
		st.Users = append(st.Users, n)
	}
	sort.Strings(st.Users)
	// taken accounts that are no longer in the configuration: restore the shell
	for n := range st.Taken {
		if !want[n] {
			restoreTaken(st, n)
		}
	}
	return warns, nil
}

func inList(l []string, s string) bool {
	for _, v := range l {
		if v == s {
			return true
		}
	}
	return false
}

func dropFrom(l []string, s string) []string {
	var r []string
	for _, v := range l {
		if v != s {
			r = append(r, v)
		}
	}
	return r
}

// restoreTaken gives a taken account its previous shell back and removes it from
// the router group. If the shell was already changed manually, it is left alone.
func restoreTaken(st *State, name string) {
	orig := st.Taken[name]
	delete(st.Taken, name)
	a, ok := lookupAccount(name)
	if !ok {
		return
	}
	if a.Shell == ShellPath {
		if orig == "" || orig == ShellPath {
			orig = fallbackShell()
		}
		restoreShell(name, orig)
	}
	leaveRouterGroup(name)
}

func restoreShell(name, shell string) {
	if have("usermod") {
		_, _ = run("usermod", "-s", shell, name)
	} else if have("chsh") {
		_, _ = run("chsh", "-s", shell, name)
	}
}

func leaveRouterGroup(name string) {
	if have("gpasswd") {
		_, _ = run("gpasswd", "-d", name, routerGroup)
	} else if have("delgroup") {
		_, _ = run("delgroup", name, routerGroup)
	}
}

func fallbackShell() string {
	b, _ := os.ReadFile(shellsFile)
	for _, s := range []string{"/bin/bash", "/bin/sh"} {
		if strings.Contains("\n"+string(b), "\n"+s+"\n") {
			return s
		}
	}
	return "/bin/sh"
}

type acct struct {
	Name  string
	UID   int
	Shell string
}

func listAccounts() []acct {
	var l []acct
	b, _ := os.ReadFile(passwdFile)
	for _, ln := range strings.Split(string(b), "\n") {
		f := strings.Split(ln, ":")
		if len(f) > 6 {
			id, _ := strconv.Atoi(f[2])
			l = append(l, acct{f[0], id, f[6]})
		}
	}
	return l
}

func nameByUID(uid int) (string, bool) {
	for _, a := range listAccounts() {
		if a.UID == uid {
			return a.Name, true
		}
	}
	return "", false
}

// Cleanup is removal on uninstall: accounts created by the router are deleted,
// taken ones get their previous shell back.
func Cleanup(p Paths) []string {
	st := LoadState(p.State())
	var done []string
	gone := map[string]bool{}
	for _, n := range st.Users {
		gone[n] = true
		deleteAccount(n)
		done = append(done, "deleted account "+n)
	}
	st.Users = nil
	for n := range st.Taken {
		restoreTaken(st, n)
		done = append(done, "restored shell of "+n)
	}
	st.DefPw = nil
	st.Save()
	return done
}
