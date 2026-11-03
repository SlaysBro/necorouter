package svc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"necorouter/internal/config"
)

// ospfSvc runs FRR (zebra + ospfd) only when router ospf is configured.
type ospfSvc struct{}

func (*ospfSvc) Name() string { return "ospf" }

func FRRDir(p Paths) string { return p.Run + "/frr" }

func (*ospfSvc) Apply(env *Env, c *config.Config) ([]string, error) {
	o := c.OSPF
	pw := c.PolicyWarnings()
	if o == nil {
		return pw, (&ospfSvc{}).Stop(env)
	}
	zebra, ospfd := find("zebra"), find("ospfd")
	if zebra == "" || ospfd == "" {
		return nil, fmt.Errorf("FRR (zebra/ospfd) is not installed (needed for router ospf)")
	}
	warns := pw
	dir := FRRDir(env.P)
	_ = os.MkdirAll(dir, 0o755)

	var z strings.Builder
	fmt.Fprintf(&z, "hostname %s\n!\n", c.Hostname)

	var b strings.Builder
	fmt.Fprintf(&b, "hostname %s\n!\n", c.Hostname)
	for _, i := range c.Ifaces {
		dev := DevOf(env, c, i.Name)
		if dev == "" {
			continue
		}
		var l []string
		switch i.OSPFAuth {
		case "message-digest":
			l = append(l, "ip ospf authentication message-digest")
			for _, k := range i.OSPFKeys {
				l = append(l, fmt.Sprintf("ip ospf message-digest-key %d md5 %s", k.ID, k.Key))
			}
		case "simple":
			l = append(l, "ip ospf authentication")
			if i.OSPFSimple != "" {
				l = append(l, "ip ospf authentication-key "+i.OSPFSimple)
			}
		}
		if i.OSPFCost > 0 {
			l = append(l, fmt.Sprintf("ip ospf cost %d", i.OSPFCost))
		}
		nw := i.OSPFNetwork
		if nw == "" && i.Tunnel != nil {
			nw = "point-to-point" // GRE: point-to-point, DR/BDR not needed
		}
		if nw != "" {
			l = append(l, "ip ospf network "+nw)
		}
		if len(l) > 0 {
			fmt.Fprintf(&b, "interface %s\n %s\n!\n", dev, strings.Join(l, "\n "))
		}
	}
	pol, pwarn := frrPolicy(c, func(n string) string { return DevOf(env, c, n) })
	warns = append(warns, pwarn...)
	b.WriteString(pol)
	b.WriteString("router ospf\n")
	if o.RouterID != "" {
		fmt.Fprintf(&b, " ospf router-id %s\n", o.RouterID)
	}
	if o.PassiveDefault {
		b.WriteString(" passive-interface default\n")
	}
	for _, n := range o.Passive {
		if d := DevOf(env, c, n); d != "" {
			fmt.Fprintf(&b, " passive-interface %s\n", d)
		}
	}
	for _, n := range o.NoPassive {
		if d := DevOf(env, c, n); d != "" {
			fmt.Fprintf(&b, " no passive-interface %s\n", d)
		}
	}
	for _, n := range o.Nets {
		fmt.Fprintf(&b, " network %s area %s\n", n.Prefix, n.Area)
	}
	for a, m := range o.AreaAuth {
		if m == "message-digest" {
			fmt.Fprintf(&b, " area %s authentication message-digest\n", a)
		} else {
			fmt.Fprintf(&b, " area %s authentication\n", a)
		}
	}
	for _, r := range o.Redistribute {
		b.WriteString(o.RedistLine(r) + "\n")
	}
	if o.DefaultOrig {
		b.WriteString(o.DefaultLine() + "\n")
	}
	b.WriteString("!\n")

	zc, oc := dir+"/zebra.conf", dir+"/ospfd.conf"
	_ = writeFileAtomic(zc, []byte(z.String()), 0o644)
	_ = writeFileAtomic(oc, []byte(b.String()), 0o644)
	// Startup does not wait for zebra to be ready: ospfd reconnects to the zserv socket
	// by itself (as all FRR/Quagga daemons do), while waiting under the router lock
	// hung the CLI on every router ospf mode command.
	// Account for the daemons. If the previous start failed on privs_init and named
	// the vty group, take it from the message: this is more accurate than any guess.
	if g := vtyGroupFromError(ospfWhy(env)); g != "" {
		identMu.Lock()
		for _, b := range []string{zebra, ospfd} {
			if vtyOverride[b] != g {
				vtyOverride[b] = g
				delete(identCache, b)
			}
		}
		identMu.Unlock()
	}
	id := frrIdent(zebra)
	if id.RootNeedsVty { // there is no service user: put root into the vty group
		if err := addToGroup("root", id.VtyGroup); err != nil {
			warns = append(warns, fmt.Sprintf("FRR requires membership in group %q and there is no service user: add root manually (usermod -aG %s root): %v", id.VtyGroup, id.VtyGroup, err))
		}
	}
	prepareFRRDir(dir, id, env.Sup.Running("zebra"))

	// Configuration check by ospfd itself (--dryrun, if the build supports it): an error in
	// the config would otherwise show up only as a daemon crash and "OSPF is not running".
	dryMu.Lock()
	changed := lastDry[dir] != b.String()
	lastDry[dir] = b.String()
	dryMu.Unlock()
	if changed && strings.Contains(frrHelp(ospfd), "--dryrun") {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		out, err := combinedCtx(ctx, ospfd, append(frrArgs(ospfd, oc, dir, "ospfd.pid"), "-C")...)
		cancel()
		if err != nil {
			warns = append(warns, "ospfd rejected the generated configuration: "+lastLines(string(out), 3))
		}
	}
	if err := env.Sup.Ensure("zebra", "zebra", append([]string{zebra}, frrArgs(zebra, zc, dir, "zebra.pid")...)); err != nil {
		return warns, err
	}
	err := env.Sup.Ensure("ospfd", b.String(), append([]string{ospfd}, frrArgs(ospfd, oc, dir, "ospfd.pid")...))
	return warns, err
}

