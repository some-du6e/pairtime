# Erratum: a log record from before the reader's boot has no monotonic reading

Date 2026-11-21. Raised by WP-AD3. Status: waiting for a decision. Until one comes, a log record's `mono` stays as WP-AD3 writes it, below.

## Input

The Claude Code transcript reader (`internal/adapters/claudecode`, `logparse.go`, `env`) dates each record by the entry's own `timestamp`, `t`, and gives it the clock of the agent that read the line: `boot_id` is the reader's boot, and `mono` is the reader's monotonic reading when it read the line, less the time since the entry, `mono_read + (t - t_read)`, never below 0.

Lookback reads the last 30 days of transcripts at install (SM 2.8; DESIGN 5.9 step 9). Take WP-AD1's capture, whose entries are from 2026-11-20 between 16:48 and 17:10 UTC, read by an agent whose computer booted at 2026-11-21 08:00 and that reads it at 09:00, with `mono_read` 3,600,000 ms. The entry at 16:48:30.275 is 58,289,725 ms before the read. The same happens to a live session's lines that the agent reads after a reboot, when the session wrote them before it, as when the agent starts late at login.

## Expected

SM 1.1: "`mono` | int64 ms | Monotonic reading at the same moment; the server re-anchors `t` from it within one boot (SP 8.4)". SP 8.4: "Within one `boot_id`, event times are re-anchored from `mono` differences, so a clock changed mid-session can't move earlier events."

## Computed

`mono` = max(3,600,000 - 58,289,725, 0) = 0, with the current boot's `boot_id`. Every lookback record before the boot gets `mono` 0 in the same boot. A server that re-anchors them from `mono` differences puts all of them at the boot's start, 08:00 on 2026-11-21, and flags the batch `clock_jump`, since their wall times are hours and days apart while their `mono` values are equal.

## Reasoning

No monotonic reading exists for a moment outside the reader's boot, and the transcript gives none, so whatever pairtime writes there is made up. `pkg/score` and ingest don't read `mono` or `boot_id` today, so nothing goes wrong yet; the server's re-anchoring (SP 8.4) is still to be built, and it would be built against SM 1.1's wording. Lookback is backfilled whatever its clock says (DESIGN 7.4), so its records gain nothing from re-anchoring. A live line read within its own boot has a `mono` that means what SM 1.1 says, to within the tailers' delay.

## Proposal

SM 1.1 says that a `source = log` record whose moment came before the reader's boot carries `mono` 0 and the `boot_id` `none`, which no boot has, and SP 8.4 re-anchors no record with that `boot_id`. Every other record keeps its boot's clock. The alternative, re-anchoring no `source = log` record at all, would also lose the check on live lines, which can be re-anchored.
