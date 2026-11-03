package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"necorouter/internal/config"
	"necorouter/internal/logx"
	"necorouter/internal/svc"
)

var errQuit = errors.New("quit")

// Version is set from cmd/necorouter/VERSION (embedded at compile time); BuildDate is set by the Makefile.
// A plain `go build` has no date.
var (
	Version   = "devbuild"
	BuildDate = ""
)

// VersionString — the version string for `necorouter version`, `show version` and the journal.
func VersionString() string {
	if BuildDate == "" {
		return Version
	}
	return Version + " " + BuildDate
}

// Router — shared state: one running-config for all sessions.
type Router struct {
	mu      sync.Mutex
	Cfg     *config.Config
	Mgr     *svc.Manager
	P       svc.Paths
	Started time.Time
	crashed bool // the previous run ended abnormally (see backup.go)

	// power management (see power.go)
	power       func(kind string) error // replacement for the system reboot (tests); nil — the real one
	pmu         sync.Mutex              // protects the reboot schedule
	reloadTimer *time.Timer
	reloadAt    time.Time
}

func NewRouter(mgr *svc.Manager, p svc.Paths) *Router {
	return &Router{Cfg: config.New(), Mgr: mgr, P: p, Started: time.Now()}
}

// Save writes running-config to startup-config.
func (r *Router) Save() error { return r.SaveDesc("") }

// SaveDesc saves running-config to startup-config; desc (if set) is written
// as a comment on the first line and is visible in the history list.
func (r *Router) SaveDesc(desc string) error {
	hdr := ""
	if desc != "" {
		hdr = "! description: " + desc + "\n"
	}
	return r.writeStartup(hdr + r.Cfg.Render())
}

// writeStartup is the only place where startup-config is overwritten: with config-history
// enabled, the previous file is first moved to history.
func (r *Router) writeStartup(text string) error {
	if r.Cfg.Features["config-history"] {
		if err := archiveStartup(r.P, keepOr(r.Cfg.StartupKeep)); err != nil {
			return err // do not overwrite startup if the old copy could not be saved
		}
	}
	return writeAtomic(r.P.Startup(), text)
}

// needAdmin: the configuration contains secrets (OSPF keys), so viewing it and
// privileged mode are available only to the admin role.
func (s *Session) needAdmin() error {
	if s.Role != "admin" {
		return fmt.Errorf("access denied: role %q is not allowed to do this (requires role admin)", s.Role)
	}
	return nil
}

// Lookup finds a user by name (passwords are stored only by Linux).
func (r *Router) Lookup(name string) *config.User {
	r.mu.Lock()
	defer r.mu.Unlock()
	if u := r.Cfg.FindUser(name); u != nil {
		c := *u
		return &c
	}
	return nil
}

// LoadStartup replays startup-config through the same CLI parser.
func (r *Router) LoadStartup() []string {
	b, err := os.ReadFile(r.P.Startup())
	if err != nil {
		return nil
	}
	return r.Replay(string(b))
}

// Replay executes configuration text; returns lines with errors.
func (r *Router) Replay(text string) []string {
	s := &Session{R: r, User: "root", Role: "admin", noApply: true}
	s.frames = []frame{{kind: "config"}}
	var errs []string
	for n, ln := range strings.Split(text, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "!") || strings.HasPrefix(t, "#") {
			continue
		}
		if t == "end" {
			break
		}
		if ln[0] != ' ' && ln[0] != '\t' {
			s.frames = []frame{{kind: "config"}}
		}
		var sb strings.Builder
		s.Exec(t, &sb)
		if o := strings.TrimSpace(sb.String()); strings.Contains(o, "% ") {
			errs = append(errs, fmt.Sprintf("line %d (%s): %s", n+1, t, strings.ReplaceAll(o, "\n", " ")))
		}
	}
	r.mu.Lock()
	warns := r.Mgr.Reconcile(r.Cfg)
	r.mu.Unlock()
	return append(errs, warns...)
}

type frame struct{ kind, obj, obj2 string }

type Session struct {
	R       *Router
	User    string
	Role    string
	frames  []frame
	noApply bool
	W       io.Writer
	seen    map[string]bool // warnings already shown in this session

	TTY bool // the client is a real terminal (allows animation)

	// Paged output of show: Ctl is called with "page"/"endpage" (the client buffers
	// output between them and shows it with a pager). PageLen: 0 — by window height,
	// -1 — disabled (terminal length 0), >0 — a fixed number of lines.
	Ctl     func(cmd string, n int)
	PageLen int

	rawLine string // original text of the current command (not tokenized)

	// pending — a [y/N] question after a warning: the next line entered is the answer.
	pending *confirmation

	// terminal monitor: new journal messages are printed to the session writer (see monitor.go)
	monMu   sync.Mutex
	monOut  io.Writer
	monStop func()

	cmu    sync.Mutex // protects cancel/ctx; NOT tied to R.mu, so Interrupt works while a command runs
	ctx    context.Context
	cancel context.CancelFunc
}

