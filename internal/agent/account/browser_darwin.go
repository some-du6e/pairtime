// SPDX-License-Identifier: AGPL-3.0-or-later

package account

import "os/exec"

// browserCommand opens u in the default browser.
func browserCommand(u string) (*exec.Cmd, error) {
	return exec.Command("open", u), nil
}
