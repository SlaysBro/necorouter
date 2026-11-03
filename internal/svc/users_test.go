package svc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"necorouter/internal/config"
)

// userEnv prepares fake useradd/chpasswd/usermod and a temporary passwd.
type userEnv struct {
	log, passwd string
	env         *Env
}

func newUserEnv(t *testing.T, passwd string) *userEnv {
	t.Helper()
	d := t.TempDir()
	u := &userEnv{log: filepath.Join(d, "calls.log"), passwd: filepath.Join(d, "passwd")}
	_ = os.WriteFile(u.passwd, []byte(passwd), 0o644)
	script := func(name, body string) {
		_ = os.WriteFile(filepath.Join(d, name), []byte("#!/bin/sh\n"+body), 0o755)
	}
	script("useradd", `echo "useradd $*" >> `+u.log+`
for a; do name=$a; done
sh=; prev=; for a; do [ "$prev" = -s ] && sh=$a; prev=$a; done
echo "$name:x:1001:1001::/home/$name:$sh" >> `+u.passwd+"\n")
	script("groupadd", `echo "groupadd $*" >> `+u.log+"\n")
	script("usermod", `echo "usermod $*" >> `+u.log+"\n")
	script("userdel", `echo "userdel $*" >> `+u.log+"\n")
	script("chpasswd", `echo "chpasswd $(cat)" >> `+u.log+"\n")
	t.Setenv("PATH", d+":"+os.Getenv("PATH"))
	oldP, oldS := passwdFile, shellsFile
	passwdFile, shellsFile = u.passwd, filepath.Join(d, "shells")
	t.Cleanup(func() { passwdFile, shellsFile = oldP, oldS })
	u.env = &Env{St: LoadState(filepath.Join(d, "state.json"))}
	return u
}

func (u *userEnv) calls() string { b, _ := os.ReadFile(u.log); return string(b) }

func cfgUsers(us ...*config.User) *config.Config { return &config.Config{Users: us} }

func TestUsersCreateLinuxAccountsWithRouterShell(t *testing.T) {
	u := newUserEnv(t, "root:x:0:0:root:/root:/bin/bash\n")
	c := cfgUsers(&config.User{Name: "admin", Role: "admin", BuiltinAdmin: true}, &config.User{Name: "neto", Role: "noc"}, &config.User{Name: "bare"})
	warns, err := (&usersSvc{}).Apply(u.env, c)
	if err != nil {
		t.Fatal(err)
	}
	got := u.calls()
	for _, want := range []string{
		"useradd -m -s " + ShellPath + " -g necorouter admin",
		"chpasswd admin:admin",
		"useradd -m -s " + ShellPath + " -g necorouter neto",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "chpasswd bare") {
		t.Fatal("account without a password must stay locked")
	}
	if len(warns) != 2 || !strings.Contains(strings.Join(warns, "\n"), "bare") || !strings.Contains(strings.Join(warns, "\n"), "neto") {
		t.Fatalf("expected no-password warnings for 'neto' and 'bare', got %v", warns)
	}
	if strings.Contains(got, "root") {
		t.Fatal("root must never be touched")
	}
	// applying again neither resets passwords nor recreates accounts
	before := u.calls()
	if _, err := (&usersSvc{}).Apply(u.env, c); err != nil {
		t.Fatal(err)
	}
	if u.calls() != before {
		t.Fatalf("second Apply must be a no-op, new calls:\n%s", strings.TrimPrefix(u.calls(), before))
	}
}

