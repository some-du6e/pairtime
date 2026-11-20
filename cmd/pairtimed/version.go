// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"

	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/version"
)

func init() {
	register(&command{name: "version", summary: "Print the version, the build details and the config_version of pairtime.toml", run: runVersion})
}

func runVersion(_ context.Context, e *env, args []string) int {
	if len(args) > 0 {
		fmt.Fprintf(e.stderr, "pairtimed: version takes no arguments, got %q\n", args)
		return 2
	}
	fmt.Fprintf(e.stdout, "pairtimed %s\n", version.Get())
	// A pairtime.toml that uses a key this release added sets this
	// config_version, so the release before it can start on the file.
	fmt.Fprintf(e.stdout, "pairtime.toml config_version %d\n", config.ConfigVersion)
	return 0
}
