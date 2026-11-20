// SPDX-License-Identifier: AGPL-3.0-or-later

// Package proctest tests the process-table readers of
// internal/platform/windows, darwin and linux against real processes:
// chains the tests spawn themselves, such as python running bash running a
// helper that reads and classifies its own chain (docs/PLAN.md, WP-OS6).
// ci.yml runs them on Linux and platform.yml on Windows and macOS. The
// package has no code outside its tests.
package proctest