var (
	dryMu   sync.Mutex
	lastDry = map[string]string{} // router directory → config already checked with --dryrun

	helpMu    sync.Mutex
	helpCache = map[string]string{}
)

// frrHelp returns the output of `bin --help` (cached). It is needed to pass
// only the options understood by the installed build: FRR versions
// (RedOS, Alpine) and Quagga differ in the set of options, and an unknown option
// makes the daemon exit immediately at startup.
func frrHelp(bin string) string {
	helpMu.Lock()
	defer helpMu.Unlock()
	if h, ok := helpCache[bin]; ok {
		return h
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _ := combinedCtx(ctx, bin, "--help")
	h := string(out)
	helpCache[bin] = h
	return h
}

// frrArgs builds the zebra/ospfd arguments. If the help could not be obtained,
// the full set of FRR options is used (previous behavior).
func frrArgs(bin, conf, dir, pid string) []string {
	return buildFRRArgs(frrHelp(bin), conf, dir, pid, frrIdent(bin))
}

// frrIdentity is the account under which to run zebra/ospfd.
//
// FRR built with --enable-vty-group (Alpine, Debian, some RHEL packages)
// refuses to start with "-u root":
//
//	privs_init: user(root) is not part of vty group specified(frrvty)
//
// The regular init runs the daemons as the build's service user (frr:frr),
// which is in the vty group. We do the same. For builds without a vty group
// (RedOS and the like) the behavior is unchanged: root.
type frrIdentity struct {
	User, Group  string
	UID, GID     int
	Drop         bool   // true: the build's service user, not root
	VtyGroup     string // vty group of the build ("" means no restriction)
	RootNeedsVty bool   // there is no service user: root has to be added to VtyGroup
}

var rootIdent = frrIdentity{User: "root", Group: "root"}

var (
	identMu        sync.Mutex
	identCache     = map[string]frrIdentity{}
	vtyOverride    = map[string]string{} // bin → group named in the daemon's error message
	reVtyBuild     = regexp.MustCompile(`--enable-vty-group=([A-Za-z0-9_.-]+)`)
	reUserBuild    = regexp.MustCompile(`--enable-user=([A-Za-z0-9_.-]+)`)
	reGroupBuild   = regexp.MustCompile(`--enable-group=([A-Za-z0-9_.-]+)`)
	reVtyErr       = regexp.MustCompile(`not part of vty group specified\(([A-Za-z0-9_.-]+)\)`)
	frrLookupUser  = user.Lookup
	frrLookupGroup = user.LookupGroup

	// overridden in tests
	addToGroup = func(userName, group string) error {
		if p, err := exec.LookPath("usermod"); err == nil {
			return exec.Command(p, "-aG", group, userName).Run()
		}
		if p, err := exec.LookPath("addgroup"); err == nil { // busybox (Alpine)
			return exec.Command(p, userName, group).Run()
		}
		return fmt.Errorf("neither usermod nor addgroup is available")
	}
)

// frrVersion returns the output of `bin --version` (FRR prints the build parameters there).
func frrVersion(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _ := combinedCtx(ctx, bin, "--version")
	return string(out)
}

// frrIdent determines which user to run the daemons of this FRR build as.
func frrIdent(bin string) frrIdentity {
	identMu.Lock()
	defer identMu.Unlock()
	if id, ok := identCache[bin]; ok && vtyOverride[bin] == id.VtyGroup {
		return id
	}
	ver := frrVersion(bin)
	vty := ""
	if m := reVtyBuild.FindStringSubmatch(ver); m != nil {
		vty = m[1]
	}
	if g := vtyOverride[bin]; g != "" { // the daemon itself reported which group is needed
		vty = g
	}
	if vty == "" { // the version did not reveal the build parameters: determine from the system
		if _, err := frrLookupGroup("frrvty"); err == nil {
			vty = "frrvty"
		}
	}
	id := rootIdent
	id.VtyGroup = vty
	if vty != "" {
		svcUser, svcGroup := "frr", "frr"
		if m := reUserBuild.FindStringSubmatch(ver); m != nil {
			svcUser = m[1]
			svcGroup = m[1]
		}
		if m := reGroupBuild.FindStringSubmatch(ver); m != nil {
			svcGroup = m[1]
		}
		if u, err := frrLookupUser(svcUser); err == nil {
			uid, _ := strconv.Atoi(u.Uid)
			gid, _ := strconv.Atoi(u.Gid)
			if g, err := frrLookupGroup(svcGroup); err == nil {
				gid, _ = strconv.Atoi(g.Gid)
			}
			id = frrIdentity{User: svcUser, Group: svcGroup, UID: uid, GID: gid, Drop: true, VtyGroup: vty}
		} else {
			id.RootNeedsVty = true
		}
	}
	identCache[bin] = id
	return id
}

// prepareFRRDir prepares the daemons' working directory: hands it to the service
// user and removes stale sockets/pid files from previous runs (otherwise
// files left over from root prevent the daemon from starting as frr).
func prepareFRRDir(dir string, id frrIdentity, running bool) {
	if !running {
		for _, pat := range []string{"*.pid", "*.api", "*.vty"} {
			m, _ := filepath.Glob(filepath.Join(dir, pat))
			for _, f := range m {
				_ = os.Remove(f)
			}
		}
	}
	if !id.Drop {
		return
	}
	_ = os.Chown(dir, id.UID, id.GID)
	_ = os.Chmod(dir, 0o755)
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		_ = os.Chown(filepath.Join(dir, e.Name()), id.UID, id.GID)
	}
}

