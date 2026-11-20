// SPDX-License-Identifier: AGPL-3.0-or-later

// Package darwin holds the client's macOS-only code (DESIGN 3.11). Every
// other file in it builds on macOS alone, without cgo (AGENTS.md). This file
// has no build constraint, so the package exists on every OS and tools that
// list ./... find it.
//
// So far it has the device keystore's half: the user's Keychain, through
// go-keyring (DESIGN 3.6, 3.12; CP 5.2).
package darwin
