# Erratum: which commit records upload, and when

Date 2026-11-24. Raised by WP-AG5's PR that uploads the commit records of `commits/`. Status: decided 2026-11-24 by the orchestrator for Karim, as D-ag5-D1, D2, D3 and D6, with D2 amended the same day.

## Input

- docs/errata/2026-11-23-late-local-records.md, Still open: "Commit records don't upload yet. A git change the writer drops still has its commit records in `commits/`, and they need the same rule once the uploader sends them, as #174 says."
- DESIGN 4.2: in local-only mode "nothing is uploaded".
- ingest-v1 2.7: the server keeps a commit record per device and sha, last write wins, and `updated` keeps the first `server_first_seen_at`.
- `commits/`: the git host's sink appends a change's records before its `git.change` is written, and keeps them whatever the ledger's Write returns. A crash between the records and the watcher's cursor scans the change again, and another clone's pull writes the commit again, so a sha can have more than one line.

## Expected

No commit record that local-only mode may have seen leaves the device, apart from the limits under Still open, a sha goes from the device once, as its first sighting of it, and a record goes only after the root of the batch its minutes rest on, so a fork or a refusal at that root keeps it on the device.

## Computed

On main at afd37242 the uploader sends no commit records: `Body` has no `commits` member.

## What the code does

- **The moment.** A line is judged at `m`, its `observed_at`, from batches.seg's rows, the stretch list they hold and the uploader's place, so `pairtime privacy show` reaches the same verdict.
- **The wait first.** While the rows don't hold every stretch the writer has, every line waits: none goes, none is withheld, and no commit mark moves. That is so while the newest stretch list the rows hold has a stretch still open, and while a row's `local_until` rose past that list, which only an older version's rows do, by `restoreStretches`' own test. The first seal frame that records the list ends the wait.
- **B\*.** B\* is the first batch sealed at or after `m`, as `rowIndex.sealedAfter` finds it. While the rows hold no B\*, the line waits, and so do the lines after it in its day's file.
- **Withheld for good.** A line never goes, nor its sha in a later line from this device, when it doesn't decode, its sha isn't 40 or 64 lowercase hex digits, or `m` is outside the server's range; when `m` is before the chain began, or, after a retention sweep, before the oldest kept batch was sealed; when B\* was sealed in local-only mode (`saved.localOnly`: a recorded gap, `LocalFrom` on, or between the server's head and `From`, rows that recorded no mode included); when `m` falls after a run's last seal and at or before the end of its stretch, `min(LocalUntil, SealedAt)` of the batch after the run (`saved.endsRun`, `straddles` for one moment); and when a stretch of the newest list holds `m` by the wall clock alone, passed or not, both edges included.
- **After B\*'s root.** A line that passes goes only once B\*'s root has been answered, stored with its receipt or passed as gone after `before_chain`: B\* < `Next`. It never goes in the request that carries B\*'s own root, and none goes while the link's first batch isn't stored (`Linking`). Past a `localStop`, none goes until those batches are answered, and a re-link that puts B\* in its gap withholds the line for good.
- **One line per sha.** A sha goes at most once per device, as the first line `commits/` holds for it. Before the prune deletes a day's file, 180 days after its date, it appends the sha of each of its lines to `commits/pruned.txt` and flushes it, whatever became of the line. Each plan takes those shas as decided, so no later line of them goes from this device. The list stays until `pairtime privacy delete` or uninstall removes `commits/`. Each plan reads every kept day's file whole: the shas of the lines before each day's mark are decided, and the new lines, oldest day first and then in file order, each add their sha, those after a line that waits included. A new line whose sha is already among them is passed over when the sha's first line went or was withheld. A new line whose sha's first line still waits waits too, holding back its day, so no mark passes a line of a sha whose first line is undecided, and each plan judges the first line first. A day's file that can't be read, for a reason other than being gone, stops every line in that plan, and doctor's `commits` problem names the day. state.json keeps nothing per sha.
- **The wire.** Each line goes through the uploader's own 2.3.3 type, so a member it lacks, as `path_hmacs` and `credited_seconds_since_parent` are until the work that fills them, stays on the device, and a line without counts goes without them. Commit records are a list of their own: 500 a request, halved on `too_many_commits`, `body_too_large` or `decompressed_too_large`, with no tie to the roots. Every commit result is final.
- **The backlog.** The first upload after the upgrade sends every first line that passes, oldest day first.

## Still open

- A line still waiting when a stretch whose edges share no monotonic clock begins, as one left open across a reboot, is withheld for good, back to the end of the stretch before it, though its `git.change` sealed online and its minutes go. The same holds for the stretches Open rebuilt from an older version's rows, which run back to back from the beginning of time to the last `local_until`, so the backlog up to that moment is withheld, and waits for this version's first seal to record them. This errs toward privacy, and costs a day or two of commits on alpha test machines. If alpha data shows the loss, the route is to match a line to its own `git.change` by project key and `t`, which needs an erratum of its own.
- A sha seen again after its first line's day was pruned stays decided, by the list of pruned shas, so no line of it goes from this device. A clock that jumps forward more than 180 days prunes every day's file at once, and the lines in them that hadn't gone yet never go. That costs their credit, never privacy, as D-inbox-age-cap accepts for `inbox/`.
- D-ag5-D1's limits stay, shared with the ledger. In the decision's words:
  - A change found by a later scan is judged by the scan's time, PLAN.md:795: a poll-only worktree, or a catch-up scan after a start whose stretch Seen ended at startup.
  - An online commit's parents and rewrites can name a local-only sha, and a rebase or amend made online carries the original author_time; those go as references and git times, never as the local-only commit's record.
  - A batch sealed online can have read config.toml milliseconds before m.
  - Steps of the wall clock beyond what widen and orderWall correct remain, #174's known limit.
  - So does a stretch a restart erased before any frame recorded it, #304's Still open.
  - And the microseconds between the line's append and its git.change's seal, after a step back with no seal since the switch.

## Decision

- D-ag5-D1: a line is judged at its `observed_at` by the rules above, the wait for the rows' stretches first, from the rows, the stretch list and the place alone, apart from the limits under Still open, which it shares with the ledger.
- D-ag5-D2: each sha goes at most once per device, as the first line `commits/` holds for it, with the set built from a whole read of every kept day's file in each plan, and no line going while a kept day's file doesn't read. A sha the prune listed before it deleted its day's file is in the set too, as #365 ruled, so no later line of it goes from this device. A new line whose sha is already in the set is passed over whether the first went or was withheld; a new line whose sha's first line still waits waits too. That amends D2's first wording, "whether the line before it went, was withheld, or still waits", under which a later line passed over while the first waited put its sha before its day's mark, and the next plan passed the first line over too, so the sha never went. The amendment only ever delays a line, and D1 still decides whether the first line goes.
- D-ag5-D3: a passing line goes only once B\* < `Next`, never beside B\*'s own root, in a list of its own.
- D-ag5-D6: the first upload sends the backlog of first lines that pass, oldest day first, at most 500 a request.
- This answers the late-local erratum's Still open item on commit records. docs/spec/ingest-v1.md section 5 gets the rule as an item of its own. DESIGN 4.2's "nothing is uploaded" already covers commit records, and stays as it is.
