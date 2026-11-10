// Package svc contains the router services. Each service is idempotent: Apply
// brings the system to the desired configuration, and resource-hungry processes
// (dnsmasq, FRR, chrony) are started only if they are actually needed.
package svc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"necorouter/internal/config"
	"necorouter/internal/logx"
)

// logw writes to the router log, or to the standard log.Logger if it is not open (tests).
func logw(l *log.Logger, skip int, level, svc, f string, a ...any) {
	if logx.Default() != nil {
		logx.Logf(skip+1, level, svc, f, a...)
	} else if l != nil {
		l.Printf(f, a...)
	}
}

type Paths struct{ Root, Etc, Var, Run string }

// DefaultPaths honors NECOROUTER_ROOT (needed only for tests).
func DefaultPaths() Paths {
	r := os.Getenv("NECOROUTER_ROOT")
	return Paths{Root: r, Etc: r + "/etc/necorouter", Var: r + "/var/lib/necorouter", Run: r + "/run/necorouter"}
}
func (p Paths) Startup() string     { return p.Etc + "/startup-config" }
func (p Paths) Banner() string      { return p.Etc + "/banner" }
func (p Paths) Issue() string       { return p.Root + "/etc/issue" }
func (p Paths) PortMap() string     { return p.Etc + "/ports.map" }
func (p Paths) Sock() string        { return p.Run + "/ctl.sock" }
func (p Paths) State() string       { return p.Var + "/state.json" }
func (p Paths) Crash() string       { return p.Etc + "/crash-config" }
func (p Paths) AutosaveDir() string { return p.Etc + "/autosave-history" }
func (p Paths) ConfigsDir() string  { return p.Etc + "/configs" }
func (p Paths) Journal() string     { return p.Var + "/config.journal" }
func (p Paths) RunSnap() string     { return p.Var + "/running.conf" }
func (p Paths) RunLock() string     { return p.Var + "/running.lock" }
func (p Paths) History() string     { return p.Etc + "/startup-history" }
func (p Paths) UpdatePrefs() string { return p.Etc + "/update.json" }
func (p Paths) Backups() string     { return p.Etc + "/backups" }
func (p Paths) LogDir() string      { return p.Root + "/var/log/necorouter" }

type Env struct {
	P     Paths
	Ports *PortMap
	Log   *log.Logger
	Sup   *Supervisor
	St    *State
}

// Service is a unit of router behavior.
type Service interface {
	Name() string
	Apply(env *Env, c *config.Config) ([]string, error) // warnings, error
	Stop(env *Env) error
	Status(env *Env) string
}

// ---- command execution ----

func run(name string, args ...string) (string, error) {
	return runIn("", name, args...)
}

func runIn(stdin, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	err := cmd.Run()
	s := strings.TrimSpace(out.String())
	if err != nil {
		return s, fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, s)
	}
	return s, nil
}

func have(bin string) bool { _, err := exec.LookPath(bin); return err == nil }

