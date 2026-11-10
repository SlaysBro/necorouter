// gendocs writes the command tree into the documentation page (docs/index.html).
// Usage: go run ./cmd/gendocs docs/index.html   (make docs)
package main

import (
	"fmt"
	"os"
	"regexp"

	"necorouter/internal/cli"
)

var treeBlock = regexp.MustCompile(`(?s)(<script type="application/json" id="tree-data">)(.*?)(</script>)`)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gendocs FILE")
		os.Exit(2)
	}
	page, err := os.ReadFile(os.Args[1])
	if err != nil {
		fail(err)
	}
	tree, err := cli.DocTreeJSON()
	if err != nil {
		fail(err)
	}
	if len(treeBlock.FindAll(page, -1)) != 1 {
		fail(fmt.Errorf("%s: the tree-data block is missing or not unique", os.Args[1]))
	}
	out := treeBlock.ReplaceAll(page, []byte("${1}"+string(tree)+"${3}"))
	for _, lang := range []string{"en", "ru"} {
		var err error
		if out, err = cli.ReplaceAccessBlock(out, lang); err != nil {
			fail(err)
		}
	}
	if err := os.WriteFile(os.Args[1], out, 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "gendocs:", err)
	os.Exit(1)
}
