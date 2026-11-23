# Erratum: a log record from before its reader's run has no monotonic reading

Date 2026-11-21. Raised by WP-AD3. Status: waiting for a decision. Until one comes, a log record's `mono` stays as WP-AD3 writes it, below.

## Input

The Claude Code transcript reader (`internal/adapters/claudecode`, `logparse.go`, `env`) dates each record by the entry's own `timestamp`, `t`. It gives the record the clock of the agent run that read the line, which is what the agent's own events carry until they get the OS's boot clock (`ledger.RunClock`): `boot_id` is `run-` and a random id, one per agent process, and `mono` is that process's milliseconds since it started when it read the line, less the time since the entry, `mono_read + (t - t_read)`, never below 0.

Two cases give the reader lines from before its run began:

1. Lookback reads the last 30 days of transcripts at install (SM 2.8; DESIGN 5.9 step 9). Take WP-AD1's capture, whose entries are from 2026-11-20 between 16:48 and 17:10 UTC, read by an agent that started at 2026-11-21 08:00 and reads it at 09:00, with `mono_read` 3,600,000 ms. The entry at 16:48:30.275 is 58,289,725 ms before the read.
2. A session that goes on while the agent is down, as for an update's restart, a crash or a late start at login. Take a line written at 10:00:00 and an agent that starts again at 10:00:30 and reads the line at 10:00:31, with `mono_read` 1,000 ms.

## Expected

SM 1.1: "`mono` | int64 ms | Monotonic reading at the same moment; the server re-anchors `t` from it within one boot (SP 8.4)". SP 8.4: "Within one `boot_id`, event times are re-anchored from `mono` differences, so a clock changed mid-session can't move earlier events. If wall and monotonic deltas disagree by more than 2 s within one boot, the batch is flagged `clock_jump`."

## Computed

1. `mono` = max(3,600,000 - 58,289,725, 0) = 0, under the run's `boot_id`. Every lookback record gets `mono` 0 under one id. A server that re-anchors them from `mono` differences puts all of them at the run's start, 08:00 on 2026-11-21, and flags the batch `clock_jump`, since their wall times are hours and days apart while their `mono` values are equal.
2. `mono` = max(1,000 - 31,000, 0) = 0, under the new run's `boot_id`. Re-anchored, the line lands at the run's start, 30 s after it was written, and its wall delta to the run's other records differs from its `mono` delta by 30 s, so the batch is flagged `clock_jump`.

## Reasoning

No monotonic reading of the run exists for a moment before the run began, and the transcript gives none, so whatever pairtime writes there is made up. `pkg/score` and ingest don't read `mono` or `boot_id` today, so nothing goes wrong yet; the server's re-anchoring (SP 8.4) is still to be built, against SM 1.1's wording. Lookback is backfilled whatever its clock says (DESIGN 7.4), so its records gain nothing from re-anchoring. A line read in the run that was going when it was written has a `mono` that means what SM 1.1 says, to within the tailers' delay. Stamping log records with the OS's boot clock, as the hook path does (`internal/platform/clock`), would leave only the moments before the boot without a reading, but the agent's own events don't carry that clock yet either.

## Proposal

SM 1.1 says that a `source = log` record whose moment came before its clock began, the agent run's today, carries `mono` 0 and the `boot_id` `none`, which no clock has, and SP 8.4 re-anchors no record with that `boot_id`. Every other record keeps its clock. The alternative, re-anchoring no `source = log` record at all, would also lose the check on live lines, which can be re-anchored.
