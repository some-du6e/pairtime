// SPDX-License-Identifier: AGPL-3.0-or-later

package account

import "os/exec"

// browserCommand opens u with the handler Windows has for its scheme.
func browserCommand(u string) (*exec.Cmd, error) {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", u), nil
}
