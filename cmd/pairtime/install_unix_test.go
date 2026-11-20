// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"syscall"
)

// runValue is Windows' login item, which no other OS has.
func runValue() (string, bool) { return "", false }

func deleteRunValue() {}

// processAlive reports whether the process pid is running. A process that
// has ended but that its parent hasn't waited for yet, a zombie, isn't.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	// Linux says in /proc when the process is a zombie: state Z, after the
	// name in parentheses.
	if stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		if i := bytes.LastIndexByte(stat, ')'); i >= 0 && i+2 < len(stat) && stat[i+2] == 'Z' {
			return false
		}
	}
	return true
}
