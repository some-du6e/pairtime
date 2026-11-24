// SPDX-License-Identifier: AGPL-3.0-or-later

// Package retention runs the device's retention sweeps (DESIGN 4.4;
// docs/PLAN.md, WP-LG6): once a day, every store pairtime keeps on the
// device deletes what is past its period. docs/retention.json's device
// entries give the periods, as the one copy the code reads (AGENTS.md), and
// config.toml's [privacy] can shorten the vault's (DESIGN 3.5).
//
// Each of DESIGN 4.4's device rows, where its data is, and what deletes it:
//
//	device.prompt_text     the vault's texts          Sweep, by the vault file's day (Within)
//	device.paths_commands  the vault's paths          Sweep, by the vault file's day (Within); no store keeps tool commands
//	device.activity        inside ledger records      Sweep, with the ledger (docs/errata/2026-11-23-device-activity-retention.md)
//	device.ledger          ledger segments, batches   Sweep, by the segment of each batch's first record
//	device.buckets         buckets/                   Sweep, by the minutes' UTC day
//	device.held_calls      held.jsonl and its logs    the agent's adapter host, as it runs (heldKeep)
//	device.commits         commits/                   the agent's commit store (commitsKeep)
//	device.uploads         uploads.jsonl              the uploader as it runs (uploadsKeep), and Sweep while it doesn't
//	device.project_names   project-names.json         the uploader, as each names answer replaces it and its notes stay 1,000 at most (capNotes); "delete everything" and uninstall
//	device.transcripts     not copied                 nothing
//	device.install         install/                   uninstall, once it has put back what install changed; "delete everything" keeps it (wipeKeeps)
//	device.key             K_dev                      "delete everything" and uninstall
//
// A store whose files are named after UTC days keeps a file while its day
// is on or after the day the period reaches back to (From), as the commit
// store does, so a file goes within a day of its newest data reaching its
// period. The vault's texts and paths, whose periods the teen can shorten,
// go once their day is as old as the period (Within), so none is kept
// longer than the number of days the teen set, nor DESIGN 4.4's.
//
// The ledger keeps the rows of the online batches the server hasn't stored
// while this computer is linked, whatever their age, and deletes their
// segments all the same (Stores.Floor;
// docs/errata/2026-11-23-unsent-batches-retention.md).
package retention

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/some-du6e/pairtime/docs"
)

// Periods are how many days the device keeps each kind of data that has a
// period (DESIGN 4.4), by docs/retention.json's device entries.
type Periods struct {
	// Text is device.prompt_text: the vault's texts of prompts, final agent
	// messages and track labels.
	Text int
	// Paths is device.paths_commands: the vault's paths of the agent's
	// edits, for recaps.
	Paths int
	// Ledger is device.ledger, which device.activity's data is kept with.
	Ledger int
	// Buckets is device.buckets.
	Buckets int
	// Held, Commits and Uploads are device.held_calls, device.commits and
	// device.uploads, which their own stores sweep.
	Held, Commits, Uploads int
}

// Device returns the periods docs/retention.json gives the device. It
// fails when an entry it reads is missing or has no days, and when
// device.activity, whose data the ledger's records hold, doesn't have the
// ledger's period.
func Device() (Periods, error) { return parse(docs.RetentionJSON) }

