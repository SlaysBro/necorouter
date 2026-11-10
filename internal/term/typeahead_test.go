package term

import (
	"os"
	"testing"
)

// Input typed while a command runs is read before anything else from the terminal.
func TestTypeaheadIsReadFirst(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "in")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("zz"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	e := NewEditor(f, os.Stdout)
	e.Typeahead([]byte("ab"))
	for _, want := range []rune{'a', 'b', 'z'} {
		r, err := e.readRune()
		if err != nil || r != want {
			t.Fatalf("got %q (%v), want %q", r, err, want)
		}
	}
}
