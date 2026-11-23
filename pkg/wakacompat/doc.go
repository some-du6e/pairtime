// SPDX-License-Identifier: AGPL-3.0-or-later

// Package wakacompat is WakaTime's heartbeat as pairtime takes it: the
// fields WakaTime's clients send (HC 2.1), read the way Hackatime reads
// them (testdata/upstream/hackatime), and the key pairtime deduplicates
// heartbeats by, so that wakatime-cli's replays of its offline queue are
// taken once (DESIGN 5.6). The agent's local endpoint uses it
// (internal/adapters/wakatime), and WP-PG11's Hackatime-equivalent seconds
// go here too.
//
// What wakatime-cli v2.28.4 really sends is in
// testdata/fixtures/wakatime/2.28.4/linux, captured in CI, and the tests
// read it.
//
// A heartbeat holds content: file paths, project and branch names, the
// user agent. So does its Key, a digest of all of it. Both stay on the
// device (AGENTS.md). The bridge keys paths per device before a record
// leaves the endpoint, and Seen holds keys in memory alone.
package wakacompat
