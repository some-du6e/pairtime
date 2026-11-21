// SPDX-License-Identifier: AGPL-3.0-or-later

// Package rehearsal is pairtime's end-to-end rehearsal of a teen's first
// hour with it, run against the real binaries the way a teen meets them:
// sign in on the server's site with Hack Club Auth, `pairtime install`,
// `pairtime login`, a Claude Code session typed at the keyboard, the hours
// on /dashboard, then `pairtime uninstall`. It holds no code, only
// TestDesktopRehearsal in rehearsal_desktop_test.go, which needs a desktop
// to type into, so rehearsal.yml runs it under Xvfb, against
// navikt/mock-oauth2-server standing in for Hack Club Auth, on every PR
// that changes what it builds or reads and on every push to main.
package rehearsal
