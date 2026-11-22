// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import "os"

// OSSignature says which signature the binary at path holds, from its
// Mach-O code signature (macho.go). It doesn't verify a certificate's
// signature, which macOS does when the binary runs, so it never says one
// is trusted.
func OSSignature(path string) (Verdict, string) {
	f, err := os.Open(path)
	if err != nil {
		return Unchecked, "its signature couldn't be checked: " + err.Error()
	}
	defer f.Close()
	v, err := machOSignature(f)
	if err != nil {
		return Unchecked, "its signature couldn't be checked: " + err.Error()
	}
	return v, machOWords(v)
}
