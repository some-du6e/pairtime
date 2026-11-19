// SPDX-License-Identifier: AGPL-3.0-or-later

// Command pairtime is the pairtime client.
//
// This is the WP-0a stub. It knows `version` and `help` and nothing else.
// The agent, hook, track and install commands come in later work packages.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/some-du6e/pairtime/internal/release"
	"github.com/some-du6e/pairtime/internal/version"
)

const usage = `pairtime counts the hours you spend building with AI: thinking, steering,
reading, testing and fixing. It skips the hours your agent runs on its own.

Usage:
  pairtime <command>

Commands:
  version   Print the version, build details and release keys
  help      Show this help

This build is a skeleton and doesn't track anything yet.
Source and status: https://github.com/some-du6e/pairtime
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes one command line and returns the process exit code:
// 0 on success, 2 for a usage error.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version", "--version":
		if len(args) > 1 {
			fmt.Fprintf(stderr, "pairtime: version takes no arguments, got %q\n", args[1:])
			return 2
		}
		fmt.Fprintf(stdout, "pairtime %s\n", version.Get())
		fmt.Fprintln(stdout, releaseKeys())
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "pairtime: unknown command %q\n\n", args[0])
		fmt.Fprint(stderr, usage)
		return 2
	}
}

// releaseKeys names the release keys compiled into this binary, the only
// keys whose signed update manifests it accepts
// (docs/spec/update-manifest.md).
func releaseKeys() string {
	keys, err := release.TrustedKeys()
	if err != nil {
		return "release keys: unreadable: " + err.Error()
	}
	if len(keys) == 0 {
		return "release keys: none"
	}
	ids := make([]string, len(keys))
	for i, k := range keys {
		ids[i] = k.ID()
	}
	return "release keys: " + strings.Join(ids, ", ")
}
