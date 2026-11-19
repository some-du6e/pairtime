// SPDX-License-Identifier: AGPL-3.0-or-later

// Package event defines pairtime's normalized event model: the envelope
// every event carries, one payload type per event kind, and the records the
// scorer derives from events. docs/design/struggle-metric.md (SM) sections
// 1 and 5 are the specification and the types follow it field for field.
// docs/schema/event.schema.json describes the same events for adapters
// written outside Go, and a test keeps the two in step.
//
// # Events
//
// Adapters turn whatever a harness, editor, shell, git or the OS exposes
// into an [Event] and append it to the local spool. The scorer reads only
// events, never a harness's own format, so supporting a new harness means
// writing an adapter, not changing the scorer (SM 1).
//
// In JSON an event is the envelope fields, then "kind", then the payload
// under "data":
//
//	{"event_id": "0193f6a1-2b3c-7d4e-8f00-112233445566", "device_id": "d_7f3a9c",
//	 "seq": 18301, "t": 1795046500000, "mono": 81163000, "boot_id": "b_51c2",
//	 "source": "os", "harness": "none", "session_key": "none",
//	 "chain_hash": "<64 hex digits>", "kind": "os.presence",
//	 "data": {"idle_any_s": 3.5, "idle_keys_s": null, "locked": false}}
//
// # Local fields
//
// Some payload fields hold prompt text, or sketches and hashes of prompts,
// commands and apps. SM 1.2 marks them local: they never leave the device,
// not even in the evidence revealed for submitted hours. They carry the
// struct tag pairtime:"local" (and "x-local": true in the schema), and
// [Event.Shareable] clears them. Call it on anything about to leave the
// device.
//
// # Validation
//
// Validate methods report every broken rule at once, joined with
// errors.Join, each naming its field by JSON path. They check what one
// record can show on its own: enums, ranges, formats, and the arithmetic SM
// defines, such as typed_len_est = len_chars - pasted_chars or a bucket's
// credited_s being the sum of its slots. Rules that need other records,
// such as the gapless seq or the hash chain, belong to the spool and the
// server.
//
// Identifiers that stand in for something private (devices, sessions,
// turns, projects, files, commands) are opaque keys of 1 to 128 letters,
// digits, '_' or '-'. A path, a command line or an email address can't pass
// as one.
package event
