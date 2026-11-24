// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux && !darwin && !windows

package hookpath

import (
	"errors"

	"github.com/some-du6e/pairtime/internal/platform"
)

var errNoTable = errors.New("pairtime can't read the process table on this OS")

func readOS() ([]platform.Proc, error) { return nil, errNoTable }

func readChainOS(int) ([]platform.Proc, error) { return nil, errNoTable }

func selfOS() (platform.Proc, error) { return platform.Proc{}, errNoTable }
