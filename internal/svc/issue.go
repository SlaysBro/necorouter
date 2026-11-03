package svc

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// issueText prepares the banner for /etc/issue: getty treats a backslash
// as the start of an escape sequence (\n, \l, ...), so we double it.
func issueText(banner string) string {
	t := strings.ReplaceAll(strings.ReplaceAll(banner, "\r\n", "\n"), "\\", "\\\\")
	return strings.TrimRight(t, "\n") + "\n\n"
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func issueDir(p Paths) string { return filepath.Join(p.Var, "issue") }

// SyncIssue writes the banner to /etc/issue. The previous /etc/issue is saved and
// restored by RestoreIssue. The file is rewritten only if the content
// changed; edits made by others after our write are kept as the "original".
func SyncIssue(p Paths) error {
	// no banner file or it is empty: no banner is shown (empty /etc/issue)
	var want []byte
	if b, err := os.ReadFile(p.Banner()); err == nil && strings.TrimSpace(string(b)) != "" {
		want = []byte(issueText(string(b)))
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	dir := issueDir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	cur, rerr := os.ReadFile(p.Issue())
	if rerr == nil && string(cur) == string(want) {
		return os.WriteFile(filepath.Join(dir, "managed.sha"), []byte(sum(want)), 0o644)
	}
	managed, _ := os.ReadFile(filepath.Join(dir, "managed.sha"))
	ours := rerr == nil && string(managed) == sum(cur) // the previous version is ours, not the "original"
	if !ours {
		_ = os.Remove(filepath.Join(dir, "orig"))
		_ = os.Remove(filepath.Join(dir, "none"))
		if rerr == nil {
			if err := os.WriteFile(filepath.Join(dir, "orig"), cur, 0o644); err != nil {
				return err
			}
		} else if err := os.WriteFile(filepath.Join(dir, "none"), nil, 0o644); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(p.Issue()), 0o755); err != nil {
		return err
	}
	if err := writeFileAtomic(p.Issue(), want, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "managed.sha"), []byte(sum(want)), 0o644)
}

// RestoreIssue restores the previous /etc/issue. If the file was changed after us,
// it leaves it alone.
func RestoreIssue(p Paths) error {
	dir := issueDir(p)
	managed, err := os.ReadFile(filepath.Join(dir, "managed.sha"))
	if err != nil {
		return nil // we did not change /etc/issue
	}
	cur, rerr := os.ReadFile(p.Issue())
	if rerr == nil && string(managed) == sum(cur) {
		if orig, err := os.ReadFile(filepath.Join(dir, "orig")); err == nil {
			if err := writeFileAtomic(p.Issue(), orig, 0o644); err != nil {
				return err
			}
		} else if _, err := os.Stat(filepath.Join(dir, "none")); err == nil {
			_ = os.Remove(p.Issue())
		}
	}
	return os.RemoveAll(dir)
}