// find looks for a binary in PATH and in typical directories of different distributions.
func find(bin string, extra ...string) string {
	if p, err := exec.LookPath(bin); err == nil {
		return p
	}
	for _, d := range append(extra, "/usr/lib/frr", "/usr/libexec/frr", "/usr/sbin", "/sbin", "/usr/local/sbin") {
		p := filepath.Join(d, bin)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---- state: what exactly the router created (so as not to touch foreign things) ----

type State struct {
	mu             sync.Mutex
	path           string
	Addrs          map[string][]string   `json:"addrs"`
	Links          map[string]string     `json:"links"`
	Routes         []string              `json:"routes"`
	Users          []string              `json:"users"`
	Taken          map[string]string     `json:"taken,omitempty"` // taken accounts: name -> previous shell
	DefPw          []string              `json:"defpw,omitempty"` // accounts with the default password
	NFT            bool                  `json:"nft"`
	Sec            bool                  `json:"sec,omitempty"` // nftables table of the security profile
	FM             bool                  `json:"fm,omitempty"`  // nftables table of filter-map
	DNS            bool                  `json:"dns"`
	TrafficShaping map[string]ShapeState `json:"traffic_shaping,omitempty"`
}

// ShapeState records only qdiscs and IFB devices created by applied QoS policies.
type ShapeState struct {
	IFB      string `json:"ifb,omitempty"`
	Download bool   `json:"download,omitempty"`
	Upload   bool   `json:"upload,omitempty"`
}

func LoadState(path string) *State {
	s := &State{path: path, Addrs: map[string][]string{}, Links: map[string]string{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, s)
	}
	if s.Addrs == nil {
		s.Addrs = map[string][]string{}
	}
	if s.Links == nil {
		s.Links = map[string]string{}
	}
	if s.Taken == nil {
		s.Taken = map[string]string{}
	}
	if s.TrafficShaping == nil {
		s.TrafficShaping = map[string]ShapeState{}
	}
	return s
}

func (s *State) Save() {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.MarshalIndent(s, "", " ")
	_ = writeFileAtomic(s.path, b, 0o600)
}

// ---- supervisor of long-running processes ----

type proc struct {
	cmd  *exec.Cmd
	sig  string
	stop bool
	done chan struct{}
	out  *ring
}

// ring keeps the last lines of process output (for diagnostics) and duplicates
// the output to the daemon's stderr.
type ring struct {
	mu    sync.Mutex
	lines []string
	part  string
}

func (r *ring) Write(p []byte) (int, error) {
	_, _ = os.Stderr.Write(p)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.part += string(p)
	for {
		i := strings.IndexByte(r.part, '\n')
		if i < 0 {
			break
		}
		if l := strings.TrimSpace(r.part[:i]); l != "" {
			r.lines = append(r.lines, l)
			if len(r.lines) > 12 {
				r.lines = r.lines[len(r.lines)-12:]
			}
		}
		r.part = r.part[i+1:]
	}
	return len(p), nil
}

func (r *ring) Last(n int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	l := r.lines
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, " | ")
}

type Supervisor struct {
	mu    sync.Mutex
	procs map[string]*proc
	exits map[string]string // last reason the process exited
	log   *log.Logger
}

func NewSupervisor(l *log.Logger) *Supervisor {
	return &Supervisor{procs: map[string]*proc{}, exits: map[string]string{}, log: l}
}

// Why explains why a process is not running: exit code and last output lines.
func (s *Supervisor) Why(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.exits[name]; e != "" {
		return e
	}
	return ""
}

// Ensure starts the process if it is not running or the configuration
// signature changed. A crashed process is restarted with a delay.
func (s *Supervisor) Ensure(name, sig string, argv []string) error {
	s.mu.Lock()
	p := s.procs[name]
	s.mu.Unlock()
	if p != nil && p.sig == sig {
		select {
		case <-p.done:
		default:
			return nil
		}
	}
	s.Stop(name)
	return s.start(name, sig, argv, 0)
}

func (s *Supervisor) start(name, sig string, argv []string, attempt int) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	out := &ring{}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = 2 * time.Second // a child holding the pipe must not hang Wait
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	p := &proc{cmd: cmd, sig: sig, done: make(chan struct{}), out: out}
	s.mu.Lock()
	s.procs[name] = p
	s.mu.Unlock()
	go func() {
		err := cmd.Wait()
		close(p.done)
		s.mu.Lock()
		cur := s.procs[name] == p
		stopped := p.stop
		if cur && !stopped {
			why := fmt.Sprintf("%s exited: %v", name, err)
			if l := out.Last(3); l != "" {
				why += " (" + l + ")"
			}
			s.exits[name] = why
		}
		s.mu.Unlock()
		if cur && !stopped {
			logw(s.log, 1, logx.LevelWarn, "SUPERVISOR", "process %s exited: %v", name, err)
			d := time.Duration(attempt+1) * 2 * time.Second
			if d > 30*time.Second {
				d = 30 * time.Second
			}
			time.Sleep(d)
			s.mu.Lock()
			still := s.procs[name] == p && !p.stop
			s.mu.Unlock()
			if still {
				_ = s.start(name, sig, argv, attempt+1)
			}
		}
	}()
	return nil
}

