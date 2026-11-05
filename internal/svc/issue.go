package svc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"necorouter/internal/config"
)

type startupBannerSvc struct{}

func (*startupBannerSvc) Name() string { return "banner" }
func (*startupBannerSvc) Apply(env *Env, c *config.Config) ([]string, error) {
	if err := SyncStartupBanner(env.P, !c.BannerStartupOff); err != nil {
		return []string{"banner: " + err.Error()}, nil
	}
	return nil, nil
}
func (*startupBannerSvc) Stop(*Env) error { return nil }
func (*startupBannerSvc) Status(env *Env) string {
	return "managed (console and SSH)"
}

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
	return SyncIssueEnabled(p, true)
}

func SyncIssueEnabled(p Paths, enabled bool) error {
	// no banner file or it is empty: no banner is shown (empty /etc/issue)
	var want []byte
	if enabled {
		if b, err := os.ReadFile(p.Banner()); err == nil && strings.TrimSpace(string(b)) != "" {
			want = []byte(issueText(string(b)))
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
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

// SyncStartupBanner applies the pre-login banner setting to the local console
// and to the managed sshd Banner directive.
func SyncStartupBanner(p Paths, enabled bool) error {
	if err := SyncIssueEnabled(p, enabled); err != nil {
		return err
	}
	return syncSSHBanner(p, enabled)
}

const (
	sshTopBegin = "# BEGIN necorouter-top"
	sshTopEnd   = "# END necorouter-top"
)

func syncSSHBanner(p Paths, enabled bool) error {
	path := filepath.Join(p.Root, "etc/ssh/sshd_config")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	begin, end := -1, -1
	for i, line := range lines {
		if line == sshTopBegin {
			begin = i
		}
		if line == sshTopEnd && begin >= 0 {
			end = i
			break
		}
	}
	if begin < 0 || end < 0 {
		return nil // do not edit an sshd_config that is not managed by the installer
	}
	want := "Banner none"
	if enabled {
		want = "Banner /etc/necorouter/banner"
	}
	var out []string
	for i, line := range lines {
		if i == end {
			out = append(out, want)
		}
		if i > begin && i < end && strings.HasPrefix(strings.TrimSpace(line), "Banner ") {
			continue
		}
		out = append(out, line)
	}
	data := []byte(strings.Join(out, "\n") + "\n")
	if string(data) == string(b) {
		return nil
	}
	mode := os.FileMode(0o644)
	st, statErr := os.Stat(path)
	if statErr == nil {
		mode = st.Mode().Perm()
	}
	tmp := path + ".necorouter.tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if statErr == nil {
		if stat, ok := st.Sys().(*syscall.Stat_t); ok {
			if err := os.Chown(tmp, int(stat.Uid), int(stat.Gid)); err != nil {
				_ = os.Remove(tmp)
				return err
			}
		}
		if err := os.Chmod(tmp, mode); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	sshd := find("sshd")
	if sshd == "" {
		_ = os.Remove(tmp)
		return fmt.Errorf("sshd is not installed; cannot validate its configuration")
	}
	if output, err := exec.Command(sshd, "-t", "-f", tmp).CombinedOutput(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("sshd rejected the banner configuration: %v: %s", err, strings.TrimSpace(string(output)))
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if p.Root == "" {
		if restorecon, err := exec.LookPath("restorecon"); err == nil {
			_ = exec.Command(restorecon, "-F", path).Run()
		}
	}
	if p.Root != "" { // unit tests use an isolated root and must not reload the host sshd
		return nil
	}
	if err := reloadSSHD(); err != nil {
		return fmt.Errorf("banner config saved but sshd reload failed: %w", err)
	}
	return nil
}

func reloadSSHD() error {
	for _, args := range [][]string{{"systemctl", "reload", "sshd"}, {"systemctl", "reload", "ssh"}, {"rc-service", "sshd", "reload"}, {"rc-service", "ssh", "reload"}} {
		bin, err := exec.LookPath(args[0])
		if err != nil {
			continue
		}
		if err := exec.Command(bin, args[1:]...).Run(); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no sshd reload command succeeded")
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
