// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows && !darwin

package account

import (
	"os"
	"os/exec"
)

// openWithCode is false here: xdg-open, and the browser it starts, keep
// the URL in their command lines, which every account on the computer can
// read in /proc on most systems, and a user code there lets anyone signed
// in to the server approve it first. So the browser gets the page that
// asks for the code, and the terminal shows the code.
const openWithCode = false

// browserCommand opens u with xdg-open, when there is a desktop session to
// open it in.
func browserCommand(u string) (*exec.Cmd, error) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return nil, ErrNoBrowser
	}
	path, err := exec.LookPath("xdg-open")
	if err != nil {
		return nil, ErrNoBrowser
	}
	return exec.Command(path, u), nil
}
