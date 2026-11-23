// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux

package main

// startInUnit has no systemd to start the agent under off Linux, so
// launchAgent starts it with startDetached, which keeps it out of what
// ends its starter: launchd's kill of a job's process group and a
// terminal's hangup on macOS, and on Windows the starter's job, when the
// job lets it out.
func startInUnit(exe, dir string) (launched, error) {
	return launched{}, errNoUserManager
}
