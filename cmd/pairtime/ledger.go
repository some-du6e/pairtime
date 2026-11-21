// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	agentledger "github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

const ledgerUsage = `Usage: pairtime ledger show [--day YYYY-MM-DD]

Prints every record pairtime's ledger holds for one day, today unless
--day names another, one record per line in plain JSON (DESIGN 8.8). A
day is this computer's own, from midnight to midnight, and a record is
on it when the time it describes is, though it may have been written
later. Records hold numbers and keys, never what you typed or what the
agent wrote, which pairtime doesn't put in its ledger.

Each line has the record's number (seq), what it holds (type), the
digests of its project's and session's keys, the batch that seals it,
and its body exactly as the ledger stores it, so

  pairtime ledger show --day 2026-11-24 > records.jsonl

keeps them. Batch 0 is a record no batch seals yet, which a crash can
still take back. Records written under an earlier device key, as before
a restore, aren't shown. Standard error says how many there were.

Exit status:
  0  the day's records were printed, if it had any
  1  pairtime couldn't read its ledger, or some of it is damaged; the
     records it could read are printed
  2  usage error
`

func init() {
	register(command{
		name:    "ledger",
		summary: "Print every record of a day in plain JSON: pairtime ledger show --day <date>",
		run:     runLedger,
	})
}

// shownRecord is one line of `pairtime ledger show`: a record's header,
// field by field, and its body as stored.
type shownRecord struct {
	Seq  uint64 `json:"seq"`
	Type string `json:"type"`
	// RType is the header's type byte, which Type names.
	RType uint8 `json:"rtype"`
	// Project and Session are the header's digests of the record's project
	// and session keys in hex, and absent when it has none.
	Project string `json:"project,omitempty"`
	Session string `json:"session,omitempty"`
	// Batch is the batch whose seal closes the record, 0 while none does.
	Batch uint64 `json:"batch"`
	// Body is the record's body byte for byte (ledger-v1 4.5).
	Body json.RawMessage `json:"body"`
}

func runLedger(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "show" {
		if len(args) > 0 && (args[0] == "-h" || args[0] == "-help" || args[0] == "--help") {
			fmt.Fprint(stdout, ledgerUsage)
			return 0
		}
		fmt.Fprint(stderr, ledgerUsage)
		return 2
	}
	flags := flag.NewFlagSet("ledger show", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(flags.Output(), ledgerUsage) }
	date := flags.String("day", "", "the day to show, YYYY-MM-DD")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "pairtime ledger show: takes no arguments, got %q\n", flags.Args())
		return 2
	}
	day := *date
	if day == "" {
		day = localToday()
	}
	// The date is checked on its own: in a zone whose clocks jump over
	// midnight, as Chile's do, the day's midnight doesn't exist.
	asked, err := time.Parse(time.DateOnly, day)
	if err != nil || asked.Format(time.DateOnly) != day {
		fmt.Fprintf(stderr, "pairtime ledger show: --day wants a day such as 2026-11-24, got %q\n", day)
		return 2
	}
	start, end := dayStart(asked, 0, viewZone()), dayStart(asked, 1, viewZone())
	dirs, err := home.Default()
	if err != nil {
		fmt.Fprintf(stderr, "pairtime ledger show: can't find pairtime's folders: %v\n", err)
		return 1
	}
	out := bufio.NewWriter(stdout)
	n, timeless, err := showRecords(out, dirs.Ledger(), start, end)
	if ferr := out.Flush(); err == nil {
		err = ferr
	}
	switch n {
	case 0:
		fmt.Fprintf(stderr, "No records on %s.\n", dayTitle(day))
	case 1:
		fmt.Fprintf(stderr, "1 record on %s.\n", dayTitle(day))
	default:
		fmt.Fprintf(stderr, "%d records on %s.\n", n, dayTitle(day))
	}
	switch timeless {
	case 0:
	case 1:
		fmt.Fprintln(stderr, "1 record of a kind this pairtime doesn't know says no time, so it isn't on any day. A newer pairtime shows it.")
	default:
		fmt.Fprintf(stderr, "%d records of kinds this pairtime doesn't know say no time, so they aren't on any day. A newer pairtime shows them.\n", timeless)
	}
	if err != nil {
		sayStoreFailure(stderr, err)
		return 1
	}
	return 0
}

// dayStart is the first instant of the local day days after date's, in
// zone. Where the clocks jump over midnight, midnight doesn't exist, and
// time.Date may give the hour before it, on the day before: the day then
// starts when the zone that skipped midnight does.
func dayStart(date time.Time, days int, zone *time.Location) time.Time {
	y, m, d := date.Date()
	t := time.Date(y, m, d+days, 0, 0, 0, 0, zone)
	want := time.Date(y, m, d+days, 0, 0, 0, 0, time.UTC)
	if ty, tm, td := t.Date(); ty != want.Year() || tm != want.Month() || td != want.Day() {
		if _, next := t.ZoneBounds(); !next.IsZero() {
			t = next
		}
	}
	return t
}

// showRecords writes each record of the ledger in dir whose time is in
// [start, end), one per line, and returns how many it wrote, and how many
// records of kinds this version doesn't know it passed over because their
// bodies say no time (ledger-v1 4.2 leaves such bodies to the kind). A
// record is written at its time or later, by a clock that may be off from
// the one that gave its time by less than a day, so the segments of UTC
// days more than a day before start's can't hold one and aren't read.
func showRecords(w io.Writer, dir string, start, end time.Time) (n, timeless int, err error) {
	from, to := start.UnixMilli(), end.UnixMilli()
	first := start.Add(-24 * time.Hour).UTC().Format(time.DateOnly)
	err = agentledger.ReadRecords(dir, first, func(s agentledger.Stored) error {
		t, err := recordTime(s.Record)
		if err != nil && s.Record.Header.RType().Later() {
			timeless++
			return nil
		}
		if err != nil {
			return fmt.Errorf("record %d: %w", s.Record.Header.Seq(), err)
		}
		if t < from || t >= to {
			return nil
		}
		line, err := encodeShown(s)
		if err != nil {
			return fmt.Errorf("record %d: %w", s.Record.Header.Seq(), err)
		}
		n++
		_, err = w.Write(line)
		return err
	})
	return n, timeless, err
}

// recordTime is the time a record describes, in Unix ms: the t of its
// event, checkpoint or upload-failure note (ledger-v1 4.5, 4.7).
func recordTime(r ledger.Record) (int64, error) {
	b, err := ledger.ParseBody(r.Body)
	if err != nil {
		return 0, err
	}
	var e struct {
		T *int64 `json:"t"`
	}
	if err := json.Unmarshal(b.E, &e); err != nil {
		return 0, err
	}
	if e.T == nil {
		return 0, errors.New("its record has no t")
	}
	return *e.T, nil
}

// encodeShown is s's line. The body goes in as stored: it is compact JSON
// already, and with HTML escaping off the encoder leaves its bytes alone.
func encodeShown(s agentledger.Stored) ([]byte, error) {
	h := s.Record.Header
	r := shownRecord{Seq: h.Seq(), Type: h.RType().String(), RType: uint8(h.RType()), Batch: s.Batch, Body: s.Record.Body}
	if p := h.Project(); p != [ledger.ProjectDigestSize]byte{} {
		r.Project = hex.EncodeToString(p[:])
	}
	if ss := h.Session(); ss != [ledger.SessionDigestSize]byte{} {
		r.Session = hex.EncodeToString(ss[:])
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
