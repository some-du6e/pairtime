// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/darwin"
)

// processTable reads this computer's processes, for agentRunning.
func processTable() ([]platform.Proc, error) { return darwin.Processes() }
