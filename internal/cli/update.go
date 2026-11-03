package cli

// Updating necorouter from GitHub releases (privileged mode):
//   update check            current version, the latest stable release and the latest pre-release
//   update list [N]         the newest N releases (default 10, up to 50)
//   update                  install the latest stable release (confirmation)
//   update pre              install the newest pre-release (confirmation)
//   update to TAG           install a given release; an older release is a downgrade (confirmation)
//
// The installer copied to /usr/local/share/necorouter/packaging does the work. It runs in a separate
// process (systemd-run or setsid), so the service restart it performs does not stop it.
// Its output goes to update.log in the log directory.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"necorouter/internal/logx"
)

var (
	updateShareDir = "/usr/local/share/necorouter/packaging" // where the installer is kept (set by install.sh)
	updateRunHook  func(args []string) error                 // tests: replaces running the installer
)

type relInfo struct {
	Tag, Date string
	Pre       bool
}

var tagRe = regexp.MustCompile(`^v?\d+(\.\d+)+(-[0-9A-Za-z.]+)?$`)

func updateAPIBase() string {
	if v := os.Getenv("NECOROUTER_API_BASE"); v != "" {
		return v
	}
	return "https://api.github.com"
}

// updateRepo reads REPO from source.conf next to the installer.
func updateRepo() (string, error) {
	b, err := os.ReadFile(filepath.Join(updateShareDir, "source.conf"))
	if err != nil {
		return "", fmt.Errorf("update source is not installed: %s/source.conf is missing", updateShareDir)
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(ln), "REPO="); ok && v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("source.conf does not name a repository")
}

