// SPDX-License-Identifier: AGPL-3.0-or-later

// Package params is pairtime's public parameter table: every tunable value
// in the credit rule, from docs/design/struggle-metric.md (SM) sections 8.1
// to 8.6 and docs/DESIGN.md section 6.3. SM 8 gives the reason for each
// value. SM 8.7's tier-2 flag thresholds aren't scoring parameters and
// aren't here.
//
// # Tables
//
// Each version of the table is a file, tables/<version>.json, and every
// file is embedded in every binary (DESIGN 6.6). A file holds its version
// and its parameters:
//
//	{"version": "0.1.0", "params": {"SAMPLE_S": 15, "COMPOSE_CPS": 1.5, ...}}
//
// A published table never changes. Changing a value is a MINOR scoring
// version with a new file and no code; adding or dropping a parameter is a
// MAJOR one (SM 7.6, DESIGN 6.6). [ByVersion] and [ByHash] look tables up.
//
// # Canonical form and hash
//
// A table's hash, which every minute bucket carries in its params field, is
// the SHA-256 of the table's canonical JSON (SM 7.6): object keys sorted
// bytewise, no whitespace, and every number exactly as the file writes it.
// So that the text of a number is a function of its value, numbers are
// plain decimals with no exponent and no trailing zeros after the point,
// and strings are printable ASCII. No number but 0 is closer to 0 than
// 0.000001, which jq would write with an exponent. So jq produces the same
// bytes, and anyone can check a hash without pairtime:
//
//	jq -cSj . pkg/params/tables/0.1.0.json | sha256sum
//
// The -j matters: without it jq ends its output with a newline, which the
// hash doesn't cover.
//
// # Reading parameters
//
// Each parameter is a variable named exactly as SM writes it, such as
// [THINK_LEAD_S], rather than in Go's usual MixedCaps, so that one search
// finds the spec, the table and every rule that reads it. Its type says how
// to read it: [Table.Int] for integers, [Table.Rat] for exact rationals and
// [Table.Bool] for flags. No parameter is ever read as a float64, so
// pkg/score can stay in integer arithmetic (DESIGN 3.7).
//
// The three types are opaque, so these variables are the only names other
// packages have. A string doesn't convert to one, so tab.Int("THINK_LEAD_S")
// doesn't compile, and a composite literal outside this package can only
// make the zero value, which names no parameter and panics when read. The
// parameters are variables only because a Go constant can't be a struct;
// nothing assigns them.
//
// So every parameter pkg/score reads through Int, Rat or Bool is a
// params.NAME selector there. A test in this package lists those selectors
// with go/ast and fails if a table lacks one of the names (TE WP0.6).
// Table.Text still takes a string, for code that shows a whole table, and
// the test doesn't see reads through it, so rules don't use it.
//
// # Names that differ from SM 8
//
//   - CHAIN_HEAD_INTERVAL_S is gone. Ledger batches every [BATCH_S] replace
//     per-event chain heads (DESIGN 6.2 D8).
//   - STREAK_DAY_S is [BUILD_DAY_S] (DESIGN 6.2 D10, 6.3).
//   - SM 8.5's SHINGLE row ("word 3-shingles; character 5-shingles under 6
//     words") holds three numbers, so it is three parameters:
//     [SHINGLE_WORDS], [SHINGLE_CHARS] and [SHINGLE_MIN_WORDS].
//   - DESIGN 6.3's CHALLENGE_K ("max(10, 10% of challengeable turns),
//     capped at 30") is likewise [CHALLENGE_K_MIN], [CHALLENGE_K_FRAC] and
//     [CHALLENGE_K_MAX].
package params
