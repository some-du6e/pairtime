// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux && !darwin && !windows

package hookpath

import (
	"errors"

	"github.com/some-du6e/pairtime/internal/platform"
)

func readOS() ([]platform.Proc, error) {
	return nil, errors.New("pairtime can't read the process table on this OS")
}

// monoNow gives no monotonic reading, so messages carry neither one.
func monoNow() (int64, string) { return 0, "" }