// Interrupt interrupts the session's running command (ping, traceroute).
func (s *Session) Interrupt() {
	s.cmu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.cmu.Unlock()
}

// begin creates the context of the next command; end releases it.
func (s *Session) begin() context.Context {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	s.ctx, s.cancel = context.WithCancel(context.Background())
	return s.ctx
}

func NewSession(r *Router, user *config.User) *Session {
	return &Session{R: r, User: user.Name, Role: user.Role, frames: []frame{{kind: "exec"}}}
}

func (s *Session) top() frame { return s.frames[len(s.frames)-1] }
func (s *Session) push(kind, obj string) {
	s.frames = append(s.frames, frame{kind: kind, obj: obj})
}
func (s *Session) pop() {
	if len(s.frames) > 1 {
		s.frames = s.frames[:len(s.frames)-1]
	}
}
func (s *Session) cfg() *config.Config { return s.R.Cfg }
func (s *Session) Printf(f string, a ...any) {
	if s.W != nil {
		fmt.Fprintf(s.W, f, a...)
	}
}

func isCfg(kind string) bool {
	switch kind {
	case "config", "if", "port", "si", "ospf", "user", "dhcp", "secprof", "fmap", "ipprof", "ikep1", "ikep2", "cmap", "rmap":
		return true
	}
	return false
}

var suffixes = map[string]string{
	"exec": ">", "priv": "#", "config": "(config)#", "if": "(config-if)#",
	"port": "(config-port)#", "si": "(config-service-instance)#",
	"ospf": "(config-router)#", "user": "(config-user)#", "dhcp": "(config-dhcp-server)#", "secprof": "(config-security-profile)#",
	"fmap": "(filter-map-ipv4)#", "ipprof": "(config-ipsec-profile)#", "ikep1": "(config-ike-phase1)#",
	"ikep2": "(config-ike-phase2)#", "cmap": "(config-crypto-map)#", "rmap": "(config-route-map)#",
}

// confirmation — an action awaiting user confirmation.
type confirmation struct {
	question string
	do       func(*Session) error
	secret   bool                         // input without echo (password)
	doIn     func(*Session, string) error // handler of the entered line (instead of y/N)
}

// ask prints a warning and waits for an answer: only y or yes performs the action, anything else cancels.
// There are no questions while replaying a configuration (noApply).
func (s *Session) ask(warn, question string, do func(*Session) error) error {
	if s.noApply {
		return do(s)
	}
	s.Printf("%s", warn)
	s.pending = &confirmation{question: question + " [y/N] ", do: do}
	return nil
}

func (s *Session) Prompt() string {
	if s.pending != nil {
		return s.pending.question
	}
	s.R.mu.Lock()
	h := s.R.Cfg.Hostname
	s.R.mu.Unlock()
	return h + suffixes[s.top().kind]
}

func (s *Session) rootsFor(kind string) []*Node { return s.prune(trees[kind]) }

// prune removes commands whose gate is closed. Configuration replay (noApply) ignores gates.
func (s *Session) prune(nodes []*Node) []*Node {
	if s.noApply {
		return nodes
	}
	var out []*Node
	for _, n := range nodes {
		if n.Gate != nil && !n.Gate(s) {
			continue
		}
		if len(n.Kids) > 0 {
			c := *n
			c.Kids = s.prune(n.Kids)
			n = &c
		}
		out = append(out, n)
	}
	return out
}

// resolve handles the "do " prefix in configuration modes.
func (s *Session) resolve(line string) (kind, rest string) {
	kind = s.top().kind
	if isCfg(kind) {
		t := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(t, "do ") || strings.HasPrefix(t, "do\t") {
			return "priv", line[len(line)-len(t)+2:]
		}
	}
	return kind, line
}

func positions(line string) []int {
	var pos []int
	in, has := false, false
	for i, r := range line {
		switch {
		case r == '"':
			if !has {
				pos = append(pos, i)
				has = true
			}
			in = !in
		case (r == ' ' || r == '\t') && !in:
			has = false
		default:
			if !has {
				pos = append(pos, i)
				has = true
			}
		}
	}
	return pos
}

