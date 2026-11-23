// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux && !darwin && !windows

package main

import "errors"

// lowerPriority can't lower lookback's reader's priority: pairtime has no
// call for this OS, so lookback reads nothing here.
func lowerPriority() error { return errors.ErrUnsupported }
