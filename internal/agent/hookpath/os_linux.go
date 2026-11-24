// SPDX-License-Identifier: AGPL-3.0-or-later

package hookpath

import (
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/linux"
)

func readOS() ([]platform.Proc, error) { return linux.Processes() }

func readChainOS(pid int) ([]platform.Proc, error) { return linux.Chain(pid) }

func selfOS() (platform.Proc, error) { return linux.Self() }
