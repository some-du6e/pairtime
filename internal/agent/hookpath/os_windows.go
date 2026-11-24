// SPDX-License-Identifier: AGPL-3.0-or-later

package hookpath

import (
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/windows"
)

func readOS() ([]platform.Proc, error) { return windows.Processes() }

func readChainOS(pid int) ([]platform.Proc, error) { return tableChain(pid) }

func selfOS() (platform.Proc, error) { return windows.Self() }
