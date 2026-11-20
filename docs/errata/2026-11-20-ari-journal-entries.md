# Erratum: Ari refuses the journal entries DESIGN 5.10's bridge would send

Date 2026-11-20. Raised by the review of #13 (WP-PG0) in issue #44, written in PR #55. Status: waiting for Karim. DESIGN 5.10 and WP-PG8's Build line stay as they are. `testdata/upstream/ari/` already holds Ari's rules, and WP-PG8's CI check validates the bridge's output against them.

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
