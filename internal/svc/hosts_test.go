package svc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateHosts: our own line is replaced, foreign lines are kept, repeating does not duplicate.
func TestUpdateHosts(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "etc", "hosts")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("127.0.0.1 localhost\n10.0.0.5 db\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := &Env{P: Paths{Root: root}}
	if err := updateHosts(env, "hq-rtr.au-team.irpo"); err != nil {
		t.Fatal(err)
	}
	if err := updateHosts(env, "hq-rtr.au-team.irpo"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	got := string(b)
	if strings.Count(got, "# necorouter") != 1 || !strings.Contains(got, "127.0.1.1\thq-rtr.au-team.irpo hq-rtr # necorouter") {
		t.Fatalf("entry wrong:\n%s", got)
	}
	if !strings.Contains(got, "127.0.0.1 localhost") || !strings.Contains(got, "10.0.0.5 db") {
		t.Fatalf("foreign lines lost:\n%s", got)
	}
	if err := updateHosts(env, "br-rtr"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	got = string(b)
	if strings.Contains(got, "hq-rtr") || !strings.Contains(got, "127.0.1.1\tbr-rtr # necorouter") || strings.Count(got, "# necorouter") != 1 {
		t.Fatalf("rename wrong:\n%s", got)
	}
}