func (s *Supervisor) Stop(name string) {
	s.mu.Lock()
	p := s.procs[name]
	if p != nil {
		p.stop = true
		delete(s.procs, name)
	}
	s.mu.Unlock()
	if p == nil || p.cmd.Process == nil {
		return
	}
	s.mu.Lock()
	delete(s.exits, name)
	s.mu.Unlock()
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
		<-p.done
	}
}

func (s *Supervisor) Running(name string) bool {
	s.mu.Lock()
	p := s.procs[name]
	s.mu.Unlock()
	if p == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (s *Supervisor) StopAll() {
	s.mu.Lock()
	names := make([]string, 0, len(s.procs))
	for n := range s.procs {
		names = append(names, n)
	}
	s.mu.Unlock()
	sort.Strings(names)
	for _, n := range names {
		s.Stop(n)
	}
}

// ---- port map: te0 -> physical Linux interface ----
//
// ports.map: "port  interface  [MAC]". Entries with a MAC are created by auto-discovery:
// the port follows the card by MAC (the interface name may change, eth0 → ens3),
// new network cards get the next free name without human involvement. The name starts with
// the card speed: e (up to 100 Mbit/s), ge (up to 1 Gbit/s), te (faster, or speed unknown).
// An entry without a MAC (two columns) is considered pinned manually and is not changed.

type PortMap struct {
	mu     sync.RWMutex
	names  []string
	m      map[string]string
	mac    map[string]string // port → MAC of an auto-discovered entry
	path   string
	sysdir string
	last   time.Time
}

type nicInfo struct {
	name, mac string
	phys      bool
	speed     int // Mbit/s from sysfs, 0 when unknown (link down, virtual card)
}

// portPrefix returns the port name prefix for a card of the given speed in Mbit/s.
func portPrefix(speed int) string {
	switch {
	case speed <= 0:
		return "te" // unknown speed: the prefix used before speeds were read
	case speed < 1000:
		return "e"
	case speed < 10000:
		return "ge"
	default:
		return "te"
	}
}

// scanNICs reads the list of network interfaces from sysfs.
func scanNICs(sysdir string) []nicInfo {
	ents, _ := os.ReadDir(sysdir)
	var out []nicInfo
	for _, e := range ents {
		n := e.Name()
		if n == "lo" {
			continue
		}
		base := sysdir + "/" + n
		if t, _ := os.ReadFile(base + "/type"); strings.TrimSpace(string(t)) != "1" {
			continue // Ethernet only (not tun, gre, sit, ppp…)
		}
		skip := false
		for _, d := range []string{"/bridge", "/tun_flags", "/bonding", "/brport"} {
			if _, err := os.Stat(base + d); err == nil {
				skip = true
			}
		}
		for _, pfx := range []string{"docker", "br-", "virbr", "vlan", "gre", "tunnel", "dummy"} {
			if strings.HasPrefix(n, pfx) {
				skip = true
			}
		}
		if skip {
			continue
		}
		speed, _ := os.ReadFile(base + "/speed")
		sp, _ := strconv.Atoi(strings.TrimSpace(string(speed)))
		mac, _ := os.ReadFile(base + "/address")
		m := strings.ToLower(strings.TrimSpace(string(mac)))
		if m == "00:00:00:00:00:00" {
			m = ""
		}
		_, err := os.Stat(base + "/device")
		out = append(out, nicInfo{name: n, mac: m, phys: err == nil, speed: sp})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func LoadPortMap(path string) *PortMap {
	return loadPortMap(path, "/sys/class/net")
}

func loadPortMap(path, sysdir string) *PortMap {
	pm := &PortMap{m: map[string]string{}, mac: map[string]string{}, path: path, sysdir: sysdir}
	if b, err := os.ReadFile(path); err == nil {
		for _, ln := range strings.Split(string(b), "\n") {
			f := strings.Fields(ln)
			if len(f) < 2 || strings.HasPrefix(f[0], "#") {
				continue
			}
			pm.names = append(pm.names, f[0])
			pm.m[f[0]] = f[1]
			if len(f) >= 3 {
				pm.mac[f[0]] = strings.ToLower(f[2])
			}
		}
	}
	pm.Refresh(true)
	return pm
}

// Refresh reconciles the port map with the real interfaces: returns ports to
// their place after a NIC rename and adds new cards. At most once every 2 s.
func (p *PortMap) Refresh(force bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !force && time.Since(p.last) < 2*time.Second {
		return
	}
	p.last = time.Now()
	nics := scanNICs(p.sysdir)
	byName := map[string]nicInfo{}
	byMAC := map[string]nicInfo{}
	for _, n := range nics {
		byName[n.name] = n
		if n.mac != "" {
			byMAC[n.mac] = n
		}
	}
	changed := false
	// 1) auto-discovered ports follow their MAC
	for _, port := range p.names {
		mac := p.mac[port]
		if mac == "" {
			continue
		}
		if cur, ok := byName[p.m[port]]; ok && cur.mac == mac {
			continue
		}
		if n, ok := byMAC[mac]; ok && n.name != p.m[port] {
			p.m[port] = n.name
			changed = true
		}
	}
	// 2) new network cards get the next free name for their speed (e0, ge0, te0, …)
	used := map[string]bool{}
	for _, port := range p.names {
		used[p.m[port]] = true
	}
	var cands []nicInfo
	for _, n := range nics {
		if !used[n.name] && n.phys {
			cands = append(cands, n)
		}
	}
	if len(cands) == 0 && len(p.names) == 0 { // container/no hardware: take virtual Ethernet interfaces
		for _, n := range nics {
			if !used[n.name] {
				cands = append(cands, n)
			}
		}
	}
	next := map[string]int{} // next index per prefix
	for _, n := range cands {
		if n.mac != "" {
			dup := false
			for _, m := range p.mac {
				if m == n.mac {
					dup = true
				}
			}
			if dup {
				continue // the card already exists under another name (it was renamed)
			}
		}
		pfx := portPrefix(n.speed)
		var port string
		for {
			port = fmt.Sprintf("%s%d", pfx, next[pfx])
			next[pfx]++
			if p.m[port] == "" {
				break
			}
		}
		p.names = append(p.names, port)
		p.m[port] = n.name
		if n.mac != "" {
			p.mac[port] = n.mac
		}
		changed = true
	}
	if changed && p.path != "" {
		p.save()
	}
}

// save writes ports.map (called under the lock).
func (p *PortMap) save() {
	names := append([]string(nil), p.names...)
	sort.SliceStable(names, func(i, j int) bool { return portNum(names[i]) < portNum(names[j]) })
	var l []string
	for _, n := range names {
		line := n + " " + p.m[n]
		if p.mac[n] != "" {
			line += " " + p.mac[n]
		}
		l = append(l, line)
	}
	hdr := "# порт  интерфейс_linux  [MAC]. Строки с MAC обновляются автоматически;\n" +
		"# чтобы закрепить порт вручную, оставьте только две колонки.\n"
	_ = writeFileAtomic(p.path, []byte(hdr+strings.Join(l, "\n")+"\n"), 0o644)
}

// portNum orders ports: e0, e1, …, ge0, …, te0, … (other names last).
func portNum(n string) int {
	m := portNameRe.FindStringSubmatch(n)
	if m == nil {
		return 1 << 30
	}
	v, _ := strconv.Atoi(m[2])
	return map[string]int{"e": 0, "ge": 1 << 20, "te": 2 << 20}[m[1]] + v
}

// portNameRe matches a port name: e, ge or te followed by the index.
var portNameRe = regexp.MustCompile(`^(e|ge|te)(\d+)$`)

func (p *PortMap) Names() []string {
	p.Refresh(false)
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]string(nil), p.names...)
}
func (p *PortMap) NIC(port string) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.m[port]
}
func (p *PortMap) Has(port string) bool {
	if p.NIC(port) == "" {
		p.Refresh(false) // the card may have just been plugged in
	}
	return p.NIC(port) != ""
}

