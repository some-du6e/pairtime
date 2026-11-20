// SPDX-License-Identifier: AGPL-3.0-or-later

package procs

import (
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/windows"
)

func readOS() ([]platform.Proc, error) { return windows.Processes() }

func selfOS() string { return windows.SelfOwner() }

// bootOS is empty: Windows records a process's creation time when it
// starts, so a process's ID can't come back on a later boot.
func bootOS() string { return "" }
