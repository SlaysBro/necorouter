package svc

import (
	"io"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"necorouter/internal/config"
)

const frrHelp10 = `Usage : zebra [OPTION...]
-d, --daemon  Runs in daemon mode
-f, --config_file Set configuration file name
-i, --pid_file Set process identifier file name
-z, --socket Set path of zebra socket
-P, --vty_port Set vty's port number
-u, --user User to run as
-g, --group Group to run as
    --vty_socket Override vty socket path`

const quaggaHelp = `Usage : zebra [OPTION...]
-d, --daemon  Runs in daemon mode
-f, --config_file Set configuration file name
-i, --pid_file Set process identifier file name
-z, --socket Set path of zebra socket
-P, --vty_port Set vty's port number
-u, --user User to run as
-g, --group Group to run as`

func TestBuildFRRArgs(t *testing.T) {
	j := func(a []string) string { return strings.Join(a, " ") }
	full := j(buildFRRArgs(frrHelp10, "c", "/d", "z.pid", rootIdent))
	for _, w := range []string{"-f c", "-i /d/z.pid", "-z /d/zserv.api", "--vty_socket /d", "-P 0", "-u root -g root"} {
		if !strings.Contains(full, w) {
			t.Errorf("FRR args lack %q: %s", w, full)
		}
	}
	// unknown help → the previous full set (RedOS behavior unchanged)
	if j(buildFRRArgs("", "c", "/d", "z.pid", rootIdent)) != full {
		t.Errorf("fallback differs from full FRR set")
	}
	// Quagga: no --vty_socket → do not pass it
	q := j(buildFRRArgs(quaggaHelp, "c", "/d", "z.pid", rootIdent))
	if strings.Contains(q, "vty_socket") {
		t.Errorf("--vty_socket passed to a build that lacks it: %s", q)
	}
	// minimal build without -u/-g
	m := j(buildFRRArgs("-f file\n-z sock\n-i pid\n", "c", "/d", "z.pid", rootIdent))
	if strings.Contains(m, "-u") || strings.Contains(m, "-P") || strings.Contains(m, "vty_socket") {
		t.Errorf("unsupported options passed: %s", m)
	}
}

