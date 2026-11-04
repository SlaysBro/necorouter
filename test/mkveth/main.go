//go:build iproute2test

// mkveth creates a veth pair and brings lo up (test bench tool).
package main

import (
	"fmt"
	"os"
	"os/exec"
)

func ip(args ...string) error {
	out, err := exec.Command("ip", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip %v: %w: %s", args, err, out)
	}
	return nil
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: mkveth A B")
		os.Exit(2)
	}
	for _, args := range [][]string{{"link", "set", "lo", "up"}, {"link", "add", os.Args[1], "type", "veth", "peer", "name", os.Args[2]}, {"link", "set", "dev", os.Args[1], "up"}, {"link", "set", "dev", os.Args[2], "up"}} {
		if err := ip(args...); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
