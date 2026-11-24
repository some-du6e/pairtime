// SPDX-License-Identifier: AGPL-3.0-or-later

// Package rollup keeps pairtimed's daily rollups (WP-SV7; DESIGN 3.10,
// SM 3.4 and 5.3): credited seconds per account, project, local day and
// activity kind, with pending minutes kept apart from final ones, and the
// agent's time on its own (agent_only) per project beside them. The
// owner's dashboard reads them (package dashboard).
//
// Ingest calls Apply inside the writer transaction that stores a minute
// bucket's revision, so the rollups never disagree with the buckets they
// sum. Apply works from the delta between a bucket's revisions, as
// additive upserts, and merges devices: one human minute per wall-clock
// minute per account (SM 3.4).
//
// Apply keeps its own copy of each device's latest revision of each minute
// (rollup_minutes), and moves the rollups from that copy, never from the
// prev a caller passes. So the rollups depend only on the latest revision
// of each device's minutes: a revision that arrives twice, late or out of
// order changes nothing it shouldn't.
//
// The rollups count a project by its account-level project id, never by a
// device's own project key (D-account-project-id). A device's project keys
// are HMACs under its own secret (AGENTS.md: keyed per device), so one
// repo has a different key on each computer. rollup_projects gives each
// (device, key) its project id, and the merge compares project ids. Until
// project registration (WP-SV10) links the keys of one repo to one id,
// each (device, key) is a project of its own, so a repo used on two
// computers shows as two projects.
//
// A minute stays pending until its device sends a final revision, or
// until the settle job (Settle), which runs through the writer beside the
// retention sweep, finds it still pending PENDING_WINDOW_S after it ended
// and re-applies it as final by the server's clock
// (D-server-settles-pending; DESIGN 3.7, 6.2 D1).
//
// An account's local day keeps at most DAY_CAP_S of credit across its
// devices, counted after the merge in the profile's time zone, so a second
// device can't lift the cap (SM 5.3). Past the cap, the further credit in
// time order, slot by slot, is X.day_cap: rollups_daily keeps the credit
// that fits, and day_cap_ms holds the rest in each project and activity
// kind, so a day's rows still sum to the buckets stored for it. A change to
// a day over the cap re-cuts the day from the changed minute on, from the
// stored credit before it, so it needs none of the day's earlier minutes,
// which the retention jobs may have deleted. The agent's time on its own is
// never capped.
//
// Rows stored before migration 0012, or cut under another DAY_CAP_S, aren't
// a cut. The settle job's first run cuts each such day of the last 182 days
// again whole (RecutDays), and so does the day's next change, while
// rollup_minutes holds all of it. A day whose minutes the retention jobs have
// partly deleted can't be cut again: it keeps its rows, which can keep more
// than DAY_CAP_S, and a change to it holds back the credit it adds and takes
// the credit it removes out of what is held back first, never below zero
// (keep). Such a day's rows sum to its buckets unless a removal stopped at
// zero.
//
// No fraud flag is stored for hours nobody submitted (D-no-ingest-flags;
// DESIGN 4.2, 8.5). SM 3.4's concurrent_devices, two devices crediting
// one slot, is computed when a submission is frozen, over the submitted
// window, from the per-device minutes rollup_minutes keeps for 180 days
// (ConcurrentMinutes). The merge keeps one slot either way.
//
// A minute counts toward the account's local day in the time zone the owner
// had set at the minute's own time: users.tz, UTC until set, or, for a
// minute before a change recorded in zone_changes, the zone the first such
// change replaced (D-sv12-lag, amended in #278 to keep every change). A
// minute's day comes from the minute itself,
// time.Unix(minute, 0).In(zone).Date(), never from a midnight made with
// time.Date, which doesn't exist where daylight saving time starts at
// midnight (America/Santiago on 2027-09-05). A minute keeps the day it
// first counted toward: no later revision, from any device, and no zone
// change moves it (D-sv12-lag). So a change counts from the moment it is
// made, and days already counted stay as they are.
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
	// nothing in the slot was credited and no project was resolved. It is
	// the device's own key; Apply counts it under the account-level
	// project id that rollup_projects gives it.
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
	// Labels is lbl, pkg/event's slot labels (event.SlotLabels): the
	// side-door labels of the anchors behind the credit, such as
	// "via_track", then focus_unknown where some of it is level C because
	// the window in front was unknown (SM 2.5), then via_relay where some
	// rests on a relayed anchor (SM 2.2 rule 9).
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
	before, err := loadMinute(ctx, tx, next.Account, minuteMS)
	if err != nil {
		return err
	}
	after := make([]device, 0, len(before)+1)
	oldDay := ""
	for _, r := range before {
		oldDay = r.day
		if r.id == next.Device {
			if r.rev >= in.rev {
				return nil
			}
			continue
		}
		after = append(after, r)
	}
	// A minute the rollups already count keeps its day, whatever the zone
	// is now. Only its first count reads the zone, the one in force at the
	// minute itself (D-sv12-lag).
	day := oldDay
	if day == "" {
		zone, err := zoneOf(ctx, tx, next.Account, minuteMS)
		if err != nil {
			return err
		}
		day = LocalDay(next.Minute, zone)
	}
	// The other devices' rows already hold project ids, so in's keys
	// become ids before the merge compares them.
	if err := toProjects(ctx, tx, next.Account, &in); err != nil {
		return err
	}
	in.day = day
	after = append(after, in)
	if err := remerge(ctx, tx, next.Account, minuteMS, before, oldDay, after, day); err != nil {
		return err
	}
	return saveDevice(ctx, tx, next.Account, minuteMS, in)
}

