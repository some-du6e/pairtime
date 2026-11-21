// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ingest is pairtimed's signed ingest: POST /api/v1/ingest/batches,
// GET /api/v1/ingest/state and GET /api/v1/time, as
// docs/spec/ingest-v1.md specifies them, whose section numbers the comments
// cite (WP-SV4, and the shape half of WP-SV5's firewall; DESIGN 3.6, 3.10,
// 4.3 and 4.5).
//
// A request is checked in the spec's order, 2.5: the device token, the
// device, the signed headers' forms, the clock, the raw body's size and
// hash, the device's Ed25519 signature over DESIGN 4.5's message, then the
// gzip member and the JSON within their limits. Everything up to there
// reads and writes nothing but the token lookup. The rest runs as one op
// of the writer goroutine (package writer), which takes one op at a time:
// the Idempotency-Key and the counter, then each batch root against the
// device's chain, with a receipt signed by the server key for each one
// stored, each minute bucket as a revision, each commit record and the
// labels. The op stores the counter and its response with them, so a
// request lands whole or not at all.
//
// The shape check is an allowlist. Every member of every item has one JSON
// type and, for a string, one form: an opaque key, an enum value of
// pkg/event, a hash or HMAC of fixed length, a version or a time. Anything
// else is refused for that item with a 422 code naming the field, never
// the value, so free text can't reach the database through a field
// (DESIGN 4.3's "never in it" column; SP 8.2). The server stores what it
// parsed, encoded again, and never the bytes a client sent.
//
// A device's counter and chain head live on WP-SV3's devices row, which
// ingest reads and writes only through device.Store inside its own writer
// transaction (D-device-row-chain-state), so they last exactly as long as
// the device does. Only a sign-in resets what the chain expects next: the
// first batch under a new one may skip the batches sealed in local-only
// mode, which become a gap the server never takes (D-local-gap). Every row
// ingest keeps names the device's account, and a request whose device_id
// has rows under another account is refused (D-device-account-binding).
package ingest
