# Erratum: DESIGN 7.4's offline label rests on upload-failure notes that DESIGN 6.5's reveal set never opens, its lookback rule on a mark no record holds, and SP 8.4's clock correction on readings ingest doesn't send

Date 2026-11-22. Raised by WP-SV6, which builds `internal/server/witness`. Status: waiting for a decision. Until it is decided, `witness.Records` reads a note's time only from a revealed body, takes the lookback mark from its caller, and never corrects a record to a time after its own wall time.

## Input

- DESIGN 7.4's table: `offline` is "Late, but sealed on a continuous chain while the ledger holds upload-failure notes, then flushed in one catch-up", and `backfilled` is "Everything else late, and all lookback".
- DESIGN 3.6: the notes are records of rtype `0x41`, and "They support the `offline` label (7.4) and are the client's own claim". ledger-v1 4.7 gives a note's body as `{"t": t, "reason": r}`, and 4.3 gives it no project and the session `none`.
- DESIGN 6.5's reveal set opens the bodies of (a) records whose header project is in `P`, (b) every `os.presence`, `os.focus` and `os.power` record, (c) every `editor.activity` record, (d) the records of sessions that share a terminal with `P`, (e) `P`'s checkpoints and (f) every record of every project where focus or presence is unknown. "Every other record in an overlapping batch: header and body hash only." A note has no project, so no rule opens it.
- A lookback record's body is an event like any other (ledger-v1 4.5). The agent's writer tells lookback's records apart by `ledger.Input.Lookback` and by the chain's creation time (`internal/agent/ledger`, `Writer.Created`), and neither reaches a body or the server.
- DESIGN 7.4: "Event times are re-anchored from the monotonic clock within one boot". SP 8.4 corrects a device's clock by `server_received_ms - clock.wall_ms`, from a wall and a monotonic reading every batch carried at send time. ingest-v1's batch roots carry no clock (2.3.1), and `X-Pairtime-Time` is the client's clock already corrected by the offset it measured (2.1).

## What the code does

1. A note supports `offline` only when its body was revealed, since the server needs its time: the failure has to fall within `WITNESS_LAG_S` after the late record's batch closed, as the client's own reading has it (`internal/agent/uploader`, `waitedOut`). A header shows only where in the chain a failure happened. With DESIGN 6.5 as written, no record a submission reveals can read `offline`.
2. `witness.Record.Lookback` is the caller's. A record so marked reads `backfilled` whatever its lag, so a client that sends the mark can only cost itself credit. Without the mark, lookback's records read by their lag like any other, and those read from a harness's log in the 15 minutes before the chain began can read `witnessed`.
3. The corrected time is the earliest of the record's wall time, its batch's `received_at`, and the `received_at` of every later record of its boot by the monotonic clock, less the monotonic time between them. A record's credit lands at its wall time, which the client scored and a submission re-scores, so a correction to a later time would let a backdated record read fresh. A wall clock that runs fast therefore reads by the server's clock, and one that runs slow reads late by as much as it is slow: a device clock 20 minutes slow gets `backfilled` for live work, as its own minute buckets do at ingest.

## Proposed

- DESIGN 6.5's reveal set gains "(g) every upload-failure note in the batches that overlap the range, whose bodies hold a time and a reason code (ledger-v1 4.7), because 7.4's `offline` rests on them". WP-SB3's Build and CI checks gain the same rule.
- WP-SB3's metadata reveal names the records lookback read, or carries the chain's creation time so the server can name them itself: every `via = log` record dated before it. WP-SB4 passes the mark as `witness.Record.Lookback`.
- DESIGN 7.4 says what re-anchoring corrects: "against the server's receipts, and never to a time after the record's own wall time, where its credit lands, so a device clock that runs slow reads late". The agent knows its offset from the server (`internal/agent/uploader`'s clock), so `pairtime doctor` could warn when it passes a few minutes.
