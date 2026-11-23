# Erratum: what WP-LO1 part 6 changes in the late-local erratum's limits

Date 2026-11-23. Raised by WP-LO1 part 6 (#263), which fixes #260's items: the CodeRabbit comments on #224 that a triage found still hold on main, and the `local_until` fold that #224's review left for after the merge. docs/errata/2026-11-23-late-local-records.md is an oracle, so this file records where #263 makes that erratum's Known limits and What the code does out of date. Status: open, for the orchestrator to decide.

## Input

- docs/errata/2026-11-23-late-local-records.md, Known limits, second item: "`local_until`, the newest stretch end, stays in every row and seal frame however old it is (#174), and in the base frame a sweep writes, as the moment the list's row recorded. Folding it touches the downgrade guard (`restoreStretches`), the uploader's straddle rule and the rebuild of state.json's modes, so it goes to a follow-up after #224, which folds the base frame's copy too."
- The same erratum's What the code does: "The writer keeps at most 64 stretches and merges the oldest two past that, which only makes more of the past count as local-only."
- #260's last item: fold a nonzero `local_until` older than the sweep cut, and check the downgrade guard, the uploader's straddle rule and the `state.json` modes rebuild.

## Expected

A retention sweep keeps no time `config.toml` changed mode from before the ledger's 180 days in `batches.seg`: not in the stretches (DESIGN 3.6's fold), and not in the moment each row records either. Merging stretches past the cap only makes more of the past count as local-only, as the old erratum says.

## Computed

On main at 858ca285:

- Every row a sweep keeps carries its `local_until` however old, and so does the base frame.
- The merge kept the second stretch's end and its passed state. After a step back of the wall clock between the two oldest stretches, that lost the first's wall range, so a late record made in it, judged by the wall clock alone, sealed online (#260, r4666785401).

## What the code does with #263

- **`local_until` folds at a sweep.** A nonzero moment older than the sweep's day becomes the start of that day by the wall clock alone, with no OS reading. That holds for each row the sweep keeps, for the base frame's moment, and for the writer's own, which its next seal frame records. Checked against its three readers:
  - The downgrade guard holds the kept rows to the base frame's moment. Both fold alike, so no kept row rises past it and ends a stretch going on.
  - The uploader's straddle rule reads the moment of the batch after a local-only gap. That batch's moment is that stretch's end, which comes after the gap's batches, so it folds only when the gap's rows are kept past the cut, as unsent rows a floor keeps are. There it moves the straddle's end later by the wall clock, which withholds more minutes, never fewer.
  - The rebuild of `state.json`'s modes takes the newest row's moment as the stretch's end. A folded one gives the cut, and the agent's Seen of it at the next Open ends nothing the writer doesn't have.
- **The merge past 64 stretches** takes the oldest adjacent pair alike passed or not, from the earlier start to the later end by the wall clock. When no pair agrees, it takes the two oldest, passed when either is, which can only drop a record of them (#260, r4666785401).
- **A full sweep of an older version's chain** keeps the stretches Open rebuilt from its rows and the writer's moment in the base frame. With no row kept, Open takes its moment from the base frame (#260, r4666785408).
- **A stretch the agent saw end** begins at the end of the newest stretch that ended before it, when the writer knew `config.toml` said online only after that end, by a clock the two share (#260, r4666785394).

## Still open

- A seal frame still keeps the `local_until` it was sealed with until its day's segment goes, as it keeps its stretch list: the old erratum's first known limit covers both.
