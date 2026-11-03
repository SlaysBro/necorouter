package term

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// Password input: the text is not repeated in the output (no echo).
func TestSecretNoEcho(t *testing.T) {
	r, w, _ := os.Pipe()
	_, _ = w.WriteString("Sup3rS3c\n")
	w.Close()
	var out bytes.Buffer
	e := NewEditor(r, &out)
	e.Secret = true
	got, err := e.ReadLine("Password: ", Hooks{})
	if err != nil || got != "Sup3rS3c" {
		t.Fatalf("got %q, %v", got, err)
	}
	if strings.Contains(out.String(), "Sup3rS3c") || !strings.Contains(out.String(), "Password: ") {
		t.Fatalf("password echoed or prompt missing: %q", out.String())
	}
}