// Exec executes one line. Returns true if the session should be closed.
func (s *Session) Exec(line string, w io.Writer) (quit bool) {
	s.begin()
	defer s.Interrupt() // release the context resources
	s.R.mu.Lock()
	defer s.R.mu.Unlock()
	s.W = w
	s.monMu.Lock()
	if s.monStop != nil {
		s.monOut = w // journal messages go to the same stream as command output
	}
	s.monMu.Unlock()
	line = strings.TrimSpace(line)
	if s.pending != nil { // answer to a [y/N] question
		p := s.pending
		s.pending = nil
		if p.doIn != nil {
			if err := p.doIn(s, line); err != nil {
				s.Printf("%% %s\n", err.Error())
			}
			return false
		}
		if a := strings.ToLower(line); a == "y" || a == "yes" {
			if err := p.do(s); err != nil {
				s.Printf("%% %s\n", err.Error())
			}
		} else {
			s.Printf("%% Aborted\n")
		}
		return false
	}
	if line == "" || line[0] == '!' || line[0] == '#' {
		return false
	}
	kind, rest := s.resolve(line)
	// output modifiers: show ... | include TEXT, | redirect FILE, > FILE
	pre := line[:len(line)-len(rest)] // the "do " prefix
	var mods []pipeMod
	if strings.ContainsAny(rest, "|>") {
		if t0, _ := tokenize(rest); len(t0) > 0 {
			if n, _ := pick(s.rootsFor(kind), t0[0], false); n != nil && n.Word == "show" {
				cmd, m, err := splitPipe(rest)
				if err != nil {
					s.Printf("%% %s\n", err.Error())
					return false
				}
				rest, mods = cmd, m
			}
		}
	}
	s.rawLine = rest // original command text: mge takes the quotes that the tokenizer strips
	toks, _ := tokenize(rest)
	d := descend(s.rootsFor(kind), toks)
	s.audit(line, pre, toks, d)
	if d.errKind != "" {
		off := len(line) - len(rest)
		if ps := positions(rest); d.errTok < len(ps) {
			off += ps[d.errTok]
		}
		if d.errKind == "ambiguous" {
			s.Printf("%% Ambiguous command: \"%s\"\n", toks[d.errTok])
		} else {
			s.Printf("%s^\n%% Invalid input detected at '^' marker.\n", strings.Repeat(" ", len(s.R.Cfg.Hostname)+len(suffixes[s.top().kind])+1+off))
			if d.errTok < len(toks) {
				if h := badTokenHint(toks, d.errTok); h != "" {
					s.Printf("%% %s\n", h)
				}
			}
		}
		return false
	}
	if len(mods) > 0 {
		for _, m := range mods {
			if m.kind == "redirect" {
				if err := s.needAdmin(); err != nil {
					s.Printf("%% %s\n", err.Error())
					return false
				}
			}
		}
		fw, err := newFilterWriter(w, mods, s.R.P.FilesDir())
		if err != nil {
			s.Printf("%% %s\n", err.Error())
			return false
		}
		s.W = fw
		defer func() {
			_ = fw.Close()
			s.W = w
		}()
	}
	noPager := false
	for _, m := range mods {
		if m.kind == "nopager" || m.kind == "redirect" {
			noPager = true
		}
	}
	for _, t := range toks { // show log follow — endless output, no pager needed
		if strings.EqualFold(t, "follow") {
			noPager = true
		}
	}
	if s.Ctl != nil && s.TTY && s.PageLen >= 0 && !noPager && len(toks) > 0 {
		if n, _ := pick(s.rootsFor(kind), toks[0], false); n != nil && n.Word == "show" {
			s.Ctl("page", s.PageLen)
			defer s.Ctl("endpage", 0)
		}
	}
	var h Handler
	if d.last != nil {
		if d.neg {
			h = d.last.Neg
		} else {
			h = d.last.Run
		}
	}
	if h == nil {
		s.Printf("%% Incomplete command.\n")
		return false
	}
	before := ""
	cfgMode := isCfg(s.top().kind) && kind != "priv"
	if cfgMode {
		before = s.R.Cfg.Fingerprint()
	}
	err := h(s, d.vals)
	if err == errQuit {
		return true
	}
	if err != nil {
		s.Printf("%% %s\n", err.Error())
		return false
	}
	if cfgMode && !s.noApply && s.R.Cfg.Fingerprint() != before {
		cur := map[string]bool{}
		for _, w := range s.R.Mgr.Reconcile(s.R.Cfg) {
			cur[w] = true
			if !s.seen[w] {
				s.Printf("%% warning: %s\n", w)
			}
		}
		s.seen = cur
	}
	return false
}

// badTokenHint explains typical mistakes in addresses, masks and prefixes.
func badTokenHint(toks []string, i int) string {
	t := toks[i]
	if k := strings.IndexByte(t, '/'); k > 0 {
		if a, err := netip.ParseAddr(t[:k]); err == nil && a.Is4() {
			return fmt.Sprintf("Invalid prefix length '%s': use A.B.C.D/M with M from 0 to 32", t[k+1:])
		}
		return fmt.Sprintf("Invalid address '%s': use dotted decimal A.B.C.D/M", t[:k])
	}
	if a, err := netip.ParseAddr(t); err == nil && a.Is4() && i > 0 {
		if _, e := netip.ParseAddr(toks[i-1]); e == nil {
			return fmt.Sprintf("Invalid mask '%s': use a contiguous mask such as 255.255.255.0", t)
		}
	}
	if strings.Count(t, ".") == 3 {
		return fmt.Sprintf("Invalid IPv4 address '%s'", t)
	}
	return ""
}

// audit writes the executed command to the journal; secrets (keys, passwords) are replaced with ***.
func (s *Session) audit(line, pre string, toks []string, d descent) {
	if s.noApply || len(toks) == 0 {
		return
	}
	text := line
	if d.secret >= 0 && d.secret < len(toks) {
		text = pre + strings.Join(toks[:d.secret], " ") + " ***"
	}
	logx.Auditf(s.User, "%s", text)
}
