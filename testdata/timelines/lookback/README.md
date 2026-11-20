# Lookback fixtures

On its first run pairtime reads up to 30 days of harness transcripts, from before it was installed (SM 2.8, DESIGN 1.2). No sampler was running then, so every slot's presence is `unknown` and SM 2.8's rules score it: prompts are `human_typed` unless SM 2.2 rules 1 to 5 label them, leads are typing time only, permission waits keep at most 60 s, a turn's reading is at most 120 s and counts only between its stop and a human prompt that follows within 600 s, and a second two sessions want goes to the one with the most recent human prompt. Everything credited this way is level C and `backfilled` (SM 5.1, DESIGN 6.4). It shows in the welcome recap and the "before pairtime" band and never in public totals, build days or submissions unless a program opts in to level C, and then at most at the handbook third inside forge-verified commit intervals (DESIGN 6.2 D5, D11).

These fixtures are WP-FX5's oracle for WP-SC7, the scorer's lookback mode (docs/PLAN.md). The credit in them is worked out by hand from SM 2.8, minute by minute, in each case's README.

| Case | What it is | Credited | Live fixture | Errata |
|---|---|---|---|---|
| [`6.1-transcript-only`](6.1-transcript-only/) | SM 6.1's three hours of flow, from the transcript alone | 5,055 s | 10,095 s | lookback, points 2 and 5 |
| [`6.4-transcript-only`](6.4-transcript-only/) | SM 6.4's overnight `/goal` run, from the transcript alone | 91 s | 810 s | lookback, point 2; WP-FX1's prompt lengths and 6.6 codes |
| [`two-sessions`](two-sessions/) | Two sessions in two projects running in parallel | 915 s | none | lookback, points 3 and 5 |

The erratum is [docs/errata/2026-11-19-sm-2.8-lookback.md](../../../docs/errata/2026-11-19-sm-2.8-lookback.md). Its point 4, which `session.start` fields SM 2.2 rule 4 can read from a transcript, applies to all three cases.

## Against the timeline format

Each case is a directory with the files of [`../README.md`](../README.md)'s format, which `internal/timelines` loads as `lookback/<case>`. `List` doesn't descend into this directory, so `internal/timelines/lookback_test.go` checks the cases the way `timelines_test.go` checks the SM fixtures, plus the rules below. The differences:

- **Events** are what a transcript reader writes: harness events only, `source` and `via` `log`, with no `os.*`, `editor.activity`, `shell.command` or `git.change`. `seq` starts at 1, because lookback runs at the agent's first start. The two SM cases take their records from the live fixtures' `events.jsonl`, keeping each record's time, keys and payload, so they change when those files do.
- **expected.json** has `evidence` `C`, `witness` `backfilled` and `input_keys` false, since no sample exists. `stated` is empty, because SM states no lookback numbers. Rows credit only `P`, `ANS`, `GO`, `Q`, `PA`, `INT` and `READ`: there is nothing to give `E`, `NAV`, `CMD`, `TEST`, `G` or `LOOKUP`, and lookback never credits reading while an agent runs, so never `SUP`.
- **Uncredited codes.** A second in an agent turn that nothing credits is `X.agent_only`. Three other kinds of second wait on an erratum, and a row with any of them has `uncredited` null, lists their codes in `codes` and names the erratum:
  - a second between a stop and the prompt that follows within 600 s, which only the 120 s cap keeps from being read, gets `X.bucket_cap` (the lookback erratum's point 2);
  - a second outside every window and reading span, with no agent running, gets `X.no_anchor`, the same point's proposal;
  - a second in a stop-hook turn gets `X.automated` or `X.agent_only` (WP-FX1's 6.6 erratum, point 1).

  Scorer tests check such a row's credit and leave its uncredited seconds alone. `check_rows` in lookback.py checks each row's codes against the seconds the replay finds in it.
- **anchors.json** holds prompts and permission answers only, all level C. A prompt's `lead_s` is its typing time, `typed_len_est / 1.5`, and a permission answer's is its wait, at most 60 s, with a 10 s trail.
- **README.md** works every row out by hand in two kinds of table, which the Go test and gen.py both parse:
  - `| Minute | Credited | Reasons | Arithmetic |`. Minute is a row's `local`, such as `14:00` or `14:09-14:11`. Reasons lists `REASON seconds` items, each with the project's name first when the case has more than one project, or says `none`. Each such row must equal the row of `expected.json` with that `local`.
  - `| Total | Seconds | Arithmetic |`. Total names an `expected.json` total, a credit reason, an uncredited code or a project. The arithmetic, whole numbers with `+`, `−`, `×` and parentheses, must give the seconds, and the seconds must equal `expected.json`'s.

## Generating

```sh
python3 -I testdata/timelines/lookback/6.1-transcript-only/gen.py           # rewrite the checked-in files
python3 -I testdata/timelines/lookback/6.1-transcript-only/gen.py --out DIR
```

Each gen.py builds its events with [`../timeline.py`](../timeline.py), writes the rows worked out by hand, and before writing checks them against `Replay` in [`lookback.py`](lookback.py), a second-by-second reading of SM 2.8 written only to catch slips in the hand arithmetic. The rows are the oracle, not `Replay`. gen.py also checks its README's tables against the rows it writes, and the two-session case checks which seconds were contested and that no typing lead was among them.
