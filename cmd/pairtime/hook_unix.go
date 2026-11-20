// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"

	"github.com/some-du6e/pairtime/internal/agent/home"
)

// mayWriteData reports whether this hook may make and write pairtime's
// files in d: whether its effective user owns the data directory, or the
// nearest directory above it while it doesn't exist. Under `sudo claude`
// the hook runs as root with the user's HOME, which macOS's sudoers keeps,
// so it finds the user's data directory; files root made there would
// lock the user's own agent out of them.
func mayWriteData(d home.Dirs) bool { return ownsPath(d.Data, os.Geteuid(), ownerOf) }

// mayStartAgent reports whether this hook may start the agent on d, by the
// same rule: an agent started as root would run as root on the user's
// files.
func mayStartAgent(d home.Dirs) bool { return mayWriteData(d) }

// ownerOf returns the uid that owns path.
func ownerOf(path string) (int, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("%s: no owner in its file info", path)
	}
	return int(st.Uid), nil
}
