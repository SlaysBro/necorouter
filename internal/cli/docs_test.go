package cli

import (
	"os"
	"regexp"
	"testing"
)

// The command tree embedded in docs/index.html must match the code. Run "make docs" after changing commands.
func TestDocsTreeMatchesCode(t *testing.T) {
	page, err := os.ReadFile("../../docs/index.html")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)<script type="application/json" id="tree-data">(.*?)</script>`).FindSubmatch(page)
	if m == nil {
		t.Fatal("docs/index.html has no tree-data block")
	}
	want, err := DocTreeJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(m[1]) != string(want) {
		t.Fatal("docs/index.html is out of date with the command tree: run make docs")
	}
}

// The access table in docs/index.html must match the roles declared on the commands. Run "make docs".
func TestDocsAccessTableMatchesCode(t *testing.T) {
	page, err := os.ReadFile("../../docs/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "ru"} {
		want, err := ReplaceAccessBlock(page, lang)
		if err != nil {
			t.Fatal(err)
		}
		if string(want) != string(page) {
			t.Fatalf("the access table (%s) in docs/index.html is out of date with the commands: run make docs", lang)
		}
	}
}
