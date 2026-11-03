package term

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func mk(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func drive(text string, rows int, fixed int, keys ...int) string {
	var out bytes.Buffer
	i := 0
	runPager(&out, text, fixed, func() int {
		if i >= len(keys) {
			return KeyQuit
		}
		k := keys[i]
		i++
		return k
	}, func() (int, int) { return rows, 80 }, func() string { return "line 30" })
	return out.String()
}

func TestWrap(t *testing.T) {
	got := wrapLines(strings.Repeat("x", 25)+"\n", 10)
	if len(got) != 3 || got[2] != "xxxxx" {
		t.Fatalf("%q", got)
	}
}

func TestShortOutputNotPaged(t *testing.T) {
	o := drive(mk(5), 24, 0)
	if strings.Contains(o, "More") || !strings.Contains(o, "line 5") {
		t.Fatalf("%q", o)
	}
}

func TestPagerNavigation(t *testing.T) {
	p := &pager{lines: wrapLines(mk(50), 80), window: 10}
	if p.handle(KeySpace, "") || p.top != 10 {
		t.Fatal(p.top)
	}
	p.handle(KeyPageUp, "")
	p.handle(KeyPageUp, "")
	if p.top != 0 {
		t.Fatal("clamp low", p.top)
	}
	p.handle(KeyEnd, "")
	if p.top != 40 || !p.atEnd() {
		t.Fatal(p.top)
	}
	if !p.handle(KeySpace, "") {
		t.Fatal("space at end must quit")
	}
	p.handle(KeyHome, "")
	p.handle(KeyLineDown, "")
	p.handle(KeyLineUp, "")
	p.handle(KeyLineUp, "")
	if p.top != 0 {
		t.Fatal(p.top)
	}
	p.handle(KeySearch, "line 30")
	if p.top != 29 {
		t.Fatal("search", p.top)
	}
	p.handle(KeySearch, "nope")
	if p.notice == "" {
		t.Fatal("no notice")
	}
	p.handle(KeyHome, "")
	if !p.handle(KeyQuit, "") {
		t.Fatal("quit")
	}
}

func TestPagerRender(t *testing.T) {
	o := drive(mk(50), 11, 0)
	if !strings.Contains(o, "line 1\r\n") || !strings.Contains(o, "line 10\r\n") || strings.Contains(o, "line 11\r\n") {
		t.Fatalf("first page wrong: %q", o)
	}
	if !strings.Contains(o, "--More--") {
		t.Fatal("no status")
	}
	o = drive(mk(50), 11, 0, KeySpace)
	if !strings.Contains(o, "line 20\r\n") {
		t.Fatal("next page")
	}
	o = drive(mk(50), 11, 5, KeyEnd)
	if !strings.Contains(o, "line 50\r\n") || !strings.Contains(o, "(END)") {
		t.Fatalf("%q", o)
	}
}
