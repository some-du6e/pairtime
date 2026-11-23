// SPDX-License-Identifier: AGPL-3.0-or-later

// Package retention runs the device's retention sweeps (DESIGN 4.4;
// docs/PLAN.md, WP-LG6): once a day, every store pairtime keeps on the
// device deletes what is past its period. docs/retention.json's device
// entries give the periods, as the one copy the code reads (AGENTS.md), and
// config.toml's [privacy] can shorten the vault's (DESIGN 3.5).
//
// Each of DESIGN 4.4's device rows, where its data is, and what deletes it:
//
//	device.prompt_text     the vault's texts          Sweep, by the vault file's day
//	device.paths_commands  the vault's paths          Sweep, by the vault file's day; no store keeps tool commands
//	device.activity        inside ledger records      Sweep, with the ledger (docs/errata/2026-11-23-device-activity-retention.md)
//	device.ledger          ledger segments, batches   Sweep, by the segment of each batch's first record
//	device.buckets         buckets/                   Sweep, by the minutes' UTC day
//	device.held_calls      held.jsonl and its logs    the agent's adapter host, as it runs (heldKeep)
//	device.commits         commits/                   the agent's commit store (commitsKeep)
//	device.uploads         uploads.jsonl              the uploader (uploadsKeep)
//	device.transcripts     not copied                 nothing
//	device.key             K_dev                      "delete everything" and uninstall
//
// A store whose files are named after UTC days keeps a file while its day
// is on or after the day the period reaches back to (From), as the commit
// store does, so a file goes within a day of its newest data reaching its
// period.
package retention

import (
	"encoding/json"
	"errors"
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
// now: a file of a day before it is past its period.
func From(now time.Time, days int) string {
	return now.UTC().AddDate(0, 0, -days).Format(time.DateOnly)
}

// Stores are the stores Sweep deletes from. A nil one is skipped, as the
// vault is when it didn't open.
type Stores struct {
	// Vault is *vault.Vault.
	Vault interface {
		Sweep(textFrom, pathsFrom string) (int, error)
	}
	// Ledger is *ledger.Writer.
	Ledger interface {
		Sweep(from string) (batches, segs int, err error)
	}
	// Buckets is *store.Buckets.
	Buckets interface {
		Drop(from string) (int, error)
	}
}

// Swept says what one Sweep deleted.
type Swept struct {
	// Entries are the vault's texts and paths, Batches and Segments the
	// ledger's, and Buckets the bucket store's day files.
	Entries, Batches, Segments, Buckets int
}

// Sweep deletes from each store what is past its period at now (DESIGN
// 4.4). It goes on to the other stores when one fails, and returns every
// error.
func Sweep(now time.Time, p Periods, s Stores) (Swept, error) {
	var out Swept
	var errs []error
	if s.Vault != nil {
		n, err := s.Vault.Sweep(From(now, p.Text), From(now, p.Paths))
		out.Entries = n
		if err != nil {
			errs = append(errs, fmt.Errorf("the vault: %w", err))
		}
	}
	if s.Ledger != nil {
		b, n, err := s.Ledger.Sweep(From(now, p.Ledger))
		out.Batches, out.Segments = b, n
		if err != nil {
			errs = append(errs, fmt.Errorf("the ledger: %w", err))
		}
	}
	if s.Buckets != nil {
		n, err := s.Buckets.Drop(From(now, p.Buckets))
		out.Buckets = n
		if err != nil {
			errs = append(errs, fmt.Errorf("the minute buckets: %w", err))
		}
	}
	return out, errors.Join(errs...)
}
