// SPDX-License-Identifier: AGPL-3.0-or-later

// Package windows holds the client's Windows-only code (DESIGN 3.11). Every
// other file in it builds on Windows alone, through golang.org/x/sys/windows
// and without cgo (AGENTS.md). This file has no build constraint, so the
// package exists on every OS and tools that list ./... find it.
//
// So far it has the device keystore's half: DPAPI, which encrypts a secret
// under the user's logon credentials (DESIGN 3.6; CP 5.2).
package windows
