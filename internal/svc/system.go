package svc

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"necorouter/internal/config"
)

// ShellPath is the shell for router users (a symlink to the binary).
const ShellPath = "/usr/local/bin/necorouter-shell"

// ---------- hostname ----------

type hostSvc struct{ last string }

// hostsMark marks the /etc/hosts line maintained by the router; other lines are left alone.
const hostsMark = "# necorouter"

// updateHosts puts the router name into /etc/hosts: without an entry, sudo and many programs
// print "unable to resolve host".
func updateHosts(env *Env, name string) error {
	path := env.P.Root + "/etc/hosts"
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var out []string
	if text := strings.TrimRight(string(b), "\n"); text != "" {
		for _, ln := range strings.Split(text, "\n") {
			if !strings.HasSuffix(strings.TrimSpace(ln), hostsMark) {
				out = append(out, ln)
			}
		}
	}
	entry := "127.0.1.1\t" + name
	if i := strings.IndexByte(name, '.'); i > 0 {
		entry += " " + name[:i]
	}
	out = append(out, entry+" "+hostsMark)
	want := []byte(strings.Join(out, "\n") + "\n")
	if string(want) == string(b) {
		return nil
	}
	if err := os.MkdirAll(env.P.Root+"/etc", 0o755); err != nil {
		return err
	}
	return writeFileAtomic(path, want, 0o644)
}

func (*hostSvc) Name() string { return "hostname" }
func (h *hostSvc) Apply(env *Env, c *config.Config) ([]string, error) {
	name := c.Hostname
	if c.Domain != "" && !strings.Contains(name, ".") {
		name = name + "." + c.Domain
	}
	if err := updateHosts(env, name); err != nil {
		return []string{"hosts: " + err.Error()}, nil
	}
	if name == h.last {
		return nil, nil
	}
	if err := syscall.Sethostname([]byte(name)); err != nil {
		return []string{"hostname: " + err.Error()}, nil
	}
	_ = os.WriteFile("/etc/hostname", []byte(name+"\n"), 0o644)
	h.last = name
	return nil, nil
}
func (*hostSvc) Stop(*Env) error      { return nil }
func (h *hostSvc) Status(*Env) string { return "ok " + h.last }

// ---------- timezone ----------

type tzSvc struct{ last string }

var tzRe = regexp.MustCompile(`^(?i)(?:utc|gmt)([+-])(\d{1,2})(?::(\d{2}))?$`)

// zoneRe: a name from the tzdata database (Europe/Moscow, America/Argentina/Buenos_Aires, Etc/GMT+3).
// It has no dots at all, so ".." and absolute paths are impossible.
var zoneRe = regexp.MustCompile(`^[A-Za-z0-9_+-]+(/[A-Za-z0-9_+-]+){0,2}$`)

// ZoneFile turns "utc+3" into Etc/GMT-3 (the sign is inverted in POSIX zones).
func ZoneFile(tz string) (string, error) {
	if m := tzRe.FindStringSubmatch(tz); m != nil {
		h, _ := strconv.Atoi(m[2])
		if h > 14 || (m[3] != "" && m[3] != "00") {
			return "", fmt.Errorf("unsupported offset")
		}
		if h == 0 {
			return "Etc/UTC", nil
		}
		sign := "-"
		if m[1] == "-" {
			sign = "+"
		}
		return fmt.Sprintf("Etc/GMT%s%d", sign, h), nil
	}
	if strings.EqualFold(tz, "utc") || strings.EqualFold(tz, "gmt") {
		return "Etc/UTC", nil
	}
	if !zoneRe.MatchString(tz) {
		return "", fmt.Errorf("bad zone")
	}
	return tz, nil
}

func (*tzSvc) Name() string { return "timezone" }
func (t *tzSvc) Apply(env *Env, c *config.Config) ([]string, error) {
	if c.Timezone == t.last || c.Timezone == "" {
		return nil, nil
	}
	z, err := ZoneFile(c.Timezone)
	if err != nil {
		return []string{"timezone: " + err.Error()}, nil
	}
	src := "/usr/share/zoneinfo/" + z
	if _, err := os.Stat(src); err != nil {
		return []string{"timezone: zoneinfo not found (install tzdata)"}, nil
	}
	_ = os.Remove("/etc/localtime")
	if err := os.Symlink(src, "/etc/localtime"); err != nil {
		return nil, err
	}
	_ = os.WriteFile("/etc/timezone", []byte(z+"\n"), 0o644)
	t.last = c.Timezone
	return nil, nil
}
func (*tzSvc) Stop(*Env) error      { return nil }
func (t *tzSvc) Status(*Env) string { return "ok " + t.last }