func TestUsersTakeOverRulesAndStopKeepsAccounts(t *testing.T) {
	u := newUserEnv(t, "root:x:0:0:root:/root:/bin/bash\nsvc:x:100:100::/:/bin/false\nbob:x:1005:1005::/home/bob:/bin/bash\n")
	c := cfgUsers(&config.User{Name: "svc"}, &config.User{Name: "bob"})
	warns, _ := (&usersSvc{}).Apply(u.env, c)
	if len(warns) != 1 || !strings.Contains(warns[0], "system account") {
		t.Fatalf("system account must be refused: %v", warns)
	}
	got := u.calls()
	if !strings.Contains(got, "usermod -s "+ShellPath+" bob") || !strings.Contains(got, "usermod -aG necorouter bob") {
		t.Fatalf("bob must get the router shell and group:\n%s", got)
	}
	if strings.Contains(got, " svc") {
		t.Fatalf("system account touched:\n%s", got)
	}
	// an account we did not create stays when removed from the configuration
	if _, err := (&usersSvc{}).Apply(u.env, cfgUsers()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(u.calls(), "userdel") {
		t.Fatal("pre-existing account must not be deleted")
	}
	// stopping the daemon does not remove created accounts (passwords live only in Linux)
	u.env.St.Users = []string{"neto"}
	_ = (&usersSvc{}).Stop(u.env)
	if strings.Contains(u.calls(), "userdel") {
		t.Fatal("Stop must not delete accounts")
	}
}

func TestSetPasswordOnlyForRouterAccounts(t *testing.T) {
	u := newUserEnv(t, "root:x:0:0:root:/root:/bin/bash\nneto:x:1001:1001::/home/neto:"+ShellPath+"\nbob:x:1002:1002::/home/bob:/bin/bash\n")
	m := &Manager{Env: u.env}
	if err := m.SetPassword("neto", "n3w pass:1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u.calls(), "chpasswd neto:n3w pass:1") {
		t.Fatalf("chpasswd not called:\n%s", u.calls())
	}
	for _, name := range []string{"root", "bob", "ghost"} {
		if err := m.SetPassword(name, "x"); err == nil {
			t.Fatalf("SetPassword(%s) must be refused", name)
		}
	}
	if err := m.SetPassword("neto", "bad\nline"); err == nil {
		t.Fatal("multi-line password must be refused")
	}
}

func TestIssueSyncRestore(t *testing.T) {
	root := t.TempDir()
	p := Paths{Root: root, Etc: root + "/etc/necorouter", Var: root + "/var/lib/necorouter", Run: root + "/run/necorouter"}
	_ = os.MkdirAll(root+"/etc", 0o755)
	_ = os.WriteFile(root+"/etc/issue", []byte("Debian \\n \\l\n"), 0o644)
	_ = os.MkdirAll(p.Etc, 0o755)
	_ = os.WriteFile(p.Banner(), []byte("a\\b|\n"), 0o644)
	if err := SyncIssue(p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p.Issue())
	if !strings.HasPrefix(string(b), "a\\\\b|\n") {
		t.Fatalf("backslash must be doubled for getty: %q", b)
	}
	// repeated sync and a banner change do not lose the real original
	_ = os.WriteFile(p.Banner(), []byte("new\n"), 0o644)
	if err := SyncIssue(p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p.Issue()); !strings.HasPrefix(string(b), "new\n") {
		t.Fatalf("banner change not applied: %q", b)
	}
	if err := RestoreIssue(p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p.Issue()); string(b) != "Debian \\n \\l\n" {
		t.Fatalf("original not restored: %q", b)
	}
	// there was no file, so after restoring there is none either
	_ = os.Remove(p.Issue())
	if err := SyncIssue(p); err != nil {
		t.Fatal(err)
	}
	if err := RestoreIssue(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Issue()); err == nil {
		t.Fatal("/etc/issue created by us must be removed on restore")
	}
	// someone else's edit made after ours is not overwritten
	_ = SyncIssue(p)
	_ = os.WriteFile(p.Issue(), []byte("admin edit\n"), 0o644)
	_ = RestoreIssue(p)
	if b, _ := os.ReadFile(p.Issue()); string(b) != "admin edit\n" {
		t.Fatalf("foreign edit overwritten: %q", b)
	}
}

// Without a banner file nothing is printed: /etc/issue is empty, the banner file itself is not created.
func TestIssueNoBannerFile(t *testing.T) {
	root := t.TempDir()
	p := Paths{Root: root, Etc: root + "/etc/necorouter", Var: root + "/var/lib/necorouter", Run: root + "/run/necorouter"}
	_ = os.MkdirAll(root+"/etc", 0o755)
	_ = os.WriteFile(root+"/etc/issue", []byte("Debian \\n \\l\n"), 0o644)
	if err := SyncIssue(p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p.Issue()); len(b) != 0 {
		t.Fatalf("no banner file must give an empty issue: %q", b)
	}
	if _, err := os.Stat(p.Banner()); !os.IsNotExist(err) {
		t.Fatalf("sync must not create the banner file: %v", err)
	}
	if err := RestoreIssue(p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p.Issue()); string(b) != "Debian \\n \\l\n" {
		t.Fatalf("original not restored: %q", b)
	}
}