func parse(data []byte) (Periods, error) {
	var file struct {
		Entries []struct {
			ID    string `json:"id"`
			Where string `json:"where"`
			Days  int    `json:"days"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return Periods{}, fmt.Errorf("retention: docs/retention.json: %w", err)
	}
	days := map[string]int{}
	for _, e := range file.Entries {
		if e.Where == "device" {
			days[e.ID] = e.Days
		}
	}
	var p Periods
	for _, f := range []struct {
		id   string
		into *int
	}{
		{"device.prompt_text", &p.Text},
		{"device.paths_commands", &p.Paths},
		{"device.ledger", &p.Ledger},
		{"device.buckets", &p.Buckets},
		{"device.held_calls", &p.Held},
		{"device.commits", &p.Commits},
		{"device.uploads", &p.Uploads},
	} {
		n, ok := days[f.id]
		if !ok || n <= 0 {
			return Periods{}, fmt.Errorf("retention: docs/retention.json has no device entry %s with days", f.id)
		}
		*f.into = n
	}
	if n := days["device.activity"]; n != p.Ledger {
		return Periods{}, fmt.Errorf("retention: device.activity is kept %d days and device.ledger %d, though its data is inside the ledger's records", n, p.Ledger)
	}
	return p, nil
}

// Shorten returns p with the vault's periods cut to the teen's, from
// config.toml's [privacy]: text and paths days, 0 for the default. A
// period longer than DESIGN 4.4's is DESIGN's, since the teen can only
// shorten them (DESIGN 3.5).
func (p Periods) Shorten(text, paths int) Periods {
	if text > 0 {
		p.Text = min(p.Text, text)
	}
	if paths > 0 {
		p.Paths = min(p.Paths, paths)
	}
	return p
}

// From returns the first UTC day a store that keeps days days keeps at
// now: a file of a day before it is past its period, its newest data
// included.
func From(now time.Time, days int) string {
	return now.UTC().AddDate(0, 0, -days).Format(time.DateOnly)
}

// Within returns the first UTC day a store keeps at now when it keeps
// nothing longer than days days: a file goes once its day is days old, as
// its oldest data reaches the period. With days 1, a text of yesterday goes
// at midnight UTC, and one of today stays.
func Within(now time.Time, days int) string { return From(now, days-1) }

// Stores are the stores Sweep deletes from. A nil one is skipped, as the
// ledger is while it isn't open.
type Stores struct {
	// Vault is *vault.Vault, or the vault's directory while none is open
	// (vault.SweepDir).
	Vault interface {
		Sweep(textFrom, pathsFrom string) (int, error)
	}
	// Ledger is *ledger.Writer, and Floor the first batch whose row it
	// keeps whatever its age, 0 for none: while this computer is linked,
	// the first online batch the server hasn't stored, whose root can only
	// link after the server's head (uploader.HeldFrom).
	Ledger interface {
		Sweep(from string, floor uint64) (batches, segs int, err error)
	}
	Floor uint64
	// Buckets is *store.Buckets.
	Buckets interface {
		Drop(from string) (int, error)
	}
	// Uploads lets go of the past uploads in uploads.jsonl older than
	// device.uploads, as the uploader does while it runs
	// (uploader.PruneUploads).
	Uploads func(now time.Time) error
}

// The stores, as Sweep names those whose sweep failed.
const (
	StoreVault   = "the vault"
	StoreLedger  = "the ledger"
	StoreBuckets = "the minute buckets"
	StoreUploads = "the record of past uploads"
)

// Swept says what one Sweep deleted.
type Swept struct {
	// Entries are the vault's texts and paths, Batches and Segments the
	// ledger's, and Buckets the bucket store's day files.
	Entries, Batches, Segments, Buckets int
}

// Sweep deletes from each store what is past its period at now (DESIGN
// 4.4). It goes on to the other stores when one fails, and returns each
// store's error that failed, by its name (StoreVault and the others), nil
// when none did.
func Sweep(now time.Time, p Periods, s Stores) (Swept, map[string]error) {
	var out Swept
	var failed map[string]error
	note := func(store string, err error) {
		if err != nil {
			if failed == nil {
				failed = map[string]error{}
			}
			failed[store] = err
		}
	}
	if s.Vault != nil {
		n, err := s.Vault.Sweep(Within(now, p.Text), Within(now, p.Paths))
		out.Entries = n
		note(StoreVault, err)
	}
	if s.Ledger != nil {
		b, n, err := s.Ledger.Sweep(From(now, p.Ledger), s.Floor)
		out.Batches, out.Segments = b, n
		note(StoreLedger, err)
	}
	if s.Buckets != nil {
		n, err := s.Buckets.Drop(From(now, p.Buckets))
		out.Buckets = n
		note(StoreBuckets, err)
	}
	if s.Uploads != nil {
		note(StoreUploads, s.Uploads(now))
	}
	return out, failed
}