// DevOf returns the Linux device name for a router ip-interface.
func DevOf(env *Env, c *config.Config, ifname string) string {
	i := c.FindIface(ifname)
	if i == nil {
		return ""
	}
	if i.Tunnel != nil || strings.HasPrefix(i.Name, "loopback") {
		return trimDev(i.Name)
	}
	p, s := c.Binding(ifname)
	if p == nil {
		return ""
	}
	nic := env.Ports.NIC(p.Name)
	if nic == "" {
		return ""
	}
	if s.Encap == "dot1q" {
		return trimDev(fmt.Sprintf("%s.%d", nic, s.VLAN))
	}
	return nic
}

func trimDev(n string) string {
	if len(n) > 15 {
		return n[:15]
	}
	return n
}

// IfaceByDev is the reverse mapping for show commands.
func IfaceByDev(env *Env, c *config.Config, dev string) string {
	for _, i := range c.Ifaces {
		if DevOf(env, c, i.Name) == dev {
			return i.Name
		}
	}
	return ""
}

// ---- manager ----

type Manager struct {
	logged   map[string]bool
	Env      *Env
	Services []Service
	mu       sync.Mutex
}

func NewManager(p Paths, l *log.Logger) *Manager {
	_ = os.MkdirAll(p.Etc, 0o755)
	_ = os.MkdirAll(p.Var, 0o755)
	_ = os.MkdirAll(p.Run, 0o755)
	env := &Env{P: p, Log: l, Sup: NewSupervisor(l), St: LoadState(p.State())}
	env.Ports = LoadPortMap(p.PortMap())
	return &Manager{Env: env, Services: []Service{
		&hostSvc{}, &startupBannerSvc{}, &tzSvc{}, &usersSvc{}, &netSvc{}, &trafficShapeSvc{}, &dnsSvc{},
		&natSvc{}, &secSvc{}, &fmapSvc{}, &dhcpSvc{}, &ospfSvc{}, &ntpSvc{}, &ipsecSvc{},
	}}
}

