package term

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// Without a terminal the live page just prints the lines as they come.
func TestFollowWithoutTerminalPrintsLines(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "in")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	lines := make(chan string, 3)
	lines <- "one"
	lines <- "two"
	lines <- "three"
	close(lines)
	var out bytes.Buffer
	Follow(f, &out, lines, nil)
	if out.String() != "one\r\ntwo\r\nthree\r\n" {
		t.Fatalf("got %q", out.String())
	}
}

// The status line tells whether the page follows the end or is paused.
func TestPagerStatusShowsFollowMode(t *testing.T) {
	p := &pager{window: 10, mode: "PAUSED"}
	if s := p.status(120); !strings.Contains(s, "PAUSED") || !strings.Contains(s, "Ctrl-C") {
		t.Fatalf("status: %q", s)
	}
	p.mode = "FOLLOW"
	if s := p.status(120); !strings.Contains(s, "FOLLOW") {
		t.Fatalf("status: %q", s)
	}
}
