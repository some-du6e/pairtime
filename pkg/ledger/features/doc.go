// SPDX-License-Identifier: AGPL-3.0-or-later

// Package features implements the keyed scorer features of pairtime's
// ledger, version 1: spec sections 10.4 to 10.8 of
// docs/spec/ledger-v1.md, whose section numbers the comments here cite.
//
// A prompt's features let the server recompute SM 4.2's dark-flow
// discounts without its text (DESIGN 3.6, 6.2 D18). [Normalize] turns a
// committed text into words by rules a person can apply by hand: pasted
// blocks and error text go by line, then lowercasing with Unicode 15.0.0's
// table, then substitutions and retry phrases. [Shingles] cuts the words
// into word 3-shingles or character 5-shingles (SM 8.5), and a [Key], which
// holds K_feat, turns each shingle into a keyed value and the set into a
// 64-value [Sketch]. The same key makes the 16-byte keyed values of error
// signatures, templates, commands and file paths, and the edit hunks of
// agent.tool records. [F] is the feature map a record body carries as "f".
//
// CC8 checks a challenged prompt's sketch against per-shingle values the
// client reveals: [VerifyShingleReveal] is the server's half.
//
// Only the device holds K_feat, so no one else can compute a feature
// value, and a reader who holds a body can't test a guess at its prompt.
// [Key] keeps K_feat in an unexported field and prints as a placeholder
// under every fmt verb. Package ledger derives it from K_dev.
//
// A later version of the features can improve a rule only as f.v = 2, with
// new keys. Records written under version 1 keep meaning what this package
// computes, which is why it pins its Unicode table instead of using
// package unicode's.
package features
