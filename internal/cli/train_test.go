package cli

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"necorouter/internal/config"
)

func TestTrainStaticWhenNotTTY(t *testing.T) {
	r := newTestRouter(t)
	s := NewSession(r, &config.User{Name: "a", Role: "admin"})
	var sb strings.Builder
	s.Exec("mge", &sb)
	if strings.Contains(sb.String(), "\x1b") || !strings.Contains(sb.String(), "Choo-choo!") || !strings.Contains(sb.String(), "MGE") {
		t.Fatalf("static output: %q", sb.String())
	}
}

func TestTrainAnimationFramesAndCursor(t *testing.T) {
	trainDelay = 0
	defer func() { trainDelay = 45 * time.Millisecond }()
	r := newTestRouter(t)
	s := NewSession(r, &config.User{Name: "a", Role: "admin"})
	s.TTY = true
	out := &syncBuf{}
	s.Exec("mge", out)
	o := out.String()
	rows := trainSmoke + trainBody + 1 + 2
	if !strings.HasPrefix(o, "\x1b[?25l") || !strings.Contains(o, "\x1b[?25h") {
		t.Fatalf("cursor not hidden/restored")
	}
	if n := strings.Count(o, "\x1b[K\n"); n < rows*20 {
		t.Fatalf("too few frames: %d lines", n)
	}
	if !strings.Contains(o, fmt.Sprintf("\x1b[%dA", rows)) {
		t.Fatalf("no cursor-up between frames (rows=%d)", rows)
	}
	if !strings.Contains(o, "(+)") || !strings.Contains(o, "(x)") || !strings.Contains(o, "MGE") {
		t.Fatalf("wheel animation / art missing")
	}
	if !strings.Contains(o, "Have a nice day") {
		t.Fatalf("no final message")
	}
	// all frames have the same height, the canvas is no wider than trainWidth
	for _, l := range trainFrame(3, 5, nil, defaultLabel) {
		if len(l) > trainWidth {
			t.Fatalf("line wider than canvas: %d", len(l))
		}
	}
	if got := len(trainFrame(0, 0, nil, defaultLabel)); got != rows {
		t.Fatalf("frame height %d != %d", got, rows)
	}
}

// Ctrl-C stops the animation, the cursor is restored, other sessions are not blocked.
func TestTrainInterruptAndNonBlocking(t *testing.T) {
	r := newTestRouter(t)
	u := &config.User{Name: "a", Role: "admin"}
	s1, s2 := NewSession(r, u), NewSession(r, u)
	s1.TTY = true
	out := &syncBuf{}
	done := make(chan struct{})
	go func() { s1.Exec("mge", out); close(done) }()
	time.Sleep(300 * time.Millisecond)
	fin := make(chan struct{})
	go func() { s2.Exec("show hostname", io.Discard); close(fin) }()
	select {
	case <-fin:
	case <-time.After(2 * time.Second):
		t.Fatal("CLI blocked during animation")
	}
	s1.Interrupt()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("animation not interrupted")
	}
	if !strings.HasSuffix(strings.TrimSpace(out.String()), "Choo!") && !strings.Contains(out.String(), "\x1b[?25h") {
		t.Fatalf("cursor not restored: %q", out.String()[len(out.String())-40:])
	}
	if !strings.Contains(out.String(), "\x1b[?25h") {
		t.Fatal("cursor not restored")
	}
}

func TestTrainListedInHelp(t *testing.T) {
	r := newTestRouter(t)
	s := NewSession(r, &config.User{Name: "a", Role: "admin"})
	if !strings.Contains(s.Help(""), "mge") {
		t.Fatalf("mge not in help: %s", s.Help(""))
	}
	if got, _ := s.Complete("mg"); got != "mge " {
		t.Fatalf("tab: %q", got)
	}
}

func TestTrainAfterText(t *testing.T) {
	r := newTestRouter(t)
	s := NewSession(r, &config.User{Name: "a", Role: "admin"})
	var sb strings.Builder
	if err := s.Exec(`mge "NECO works"`, &sb); strings.Contains(sb.String(), "%") || err {
		t.Fatalf("unexpected: %q", sb.String())
	}
	out := sb.String()
	if !strings.Contains(out, `NECO works`) {
		t.Fatalf("text after the pass not applied: %q", out)
	}
	if !strings.Contains(out, "MGE") {
		t.Fatalf("tender label must stay fixed: %q", out)
	}
	if strings.Contains(out, "Choo-choo!") {
		t.Fatalf("custom text must be printed alone: %q", out)
	}
	sb.Reset()
	s.Exec("mge 'single quotes'", &sb)
	if !strings.Contains(sb.String(), "single quotes") {
		t.Fatalf("single quotes: %q", sb.String())
	}
	sb.Reset()
	s.Exec("mge", &sb)
	if !strings.Contains(sb.String(), `Choo-choo! Have a nice day.`) {
		t.Fatalf("default text changed: %q", sb.String())
	}
	for _, bad := range []string{
		"mge NECO",                              // without quotes
		`mge "NECO'`,                            // different quotes
		`mge "` + strings.Repeat("x", 65) + `"`, // more than 64
	} {
		sb.Reset()
		s.Exec(bad, &sb)
		if !strings.Contains(sb.String(), "quotes") {
			t.Fatalf("%s: no error: %q", bad, sb.String())
		}
	}
	sb.Reset()
	s.Exec(`mge ""`, &sb)
	if !strings.Contains(sb.String(), "%") {
		t.Fatalf("empty text must be rejected: %q", sb.String())
	}
	sb.Reset()
	s.Exec(`mge "`+strings.Repeat("x", 64)+`"`, &sb)
	if strings.Contains(sb.String(), "%") {
		t.Fatalf("64 characters must be accepted: %q", sb.String())
	}
}

func TestFitLabel(t *testing.T) {
	if got := fitLabel("ab"); len(got) != labelWidth || !strings.Contains(got, "ab") {
		t.Fatalf("%q", got)
	}
	if got := fitLabel(defaultLabel); got != "  MGE   " {
		t.Fatalf("MGE must be centered on the tender: %q", got)
	}
}