// ---------- users ----------

type usersSvc struct{}

var userRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func (*usersSvc) Name() string { return "users" }

// DefaultAdminPassword is the password of the built-in admin account when first created.
const DefaultAdminPassword = "admin"

// routerGroup is the router accounts group (for sshd restrictions).
const routerGroup = "necorouter"

var (
	passwdFile = "/etc/passwd" // overridden in tests
	shellsFile = "/etc/shells"
)

type account struct {
	UID   int
	Shell string
}

func lookupAccount(name string) (account, bool) {
	b, err := os.ReadFile(passwdFile)
	if err != nil {
		return account{}, false
	}
	for _, ln := range strings.Split(string(b), "\n") {
		f := strings.Split(ln, ":")
		if len(f) > 6 && f[0] == name {
			id, _ := strconv.Atoi(f[2])
			return account{UID: id, Shell: f[6]}, true
		}
	}
	return account{}, false
}

func joinRouterGroup(name string) {
	if have("usermod") {
		_, _ = run("usermod", "-aG", routerGroup, name)
	} else {
		_, _ = run("addgroup", name, routerGroup)
	}
}

func deleteAccount(name string) {
	if have("userdel") {
		_, _ = run("userdel", "-r", name)
	} else {
		_, _ = run("deluser", "--remove-home", name)
	}
}

// setPassword sets a Linux account password (chpasswd hashes it itself).
func setPassword(name, pw string) error {
	if pw == "" || strings.ContainsAny(pw, "\r\n") {
		return fmt.Errorf("password must be non-empty and single-line")
	}
	if _, err := runIn(name+":"+pw+"\n", "chpasswd"); err != nil {
		return fmt.Errorf("chpasswd: %v", err)
	}
	return nil
}

// SetPassword changes a router account password (see SetAccountPassword).
func (m *Manager) SetPassword(name, pw string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	err := SetAccountPassword(m.Env, name, pw)
	m.Env.St.Save()
	return err
}

// DefaultPassword reports that the account still has the default password.
func (m *Manager) DefaultPassword(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return inList(m.Env.St.DefPw, name)
}

func (*usersSvc) Apply(env *Env, c *config.Config) ([]string, error) {
	return SyncAccounts(env, c)
}

func ensureShell() {
	b, _ := os.ReadFile(shellsFile)
	if !strings.Contains(string(b), ShellPath) {
		f, err := os.OpenFile(shellsFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
		if err == nil {
			fmt.Fprintln(f, ShellPath)
			f.Close()
		}
	}
}

// Stop does NOT remove accounts: passwords now live only in Linux, and
// stopping the daemon must not lock the administrator out (with the daemon
// stopped, the shell simply reports that the router is not running).
func (*usersSvc) Stop(env *Env) error { return nil }
func (*usersSvc) Status(env *Env) string {
	return fmt.Sprintf("ok %d account(s)", len(env.St.Users))
}

// ---------- dns (resolv.conf) ----------

type dnsSvc struct{}

const resolvMark = "# managed by necorouter\n"

func (*dnsSvc) Name() string { return "dns" }
func (*dnsSvc) Apply(env *Env, c *config.Config) ([]string, error) {
	orig := env.P.Var + "/resolv.conf.orig"
	if len(c.NameServers) == 0 {
		return nil, restoreResolv(env, orig)
	}
	cur, _ := os.ReadFile("/etc/resolv.conf")
	if !strings.HasPrefix(string(cur), resolvMark) {
		_ = writeFileAtomic(orig, cur, 0o644)
	}
	var b strings.Builder
	b.WriteString(resolvMark)
	if c.Domain != "" {
		fmt.Fprintf(&b, "search %s\n", c.Domain)
	}
	for _, n := range c.NameServers {
		fmt.Fprintf(&b, "nameserver %s\n", n)
	}
	if string(cur) != b.String() {
		// resolv.conf may be a bind mount (containers), so write in place
		if err := os.WriteFile("/etc/resolv.conf", []byte(b.String()), 0o644); err != nil {
			return nil, err
		}
	}
	env.St.DNS = true
	return nil, nil
}
func restoreResolv(env *Env, orig string) error {
	if !env.St.DNS {
		return nil
	}
	if b, err := os.ReadFile(orig); err == nil {
		_ = os.WriteFile("/etc/resolv.conf", b, 0o644)
	}
	env.St.DNS = false
	return nil
}
func (*dnsSvc) Stop(env *Env) error { return restoreResolv(env, env.P.Var+"/resolv.conf.orig") }
func (*dnsSvc) Status(env *Env) string {
	if env.St.DNS {
		return "managing /etc/resolv.conf"
	}
	return "idle"
}
