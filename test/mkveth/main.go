//go:build nltest

// mkveth creates a veth pair and brings lo up (test bench tool).
package main

import (
	"fmt"
	"os"

	"necorouter/internal/nl"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: mkveth A B")
		os.Exit(2)
	}
	c, err := nl.Open()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer c.Close()
	_ = c.SetUp("lo", true)
	for _, f := range []func() error{
		func() error { return c.AddVeth(os.Args[1], os.Args[2]) },
		func() error { return c.SetUp(os.Args[1], true) },
		func() error { return c.SetUp(os.Args[2], true) },
	} {
		if err := f(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
