// SPDX-License-Identifier: AGPL-3.0-or-later

package account

import "os/exec"

// openWithCode is true: macOS doesn't let one account read the command
// lines of another's programs, so the browser can get the page with the
// code filled in.
const openWithCode = true

// browserCommand opens u in the default browser.
func browserCommand(u string) (*exec.Cmd, error) {
	return exec.Command("open", u), nil
}
