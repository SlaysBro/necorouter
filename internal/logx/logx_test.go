package logx

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestWriteReadRotate(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	SetDefault(l)
	defer SetDefault(nil)
	Infof("TEST", "hello\tworld [x]")
	Auditf("admin", "show version")
	es, _, err := ReadAll(dir)
	if err != nil || len(es) != 2 {
		t.Fatalf("entries=%v err=%v", es, err)
	}
	if es[0].Svc != "TEST" || es[0].Level != LevelInfo || !strings.Contains(es[0].Msg, "hello world (x)") && !strings.Contains(es[0].Msg, "hello world [x]") {
		t.Fatalf("bad entry %+v", es[0])
	}
	if es[1].Svc != "IMISH" || es[1].Msg != "AUDIT [admin] show version" {
		t.Fatalf("bad audit %+v", es[1])
	}
	if !strings.Contains(es[0].Src, "logx_test.go:") {
		t.Fatalf("source %q", es[0].Src)
	}
	if strings.Contains(es[0].String(false), "logx_test.go") || !strings.Contains(es[0].String(true), "[logx_test.go:") {
		t.Fatalf("excessive format wrong: %q / %q", es[0].String(false), es[0].String(true))
	}
	// rotation: write more than maxFileSize, nothing is lost within the retained files
	big := strings.Repeat("x", 1000)
	for i := 0; i < 3000; i++ {
		Infof("BIG", "%d %s", i, big)
	}
	es, _, _ = ReadAll(dir)
	if len(es) < 1000 || es[len(es)-1].Msg[:4] != "2999" {
		t.Fatalf("after rotation: %d entries", len(es))
	}
}

func TestFollow(t *testing.T) {
	dir := t.TempDir()
	l, _ := Open(dir, nil)
	SetDefault(l)
	defer SetDefault(nil)
	Infof("A", "old")
	_, off, _ := ReadAll(dir)
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan string, 4)
	done := make(chan struct{})
	go func() {
		Follow(ctx, dir, off, func(e Entry) { got <- e.Msg })
		close(done)
	}()
	Infof("A", "new")
	select {
	case m := <-got:
		if m != "new" {
			t.Fatalf("got %q", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no new entry")
	}
	cancel()
	<-done
}

func TestNilDefaultIsSilent(t *testing.T) {
	SetDefault(nil)
	Infof("X", "nothing")
	Auditf("u", "nothing")
}
