package cli

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"necorouter/internal/config"
	"necorouter/internal/svc"
)

// Configuration files:
//   - startup, crash — service files (startup-config and crash-config);
//   - running        — the current configuration (only as a source);
//   - startup-YYYYMMDD-HHMMSS-NNN and autosave-… — copies from history (config-history service);
//   - other names — user files (write file NAME) in /etc/necorouter/configs/.
var (
	fileNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,31}$`)
	histNameRe = regexp.MustCompile(`^(startup|autosave)-\d{8}-\d{6}-\d{3}$`)
)

// validFileName checks a user file name; service names and prefixes are taken by history.
func validFileName(n string) error {
	if !fileNameRe.MatchString(n) {
		return fmt.Errorf("invalid file name %q: letters, digits, '.', '_' and '-' only, up to 32 characters, starting with a letter", n)
	}
	l := strings.ToLower(n)
	switch l {
	case "startup", "crash", "running", "autosave", "startup-config", "crash-config", "running-config":
		return fmt.Errorf("file name %q is reserved", n)
	}
	if strings.HasPrefix(l, "startup-") || strings.HasPrefix(l, "autosave-") {
		return fmt.Errorf("file name %q is reserved: the prefixes startup- and autosave- belong to the history", n)
	}
	return nil
}

// cfgRef — a found configuration file. Kind: running, startup, crash, history, file.
type cfgRef struct {
	Kind, Name, Path string
}

// ref turns a name into a file. A name cannot contain a path, so escaping the directory is impossible.
func (r *Router) ref(name string) (cfgRef, error) {
	switch strings.ToLower(name) {
	case "running", "running-config":
		return cfgRef{Kind: "running", Name: "running"}, nil
	case "startup", "startup-config":
		return cfgRef{"startup", "startup", r.P.Startup()}, nil
	case "crash", "crash-config":
		return cfgRef{"crash", "crash", r.P.Crash()}, nil
	}
	if histNameRe.MatchString(name) {
		dir := r.P.History()
		if strings.HasPrefix(name, "autosave-") {
			dir = r.P.AutosaveDir()
		}
		return cfgRef{"history", name, filepath.Join(dir, name+".conf")}, nil
	}
	if err := validFileName(name); err != nil {
		return cfgRef{}, err
	}
	return cfgRef{"file", name, filepath.Join(r.P.ConfigsDir(), name+".conf")}, nil
}

// readCfg reads a file; running is built from memory (called with r.mu held).
func (r *Router) readCfg(c cfgRef) (string, error) {
	if c.Kind == "running" {
		return r.Cfg.Render(), nil
	}
	b, err := os.ReadFile(c.Path)
	if err != nil {
		return "", fmt.Errorf("file %q not found", c.Name)
	}
	return string(b), nil
}

// descOf extracts the description from the first line "! description: ...".
func descOf(text string) string {
	first, _, _ := strings.Cut(text, "\n")
	return strings.TrimSpace(strings.TrimPrefix(first, "! description:"))
}

type fileEntry struct {
	Name, Kind, Desc string
	Size             int64
	Mod              time.Time
}

func entryOf(name, kind, path string) (fileEntry, bool) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return fileEntry{}, false
	}
	e := fileEntry{Name: name, Kind: kind, Size: st.Size(), Mod: st.ModTime()}
	if b, err := os.ReadFile(path); err == nil {
		e.Desc = descOf(string(b))
	}
	return e, true
}

// listFiles: startup-config, crash-config (if present) and user files by name.
func (r *Router) listFiles() []fileEntry {
	var out []fileEntry
	if e, ok := entryOf("startup-config", "startup", r.P.Startup()); ok {
		out = append(out, e)
	}
	if e, ok := entryOf("crash-config", "crash", r.P.Crash()); ok {
		out = append(out, e)
	}
	ents, _ := os.ReadDir(r.P.ConfigsDir())
	var names []string
	for _, e := range ents {
		if n := e.Name(); strings.HasSuffix(n, ".conf") && !e.IsDir() {
			names = append(names, strings.TrimSuffix(n, ".conf"))
		}
	}
	sort.Strings(names)
	for _, n := range names {
		if e, ok := entryOf(n, "file", filepath.Join(r.P.ConfigsDir(), n+".conf")); ok {
			out = append(out, e)
		}
	}
	return out
}

// listHistoryEntries: copies from startup-history and autosave-history, newest first.
func (r *Router) listHistoryEntries() []fileEntry {
	var out []fileEntry
	for _, h := range []struct{ dir, prefix, kind string }{
		{r.P.History(), "startup-", "startup-history"},
		{r.P.AutosaveDir(), "autosave-", "autosave"},
	} {
		for _, n := range listHistory(h.dir, h.prefix) {
			name := strings.TrimSuffix(n, ".conf")
			if e, ok := entryOf(name, h.kind, filepath.Join(h.dir, n)); ok {
				out = append(out, e)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out
}

func formatEntries(es []fileEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-34s %-16s %-19s %6s  %s\n", "Name", "Type", "Modified", "Bytes", "Description")
	for _, e := range es {
		fmt.Fprintf(&b, "%-34s %-16s %-19s %6d  %s\n", e.Name, e.Kind, e.Mod.Format("2006-01-02 15:04:05"), e.Size, e.Desc)
	}
	return b.String()
}

// validateConfig replays text in a separate router without services (nothing is done to the system)
// and returns the resulting configuration and the parse errors.
func (r *Router) validateConfig(text string) (*config.Config, []string) {
	tmp, err := os.MkdirTemp("", "necorouter-validate")
	if err != nil {
		return nil, []string{err.Error()}
	}
	defer os.RemoveAll(tmp)
	p := svc.Paths{Etc: tmp + "/etc", Var: tmp + "/var", Run: tmp + "/run"}
	_ = os.MkdirAll(p.Etc, 0o755)
	if b, err := os.ReadFile(r.P.PortMap()); err == nil { // port names — as on this machine
		_ = os.WriteFile(p.PortMap(), b, 0o600)
	}
	mgr := svc.NewManager(p, log.New(io.Discard, "", 0))
	mgr.Services = nil
	sr := NewRouter(mgr, p)
	errs := sr.Replay(text)
	return sr.Cfg, errs
}

func errList(errs []string, n int) string {
	if len(errs) > n {
		errs = append(errs[:n:n], fmt.Sprintf("... and %d more", len(errs)-n))
	}
	return strings.Join(errs, "\n% ")
}

// loadRunning replaces running-config with configuration cfg and applies it. Self-lockout protection:
// after loading there must remain an administrator, and the current user must have the admin role.
func (s *Session) loadRunning(cfg *config.Config) error {
	admin := false
	for _, u := range cfg.Users {
		if u.Role == "admin" {
			admin = true
		}
	}
	if !admin {
		return fmt.Errorf("refusing to load: the configuration has no user with role admin")
	}
	if s.User != "root" {
		if u := cfg.FindUser(s.User); u == nil || u.Role != "admin" {
			return fmt.Errorf("refusing to load: the configuration does not keep user %q with role admin (you would lose access)", s.User)
		}
	}
	s.R.Cfg = cfg
	for _, w := range s.R.Mgr.Reconcile(s.R.Cfg) {
		s.Printf("%% warning: %s\n", w)
	}
	return nil
}

// diffText shows lines that are only in a (-) or only in b (+); comments are ignored.
func diffText(a, b string) (string, error) {
	x := strings.Split(strings.TrimSuffix(normalize(a), "\n"), "\n")
	y := strings.Split(strings.TrimSuffix(normalize(b), "\n"), "\n")
	if x[0] == "" {
		x = nil
	}
	if y[0] == "" {
		y = nil
	}
	if len(x) > 2000 || len(y) > 2000 {
		return "", fmt.Errorf("files are too large to compare")
	}
	// length of the longest common subsequence
	l := make([][]int, len(x)+1)
	for i := range l {
		l[i] = make([]int, len(y)+1)
	}
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				l[i][j] = l[i+1][j+1] + 1
			} else if l[i+1][j] >= l[i][j+1] {
				l[i][j] = l[i+1][j]
			} else {
				l[i][j] = l[i][j+1]
			}
		}
	}
	var out strings.Builder
	i, j := 0, 0
	for i < len(x) || j < len(y) {
		switch {
		case i < len(x) && j < len(y) && x[i] == y[j]:
			i++
			j++
		case j >= len(y) || (i < len(x) && l[i+1][j] >= l[i][j+1]):
			fmt.Fprintf(&out, "- %s\n", x[i])
			i++
		default:
			fmt.Fprintf(&out, "+ %s\n", y[j])
			j++
		}
	}
	return out.String(), nil
}