// fetchReleases returns the newest n published releases (drafts are skipped).
func fetchReleases(n int) ([]relInfo, error) {
	repo, err := updateRepo()
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=%d", updateAPIBase(), repo, n)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("cannot reach GitHub: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var raw []struct {
		TagName     string `json:"tag_name"`
		Prerelease  bool   `json:"prerelease"`
		Draft       bool   `json:"draft"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("unexpected answer from GitHub: %v", err)
	}
	var out []relInfo
	for _, r := range raw {
		if r.Draft || r.TagName == "" {
			continue
		}
		date := r.PublishedAt
		if len(date) >= 10 {
			date = date[:10]
		}
		out = append(out, relInfo{Tag: r.TagName, Date: date, Pre: r.Prerelease})
	}
	return out, nil
}

// cmpVer compares version tags such as "0.21", "v0.21" and "v0.22-beta.1".
// A pre-release is older than the release with the same numbers.
func cmpVer(a, b string) int {
	na, pa := verParts(a)
	nb, pb := verParts(b)
	for i := 0; i < len(na) || i < len(nb); i++ {
		x, y := at(na, i), at(nb, i)
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case pa && !pb:
		return -1
	case !pa && pb:
		return 1
	}
	return 0
}

func verParts(tag string) ([]int, bool) {
	core, suffix, hasPre := strings.Cut(strings.TrimPrefix(tag, "v"), "-")
	var nums []int
	for _, p := range strings.Split(core, ".") {
		n, _ := strconv.Atoi(p)
		nums = append(nums, n)
	}
	return nums, hasPre && suffix != ""
}

func at(l []int, i int) int {
	if i < len(l) {
		return l[i]
	}
	return 0
}

func normTag(t string) string {
	if !strings.HasPrefix(t, "v") {
		return "v" + t
	}
	return t
}

func (s *Session) updateCheck() error {
	rels, err := fetchReleases(30)
	if err != nil {
		return err
	}
	var stable, pre *relInfo
	for i := range rels {
		if rels[i].Pre && pre == nil {
			pre = &rels[i]
		}
		if !rels[i].Pre && stable == nil {
			stable = &rels[i]
		}
	}
	s.Printf("current:           v%s\n", Version)
	s.Printf("latest stable:     %s\n", describe(stable))
	s.Printf("latest pre-release: %s\n", describe(pre))
	if stable != nil && cmpVer(stable.Tag, Version) > 0 {
		s.Printf("%% A newer stable release is available: update\n")
	}
	return nil
}

func describe(r *relInfo) string {
	if r == nil {
		return "none"
	}
	return fmt.Sprintf("%s (%s)", r.Tag, r.Date)
}

func (s *Session) updateList(n int) error {
	rels, err := fetchReleases(n)
	if err != nil {
		return err
	}
	if len(rels) == 0 {
		s.Printf("no releases\n")
		return nil
	}
	for _, r := range rels {
		kind := "release"
		if r.Pre {
			kind = "pre-release"
		}
		s.Printf("%-16s %-12s %s\n", r.Tag, kind, r.Date)
	}
	return nil
}

// updateInstall asks for confirmation (a downgrade gets its own warning) and then runs the installer.
func (s *Session) updateInstall(tag string) error {
	tag = normTag(tag)
	if cmpVer(tag, Version) < 0 {
		return s.ask(s.saveWarning()+fmt.Sprintf("%% %s is older than the current v%s: this is a downgrade.\n", tag, Version),
			fmt.Sprintf("downgrade to %s?", tag), func(s *Session) error { return s.startUpdate(tag) })
	}
	return s.ask(s.saveWarning(), fmt.Sprintf("install %s?", tag), func(s *Session) error { return s.startUpdate(tag) })
}

func (s *Session) updateLatest(pre bool) error {
	rels, err := fetchReleases(30)
	if err != nil {
		return err
	}
	for _, r := range rels {
		if r.Pre == pre {
			if !pre && cmpVer(r.Tag, Version) == 0 {
				s.Printf("already on the latest release (v%s)\n", Version)
				return nil
			}
			return s.updateInstall(r.Tag)
		}
	}
	if pre {
		return fmt.Errorf("no pre-release found")
	}
	return fmt.Errorf("no stable release found")
}

func (s *Session) updateTo(tag string) error {
	if !tagRe.MatchString(tag) {
		return fmt.Errorf("bad release tag %q (for example v0.21 or v0.22-beta.1)", tag)
	}
	tag = normTag(tag)
	rels, err := fetchReleases(50)
	if err != nil {
		return err
	}
	for _, r := range rels {
		if r.Tag == tag {
			return s.updateInstall(tag)
		}
	}
	return fmt.Errorf("release %s not found", tag)
}

// startUpdate runs the installer for tag in a separate process.
func (s *Session) startUpdate(tag string) error {
	args := []string{"--version", tag, "--no-deps"}
	logx.Auditf(s.User, "update to %s started", tag)
	if updateRunHook != nil {
		return updateRunHook(args)
	}
	installer := filepath.Join(updateShareDir, "install.sh")
	if _, err := os.Stat(installer); err != nil {
		return fmt.Errorf("installer not found: %s", installer)
	}
	log := filepath.Join(s.R.P.LogDir(), "update.log")
	if err := os.MkdirAll(s.R.P.LogDir(), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(log, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()
	var cmd *exec.Cmd
	if p, err := exec.LookPath("systemd-run"); err == nil && systemdActive() {
		cmd = exec.Command(p, append([]string{"--unit=necorouter-update", "--collect",
			"--property=StandardOutput=append:" + log, "--property=StandardError=append:" + log,
			"sh", installer}, args...)...)
	} else {
		p, err := exec.LookPath("setsid")
		if err != nil {
			return fmt.Errorf("neither systemd-run nor setsid is available to start the update")
		}
		cmd = exec.Command(p, append([]string{"sh", installer}, args...)...)
		cmd.Stdout, cmd.Stderr = f, f
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot start the update: %v", err)
	}
	logx.Infof("UPDATE", "update to %s started; output in %s", tag, log)
	s.Printf("%% Update to %s started in the background. The router restarts and sessions end.\n", tag)
	s.Printf("%% Progress: %s. After the restart check: show version\n", log)
	return nil
}

func systemdActive() bool {
	st, err := os.Stat("/run/systemd/system")
	return err == nil && st.IsDir()
}

func updateNode() *Node {
	return K("update", "Check and install necorouter releases from GitHub",
		K("check", "Show the current version and the latest releases").R(func(s *Session, _ []string) error {
			return s.updateCheck()
		}),
		K("list", "List recent releases",
			A(KInt, "N", "Number of releases (1-50)").Rng(1, 50).R(func(s *Session, v []string) error {
				n, _ := strconv.Atoi(v[0])
				return s.updateList(n)
			}),
		).R(func(s *Session, _ []string) error { return s.updateList(10) }),
		K("pre", "Install the newest pre-release").R(func(s *Session, _ []string) error {
			return s.updateLatest(true)
		}),
		K("to", "Install a given release (an older one is a downgrade)",
			A(KWord, "TAG", "Release tag, for example v0.21").R(func(s *Session, v []string) error {
				return s.updateTo(v[0])
			})),
	).R(func(s *Session, _ []string) error { return s.updateLatest(false) })
}
