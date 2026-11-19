// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"

	"github.com/some-du6e/pairtime/internal/version"
)

func init() {
	register(&command{name: "version", summary: "Print the version and build details", run: runVersion})
}

func runVersion(_ context.Context, e *env, args []string) int {
	if len(args) > 0 {
		fmt.Fprintf(e.stderr, "pairtimed: version takes no arguments, got %q\n", args)
		return 2
	}
	fmt.Fprintf(e.stdout, "pairtimed %s\n", version.Get())
	return 0
}
