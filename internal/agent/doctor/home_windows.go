// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"strings"

	"golang.org/x/sys/windows"
)

// HomeSpellings are the spellings of the home folder home that a path can
// carry: itself and its 8.3 short form, such as C:\Users\KARIME~1 for
// C:\Users\karimeltaib, which TEMP often holds.
func HomeSpellings(home string) []string {
	out := []string{home}
	p, err := windows.UTF16PtrFromString(home)
	if err != nil {
		return out
	}
	buf := make([]uint16, windows.MAX_PATH)
	n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err == nil && int(n) > len(buf) {
		buf = make([]uint16, n)
		n, err = windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
	}
	if err != nil || n == 0 || int(n) > len(buf) {
		return out
	}
	if short := windows.UTF16ToString(buf[:n]); short != "" && !strings.EqualFold(short, home) {
		out = append(out, short)
	}
	return out
}
