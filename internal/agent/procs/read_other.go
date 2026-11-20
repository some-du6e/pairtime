// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux && !darwin && !windows

package procs

import (
	"errors"

	"github.com/some-du6e/pairtime/internal/platform"
)

func readOS() ([]platform.Proc, error) {
	return nil, errors.New("pairtime can't read the process table on this OS")
}
