# Erratum: on a computer whose agent can't sample, the scorer stopped finalizing 15 minutes after each agent start

Date 2026-11-23. Raised by WP-HL1, from a linked alpha device's counts and times. Status: decided 2026-11-23 by the orchestrator for Karim, as the Decision section below records: the proposal, which #234 applies to SM 2.1, `pkg/score` and the agent's scorer loop as scoring 0.5.0, under `oracle-change`.

## Input

A Linux server with no graphical session runs pairtime v0.1.0-alpha.6, linked and online, beside an agent app's server (T3 Code) that runs Claude Code sessions. It has no idle source and no focus channel (`internal/agent/presence/presence.go`, `internal/agent/focus/os_linux.go`). Its ledger holds no `os.presence` and no `os.power` record since 2026-11-21, and five `os.focus` records, each `app_class` `unknown`: since #192 the focus component writes one each time the agent starts and never repeats it. Its minute buckets stopped about 15 minutes after each agent start, while batch roots kept flowing. Stretches of up to three hours between starts were never derived, and two of the minutes left pending reached the server as empty final revisions. The scorer's window stayed where it started, so each Step re-scored about 57,000 records and the agent ran at about 38% CPU. `doctor` said ok throughout.

The code it ran:

- `pkg/score/score.go`: the scorer's present is `min(high, own + PENDING_WINDOW_S)`, where `high` is the latest record's `t` and `own` the latest `os.presence`, `os.focus` or `os.power`. `FinalBefore` is the slot of `now - PENDING_WINDOW_S`.
- `pkg/score/presence.go`: with no sample, slots start at the later of the first record and `BUCKET_TTL_S` before now. A device counts as never sampling, which gives it whole minutes and SM 5.2's codes for a live slot whose presence is unknown, only when it has no `os.presence`, `os.focus` or `os.power` record at all.
- `internal/agent/scorer/loop.go`: a minute whose latest revision is pending and that a Step's view doesn't hold gets an empty revision, final once `FinalBefore` passes it.

## Expected

- SM 2.1: `unknown` is a slot where "the sampler wasn't running: ... a machine with no desktop to sample, such as a server", and SM 5.2 gives "every slot of a server with no desktop" the codes for a live slot whose presence is unknown.
- DESIGN 3.7 and 6.2 D1: a minute stays pending for `PENDING_WINDOW_S` after it ends, and then it is final.
- SM 5.1: a minute in which an agent ran has a bucket, with its `agent_running_s`.

## Computed

Take an agent that starts at T0, so its `os.focus` is at T0, and runs turns for hours.

1. `own` is T0, so `now` is at most T0 + 900 s and `FinalBefore` at most T0. No minute after T0 becomes final, and none after T0 + 15 minutes is derived.
2. The agent restarts at T1 and writes its next `os.focus`. `now` becomes T1 + 900 s, and with no sample the slots start at T1 + 900 s − 7,200 s. The minutes from T0 + 15 minutes to about T1 − 1 h 45 min are never derived. The pending minutes from T0 to T0 + 15 minutes aren't in the Step's view, so the loop gives each an empty final revision, zeroing its agent time.
3. The window starts `Lookback` before `FinalBefore`, which stays at T0, so it keeps every record since T0 − 6 h and grows until the next start.
4. Because of the `os.focus` records, the device doesn't count as never sampling, so its uncredited seconds are `X.absent` rather than SM 5.2's codes for a live slot whose presence is unknown, and its slots aren't whole minutes.

The same holds on a desktop that gives no idle time, KDE and wlroots among them (`internal/agent/presence/os_linux.go`), where the user is at the keyboard.

## Options