// Reconcile brings the system to the configuration; returns warnings.
func (m *Manager) Reconcile(c *config.Config) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Env.Ports.Refresh(false) // auto-discover network cards before applying
	var warns []string
	cur := map[string]bool{}
	for _, s := range m.Services {
		w, err := s.Apply(m.Env, c)
		warns = append(warns, w...)
		if err != nil {
			e := fmt.Sprintf("%s: %v", s.Name(), err)
			warns = append(warns, e)
			if !m.logged[e] {
				logw(m.Env.Log, 1, logx.LevelError, "SERVICE", "service %s", e)
			}
			cur[e] = true
		}
	}
	m.logged = cur
	m.Env.St.Save()
	return warns
}

// Restart stops one service and applies the current configuration to it again.
func (m *Manager) Restart(name string, c *config.Config) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.Services {
		if s.Name() == name {
			if err := s.Stop(m.Env); err != nil {
				return nil, err
			}
			return s.Apply(m.Env, c)
		}
	}
	return nil, fmt.Errorf("no such process %s", name)
}

// Shutdown stops everything the router started.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.Services) - 1; i >= 0; i-- {
		_ = m.Services[i].Stop(m.Env)
	}
	m.Env.Sup.StopAll()
	m.Env.St.Save()
}

func (m *Manager) Status() string {
	var b strings.Builder
	for _, s := range m.Services {
		fmt.Fprintf(&b, "%-10s %s\n", s.Name(), s.Status(m.Env))
	}
	return b.String()
}
