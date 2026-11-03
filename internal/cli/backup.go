package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"necorouter/internal/config"
	"necorouter/internal/svc"
)

// Configuration backups (all files are 0600 in /etc/necorouter or /var/lib/necorouter):
//   - running.conf  — snapshot of running-config, updated right after every change;
//     needed so the crash copy is up to date (SIGKILL and power loss cannot be intercepted);
//   - running.lock  — "daemon running" marker; a clean shutdown removes it;
//   - crash-config  — copy of the snapshot made at startup after an abnormal termination;
//   - startup-history/ and autosave-history/ — history of copies (config-history service):
//     old startup-configs on overwrite and timer-based copies of running-config.
const (
	snapEvery   = time.Second // period of the change check; the autosave period is set in the configuration
	defaultKeep = 10          // how many history copies to keep if keep is not set
	maxKeep     = 64          // upper bound for keep
)

// keepOr returns keep or the default value.
func keepOr(n int) int {
	if n <= 0 {
		return defaultKeep
	}
	return n
}

// writeAtomic writes a file with mode 0600 via a temporary file and a rename.
func writeAtomic(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil { // data is on disk before the rename
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil { // the rename record is flushed to disk too
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// normalize strips comments and blank lines: only the configuration content is compared.
func normalize(s string) string {
	var b strings.Builder
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimRight(ln, " \t\r")
		if t == "" || strings.HasPrefix(strings.TrimLeft(t, " \t"), "!") {
			continue
		}
		b.WriteString(t)
		b.WriteByte('\n')
	}
	return b.String()
}

// RecoverCrash is called before the configuration is loaded. If the running marker is left over from
// the previous run, the last snapshot is copied to crash-config.
func (r *Router) RecoverCrash() (bool, error) {
	if _, err := os.Stat(r.P.RunLock()); err != nil {
		return false, nil
	}
	r.crashed = true
	b, err := os.ReadFile(r.P.RunSnap())
	if err != nil {
		return true, fmt.Errorf("crash backup: %w", err)
	}
	return true, writeAtomic(r.P.Crash(), string(b))
}

// Crashed reports that the previous run ended abnormally (set before connections are accepted).
func (r *Router) Crashed() bool { return r.crashed }

// MarkRunning sets the running marker; called after the socket is open.
func (r *Router) MarkRunning() error {
	return writeAtomic(r.P.RunLock(), "running\n")
}

// MarkClean finishes a clean shutdown: final snapshot and marker removal.
// Call only after Watch has stopped.
func (r *Router) MarkClean() error {
	if _, err := r.syncFile(r.P.RunSnap(), ""); err != nil {
		return err
	}
	return os.Remove(r.P.RunLock())
}

// syncFile writes running-config to path if it differs from last.
// Returns the fingerprint of the written state; on error, the previous last (retried on the next tick).
func (r *Router) syncFile(path, last string) (string, error) {
	r.mu.Lock()
	fp := r.Cfg.Fingerprint()
	if fp == last {
		r.mu.Unlock()
		return last, nil
	}
	text := r.Cfg.Render()
	r.mu.Unlock()
	if err := writeAtomic(path, text); err != nil {
		return last, err
	}
	return fp, nil
}

// Watch maintains the running-config snapshot and the autosave history until done is closed.
// Errors are logged once until they clear.
func (r *Router) Watch(done <-chan struct{}, logf func(string, ...any)) {
	t := time.NewTicker(snapEvery)
	defer t.Stop()
	var snapFP string
	var snapErr, autoErr error
	lastAuto := time.Now()
	for {
		select {
		case <-done:
			return
		case now := <-t.C:
			fp, err := r.syncFile(r.P.RunSnap(), snapFP)
			if err != nil && snapErr == nil {
				logf("running snapshot: %v", err)
			}
			snapFP, snapErr = fp, err

			// autosave history: requires the config-history service and an interval; fires on the period
			r.mu.Lock()
			on := r.Cfg.AutosaveOn && r.Cfg.Features["config-history"] // off by default; enabled by autosave interval with feature config-history
			mins := r.Cfg.AutosaveMin
			if mins == 0 {
				mins = config.AutosaveDefaultMin
			}
			period := time.Duration(mins) * time.Minute
			keep := keepOr(r.Cfg.AutosaveKeep)
			r.mu.Unlock()
			if !on || now.Sub(lastAuto) < period {
				continue
			}
			lastAuto = now
			err = r.autosaveOnce(keep)
			if err != nil && autoErr == nil {
				logf("autosave: %v", err)
			}
			autoErr = err
		}
	}
}

// autosaveOnce saves running-config to the autosave history if it differs from startup-config
// and from the last autosave copy (identical copies are not piled up).
func (r *Router) autosaveOnce(keep int) error {
	r.mu.Lock()
	text := r.Cfg.Render()
	r.mu.Unlock()
	body := normalize(text)
	if st, err := os.ReadFile(r.P.Startup()); err == nil && normalize(string(st)) == body {
		return nil
	}
	dir := r.P.AutosaveDir()
	if last := newestHistory(dir, "autosave-"); last != "" {
		if b, err := os.ReadFile(last); err == nil && normalize(string(b)) == body {
			return nil
		}
	}
	return pushHistory(dir, "autosave-", text, keep)
}

// archiveStartup moves the current startup-config into startup-history/ before it is overwritten.
func archiveStartup(p svc.Paths, keep int) error {
	b, err := os.ReadFile(p.Startup())
	if os.IsNotExist(err) {
		return nil // there has been no first save yet
	}
	if err != nil {
		return err
	}
	return pushHistory(p.History(), "startup-", string(b), keep)
}

// pushHistory writes text to dir as prefixYYYYMMDD-HHMMSS-NNN.conf and keeps the last keep copies.
// One rotation implementation for startup-history and autosave-history.
func pushHistory(dir, prefix, text string, keep int) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ts := time.Now().Format("20060102-150405")
	var path string
	for n := 1; ; n++ {
		path = filepath.Join(dir, fmt.Sprintf("%s%s-%03d.conf", prefix, ts, n))
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
	}
	if err := writeAtomic(path, text); err != nil {
		return err
	}
	names := listHistory(dir, prefix)
	for i := 0; i < len(names)-keep; i++ {
		if err := os.Remove(filepath.Join(dir, names[i])); err != nil {
			return err
		}
	}
	return nil
}

// listHistory returns history file names in ascending order (oldest first).
func listHistory(dir, prefix string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, prefix) && strings.HasSuffix(n, ".conf") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// newestHistory is the path to the newest copy, or "".
func newestHistory(dir, prefix string) string {
	names := listHistory(dir, prefix)
	if len(names) == 0 {
		return ""
	}
	return filepath.Join(dir, names[len(names)-1])
}
