// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux && !darwin && !windows

package main

import (
	"errors"

	"github.com/some-du6e/pairtime/internal/platform"
)

// processTable reads this computer's processes, for agentRunning. pairtime
// has no reader for this OS.
func processTable() ([]platform.Proc, error) {
	return nil, errors.New("pairtime can't read this OS's processes")
}
