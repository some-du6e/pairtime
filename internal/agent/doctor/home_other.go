// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package doctor

// HomeSpellings are the spellings of the home folder home that a path can
// carry: on this OS, itself alone.
func HomeSpellings(home string) []string { return []string{home} }
