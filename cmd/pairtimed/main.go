// SPDX-License-Identifier: AGPL-3.0-or-later

// Command pairtimed is the pairtime server.
//
// This is the WP-0a stub. It knows `version` and `help` and nothing else.
// Ingest, accounts, rollups and the dashboard come in later work packages.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/some-du6e/pairtime/internal/version"
)

const usage = `pairtimed is the pairtime server: accounts, projects, rollups and the live
dashboard for time the pairtime client has scored on each device.

Usage:
  pairtimed <command>

Commands:
  version   Print the version and build details
  help      Show this help

This build is a skeleton and doesn't serve anything yet.
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
			fmt.Fprintf(stderr, "pairtimed: version takes no arguments, got %q\n", args[1:])
			return 2
		}
		fmt.Fprintf(stdout, "pairtimed %s\n", version.Get())
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "pairtimed: unknown command %q\n\n", args[0])
		fmt.Fprint(stderr, usage)
		return 2
	}
}
