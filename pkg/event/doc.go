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
//	 "source": "os", "via": "os", "harness": "none", "session_key": "none",
//	 "kind": "os.presence",
//	 "data": {"idle_any_s": 3.5, "idle_keys_s": null, "locked": false}}
//
// Source is what a record claims produced it; via is how the pairtime
// agent received it, and the agent sets via itself (DESIGN 4.1 E-2). The
// envelope has no hash chain: the ledger's Merkle batches replace it
// (DESIGN 3.6).
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
// The ledger doesn't store local fields either. pkg/ledger derives keyed
// scorer features and content commitments from them with the device keys,
// then writes the event's Shareable form, so the server can re-score a
// submitted window without ever seeing them (DESIGN 3.6, 4.1).
//
// # Validation
//
// Validate methods report every broken rule at once, joined with
// errors.Join, each naming its field by JSON path. They check what one
// record can show on its own: enums, ranges, formats, and the arithmetic SM
// defines, such as typed_len_est = len_chars - pasted_chars or a bucket's
// credited_s being the sum of its slots. Rules that need other records,
// such as the ledger's gapless record numbers and its batch chain, belong
// to pkg/ledger and the server.
//
// Identifiers that stand in for something private (devices, sessions,
// turns, projects, files) are opaque keys of 1 to 128 letters, digits, '_'
// or '-', keyed per device so nobody can recompute one from a guess. The
// format rejects separators, spaces, dots and '@', so a path, a command
// line with arguments or an email address can't pass as a key. A bare
// name like "Makefile" can, so the keying keeps a key private, not the
// format. Digests of content, such as command-line hashes, are local
// fields, not keys. The exception is a digest keyed so that a guess can't
// be tested, such as agent.tool's error_sig (DESIGN 3.6).
package event