// vtyGroupFromError extracts the group name from a privs_init message.
func vtyGroupFromError(msg string) string {
	if m := reVtyErr.FindStringSubmatch(msg); m != nil {
		return m[1]
	}
	return ""
}

func buildFRRArgs(help, conf, dir, pid string, id frrIdentity) []string {
	known := strings.TrimSpace(help) != ""
	has := func(opt string) bool { return !known || strings.Contains(help, opt) }
	a := []string{"-f", conf, "-i", dir + "/" + pid, "-z", dir + "/zserv.api"}
	if has("--vty_socket") {
		a = append(a, "--vty_socket", dir)
	}
	if has("--vty_port") {
		a = append(a, "-P", "0")
	}
	if has("--user") && has("--group") {
		a = append(a, "-u", id.User, "-g", id.Group)
	}
	return a
}

func (*ospfSvc) Stop(env *Env) error {
	env.Sup.Stop("ospfd")
	env.Sup.Stop("zebra")
	return nil
}
func (*ospfSvc) Status(env *Env) string {
	if env.Sup.Running("ospfd") && env.Sup.Running("zebra") {
		return "running (frr zebra+ospfd)"
	}
	if why := ospfWhy(env); why != "" {
		return "FAILED: " + why
	}
	return "idle"
}

// ospfWhy explains why the OSPF processes are not running.
func ospfWhy(env *Env) string {
	if w := env.Sup.Why("zebra"); w != "" {
		return w
	}
	return env.Sup.Why("ospfd")
}

func lastLines(s string, n int) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, " | ")
}

// OSPFShow runs an ospfd show command via vtysh.
func OSPFShow(env *Env, c *config.Config, cmd string) (string, error) {
	if c == nil || c.OSPF == nil {
		return "", fmt.Errorf("OSPF is not configured (use 'router ospf 1' in configuration mode)")
	}
	if find("ospfd") == "" || find("zebra") == "" {
		return "", fmt.Errorf("OSPF is configured but FRR (zebra/ospfd) is not installed")
	}
	if !env.Sup.Running("ospfd") || !env.Sup.Running("zebra") {
		if why := ospfWhy(env); why != "" {
			return "", fmt.Errorf("OSPF is not running: %s", why)
		}
		return "", fmt.Errorf("OSPF is not running yet (starting); try again in a moment")
	}
	v := find("vtysh")
	if v == "" {
		return "", fmt.Errorf("vtysh is not installed")
	}
	args := []string{"-c", cmd}
	if strings.Contains(frrHelp(v), "--vty_socket") || strings.TrimSpace(frrHelp(v)) == "" {
		args = append([]string{"--vty_socket", FRRDir(env.P)}, args...)
	}
	// show commands run under the router lock: a hung ospfd must not
	// hang the whole CLI, so vtysh has a wait limit.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := combinedCtx(ctx, v, args...)
	s := strings.TrimSpace(string(out))
	if ctx.Err() != nil {
		return s, fmt.Errorf("vtysh did not answer (ospfd is not responding)")
	}
	if err != nil {
		return s, fmt.Errorf("vtysh: %v: %s", err, s)
	}
	return s, nil
}

// combinedCtx runs a command with a time limit. WaitDelay keeps a
// child that holds the output pipe from hanging the call after the context is cancelled.
func combinedCtx(ctx context.Context, name string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, name, args...)
	c.WaitDelay = time.Second
	return c.CombinedOutput()
}
