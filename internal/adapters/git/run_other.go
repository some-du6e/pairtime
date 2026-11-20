// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package git

import "os/exec"

// hideWindow does nothing outside Windows, where no console window opens.
func hideWindow(*exec.Cmd) {}
