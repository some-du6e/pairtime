// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import "github.com/some-du6e/pairtime/internal/platform/windows"

// optIntoEcoQoS asks Windows to run the agent on its most efficient
// frequencies and cores (DESIGN 3.2; CP 2.2).
func optIntoEcoQoS() error {
	return windows.SetEcoQoS()
}
