// SPDX-License-Identifier: AGPL-3.0-or-later

package procs

import (
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/darwin"
)

func readOS() ([]platform.Proc, error) { return darwin.Processes() }

func selfOS() string { return darwin.SelfOwner() }

// bootOS is empty: macOS records a process's start in wall-clock time when
// it starts, so a process's ID can't come back on a later boot.
func bootOS() string { return "" }
