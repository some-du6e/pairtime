// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux && !darwin && !windows

package clock

// monoNow gives no monotonic reading, so stamps carry neither one.
func monoNow() (int64, string) { return 0, "" }