// remerge moves the account's rollups for the minute that starts at
// minuteMS from the device rows before, which counted toward oldDay (""
// when there were none), to the rows after, which count toward day: it
// merges each (SM 3.4), applies the day cap to the difference (SM 5.3;
// capped), and adds it to rollups_daily. A minute keeps its day, so oldDay
// is day or "". Apply and the settle job both take this path.
func remerge(ctx context.Context, tx writer.Tx, account string, minuteMS int64, before []device, oldDay string, after []device, day string) error {
	if oldDay != "" && oldDay != day {
		return fmt.Errorf("rollup: %s's minute %d would move from %s to %s, but a minute keeps its day", account, minuteMS, oldDay, day)
	}
	next := merge(after)
	var prev merged
	if oldDay != "" {
		prev = merge(before)
	}
	d := next.contribution(day)
	d.subtract(prev.contribution(day))
	d, err := capped(ctx, tx, account, day, minuteMS, prev, next, d)
	if err != nil {
		return err
	}
	return addDaily(ctx, tx, account, d)
}

// ApplyEvent is Apply for ingest, which holds a revision as pkg/event's
// MinuteBucket, agent_only included, and the account apart. prev is the
// revision next replaces, or nil. It has ingest's RollupFunc signature,
// and pairtimed passes it as ingest's Rollup (cmd/pairtimed/ingest.go).
func ApplyEvent(ctx context.Context, tx writer.Tx, account string, prev *event.MinuteBucket, next event.MinuteBucket) error {
	var p *Bucket
	if prev != nil {
		b := FromEvent(account, *prev)
		p = &b
	}
	return Apply(ctx, tx, p, FromEvent(account, next))
}

// FromEvent turns a minute bucket as pkg/event holds it, agent_only
// included (D-bucket-agent-only), into a Bucket of account's.
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
	if len(m.AgentOnly) > 0 {
		b.AgentOnly = make(map[string]float64, len(m.AgentOnly))
		for p, s := range m.AgentOnly {
			b.AgentOnly[p] = s
		}
	}
	return b
}

// LocalDay is the local date, YYYY-MM-DD in zone, of the minute that
// starts minute seconds after the Unix epoch.
func LocalDay(minute int64, zone *time.Location) string {
	y, m, d := time.Unix(minute, 0).In(zone).Date()
	return fmt.Sprintf("%04d-%02d-%02d", y, int(m), d)
}
