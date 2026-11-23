// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"encoding/json"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// The JSON of docs/spec/ingest-v1.md, the wire format WP-SV4's ingest
// reads. Field names, members and their order follow the spec; nothing
// here is free text (DESIGN 4.3, 4.5).

// Body is the JSON inside a request's gzip stream (ingest-v1 2.3). An
// absent member and an empty one mean the same, so empty ones are left
// out. Commit records wait for the git adapter's wiring (WP-AD5), so the
// uploader sends none yet.
type Body struct {
	Batches []Root               `json:"batches,omitempty"`
	Buckets []event.MinuteBucket `json:"buckets,omitempty"`
	Labels  *Labels              `json:"labels,omitempty"`
}

// Empty reports whether b carries nothing.
func (b Body) Empty() bool { return len(b.Batches) == 0 && len(b.Buckets) == 0 && b.Labels == nil }

// Root is a batch root, DESIGN 4.3's {b, n, R_b, H_b, prev} (ingest-v1
// 2.3.1). Hashes are 64 lowercase hex digits, as ledger.Hash writes them.
type Root struct {
	B    uint64      `json:"b"`
	N    uint64      `json:"n"`
	Root ledger.Hash `json:"R_b"`
	Head ledger.Hash `json:"H_b"`
	Prev ledger.Hash `json:"prev"`
}

// Labels are DESIGN 4.3's labels (ingest-v1 2.3.4): what build of the
// client and of the scorer produced the numbers, and the harnesses the
// device records.
type Labels struct {
	ClientBuild    string    `json:"client_build"`
	ScoringVersion string    `json:"scoring_version"`
	Params         string    `json:"params"`
	Harnesses      []Harness `json:"harnesses"`
	TypingModule   bool      `json:"typing_module"`
	VM             bool      `json:"vm"`
}

// Harness is a harness the device records, with the version harnessVersion
// gives for the one it last named: that version when it is in tier 1's
// form, the number it starts with when only its suffix isn't, and none when
// the harness named no version, or none in D-version-grammar's form
// (ingest-v1 2.3.4).
type Harness struct {
	Name    event.Harness `json:"name"`
	Version string        `json:"version,omitempty"`
}

// Response is the answer to a request that reached processing, status 200
// (ingest-v1 2.8). Each result answers the item at its place in the
// request's array of the same name.
type Response struct {
	ReceivedAt uint64       `json:"received_at"`
	Req        uint64       `json:"req"`
	Head       *Head        `json:"head"`
	Batches    []ItemResult `json:"batches"`
	Buckets    []ItemResult `json:"buckets"`
	Commits    []ItemResult `json:"commits"`
	Labels     *ItemResult  `json:"labels"`
}

// Head is the device's stored head: the last batch the server holds and
// its H_b, and in GET /api/v1/ingest/state when the server stored it.
type Head struct {
	B          uint64      `json:"b"`
	Head       ledger.Hash `json:"H_b"`
	ReceivedAt uint64      `json:"received_at,omitempty"`
}

// ItemResult is one item's result (ingest-v1 2.8, 2.9).
type ItemResult struct {
	Status int    `json:"status"`
	Code   string `json:"code"`
	Field  string `json:"field,omitempty"`
	// Rev is the revision the server holds, which a bucket's superseded,
	// rev_conflict and too_many_revisions name.
	Rev *uint32 `json:"rev,omitempty"`
	// B and Receipt are a batch root's: its number once it passed its
	// form, and the receipt of an accepted or duplicate root.
	B       uint64       `json:"b,omitempty"`
	Receipt *WireReceipt `json:"receipt,omitempty"`
}

// WireReceipt is a receipt as the response carries it: ledger-v1 7's,
// with the id of the server key that signed it.
type WireReceipt struct {
	ReceivedAt uint64 `json:"received_at"`
	KeyID      string `json:"key_id"`
	Sig        string `json:"sig"` // 128 hex digits
}

// Refusal is the body of a request refused as a whole (ingest-v1 2.6).
type Refusal struct {
	Error  string `json:"error"`
	Detail string `json:"detail"`
}

// State is GET /api/v1/ingest/state's answer (ingest-v1 3).
type State struct {
	Device     string  `json:"device"`
	Req        uint64  `json:"req"`
	Head       *Head   `json:"head"`
	ChainStart *uint64 `json:"chain_start"`
	// Relinked is set when the device signed in again after the server
	// stored its head and hasn't stored a batch since, and Gaps are the
	// local-only gaps the server recorded, oldest first: it never takes
	// their roots (D-local-gap).
	Relinked  bool       `json:"relinked"`
	Gaps      []StateGap `json:"gaps"`
	ServerKey *ServerKey `json:"server_key"`
}

// StateGap is one of the server's local-only gaps: the first and the last
// batch it skipped.
type StateGap struct {
	First uint64 `json:"first"`
	Last  uint64 `json:"last"`
}

// ServerKey is the key receipts are signed with now: its id, the first 16
// hex digits of the SHA-256 of its public key, and the key in hex.
type ServerKey struct {
	KeyID     string `json:"key_id"`
	PublicKey string `json:"public_key"`
}

// timeReply is GET /api/v1/time's answer (ingest-v1 4).
type timeReply struct {
	ServerTime int64 `json:"server_time"`
}

// The item codes the uploader acts on (ingest-v1 2.9). Every code is
// final for its item: the uploader never sends an item again once it got
// one (ingest-v1 5 step 5).
const (
	codeAccepted      = "accepted"
	codeDuplicate     = "duplicate"
	codeSuperseded    = "superseded"
	codeBatchConflict = "batch_conflict"
	codePrevMismatch  = "prev_mismatch"
	codeChainGap      = "chain_gap"
	codeBeforeChain   = "before_chain"
	codeFutureMinute  = "future_minute"
	codeRevConflict   = "rev_conflict"
)

// The request refusals the uploader acts on (ingest-v1 2.6).
const (
	refusalIngestOff    = "ingest_off"
	refusalUnavailable  = "unavailable"
	refusalClockSkew    = "clock_skew"
	refusalCounter      = "counter_stale"
	refusalBodyHash     = "body_hash_mismatch"
	refusalTokenMissing = "token_missing"
	refusalTokenInvalid = "token_invalid"
	refusalRevoked      = "device_revoked"
	refusalMismatch     = "device_mismatch"
	refusalKeyMissing   = "device_key_missing"
	refusalKeyReused    = "idempotency_key_reused"
	refusalSignature    = "signature_invalid"

	refusalBodyUnreadable = "body_unreadable"
	refusalBodyTooLarge   = "body_too_large"
	refusalDecompressed   = "decompressed_too_large"
	refusalTooManyBatches = "too_many_batches"
	refusalTooManyBuckets = "too_many_buckets"

	// refusalTooManyNames is a names request past the server's limits
	// (project-names-v1 1.5).
	refusalTooManyNames = "too_many_names"
)

// encodeBody returns b's JSON exactly as the uploader sends it: what
// encoding/json writes, members in the order above, and a newline at the
// end, so `pairtime privacy show` prints a line. json.Marshal writes maps,
// such as a bucket's uncredited seconds, in key order, so the same Body
// always gives the same bytes.
func encodeBody(b Body) ([]byte, error) {
	out, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
