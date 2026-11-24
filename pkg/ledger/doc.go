// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ledger implements pairtime's ledger format,
// docs/spec/ledger-v1.md, whose section numbers the comments here cite.
// Part 1 is the device keys, record headers, nonces and bodies, entries,
// RFC 9162 Merkle trees and inclusion proofs, the batch chain, the
// server's receipts, the day trees, and verification. Part 2, section 10,
// is the content: the token commitment of each prompt, final agent message
// and track label, the redacted reveal that opens one, and the challenge
// draw. Its keyed features are in package features. DESIGN 3.6, 7.4 and
// 7.5 say what goes into each value; the spec says how each is encoded,
// byte for byte.
//
// tools/ledger_ref.py implements the same format again in standard-library
// Python and shares no code with this package. The tests check this
// package against testdata/ledger/vectors.json and vectors-content.json,
// which the reference wrote, and run the reference's check command on
// batches, texts and reveals this package wrote.
//
// # Writing
//
// A device derives its [Keys] from K_dev once, with [DeriveKeys]. For each
// record, [EventRecord], [CheckpointRecord] or [UploadFailureRecord]
// returns the 33-byte [Header] and the body, which the writer stores as
// they are: every hash covers the stored bytes, and nobody re-encodes a
// body to verify it. [EventRecord] takes the record's [Content] too: the
// text it commits and the inputs of its features, which reach the body
// only as the commitment "c" and the keyed feature map "f". [Seal] turns
// the entries of the records written since the last batch into the next
// [BatchRow].
//
// # Content
//
// When a record is drawn, the device opens its text from the vault with
// [Keys.Open] and removes spans with [Opening.Reveal]. The server checks
// the reveal against the record's [Commitment] with [VerifyReveal], which
// returns the first verdict of spec 10.3 that applies. [DrawSeed] and
// [Draw] give the challenge draw of DESIGN 7.5 step 4, which an auditor
// recomputes from the bundle, through [DrawSeedFromDigest] from the
// SHA-256(M) the bundle carries. Draw takes the parameter table the draw
// ran under, which sets CHALLENGE_K.
//
// # Verifying
//
// [Chain.Verify] checks a run of batches as spec section 9 says. It
// reports a gap in b or seq ([GapError]), a root that isn't the tree of
// the batch's entries ([RootMismatchError]) and a head that doesn't follow
// from the chain ([HeadMismatchError]) as distinct errors, because each
// points at a different fault: a batch or record hidden or dropped, a
// record changed or moved, a chain rewritten. Receipts, inclusion proofs
// and day trees have their own functions.
//
// # Keys
//
// K_dev and the keys derived from it never leave the device unencrypted,
// and nothing logs them. [Keys] keeps them in unexported fields and prints
// as a placeholder under every fmt verb. Nonces are compared in constant
// time. An [Opening] holds a text and its salts, and prints as a
// placeholder too.
package ledger
