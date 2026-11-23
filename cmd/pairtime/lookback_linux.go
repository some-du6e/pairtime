// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "github.com/some-du6e/pairtime/internal/platform/linux"

// lowerPriority puts lookback's reader at nice 19 and in the idle I/O
// class, on every thread (linux.Background).
func lowerPriority() error { return linux.Background() }