func fakeFRR(t *testing.T, zebraHelp, body string) {
	t.Helper()
	bin := t.TempDir()
	// "daemon": prints help on --help, otherwise checks the options and runs
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do [ \"$a\" = \"--version\" ] && { echo \"$FAKE_FRR_VERSION\"; exit 0; }; done\n" +
		"for a in \"$@\"; do [ \"$a\" = \"--help\" ] && { cat <<'EOF'\n" + zebraHelp + "\nEOF\nexit 0; }; done\n" + body
	for _, n := range []string{"zebra", "ospfd"} {
		if err := os.WriteFile(filepath.Join(bin, n), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

func ospfEnv(t *testing.T) (*Env, *config.Config) {
	t.Helper()
	t.Setenv("NECOROUTER_ROOT", t.TempDir())
	m := NewManager(DefaultPaths(), log.New(io.Discard, "", 0))
	t.Cleanup(m.Env.Sup.StopAll)
	c := config.New()
	c.OSPF = &config.OSPF{ID: 1}
	return m.Env, c
}

// Regression "hangs after router ospf 1": Apply must not wait for zebra to be ready
// (the socket might never appear), including when zebra does not create it.
func TestOSPFApplyDoesNotBlockWhenZebraSocketNeverAppears(t *testing.T) {
	fakeFRR(t, frrHelp10, "sleep 30\n") // running but does not create the socket
	env, c := ospfEnv(t)
	start := time.Now()
	if _, err := (&ospfSvc{}).Apply(env, c); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("Apply blocked for %v", d)
	}
	if !env.Sup.Running("zebra") || !env.Sup.Running("ospfd") {
		t.Fatal("daemons are not running")
	}
	// repeated Apply with the same config is idempotent and fast
	start = time.Now()
	if _, err := (&ospfSvc{}).Apply(env, c); err != nil || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("second Apply: err=%v took %v", err, time.Since(start))
	}
}

// A build without --vty_socket (Quagga/old): a daemon that fails on an unknown option must still start.
func TestOSPFStartsOnBuildWithoutVtySocket(t *testing.T) {
	fakeFRR(t, quaggaHelp, `for a in "$@"; do case "$a" in --vty_socket) echo "unrecognized option" >&2; exit 1;; esac; done
sleep 30
`)
	env, c := ospfEnv(t)
	if _, err := (&ospfSvc{}).Apply(env, c); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if !env.Sup.Running("zebra") || !env.Sup.Running("ospfd") {
		t.Fatal("daemons died on startup (unsupported option passed?)")
	}
}

// The GRE/OSPF config for FRR is unchanged: the tunnel is point-to-point, the MD5 keys are in place.
func TestOSPFConfigGREUnchanged(t *testing.T) {
	fakeFRR(t, frrHelp10, "sleep 30\n")
	env, c := ospfEnv(t)
	c.Ifaces = append(c.Ifaces, &config.Iface{Name: "tunnel.0", Addrs: []string{"10.10.10.1/30"},
		Tunnel:   &config.Tunnel{Src: "172.16.1.2", Dst: "172.16.2.2", Mode: "gre"},
		OSPFAuth: "message-digest", OSPFKeys: []config.OSPFKey{{ID: 1, Key: "P@ssw0rd"}}})
	c.OSPF.Nets = []config.OSPFNet{{Prefix: "10.10.10.0/30", Area: "0"}}
	if _, err := (&ospfSvc{}).Apply(env, c); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(FRRDir(env.P) + "/ospfd.conf")
	for _, w := range []string{"ip ospf authentication message-digest", "ip ospf message-digest-key 1 md5 P@ssw0rd",
		"ip ospf network point-to-point", "router ospf\n", " network 10.10.10.0/30 area 0"} {
		if !strings.Contains(string(b), w) {
			t.Errorf("ospfd.conf lacks %q:\n%s", w, b)
		}
	}
}

// "OSPF is not running" must explain the cause: exit code and daemon output.
func TestOSPFShowExplainsWhyNotRunning(t *testing.T) {
	fakeFRR(t, frrHelp10, `echo "ospfd.conf line 7: % Unknown command" >&2; exit 1
`)
	env, c := ospfEnv(t)
	if _, err := (&ospfSvc{}).Apply(env, c); err != nil {
		t.Fatal(err)
	}
	var err error
	for i := 0; i < 30; i++ { // the daemon crashes immediately; wait for the cause to be recorded
		time.Sleep(100 * time.Millisecond)
		_, err = OSPFShow(env, c, "show ip ospf neighbor")
		if err != nil && strings.Contains(err.Error(), "exited") {
			break
		}
	}
	if err == nil || !strings.Contains(err.Error(), "OSPF is not running") || !strings.Contains(err.Error(), "Unknown command") {
		t.Fatalf("message lacks reason: %v", err)
	}
	if st := (&ospfSvc{}).Status(env); !strings.HasPrefix(st, "FAILED") {
		t.Fatalf("status: %q", st)
	}
	// without router ospf: a clear message
	if _, err := OSPFShow(env, config.New(), "x"); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unconfigured message: %v", err)
	}
}

// ospfd --dryrun rejects the config → a warning with the error text.
func TestOSPFDryrunWarning(t *testing.T) {
	help := frrHelp10 + "\n-C, --dryrun  Check configuration for validity and exit"
	fakeFRR(t, help, `for a in "$@"; do [ "$a" = "-C" ] && { echo "line 4: % Unknown command: bogus" >&2; exit 1; }; done
sleep 30
`)
	env, c := ospfEnv(t)
	warns, err := (&ospfSvc{}).Apply(env, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) == 0 || !strings.Contains(warns[0], "Unknown command: bogus") {
		t.Fatalf("warnings: %v", warns)
	}
}

// ---- daemon account (privs_init / vty group) ----

// simulated FRR check: "-u root" with a vty-group build → refusal
const privsCheckBody = `u=""
while [ $# -gt 0 ]; do [ "$1" = "-u" ] && u=$2; shift; done
[ "$u" = root ] && { echo "privs_init: user(root) is not part of vty group specified(frrvty)" >&2; exit 1; }
sleep 30
`

func TestFRRRunsAsServiceUserWhenBuildHasVtyGroup(t *testing.T) {
	t.Setenv("FAKE_FRR_VERSION", "FRRouting 8.4 configured with: '--enable-user=nobody' '--enable-group=nogroup' '--enable-vty-group=frrvty'")
	fakeFRR(t, frrHelp10, privsCheckBody)
	env, c := ospfEnv(t)
	if _, err := (&ospfSvc{}).Apply(env, c); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if !env.Sup.Running("zebra") || !env.Sup.Running("ospfd") {
		t.Fatalf("daemons died: %s", ospfWhy(env))
	}
	if why := ospfWhy(env); strings.Contains(why, "vty group") {
		t.Fatalf("privs error still happens: %s", why)
	}
	// the directory is handed to the service user
	st, err := os.Stat(FRRDir(env.P))
	if err != nil {
		t.Fatal(err)
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && os.Geteuid() == 0 {
		if sys.Uid == 0 {
			t.Fatalf("frr dir still owned by root")
		}
	}
	args := strings.Join(buildFRRArgs(frrHelp10, "c", "/d", "z", frrIdent(find("zebra"))), " ")
	if !strings.Contains(args, "-u nobody -g nogroup") {
		t.Fatalf("args: %s", args)
	}
}

// A build without a vty group (RedOS): previous behavior — root, nothing changes.
func TestFRRStaysRootWithoutVtyGroup(t *testing.T) {
	withoutFRRSystemIdentity(t)
	t.Setenv("FAKE_FRR_VERSION", "FRRouting 7.5 configured with: '--enable-user=frr'")
	fakeFRR(t, frrHelp10, "sleep 30\n")
	env, c := ospfEnv(t)
	if _, err := (&ospfSvc{}).Apply(env, c); err != nil {
		t.Fatal(err)
	}
	id := frrIdent(find("zebra"))
	if id.Drop || id.User != "root" || id.VtyGroup != "" || id.RootNeedsVty {
		t.Fatalf("identity changed for a build without vty group: %+v", id)
	}
	if a := strings.Join(buildFRRArgs(frrHelp10, "c", "/d", "z", id), " "); !strings.Contains(a, "-u root -g root") {
		t.Fatalf("args: %s", a)
	}
}

// No service user: root is added to the vty group.
func TestFRRAddsRootToVtyGroupWhenNoServiceUser(t *testing.T) {
	var gotUser, gotGroup string
	old := addToGroup
	addToGroup = func(u, g string) error { gotUser, gotGroup = u, g; return nil }
	defer func() { addToGroup = old }()
	t.Setenv("FAKE_FRR_VERSION", "FRRouting 9.0 configured with: '--enable-user=nosuchfrruser' '--enable-vty-group=frrvty'")
	fakeFRR(t, frrHelp10, "sleep 30\n")
	env, c := ospfEnv(t)
	if _, err := (&ospfSvc{}).Apply(env, c); err != nil {
		t.Fatal(err)
	}
	if gotUser != "root" || gotGroup != "frrvty" {
		t.Fatalf("addToGroup(%q,%q)", gotUser, gotGroup)
	}
}

// The version did not reveal the build parameters: the daemon failed on privs_init → the next
// Apply takes the group from the error message and fixes the startup.
func TestFRRLearnsVtyGroupFromErrorMessage(t *testing.T) {
	withoutFRRSystemIdentity(t)
	var gotGroup string
	old := addToGroup
	addToGroup = func(u, g string) error { gotGroup = g; return nil }
	defer func() { addToGroup = old }()
	t.Setenv("FAKE_FRR_VERSION", "FRRouting 8.0")
	fakeFRR(t, frrHelp10, `echo "privs_init: user(root) is not part of vty group specified(customvty)" >&2; exit 1
`)
	env, c := ospfEnv(t)
	if _, err := (&ospfSvc{}).Apply(env, c); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30 && !strings.Contains(ospfWhy(env), "customvty"); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(ospfWhy(env), "customvty") {
		t.Fatalf("no failure recorded: %q", ospfWhy(env))
	}
	c.OSPF.RouterID = "1.1.1.1" // config change → new Apply
	if _, err := (&ospfSvc{}).Apply(env, c); err != nil {
		t.Fatal(err)
	}
	if gotGroup != "customvty" {
		t.Fatalf("group not learned from error: %q", gotGroup)
	}
}

// Keep the daemon identity tests independent of accounts/groups installed on the
// machine running the suite (for example, Ubuntu may already have frr:frr/frrvty).
func withoutFRRSystemIdentity(t *testing.T) {
	t.Helper()
	oldUser, oldGroup := frrLookupUser, frrLookupGroup
	frrLookupUser = func(string) (*user.User, error) { return nil, user.UnknownUserError("missing") }
	frrLookupGroup = func(string) (*user.Group, error) { return nil, user.UnknownGroupError("missing") }
	t.Cleanup(func() { frrLookupUser, frrLookupGroup = oldUser, oldGroup })
}

func TestVtyGroupFromError(t *testing.T) {
	if g := vtyGroupFromError("zebra exited: exit status 1 (privs_init: user(root) is not part of vty group specified(frrvty))"); g != "frrvty" {
		t.Fatalf("%q", g)
	}
	if vtyGroupFromError("something else") != "" {
		t.Fatal("false positive")
	}
}
