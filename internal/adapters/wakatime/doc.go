// SPDX-License-Identifier: AGPL-3.0-or-later

// Package wakatime is pairtime's side of the WakaTime dual-send (DESIGN
// 5.6, 5.7; docs/PLAN.md, WP-AD16): the local endpoint that takes the
// heartbeats wakatime-cli sends through pairtime's [api_urls] line, in
// the API Hackatime answers, and later the bridge that turns them into
// events.
//
// The endpoint is an http.Handler and nothing more. The agent mounts it on
// its loopback server, behind localhttp.Guard, looks up the connection's
// peer for it (internal/agent/peer), and hands what it takes to the
// bridge. Each request gets one answer per heartbeat in Hackatime's shapes
// (testdata/upstream/hackatime/responses), and a heartbeat the endpoint
// took before is answered as it was and passed on once, so wakatime-cli's
// replays of its offline queue are taken once (pkg/wakacompat). What
// wakatime-cli v2.28.4 sends is in testdata/fixtures/wakatime/2.28.4/linux,
// captured in CI, and the tests replay it.
package wakatime
