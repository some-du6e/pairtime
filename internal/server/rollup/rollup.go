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
// Apply keeps its own copy of each device's latest revision of each minute
// (rollup_minutes), and moves the rollups from that copy, never from the
// prev a caller passes. So the rollups depend only on the latest revision
// of each device's minutes: a revision that arrives twice, late or out of
// order changes nothing it shouldn't.
//
// A minute counts toward the account's local day, in the time zone the
// owner set (users.tz, UTC until set). A minute's day comes from the
// minute itself, time.Unix(minute, 0).In(zone).Date(), never from a
// midnight made with time.Date, which doesn't exist where daylight saving
// time starts at midnight (America/Santiago on 2027-09-05). Changing the
// zone moves the minutes counted after the change; days already counted
// stay where they are.
//
// The agent's side of the chess clock is the bucket's agent_only: per
// project key, the seconds a turn of that project ran in the minute that
// weren't credited to it (SM 2.6, D-bucket-agent-only). The rollups count
// it per project, so the server's clock is the device's. agent_running_s,
// which overlaps the owner's time and names no project, counts toward
// nothing here.
//
// This file is the contract between ingest (WP-SV4) and the rollups, fixed
// by the dashboard wave. Four fields differ from the wave's first sketch:
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
//   - AgentOnly is the bucket's agent_only, which D-bucket-agent-only added
//     after the sketch.
package rollup

import (
	"context"
	"fmt"
	"time"

	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/event"
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
	// minute, in any project (SM 5.1). The rollups don't count it.
	AgentRunningS float64
	// AgentOnly is agent_only: for each project key, the seconds a turn of
	// that project ran in this minute that weren't credited to it, 0 to 60
	// in steps of 0.5 (SM 2.6, D-bucket-agent-only).
	AgentOnly map[string]float64
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

// Apply moves the rollups from the account's minute as it stood to the
// minute with next, inside tx, the writer transaction that stores next.
// prev is the revision next replaces, or nil; Apply reads what it applied
// before from rollup_minutes instead, so prev only has to name the same
// device and minute when it is set. A revision no newer than the one
// Apply holds for that device and minute changes nothing. Apply refuses a
// bucket that breaks the shapes Bucket describes, which fails the caller's
// transaction.
func Apply(ctx context.Context, tx writer.Tx, prev *Bucket, next Bucket) error {
	if prev != nil && (prev.Account != next.Account || prev.Device != next.Device || prev.Minute != next.Minute) {
		return fmt.Errorf("rollup: prev is %s's minute %d on %s, but next is %s's minute %d on %s", prev.Account, prev.Minute, prev.Device, next.Account, next.Minute, next.Device)
	}
	in, err := minuteOf(next)
	if err != nil {
		return err
	}
	minuteMS := next.Minute * 1000
	rows, err := loadMinute(ctx, tx, next.Account, minuteMS)
	if err != nil {
		return err
	}
	before := make([]device, 0, len(rows)+1)
	after := make([]device, 0, len(rows)+1)
	oldDay := ""
	for _, r := range rows {
		oldDay = r.day
		before = append(before, r)
		if r.id == next.Device {
			if r.rev >= in.rev {
				return nil
			}
			continue
		}
		after = append(after, r)
	}
	zone, _, err := zoneOf(ctx, tx, next.Account)
	if err != nil {
		return err
	}
	day := LocalDay(next.Minute, zone)
	in.day = day
	after = append(after, in)

	was, now := merge(before), merge(after)
	delta := now.contribution(day)
	if oldDay != "" {
		delta.subtract(was.contribution(oldDay))
	}
	if err := addDaily(ctx, tx, next.Account, delta); err != nil {
		return err
	}
	if err := saveDevice(ctx, tx, next.Account, minuteMS, in); err != nil {
		return err
	}
	if oldDay != "" && oldDay != day {
		if err := moveMinute(ctx, tx, next.Account, minuteMS, day); err != nil {
			return err
		}
	}
	if was.concurrent != now.concurrent {
		if err := setFlag(ctx, tx, next.Account, minuteMS, FlagConcurrentDevices, now.concurrent); err != nil {
			return err
		}
	}
	return nil
}

// FlagConcurrentDevices is the flag rollup_flags holds for a minute in
// which two of an account's devices credit the same slot (SM 3.4).
const FlagConcurrentDevices = "concurrent_devices"

// ApplyEvent is Apply for ingest, which holds buckets as pkg/event's
// MinuteBucket and the account apart: its signature is ingest's
// RollupFunc, so serve.go passes it as it is.
func ApplyEvent(ctx context.Context, tx writer.Tx, account string, prev *event.MinuteBucket, next event.MinuteBucket) error {
	var p *Bucket
	if prev != nil {
		b := FromEvent(account, *prev)
		p = &b
	}
	return Apply(ctx, tx, p, FromEvent(account, next))
}

// FromEvent turns a minute bucket as pkg/event holds it into a Bucket of
// account's.
func FromEvent(account string, m event.MinuteBucket) Bucket {
	b := Bucket{
		Account:       account,
		Device:        m.Device,
		Minute:        m.Minute.Unix(),
		Rev:           int(m.Rev),
		State:         string(m.State),
		CreditedS:     m.CreditedS,
		AgentRunningS: m.AgentRunningS,
	}
	for _, s := range m.Slots {
		slot := Slot{Project: s.Project, CreditedS: s.Credited, Why: string(s.Why), Kind: string(s.Kind), Level: string(s.Ev)}
		for _, l := range s.Lbl {
			slot.Labels = append(slot.Labels, string(l))
		}
		b.Slots = append(b.Slots, slot)
	}
	if len(m.Uncredited) > 0 {
		b.Uncredited = make(map[string]float64, len(m.Uncredited))
		for r, s := range m.Uncredited {
			b.Uncredited[string(r)] = s
		}
	}
	// TODO(WP-EV3): copy m's agent_only into b.AgentOnly once pkg/event's
	// MinuteBucket has it (D-bucket-agent-only). Until then a bucket from
	// ingest has no agent time on its own.
	return b
}

// LocalDay is the local date, YYYY-MM-DD in zone, of the minute that
// starts minute seconds after the Unix epoch.
func LocalDay(minute int64, zone *time.Location) string {
	y, m, d := time.Unix(minute, 0).In(zone).Date()
	return fmt.Sprintf("%04d-%02d-%02d", y, int(m), d)
}
