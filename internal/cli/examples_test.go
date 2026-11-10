package cli

import (
	"os"
	"testing"
)

// The example configurations are applied in full without errors (the same as test/examples.sh).
func TestExampleConfigsApply(t *testing.T) {
	for _, f := range []string{"../../test/fixtures/example-1.conf", "../../test/fixtures/example-2.conf"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		r := newTestRouter(t)
		if errs := r.Replay(string(b)); len(errs) != 0 {
			t.Errorf("%s: %v", f, errs)
		}
	}
}
