# Erratum: DESIGN 3.10 raises `concurrent_devices` inside ingest, for hours that DESIGN 4.2 and 8.5 and the privacy notice say are never checked for fraud

Date 2026-11-20. Raised by the privacy review of #137 (WP-SV7) in issue #142. Status: decided 2026-11-20 by the orchestrator for Karim (D-no-ingest-flags). Ingest and the rollups never store a fraud flag for hours nobody submitted. The cross-device merge stays at ingest. `concurrent_devices`, like every tier-2 flag of SM 4.5 and 8.7, is computed only when a submission is frozen, over the submitted window, from the per-device minutes the rollups keep for 180 days. #137 does this. A later docs PR changes DESIGN 3.10's wording.

## Input

- DESIGN 3.10's table, row "Cross-device merge", inside ingest: "One human minute per wall-clock minute per account; conflicts raise `concurrent_devices` (SM 3.4)". PLAN WP-SV7 builds the merge "with the `concurrent_devices` flag (SM 3.4)", and its CI checks include "two devices crediting one minute keep one and raise the flag".
- SM 3.4: of two devices crediting one slot, the server keeps one, "drops the other, and raises `concurrent_devices` for the review queue". SM 4.5 and 8.7 list `concurrent_devices` among the tier-2 flags that set review priority and audit probability.
- DESIGN 4.2, tier 1: "Nothing in this tier is scored for fraud unless the teen submits (TP B2 point 6)".
- DESIGN 7.2, rank 6: "Server risk score, on submitted windows only".
- DESIGN 8.1, purpose "Fraud protection": "Checks, risk scores and audits, only on windows the teen submits (TP B2 point 6)".
- DESIGN 8.5: fraud scoring "runs only on windows a teen submits, never on logged-only hours (TP B2 point 6)".
- DESIGN 9.7, item 3.6: "Flags never run on unsubmitted windows".
- The privacy notice pairtimed serves (`internal/server/pages/privacy.md`): "Hours are checked for fraud only when you submit them to a program", "pairtime doesn't score your other hours for fraud", and "Fraud checks only ever run on hours you submit, so hours you never submit are never checked".

## What the code did

#137 at 271dc63 wrote a `rollup_flags` row for every minute in which two of an account's devices credited the same slot. It did so inside the writer transaction that stores each bucket revision, whether or not anyone had submitted the minute, and kept the row for 180 days "for the review queue". So a teen with two computers who never submits got a stored fraud flag for each such minute, while the notice told them those hours are never checked. `docs/retention.json` listed the rows only as "a note on each minute in which two of your devices counted the same 15 seconds".

## Decision

DESIGN 3.10's "conflicts raise `concurrent_devices`" yields to DESIGN 4.2, 7.2 rank 6, 8.1, 8.5 and 9.7 item 3.6, and to the notice:

1. Ingest still merges an account's devices one minute at a time, as SM 3.4 says, so every total counts one human minute per wall-clock minute. The merge needs no stored flag.
2. Nothing stores `concurrent_devices`, or any other fraud flag, for a minute nobody has submitted. Migration 0005 has no `rollup_flags` table and no retention job for one.
3. The submission freeze (WP-SB1) computes `concurrent_devices` over the submitted window from the per-device minutes `rollup_minutes` keeps for 180 days, the furthest back a submission reaches. SM 3.4's review queue holds submissions, so the flag still reaches it.
4. `docs/retention.json` lists no note on minutes that two devices both counted, and says what the lifetime rollup keeps: daily totals per project and kind of work, split by evidence level, and the agent's own time per project.

## What the code does

`rollup.Apply` merges as before and keeps one slot, and it writes no flag. `rollup.ConcurrentMinutes` counts a window's minutes in which two devices credited one slot, by reading `rollup_minutes` and merging each minute as `Apply` does. Nothing calls it until WP-SB1's freeze does. `TestApply` checks that two devices crediting one slot keep one, with the minute counted concurrent over its window. `TestApplyWritesOnlyRollups` checks that `Apply` writes no table but the rollups' own. `TestConcurrentMinutes` checks the count over the window it is asked about.

## For the docs PR

DESIGN 3.10's "Cross-device merge" row and PLAN WP-SV7's Build and CI checks lines should say that the submission freeze computes the flag over the submitted window, and that ingest doesn't raise it.
