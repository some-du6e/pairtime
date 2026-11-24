# Erratum: SM 4.2's discounts and DESIGN 3.7's 600 s bound

Date 2026-11-23. Raised by WP-ERR1, from the question the [return-rule look-ahead erratum](2026-11-19-return-rule-lookahead.md) left open. Status: decided 2026-11-23 by the orchestrator for Karim: SM 4.2's counts read only what is on record before the prompt they test, and the rewrite loop reads a turn's edits on record within `READ_HORIZON_S` - `THINK_LEAD_S` - `COMPOSE_MAX_S` after its prompt (Decision, below). No fixture's output changes.

## Input

- DESIGN 3.7 and 6.2 D1, as the return-rule decision wrote them: every rule decides a second from what is on record within `READ_HORIZON_S` = 600 s after it, and a minute is final `PENDING_WINDOW_S` = 900 s after it ends.
- SM 4.2's rewrite loop: `overlap(k)` compares the lines the agent changed in turn `k` with those it changed in turn `k-1`. A repeat turn's discount withholds its lead, which reaches back up to `THINK_LEAD_S` + `COMPOSE_MAX_S` = 420 s before its prompt.
- SM 4.2's change events end the re-roll, fix-it, autopilot and rewrite counts. A typed command's `shell.command` is written when the command ends, a `run_green` or `error_resolved` is known only when its command ends, and a rejected permission only when its record pairs with the next tool event.

## Expected

Every rule decides a second from what is on record within the bound DESIGN 3.7 sets, or DESIGN 3.7 names where it doesn't.

## What the rules give

- The rewrite loop decides turn `k`'s lead from turn `k`'s own edits, which come whenever the agent makes them. An edit more than 180 s after its prompt decides lead seconds more than 600 s before it, and one 20 minutes in decides minutes that are already final.
- A change event recorded after a prompt it frees, such as a long typed command started before the prompt, decides that prompt's discount, and so its lead, as late as the command runs.
- Otherwise the re-roll, fix-it and autopilot counts read only records from before the prompt.

No fixture has a typed command or a rejected permission. Every rewrite repeat in `testdata/timelines/6.5` comes from an edit 35 to 40 s after its prompt, and the discounts of `4.2-same-error` and `4.2-alternation` are decided at the prompt.

## Decision

Decided 2026-11-23 by the orchestrator for Karim.

1. A change event counts from when it is on record: the later of its own time and its record's t (DESIGN 3.7). Each of SM 4.2's four counts reads only the change events, error signatures, outputs and prompts on record before the prompt it tests, so the re-roll, fix-it and autopilot discounts are decided at the prompt. A typed command still running when a prompt is sent doesn't free that prompt; it frees the prompts sent after its record. A run_green or error_resolved counts from the end of its command, and a rejected or edited permission from the record that pairs it with the next tool event. If shell integration later records a command's start (OSC 133/633), a typed command counts from that start record.
2. The rewrite loop reads turn k's agent edits on record within READ_HORIZON_S - THINK_LEAD_S - COMPOSE_MAX_S after prompt k (180 s with today's table), and turn k-1's edits on record before prompt k. A later edit doesn't count toward overlap(k). So a turn's repeat status is decided at most 180 s after its prompt, and every second of its lead within READ_HORIZON_S. No new parameter: the window is that formula, so it follows THINK_LEAD_S through calibration.
3. The reading and return credit an anchor decides before itself (SM 2.1's return rule, SM 2.4's draining within READ_HORIZON_S of a human anchor) is then decided up to 780 s after the second when the anchor's turn is a rewrite candidate: inside the pending window, with 120 s left for late records. DESIGN 3.7 names this beside D-relayed's two reaches, which are also bounded by the pending window rather than READ_HORIZON_S: a relayed lead waits for its pairing up to RELAY_MATCH_S after sent_at, up to 900 s after the lead's first second with no margin for late records (SM 2.3, SM 8's RELAY_MATCH_S row), and a stop's relayed reading waits for a pairing within 840 s of the stop (SM 2.4).
4. DESIGN 3.7 and D1 then name one part of SM that still lacks a bound: lookups (SM 2.5). Decided 2026-11-23 in [the lookup look-ahead erratum](2026-11-20-lookup-lookahead.md): a lookup is decided `READ_HORIZON_S` after its run's start.
5. No fixture's expected output should change: 6.5's rewrite rows rest on edits 35 to 40 s after their prompts, and 4.2-same-error's and 4.2-alternation's discounts are decided at the prompt. The applier confirms this and reports any row that moves instead of changing it. WP-SC6's CI checks gain two table tests: a turn whose overlapping edits come only after the window isn't a rewrite repeat; a typed command running across a prompt doesn't free that prompt but frees the next one.

SM 4.2 now states points 1 and 2, DESIGN 3.7 and 6.2 D1 points 3 and 4, and PLAN's WP-SC6 the two table tests of point 5.

Why the alternatives lost:

- Cutting each input at the reach, second by second, as `pkg/score` already does for a long typed command's lead: a discount would vary second by second inside one lead; heavy for the scorer, CC6 and checkpoints for little gain.
- Holding finality for a pending discount: unbounded, the same problem lookups have.
- A new `REWRITE_READ_S` parameter: the formula keeps the window tied to the lead it protects through calibration.
