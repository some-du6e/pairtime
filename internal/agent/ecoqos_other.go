// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package agent

// optIntoEcoQoS has nothing to do outside Windows. On macOS the agent's
// LaunchAgent leaves ProcessType at Background, which is the installer's
// part (DESIGN 3.2).
func optIntoEcoQoS() error {
	return nil
}
