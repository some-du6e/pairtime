# Erratum: a log record from before its reader's run has no monotonic reading

Date 2026-11-21. Raised by WP-AD3. Status: decided 2026-11-23 by the orchestrator for Karim (E-log-record-clock), amended: a `mono` of 0 is the one mark of no reading, for every source, and no `boot_id` value is reserved. Applied to SM 1.1, DESIGN 7.4, PLAN's WP-AD4 and the lookback fixtures' READMEs by the docs PR that carries this decision.

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

Neither harm happens on main. DESIGN 7.4, as #203 amended it, never corrects a record to a time after its own wall time, and `internal/server/witness` puts a record with `mono` 0 into no boot, so it neither bounds another record nor is bounded by one. DESIGN and SM 8.7 have no `clock_jump`. The arithmetic stands: the first user entry of WP-AD1's capture is 16:48:30.275, 58,289,725 ms before the read, and today's reader gives 0 in both cases through its 10 s limit as well.

## Reasoning

No monotonic reading of the run exists for a moment before the run began, and the transcript gives none, so whatever pairtime writes there is made up. `pkg/score` and ingest don't read `mono` or `boot_id` today, so nothing goes wrong yet; the server's re-anchoring (SP 8.4) is still to be built, against SM 1.1's wording. Lookback is backfilled whatever its clock says (DESIGN 7.4), so its records gain nothing from re-anchoring. A line read in the run that was going when it was written has a `mono` that means what SM 1.1 says, to within the tailers' delay. Stamping log records with the OS's boot clock, as the hook path does (`internal/platform/clock`), would leave only the moments before the boot without a reading, but the agent's own events don't carry that clock yet either.

## Proposal

SM 1.1 says that a `source = log` record whose moment came before its clock began, the agent run's today, carries `mono` 0 and the `boot_id` `none`, which no clock has, and SP 8.4 re-anchors no record with that `boot_id`. Every other record keeps its clock. The alternative, re-anchoring no `source = log` record at all, would also lose the check on live lines, which can be re-anchored.

## Decision

Decided 2026-11-23 by the orchestrator for Karim (E-log-record-clock). The proposal's aim stands, since a record with no reading is never re-anchored, and its means change:

1. **0 is no reading.** A `mono` of 0 is no reading, whatever the record's source and `boot_id`. The server neither re-anchors such a record nor bounds another by it, so it reads by the earlier of its `t` and its batch's `received_at`. The agent's writer holds it to the local-only stretches by its `t`, or, for a lookback record, by when the writer took it in (`Writer.lookback`, D-late-local).
2. **No reserved `boot_id`.** The proposal's `none` is not taken. A record with no reading keeps the id of the clock it would have read: the reading run's for a log record, the request's boot for a permission answer. A stamp the OS gave no reading for, as a hook call's when neither the hook nor the agent could read the OS's clock, takes the agent run's own id with `mono` 0, since `pkg/event` refuses an empty `boot_id`.
3. **When a record has none.** A record carries 0 where no reading of its clock exists for its moment, as for a log line dated, by the wall clock, before the run that read it. It also carries 0 where its reading would rest on more than 10 s of the wall clock, which a suspend or a step can put any distance off: a log line or a WakaTime heartbeat the agent takes in more than 10 s after the time it states, or a permission answer more than 10 s after its request. This widens the proposal's "before its clock began" to the late reads the readers already treat this way.
4. **No derived reading runs ahead.** A reading derived from the wall clock over 10 s or less is never more than the agent's own reading of that clock when it took the record in. A line or a heartbeat carries that reading less the age it states. A permission answer carries the request's reading moved on by the wait, capped at the agent's reading of the request's boot when it took Claude Code's report in, and 0 without one. So a record's stated time can only loosen the bounds its intake gives the other records of its boot.
5. **No `clock_jump`.** SP 8.4's flag stays out. DESIGN 7.4 and SM 8.7 don't have it, D-no-ingest-flags keeps every flag off minutes nobody submitted, and a log record's `t` is its line's own claim, so a check of wall against monotonic deltas would let a line's stated time flag the agent's batches.
6. **The 10 s is not a parameter.** The code calls it `LATE_READ_S`: claudecode's and the WakaTime bridge's `lateRead`, the agent's `scanLate`. Neither `pkg/score` nor the server reads it, so it stays out of SM 8's table and DESIGN 6.3. DESIGN writes it as 10 s, because DESIGN reads a name in capitals as a table parameter.

