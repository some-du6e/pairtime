// SPDX-License-Identifier: AGPL-3.0-or-later

package account

import "os/exec"

// openWithCode is true: Windows doesn't let one account read the command
// lines of another's programs without administrator rights, so the
// browser can get the page with the code filled in.
const openWithCode = true

// browserCommand opens u with the handler Windows has for its scheme.
func browserCommand(u string) (*exec.Cmd, error) {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", u), nil
}
