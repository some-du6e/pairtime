// SPDX-License-Identifier: AGPL-3.0-or-later

package hookpath

import (
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/darwin"
)

func readOS() ([]platform.Proc, error) { return darwin.Processes() }

func selfOS() (platform.Proc, error) { return darwin.Self() }
