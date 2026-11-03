package main

import (
	_ "embed"
	"strings"

	"necorouter/internal/cli"
)

// rawVersion is the version string from cmd/necorouter/VERSION. It is embedded at compile time,
// so every build (make or plain `go build`) reports the same version.
//
//go:embed VERSION
var rawVersion string

func init() {
	if v := strings.TrimSpace(rawVersion); v != "" {
		cli.Version = v
	}
}