1. **The agent repeats a timed record.** While a project context is live, the agent writes an `os.focus` saying focus is `unknown` every `SLOW_SAMPLE_S`, so `own` keeps up. The scorer's rules stay as they are. But the record would say nothing new each minute, against SM 1.2's "one record with `unknown` each time focus turns unknown". Ledgers written before the change keep the stall. The slots after a silence longer than `BUCKET_TTL_S` still start `BUCKET_TTL_S` before now, so the minutes pending before the silence are still lost. And the device still doesn't count as never sampling.
2. **The scorer's clock reads only the presence sampler's records.** An `os.presence` comes every 15 to 60 s while a project context is live, and an `os.power` comes with one, since a power event takes a sample at once (DESIGN 3.3). In the agent both come from the presence sampler, which records neither on a device with no idle source. An `os.focus` comes only when focus changes, so it says nothing about how far the present has come. On a device with neither, nothing else keeps time, and the latest record does, as it already does for a device with no OS record at all (#219's relayed cases). The same records decide whether a device never samples.

## Proposal

Option 2, which is the smallest rule SM can state, is deterministic from the records, so the server's re-score (DESIGN 6.5, CC6) gets the same result, and also mends ledgers already written once the agent re-derives them.

1. **The clock.** The scorer's present is the latest record's `t`, but no more than `PENDING_WINDOW_S` past the latest `os.presence` or `os.power`. With neither, it is the latest record's `t`. So a minute is final once a record is dated `PENDING_WINDOW_S` past its end, and on a sampling device also a sample or an `os.power` dated past its end. On a sampling device every `os.focus` comes with a sample at its time (DESIGN 3.3), so this changes nothing there, except after an `os.focus` that comes after the last sample. There `FinalBefore` used to run ahead of the samples, into slots no sample had decided yet, which were then never derived.
2. **Never sampling.** A device never samples while its agent runs when it has no `os.presence` and no `os.power` record and some record came by a path other than a log reader's. Its `os.focus` records don't count. Every live slot there is `unknown`, whole minutes are derived, and SM 5.2's codes apply, as for #219's server with no OS record.
3. **The slots of such a device** start `BUCKET_TTL_S` before the first record its agent wrote by a path other than a log reader's, as a sampling device's start `BUCKET_TTL_S` before its first sample, instead of `BUCKET_TTL_S` before now. A Step after a silence longer than `BUCKET_TTL_S`, or after a restart, then derives the minutes still pending before it, and the agent's window, a fresh `State` each Step, derives every minute from the window's start. One Step over such a device's records now gives what a Step a minute gives, which it didn't for streams longer than `BUCKET_TTL_S` (`scoreRelayed`'s old note).
4. **No empty final for a minute no Step derived.** The loop gives an empty revision only to a pending minute the Step derived with no credit and no agent time. A pending minute the Step didn't derive at all keeps its latest revision, and stays pending unless a later Step derives it. Once `FinalBefore` passes it, no Step can make it final, and the loop stops looking at it, as `pkg/score`'s `Revisions` already does with a minute it no longer derives. After 3, this happens only to a minute whose records the ledger no longer holds, or that an earlier version left pending.
5. **Version.** Rules 1 to 3 change outputs on such devices, so `pkg/score/VERSION` becomes 0.5.0 (DESIGN 6.6, a 0.x MINOR), and `BuiltinScoringVersion` with it. No timeline's golden changes except in `v`: no timeline has an `os.focus` without samples. The evidence definition 0.4.0 covers 0.5.0, since DESIGN 6.4 doesn't change. PLAN's WP-SC6 takes 0.6.0.

### What SM 2.1 gains

After the paragraph on hard breaks:

> A machine whose agent can't sample, one with no desktop or with a desktop that gives no idle time (KDE, wlroots), records no `os.presence` and no `os.power`, so every live slot there is `unknown`. The `os.focus` its agent writes as it starts, saying focus is `unknown` (1.2), is no sample. There the records do what the samples do elsewhere. The slots start `BUCKET_TTL_S` before the first record the agent wrote live, by any path but a log reader, as a sampling machine's start `BUCKET_TTL_S` before its first sample. And a minute is final once a record is dated `PENDING_WINDOW_S` past the minute's end (DESIGN 3.7), where a sampling machine also needs a sample or an `os.power` dated past the minute's end, so that a record dated ahead of the clock waits for the samples.

DESIGN doesn't change: the agent records what it recorded before, and DESIGN 3.7's "a minute stays `pending` for `PENDING_WINDOW_S` = 900 s after it ends" holds with SM 2.1's clock.

## Decision

Decided 2026-11-23 by the orchestrator for Karim, on #234:

1. The scorer-side rule is taken over repeating a timed `os.focus`. The scorer's clock reads only the presence sampler's records, `os.presence` and `os.power`, and with neither, now is the newest record. The same records decide whether a device never samples. This rescues ledgers already written, records nothing new, and needs no change to DESIGN 3.3.
2. On a device that never samples, a record dated ahead of the clock moves now, as the first limit below says. Submission already refuses a record dated more than 5 minutes after its batch reached the server (DESIGN 7.4), so the window this opens is bounded.
3. A minute no Step derived stays pending and is never made final from a stale revision. Only a minute a Step derived with nothing in it gets an empty revision.

## Limits

- On a device that never samples, a record dated ahead of the device's clock, such as a bridge prompt with a skewed transcript timestamp, moves the present with it, so the minutes before its time become final early and the records that come after them can't change them. That was already so for a device with no OS record, and a submission refuses a record dated more than 5 minutes after its batch reached the server (DESIGN 7.4), which bounds how far ahead such a record can be. Bounding the present by the records pairtime stamps itself, a hook's, an app reader's or an OS watcher's, would guard against it, but it would stall a device whose only harness records come by a log reader, such as Codex with untrusted hooks, Claude Code with its folder trust pending or a `--bare` run, which is the failure this erratum fixes.
- A machine that samples at some times and not at others, as a desktop reached over SSH while its graphical session is logged out, still waits for its next sample: its slots after the last sample stay undecided until one comes, and `FinalBefore` waits with them, as before.
- After a long silence, the first Step on a device that never samples derives every slot of the silence once, as a sampling device's first Step after a long suspend already does.
- A minute that the old code gave an empty final revision stays as it is, since a final revision never changes. On a device that ran the old code, the first Step after the upgrade derives every minute from where the scorer stopped, up to the window's records.
