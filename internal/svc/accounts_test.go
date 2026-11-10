package svc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"necorouter/internal/config"
)

// realUsermod replaces the usermod stub with a script that edits the fake passwd.
func realUsermod(t *testing.T, u *userEnv) {
	t.Helper()
	d := filepath.Dir(u.passwd)
	body := `#!/bin/sh
echo "usermod $*" >> ` + u.log + `
if [ "$1" = -s ]; then
  sed -i "s#^\($3:[^:]*:[^:]*:[^:]*:[^:]*:[^:]*:\).*#\1$2#" ` + u.passwd + `
fi
`
	_ = os.WriteFile(filepath.Join(d, "usermod"), []byte(body), 0o755)
}

func TestTakenAccountGetsOriginalShellBack(t *testing.T) {
	u := newUserEnv(t, "root:x:0:0:root:/root:/bin/bash\nbob:x:1005:1005::/home/bob:/bin/zsh\n")
	realUsermod(t, u)
	_ = os.WriteFile(shellsFile, []byte("/bin/sh\n/bin/bash\n/bin/zsh\n"), 0o644)
	if _, err := (&usersSvc{}).Apply(u.env, cfgUsers(&config.User{Name: "bob"})); err != nil {
		t.Fatal(err)
	}
	if a, _ := lookupAccount("bob"); a.Shell != ShellPath {
		t.Fatalf("bob must get the router shell, has %q", a.Shell)
	}
	if u.env.St.Taken["bob"] != "/bin/zsh" {
		t.Fatalf("original shell not recorded: %v", u.env.St.Taken)
	}
	// removed from the configuration: the previous shell is restored
	if _, err := (&usersSvc{}).Apply(u.env, cfgUsers()); err != nil {
		t.Fatal(err)
	}
	if a, _ := lookupAccount("bob"); a.Shell != "/bin/zsh" {
		t.Fatalf("original shell must be restored, has %q", a.Shell)
	}
	if len(u.env.St.Taken) != 0 {
		t.Fatalf("record must be dropped: %v", u.env.St.Taken)
	}
	if strings.Contains(u.calls(), "userdel") {
		t.Fatal("a taken-over account must never be deleted")
	}
}

func TestTakenAccountShellChangedByAdminIsLeftAlone(t *testing.T) {
	u := newUserEnv(t, "root:x:0:0:root:/root:/bin/bash\nbob:x:1005:1005::/home/bob:/bin/zsh\n")
	realUsermod(t, u)
	_, _ = (&usersSvc{}).Apply(u.env, cfgUsers(&config.User{Name: "bob"}))
	// the administrator changed the shell themselves
	b, _ := os.ReadFile(u.passwd)
	_ = os.WriteFile(u.passwd, []byte(strings.Replace(string(b), ShellPath, "/bin/fish", 1)), 0o644)
	_, _ = (&usersSvc{}).Apply(u.env, cfgUsers())
	if a, _ := lookupAccount("bob"); a.Shell != "/bin/fish" {
		t.Fatalf("manual shell must be kept, has %q", a.Shell)
	}
}

func TestResolveRequiresRouterShell(t *testing.T) {
	newUserEnv(t, "root:x:0:0::/root:/bin/bash\nneto:x:1001:1001::/h:"+ShellPath+"\nbob:x:1002:1002::/h:/bin/bash\nsvc:x:500:500::/:"+ShellPath+"\n")
	if n, err := ResolveUID(0); err != nil || n != "root" {
		t.Fatal("root must resolve to root")
	}
	if n, err := ResolveUID(1001); err != nil || n != "neto" {
		t.Fatalf("neto: %v %v", n, err)
	}
	for _, uid := range []int{1002, 500, 4242} {
		if _, err := ResolveUID(uid); err == nil {
			t.Fatalf("uid %d must be refused", uid)
		}
	}
}

func TestCleanup(t *testing.T) {
	u := newUserEnv(t, "root:x:0:0:root:/root:/bin/bash\nbob:x:1005:1005::/home/bob:/bin/zsh\n")
	realUsermod(t, u)
	d := filepath.Dir(u.passwd)
	p := Paths{Etc: d + "/etc", Var: d + "/var", Run: d + "/run"}
	u.env.P = p
	_ = os.MkdirAll(p.Var, 0o755)
	u.env.St = LoadState(p.State())
	_, _ = (&usersSvc{}).Apply(u.env, cfgUsers(&config.User{Name: "admin", BuiltinAdmin: true}, &config.User{Name: "bob"}))
	u.env.St.Save()
	Cleanup(p)
	calls := u.calls()
	if !strings.Contains(calls, "userdel -r admin") || strings.Contains(calls, "userdel -r bob") {
		t.Fatalf("cleanup must delete only router-created accounts:\n%s", calls)
	}
	if a, _ := lookupAccount("bob"); a.Shell != "/bin/zsh" {
		t.Fatalf("bob shell not restored: %q", a.Shell)
	}
}
