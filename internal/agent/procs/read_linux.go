// SPDX-License-Identifier: AGPL-3.0-or-later

package procs

import (
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/linux"
)

func readOS() ([]platform.Proc, error) { return linux.Processes() }
