# Erratum: what WP-LO1 part 6 changes in the late-local erratum's limits

Date 2026-11-23. Raised by WP-LO1 part 6 (#263), which fixes #260's items: the CodeRabbit comments on #224 that a triage found still hold on main, and the `local_until` fold that #224's review left for after the merge. The review of #263 (#273) changed what the sweep's base frame keeps, the order of the stretch list, and when a sweep writes `batches.seg` again. docs/errata/2026-11-23-late-local-records.md is an oracle, so this file records where #263 makes that erratum's Known limits and What the code does out of date. Status: decided 2026-11-23 by the orchestrator for Karim, as #260's follow-ups to D-late-local.

## Input

- docs/errata/2026-11-23-late-local-records.md, Known limits, second item: "`local_until`, the newest stretch end, stays in every row and seal frame however old it is (#174), and in the base frame a sweep writes, as the moment the list's row recorded. Folding it touches the downgrade guard (`restoreStretches`), the uploader's straddle rule and the rebuild of state.json's modes, so it goes to a follow-up after #224, which folds the base frame's copy too."
- The same erratum's What the code does, on the merge: "The writer keeps at most 64 stretches and merges the oldest two past that, which only makes more of the past count as local-only."
- The same, on the base frame's moment: "A sweep that moves the list into its base frame keeps there the moment the list's row recorded, and Open holds the rows kept after the frame to it the same way."
- The same, on where a stretch the agent saw end begins: "One that ends nothing the writer has, being later than every end it knows or taken in a boot no end of the writer's is of, is recorded as a stretch from where the writer last knew the file said online."
- #260's last item: fold a nonzero `local_until` older than the sweep cut, and check the downgrade guard, the uploader's straddle rule and the `state.json` modes rebuild.

## Expected

A retention sweep keeps no time `config.toml` changed mode from before the ledger's 180 days in `batches.seg`. That covers the stretches (DESIGN 3.6's fold) and the moment each row and the base frame record. A sweep changes nothing that Open found before it, so a restart after it holds records to the same stretches. Merging stretches past the cap only makes more of the past count as local-only, as the old erratum says.

## Computed

On main at 858ca285:

- Every row a sweep keeps carries its `local_until` however old, and so does the base frame.
- The merge kept the second stretch's end and its passed state. After a step back of the wall clock between the two oldest stretches, that lost the first's wall range, so a late record made in it, judged by the wall clock alone, sealed online (#260, r4666785401).

At #263's first head, 65a7a0f9, which folded the moments (#273):

- A sweep wrote into the base frame the list `batches.seg` held, which could be from before what Open found. After a downgrade, Open's guard ends the stretch going on at the older version's rise, in memory. The fold then made the kept rows' moments and the base frame's alike, so a restart before the next seal found no rise. The stretch went on, and the first seal after the restart was local-only after the online batches. A stretch end the agent's `Seen` gave at Open was lost the same way, since the folded moment made the end older than every moment.
- A sweep that dropped no row and folded no stretch left the rows' moments as they were, however old.
- The merge took the first stretch's start and the second's end by the monotonic clocks. A stretch the agent saw end was appended after the newest one, though after a step back of the wall clock its end could be earlier than that one's by the OS's clock. A merge of the two then ended where the earlier ended, so a hook call made in the later one, judged by its mono, sealed online. Main's merge lost the same range.

## Decision

- The bullets below replace the old erratum's second known limit for the rows and the base frame. They also replace its merge sentence, its base-frame-moment sentence and its learned-start sentence.
- Seal frames stay under its first known limit, as Still open below says.
- The `seenLocked` rule below is recorded: a `Seen` end raises the moment only when it is later by a clock the two hold.
- DESIGN 3.6 and 4.2 are unchanged.

## What the code does with #263

- **`local_until` folds at a sweep.** A nonzero moment older than the sweep's day becomes the start of that day by the wall clock alone, with no OS reading. That holds for each row the sweep keeps, for the base frame's moment, and for the writer's own, which its next seal frame records. A sweep writes `batches.seg` again when a moment there would fold, even when it drops no row and folds no stretch, for the live chain and the chains in `old/` alike (#273). Checked against its three readers:
  - The downgrade guard holds the kept rows to the base frame's moment. The base frame keeps the writer's own folded list and moment, which already ended every stretch Open's guard found, so kept rows whose folded moments no longer show the rise end nothing.
  - The uploader's straddle rule reads the moment of the batch after a local-only gap. That batch's moment is that stretch's end, which comes after the gap's batches, so it folds only when the gap's rows are kept past the cut, as unsent rows a floor keeps are. There it moves the straddle's end later by the wall clock, which withholds more minutes, never fewer.
  - The rebuild of `state.json`'s modes takes the newest row's moment as the stretch's end. A folded one gives the cut, and the agent's Seen of it at the next Open ends nothing the writer doesn't have.
- **The base frame keeps the writer's own stretches and moment** whenever a sweep writes `batches.seg`, as the writer's next seal frame would record them, after the fold (#273). These hold what Open found that no seal frame has yet: the stretch that an older version's rows end after a downgrade, a stretch end the agent's `Seen` gave, and the stretches Open rebuilt from an older version's rows. Open keeps the base frame's moment when it is later than the newest row's, by each clock, so a `Seen` end it holds isn't taken for one the writer never had. When that moment is later than every batch's, no batch was sealed after the stretch end it holds. The first batch after it that can be marked takes the mark, as in the run that took the end. With no row kept, Open takes its moment from the base frame (#260, r4666785408).
- **The merge past 64 stretches** takes the oldest adjacent pair alike passed or not, from the earlier start to the later end by the wall clock. When no pair agrees, it takes the two oldest, passed when either is, which can only drop a record of them (#260, r4666785401). The list stays in time order, as the next bullet says, so the one they become starts at the first's start and ends at the second's end by every clock too. Only more of the past counts as local-only.
- **A stretch the agent saw end** begins at the end of the newest stretch that ended before it, when the writer knew `config.toml` said online only after that end, by a clock the two share (#260, r4666785394). It goes in the list right after that stretch, or first when none ended before it, and before the stretch going on, so the list stays in time order (#273).
- **A `Seen` end moves the moment only when it is later by a clock the two hold** (`seenLocked`). An end earlier by each of them, as one the agent kept from before a sweep folded the moment to the wall clock alone, adds none of its readings. It is no stretch the writer didn't know of.

## Still open

- A seal frame still keeps the `local_until` it was sealed with until its day's segment goes, as it keeps its stretch list: the old erratum's first known limit covers both.
