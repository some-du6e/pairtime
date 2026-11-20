// SPDX-License-Identifier: AGPL-3.0-or-later

package procs

import (
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/linux"
)

func readOS() ([]platform.Proc, error) { return linux.Processes() }

func selfOS() string { return linux.SelfOwner() }

// bootOS is the kernel's boot id, or "" when /proc won't say, in which
// case state.json's entries are taken as this boot's: the first scan drops
// those whose process isn't running.
func bootOS() string {
	id, _ := linux.BootID()
	return id
}
