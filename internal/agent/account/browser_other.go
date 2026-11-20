// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows && !darwin

package account

import (
	"os"
	"os/exec"
)

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
