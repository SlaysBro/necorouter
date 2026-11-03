package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const fakeReleases = `[
 {"tag_name":"v0.22-beta.1","prerelease":true,"draft":false,"published_at":"2026-11-02T10:00:00Z"},
 {"tag_name":"v0.21","prerelease":false,"draft":false,"published_at":"2026-11-01T10:00:00Z"},
 {"tag_name":"v0.20","prerelease":false,"draft":false,"published_at":"2026-10-20T10:00:00Z"},
 {"tag_name":"v0.99-draft","prerelease":false,"draft":true,"published_at":"2026-11-03T10:00:00Z"}
]`

// setupUpdate points the update code at a fake GitHub API and a temporary installer directory.
func setupUpdate(t *testing.T, version string) *[][]string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/SlaysBro/necorouter/releases" {
			http.NotFound(w, r)
			return
		}
		var all []map[string]any
		_ = json.Unmarshal([]byte(fakeReleases), &all)
		if n, err := strconv.Atoi(r.URL.Query().Get("per_page")); err == nil && n < len(all) {
			all = all[:n] // like the real API
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(all)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("NECOROUTER_API_BASE", srv.URL)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "source.conf"), []byte("REPO=SlaysBro/necorouter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldDir, oldVer, oldHook := updateShareDir, Version, updateRunHook
	updateShareDir, Version = dir, version
	t.Cleanup(func() { updateShareDir, Version, updateRunHook = oldDir, oldVer, oldHook })

	var calls [][]string
	updateRunHook = func(args []string) error { calls = append(calls, args); return nil }
	return &calls
}

func TestCmpVer(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.21", "v0.21", 0},
		{"v0.22", "v0.21", 1},
		{"v0.9", "v0.21", -1},
		{"v0.22-beta.1", "v0.22", -1},
		{"v0.22", "v0.22-beta.1", 1},
		{"v0.21.1", "v0.21", 1},
	}
	for _, c := range cases {
		if got := cmpVer(c.a, c.b); got != c.want {
			t.Errorf("cmpVer(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestUpdateCheckAndList(t *testing.T) {
	setupUpdate(t, "0.20")
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	out := runCmd(s, "update check")
	for _, w := range []string{"current:           v0.20", "latest stable:     v0.21", "latest pre-release: v0.22-beta.1", "newer stable release"} {
		if !strings.Contains(out, w) {
			t.Fatalf("update check misses %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "v0.99") {
		t.Fatal("drafts must not be listed")
	}
	out = runCmd(s, "update list 2")
	if !strings.Contains(out, "v0.22-beta.1") || !strings.Contains(out, "pre-release") || strings.Contains(out, "v0.20") {
		t.Fatalf("update list 2:\n%s", out)
	}
}

func TestUpdateInstallConfirmsAndDowngradeWarns(t *testing.T) {
	calls := setupUpdate(t, "0.21")
	r := newTestRouter(t)
	s := sessionOn(r, "root")

	if out := runCmd(s, "update"); !strings.Contains(out, "already on the latest release") || len(*calls) != 0 {
		t.Fatalf("update on the latest release must do nothing: %q", out)
	}
	runCmd(s, "update to v0.20")
	if !strings.Contains(s.Prompt(), "downgrade to v0.20?") {
		t.Fatalf("downgrade must ask: prompt=%q", s.Prompt())
	}
	runCmd(s, "n")
	if len(*calls) != 0 {
		t.Fatalf("answer n must not run the installer: %v", *calls)
	}

	runCmd(s, "update to v0.20")
	runCmd(s, "y")
	if len(*calls) != 1 || strings.Join((*calls)[0], " ") != "--version v0.20 --no-deps" {
		t.Fatalf("installer args: %v", *calls)
	}
}

func TestUpdateRejectsBadAndUnknownTags(t *testing.T) {
	setupUpdate(t, "0.21")
	r := newTestRouter(t)
	s := sessionOn(r, "root")
	if out := runCmd(s, "update to ../etc"); !strings.Contains(out, "bad release tag") {
		t.Fatalf("bad tag accepted: %q", out)
	}
	if out := runCmd(s, "update to v9.9"); !strings.Contains(out, "not found") {
		t.Fatalf("unknown tag accepted: %q", out)
	}
}
