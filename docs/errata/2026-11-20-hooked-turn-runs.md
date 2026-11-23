# Erratum: how long a turn runs before its stop comes

Date 2026-11-20. Raised by WP-SC2's review fix-up. Status: decided 2026-11-20 by the orchestrator for Karim, with the turns-without-a-stop erratum: both additions stand and SM 1.3 states them, an `agent.interrupt` ends a turn as a stop does, and a gap of more than `READ_HORIZON_S`, the bound DESIGN 3.7 sets for every rule, ends the run of a turn no hook or log reader sees (Decision, below). It follows on from [the turns-without-a-stop erratum](2026-11-19-turns-without-a-stop.md), and it was raised by the #52 review (#80).

## Input

- The turns-without-a-stop erratum's rule: "A turn with no `agent.stop` runs from its prompt to the latest of its agent records the scorer has received so far, and doesn't run while it has none. A later prompt in the session ends a turn either way." It was written for track and bridge turns, which never record a stop.
- A turn that hooks or a log reader see has no `agent.stop` either until its Stop hook fires (SM 1.2). Until then its agent records can pause for a long time. A Claude Code turn that runs one 40-minute Bash build writes its `agent.tool` when the call ends, from `PostToolUse`. One that waits 40 minutes on a permission prompt writes its `agent.permission` after the answer, when the request pairs with the next tool event.
- DESIGN 3.7 and 6.2 D1: a minute is final `PENDING_WINDOW_S` = 900 s after it ends. `pkg/score` keeps what it derived pending for `PENDING_WINDOW_S` + `RETURN_GAP_MAX_S` + `READ_HORIZON_S` = 2,100 s behind the latest record (the [return-rule look-ahead erratum](2026-11-19-return-rule-lookahead.md)).
- SM 2.4 (`SUP` while the agent runs, `READ` after), 2.5 (browser time during an agent run is waiting, not a lookup), 5.1 (`agent_running_s`) and 5.2 (`X.agent_only`) all read whether a turn is running.

## Expected

A turn counts as running for as long as its agent works on it, in every minute, and a Step per minute gives what one Step over the same records gives, as a re-score after the stop came does (DESIGN 6.5).

## What the rules give

Under the last-record rule, a hooked turn whose agent records pause for longer than the 2,100 s that stay pending goes final as if it had stopped. Its minutes get `agent_running_s` 0, `READ` instead of `SUP`, `X.no_anchor` instead of `X.agent_only`, and a browser run in that time can count as a lookup. When the stop comes, one Step over all the records runs the turn up to its stop, so the final minutes don't reproduce.

The erratum's "received so far" leaves the same gap for any turn, as its last paragraph says. When two of a turn's agent records come further apart than what stays pending, the turn runs in the minutes still pending when the later record comes, but not in the final ones before them, and a re-score of the same records runs it through both.

## What the scorer does

It keeps the last-record rule for turns that no hook or log reader sees: the track, plugin and bridge turns that erratum is about. It adds two things.

1. A turn in a session that hooks or a log reader see runs up to "now" until its stop comes, since its hooks will report the stop. It runs no further than `BUCKET_TTL_S` after its last agent record, or after its prompt while it has none.
2. A record changes nothing derived more than `READ_HORIZON_S` = 600 s before its own time, the bound DESIGN 3.7 sets for every rule (docs/errata/2026-11-19-return-rule-lookahead.md). So a silence of a turn's agent records longer than `READ_HORIZON_S` ends the run of a turn no hook or log reader sees at the last record before the silence. In a hooked session, a silence longer than `BUCKET_TTL_S` ends the run `BUCKET_TTL_S` after the last record. A later record of the turn doesn't start it again. A continuation whose first record comes more than `READ_HORIZON_S` after the session's previous stop starts at that record, not at the stop.

The live scorer and a re-score of the same records then agree. `turnBuild.runUntil` in `pkg/score/turns.go` is the code. No fixture row changes: every turn in the side-door cases (a), (b) and (e) keeps its run.

## For Karim

1. Accept the two additions, or pick another bound for a hooked turn without its stop. `BUCKET_TTL_S` keeps a turn whose session crashed before its Stop hook running for up to two hours after its last record, with agent time and `X.agent_only` on every idle second, until a later prompt or a `session.end` ends it. A shorter bound, such as the 2,100 s that stay pending, would end a 40-minute build's turn before its stop.
2. Whatever is chosen belongs in SM 1.3, beside the turns-without-a-stop rule.

## Decision

Both additions stand, and SM 1.3 states them (the turns-without-a-stop erratum's Decision), with an `agent.interrupt` ending a turn as a stop does. `BUCKET_TTL_S` stays the bound for a turn whose hooks or logs will report how it ends. A shorter one, such as what stays pending, would end a 40-minute build's turn before its stop. The cost, up to two hours of agent time after a crash that leaves no `session.end`, credits nothing. It only withholds that project's lookups in the meantime.
