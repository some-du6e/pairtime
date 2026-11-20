// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/some-du6e/pairtime/internal/install"
)

// runValue returns pairtime's Run value under HKEY_CURRENT_USER, which is
// its login item on Windows, and whether it is there.
func runValue() (string, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, install.RunKey, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(install.RunValue)
	return v, err == nil
}

// deleteRunValue deletes pairtime's Run value.
func deleteRunValue() {
	k, err := registry.OpenKey(registry.CURRENT_USER, install.RunKey, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	k.DeleteValue(install.RunValue)
}

// stillActive is the exit code GetExitCodeProcess gives a process that
// hasn't ended (STILL_ACTIVE).
const stillActive = 259

// processAlive reports whether the process pid is running.
func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
