// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !darwin && !windows

package doctor

import "runtime"

// OSSignature says the binary isn't code-signed: Linux, and the other OSes
// pairtime builds for besides Windows and macOS, don't sign programs.
func OSSignature(string) (Verdict, string) {
	if runtime.GOOS == "linux" {
		return NoSigning, "not code-signed, as Linux programs aren't"
	}
	return NoSigning, "not code-signed, as this OS's programs aren't"
}