`none` was turned down for three reasons. 0 is already the one marker every consumer reads. Alpha ledgers already hold `run-` records with `mono` 0 (D-late-local's batch 4269), which a signed chain can't rewrite. And no consumer has to change, where a reserved id would be a second marker for every reader to check.

## What the code does

Already as decided:

- claudecode's `env`: 0 past 10 s, the clamp at 0 and at the read's reading, and the arrival's `boot_id` kept. The WakaTime bridge's `monoAt` follows the same rule with its batch clock's `boot_id`.
- `internal/agent/logs.go`'s `scanStart` (`scanLate`), and `internal/agent/git.go`'s `sink`, which takes the scan's own reading (`git.Change.Found`). #222's T3 reader stamps each `relay.message` with a reading at its own `t`, when it read the row.
- `internal/server/witness`'s `correct`, where only a record with `mono` above 0 joins a boot (TestReanchoring's "a record without a monotonic reading neither bounds others nor is bounded"), and `internal/agent/ledger`'s `moment.before` and `reached`, the stretch edges and `Writer.lookback`, which read a reading only where `mono` is above 0.

The code PR after this one:

- `permission.go`'s `Decide` moves the request's reading on by the wall-clock wait with no cap, so a forward step during a wait of up to 10 s can put the answer's reading up to 10 s ahead of the agent's own, which point 4 forbids. Nothing calls `Decide` yet. WP-AD4's receiver passes its own OS-clock stamp of the export's arrival in `ToolDecision`, and `Decide` caps the reading with it (PLAN, WP-AD4).
- A hook call whose hook and agent stamps both lack an OS reading gets an empty `boot_id` from `hookpath.Times`, which `pkg/event` refuses, so every hook call on such a computer is lost. The agent's `adapterHost.read` gives such a call this run's id with `mono` 0 (point 2).
- `sink`'s branch for a change with no `Found` derives a reading from the wall clock with no 10 s limit. `scan.go` always sets `Found`, so nothing reaches it, and it goes: a zero `Found` gives 0 through `ledger.RunClock`.
- `internal/server/witness`'s package doc and `Records` say a reading must be taken at its record's own time, which the readers' derived readings aren't. They state point 4's rule instead.
- `pkg/event`'s comments and the schema's descriptions of `t`, `mono` and `boot_id` follow SM 1.1. Validation stays: `mono` at least 0, `boot_id` a non-empty key.
- A claudecode test pins the reader's clock, `boot_id` included, which nothing tests today. `internal/agent`'s TestLogRecordsByTheRunClock already pins the `mono` of a line read promptly, exactly 10 s late, 10,001 ms late and an hour late.

## Still open

- A true reading of 0, an event in a run's first millisecond, reads as none and drops one bound.
- When the agent's own events move to the OS's boot clock, log records move with them. `Writer.lookback` and the server's lookback mark (docs/errata/2026-11-22-server-witness-inputs.md, decision 2) look for a reading of a run's clock, a `boot_id` that starts with `run-`, so they change in the same PR.
- The lookback fixtures' log records keep a live boot's `mono` and `boot_id`, which no scoring rule reads. Their READMEs say what the reader would write instead.
- A line written before the run whose stated time reads, by the wall clock, after the run's start and no more than 10 s before its read, as after a backward step of the wall clock between the write and the read, isn't dated before the run by the wall clock, which is what point 3 tests, so it takes the reading derived from its stated age. The 10 s limit doesn't catch it, since a step back shortens the age a line states whatever its true age, and only point 4's cap, the agent's own reading when it took the line in, bounds that reading. The reading puts the line in its run's boot: in the server's re-anchoring it bounds each record of that boot with a lower reading, where 0 would bound none, so it can move such a record's corrected time earlier. The cap keeps each such bound no tighter than one a record the agent took in at that moment would give, so a record whose reading is its own is never put before it was made.
- The agent's writer holds such a line to the local-only stretches by that reading, as made in this run, after every edge an earlier run recorded (`Writer.earlier`). So a line made in an earlier run's local-only stretch, which that run hadn't taken and this run reads after such a step, is sealed online, where with 0 the stretch would hold it by its `t`. The line and that stretch share no monotonic clock, only the wall clock that stepped, so this falls within D-late-local's known limit for a step of the wall clock where an event and the moment share no monotonic clock (docs/errata/2026-11-23-late-local-records.md, Still open). A later decision may give such lines 0.

This file, SM 1.1, DESIGN 7.4, PLAN's WP-AD4 and the lookback READMEs ship in one docs-only PR. The code PR after it carries `pkg/event`'s comments and the schema in one commit, the witness comments, the hook-call fill, `sink` and the claudecode test.
