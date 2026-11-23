// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "github.com/some-du6e/pairtime/internal/platform/darwin"

// lowerPriority puts lookback's reader in macOS's background state
// (darwin.Background).
func lowerPriority() error { return darwin.Background() }
