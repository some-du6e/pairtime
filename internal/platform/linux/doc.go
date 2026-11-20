// SPDX-License-Identifier: AGPL-3.0-or-later

// Package linux holds the client's Linux-only code (DESIGN 3.11). Every
// other file in it builds on Linux alone, without cgo (AGENTS.md). This
// file has no build constraint, so the package exists on every OS and tools
// that list ./... find it.
//
// So far it has the device keystore's half: the desktop's Secret Service
// over the session D-Bus, through go-keyring (DESIGN 3.6, 3.12; CP 5.2).
// Tests that need a session bus are in *_desktop_test.go files, which
// platform.yml runs inside a private bus (AGENTS.md); fakesecret is the
// Secret Service they talk to.
package linux
