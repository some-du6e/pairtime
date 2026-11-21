// SPDX-License-Identifier: AGPL-3.0-or-later

// Package rollup keeps pairtimed's daily rollups (WP-SV7; DESIGN 3.10,
// SM 3.4 and 5.3): credited seconds per account, project, local day and
// activity kind, with pending minutes kept apart from final ones, and the
// agent's running time beside them. The owner's dashboard reads them
// (package dashboard).
//
// Ingest calls Apply inside the writer transaction that stores a minute
// bucket's revision, so the rollups never disagree with the buckets they
// sum. Apply works from the delta between a bucket's revisions, as
// additive upserts, and merges devices: one human minute per wall-clock
// minute per account, with concurrent_devices raised when two devices
// credit the same slot (SM 3.4).
//
// This file is the contract between ingest (WP-SV4) and the rollups, fixed
// by the dashboard wave. Three fields differ from the wave's first sketch:
//
//   - Account is a string, the users row's id. Accounts are keyed by a
//     UUIDv7 in TEXT (migration 0002, package store's rules), so there is
//     no int64 row id to carry.
//   - Seconds are float64, as in pkg/event. A slot's cr is 0 to 15 seconds
//     in steps of 0.5, because a half-weight second credits half a second
//     (SM 4.3, 5.1), so an int would drop the halves. agent_running_s and
//     the uncredited seconds are float64 in pkg/event too.
//   - A slot's lbl is a list, as in pkg/event: a slot can rest on more than
//     one side door (DESIGN 4.1 E-6).
package rollup

import (
	"context"
	"errors"

	"github.com/some-du6e/pairtime/internal/server/writer"
)

// Bucket is one stored minute-bucket revision (DESIGN 4.3), already
// through ingest's shape check.
type Bucket struct {
	// Account is the id of the device owner's users row, a UUIDv7.
	Account string
	// Device is the opaque device_id.
	Device string
	// Minute is Unix seconds at the start of the minute, UTC.
	Minute int64
	// Rev is the bucket's revision. A higher Rev supersedes a lower one for
	// the same device and minute (DESIGN 3.7).
	Rev int
	// State is the bucket's state as uploaded: "pending" within the
	// 900-second pending window, then "final", or SM's own "provisional"
	// or "restored" (pkg/event.BucketState). Rollups count "pending" as
	// pending and the other three as final.
	State string
	// Slots are the minute's 15-second slots in order, so Slots[i] starts
	// 15*i seconds into the minute. A bucket has 4; missing trailing slots
	// count as empty.
	Slots []Slot
	// CreditedS is the sum of the slots' CreditedS, at most 60.
	CreditedS float64
	// AgentRunningS is the seconds an agent turn was running in this
	// minute, in any project (SM 5.1).
	AgentRunningS float64
	// Uncredited is the seconds looked at and not credited, by uncredited
	// reason code (SM 5.2).
	Uncredited map[string]float64
}

// Slot is one 15-second slot of a Bucket.
type Slot struct {
	// Project is the opaque project key from the client (p), or "" when
	// nothing in the slot was credited and no project was resolved.
	Project string
	// CreditedS is cr: 0 to 15 seconds, in steps of 0.5.
	CreditedS float64
	// Why is the dominant credit reason code, "" when nothing was
	// credited.
	Why string
	// Kind is the activity kind (SM 5.1), "" when nothing was credited.
	Kind string
	// Level is ev: "A", "B" or "C" (DESIGN 6.4).
	Level string
	// Labels is lbl: the side-door labels of the anchors behind the
	// credit, such as "via_track".
	Labels []string
}

// ErrNotBuilt is what Apply returns until WP-SV7's rollups land. It
// changes nothing, so a caller wired early must not treat it as a failed
// ingest; the dashboard wave keeps the call behind TODO(WP-SV7) until then.
var ErrNotBuilt = errors.New("rollup: Apply isn't built yet (WP-SV7)")

// Apply moves the rollups from prev (nil for a first revision) to next. It
// runs inside the writer transaction tx that stores next, so either both
// land or neither does.
func Apply(ctx context.Context, tx writer.Tx, prev *Bucket, next Bucket) error {
	return ErrNotBuilt
}
