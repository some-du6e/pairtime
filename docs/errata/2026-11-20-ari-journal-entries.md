# Erratum: Ari refuses the journal entries DESIGN 5.10's bridge would send

Date 2026-11-20. Raised by the review of #13 (WP-PG0) in issue #44, written in PR #55. Status: decided 2026-11-20 by the orchestrator for Karim: one `journals[]` entry per row, valued as pairtime's review stands, with `text` and `seconds` rounded down; a row over 86,400 s cut at each UTC midnight; past 200 entries, one entry per person per UTC day, refused past that. DESIGN 5.10, WP-PG8 and `testdata/upstream/ari/` change in one docs PR. `testdata/upstream/ari/` already holds Ari's rules, and WP-PG8's CI check validates the bridge's output against them.

## Input

DESIGN 5.10's row for programs on Ari sends "pairtime rows as `journals[]` entries (`at`, `seconds`, and `markdown` saying "Tracked by pairtime" with the row's evidence link), one per devlog window". WP-PG8's Build line asks for the same. D13 (DESIGN 6.2) makes a submission a list of devlog windows, each the credited time between two consecutive devlogs, and a single window when the teen has no devlogs.

A teen with no devlogs and 30 credited hours on one project gets one window, so the bridge sends one entry:

```json
{"at": "2026-11-18T21:40:00Z", "seconds": 108000, "markdown": "Tracked by pairtime: **30 h** credited. Evidence: https://pairtime.example.org/review/submissions/sub_01JDEXAMPLE/rows/r_1"}
```

## Expected

Ari takes the ship, and its reviewer settles one journals row of 108,000 s (DESIGN 9.4, route B).

## What Ari does

It answers 422. ari-webhooks at `01a47d67aa91` (`internal/ingest/validate.go`, the validator `testdata/upstream/ari/` was written from) checks the list and then each entry, in this order:

- The list holds at most 200 entries, or it's 422 `journals` (line 207).
- `at`, or `date` when `at` is null, parses (lines 215-222).
- The length is at most a day. `seconds` must be a whole number from 0 to 86,400, and `minutes` or `hours` must come to at most 1,440 minutes. Anything else is 422 `journals.minutes`, even when the bad value is in `seconds` (lines 223-242, and `wholeSeconds` in `payload.go` lines 132-138).
- `text` isn't blank, or it's 422 `journals.text` (lines 243-246). `markdown` doesn't stand in for it. Ari falls back from a missing `markdown` to `text`, not the other way round (lines 258-261).

So the entry above gets 422 `journals.minutes`. With a day or less in it, it would get 422 `journals.text`, because DESIGN's entries have no `text`. Issue #14 already tracks that one. A submission of more than 200 devlog windows gets 422 `journals` whatever its entries hold.

The cap bites whenever one window holds more than 24 credited hours: a teen with no devlogs and more than a day of credit, or anyone who goes past 24 credited hours between two devlogs. Nothing in DESIGN limits how much credit a window on an Ari program can hold.

## What the capture does

`ingest.schema.json` already states all three rules. `#/$defs/journal_entry` requires `text`, its `seconds` has a maximum of 86,400, and `journals` has `maxItems` 200. `PROVENANCE.md` names them under "What a pairtime bridge has to send", and the pairtime example ship carries `text`. A bridge built to DESIGN 5.10 as written fails WP-PG8's own CI check, which validates its output against this schema.

## For Karim

1. `text`: add it to DESIGN 5.10's entry shape and WP-PG8's Build line (issue #14), for example a plain line that names pairtime and the window, as in `ingest.example-pairtime-journals.json`.
2. A window over 86,400 s. Either:
   - Split it into consecutive entries of at most 86,400 s, cut between work sessions, each with its own `at` and a `text` naming the window and the part. Every other window keeps its one entry, so Ari's reviewer still gets a row per devlog.
   - Send one entry per work session, D13's rows, instead of one per window. Ari's reviewer then cuts the same rows pairtime's review page shows. DESIGN 5.10's "pairtime rows" already reads that way, so only "one per devlog window" changes. The 200-entry limit then counts sittings, not devlogs.

   The first changes less, since every window that fits keeps DESIGN's one row per devlog. Both need a work session to fit in one entry. SM 5.3 breaks sessions at 20-minute gaps and caps a day at 12 credited hours, so a session over 86,400 s would have to run through a whole local day with no 20-minute break between credited minutes. The bridge still has to cut such a session if one turns up.
3. Past 200 entries: should the bridge merge adjacent windows until the ship fits, or refuse and ask the program to split the submission?

## Decision

Decided 2026-11-20 by the orchestrator for Karim. DESIGN 5.10 now has the bridge, and WP-PG8's Build and CI lines follow it.

1. `text` is added. It names pairtime and gives the row's start, end and seconds, and `markdown` adds the row's evidence link.
2. One entry per row, the second option: each work-session row, a loop row once a reviewer raises it (D12) and held minutes once a reviewer clears them (D7), valued as pairtime's review stands when the program builds the ship. Ari's reviewer then cuts the sitting pairtime's review page shows (D13; AD D5), and the cap never touches an honest teen, because credit can't pass wall time (SM 3.1, 3.4). A row over 86,400 s is still cut at each UTC midnight, and each part says "part k of n" with the whole row's start, end and seconds.
3. Past 200 entries, one entry per person per UTC day, and the bridge refuses past that. Day entries and midnight cuts use the frozen minute buckets, never a total split pro rata.

Three points this erratum didn't raise. `wholeSeconds` (`payload.go` lines 132-138) refuses fractions and pairtime credits in half-seconds, so `seconds` is rounded down, and a row that rounds to 0 sends nothing. Ari adds up every journal entry (`settlement.ts` lines 135-153, 170), so the bridge's entries are the only ones with seconds, and a program's own devlog entries go at 0 s and count toward the 200. Ari answers 409 `already_queued` to a ship sent under the `external_id` of one still open (`internal/ingest/process.go` lines 103-105), so a raise or clearance after the ship went out reaches Ari only when the program ships again.
