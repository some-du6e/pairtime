// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package main

import (
	"errors"
	"os"
)

// errRootElsewhere means install or uninstall runs as root in a home
// directory that root doesn't own.
var errRootElsewhere = errors.New("this runs as root, through sudo or su, and the files it wrote in your home folder would belong to root, out of reach of your own pairtime and Claude Code; run it again without sudo")

// checkUser refuses to run as root in a home directory that root doesn't
// own, as `sudo pairtime install` does on macOS, whose sudo keeps HOME.
// Every file it wrote there, settings.json among them, would belong to
// root, and the agent it started would run as root, while pairtime never
// needs admin rights (DESIGN 3.2). The hook refuses the same (mayWriteData).
// Root setting pairtime up in its own home directory is root's own
// pairtime.
func checkUser(home string) error { return checkUID(os.Geteuid(), home, ownerOf) }

// checkUID is checkUser for the effective user euid, with owner saying who
// owns a path.
func checkUID(euid int, home string, owner func(string) (int, error)) error {
	if euid == 0 && !ownsPath(home, 0, owner) {
		return errRootElsewhere
	}
	return nil
}

// protectDir makes dir this user's only, as home makes its directories.
func protectDir(dir string) error { return os.Chmod(dir, 0o700) }
