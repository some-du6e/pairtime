# SM 6.5: an hour of dark-flow re-rolling

Alex, 15, on a Windows 10 laptop without the key counter, fights one layout bug in `pantry-app` with Claude Code in Windows Terminal from 20:00 to 21:00. From 20:05 to 20:38 he asks for the same navbar fix 17 times and the agent rewrites the same eight lines each time, until his own edit at 20:41 fixes it. Every SM row is used: 1,490 s credited, `excluded_loops_s` 2,010 and `X.agent_only` 100.

| | |
|---|---|
| SM rows | 20:00 to 20:07, the range 20:08 to 20:37, 20:38 to 20:42, the range 20:43 to 20:59, the totals table and the paragraphs after it |
| Date and zone | Wednesday 2026-11-25, Asia/Manila, UTC+08:00. 20:00 local is 12:00Z |
| OS and evidence | Windows 10 without the key counter: `idle_keys_s` is null, every credited second is level B. Witnessed |
| Window | 20:00:00 to 21:00:00. Records from 19:57:35 start the session. Records to 21:01:00 hold the 21:00:30 prompt whose lead covers 20:58 to 21:00 |
| Project | `pantry-app` |
| Errata | [lookup slots](../../../docs/errata/2026-11-19-sm-6.5-lookup-slots.md): 20:02's 20 s lookup can't be 20 s under slot-level focus. [Loop codes](../../../docs/errata/2026-11-19-sm-6.5-loop-codes.md): SM doesn't say which code each loop second carries, why 20:03:20-:40 isn't one, or how "fix it!!" normalizes. [Prompt length](../../../docs/errata/2026-11-19-sm-6.5-prompt-length.md): the first prompt's quote has 87 characters, SM counts 95 |
| Other files | `hunks.json`, the edit hunks `agent.tool` can't carry yet (below). `neardup.py`, SM 4.2's normalization and re-roll count, which gen.py uses to check the prompts and `4.2-alternation` shares |

## Events

- 19:57:35: Alex clicks Windows Terminal and starts `claude`. Its `SessionStart` fires at 19:57:44 (`permission_mode` `acceptEdits`, since SM has no permission prompts) and the terminal now hosts the session.
- 22 prompts through `UserPromptSubmit`. T1 at 20:01:00 and T2 at 20:03:00 are credited. T3 to T19, at 20:05:00 and every two minutes to 20:37:00, are the loop. T20 at 20:43:00 and T21 at 20:49:00 are the two turns after his fix, and T22 at 21:00:30 is the next request.
- T1 to T19 each make a read, one edit of `Navbar.css` and a 110-word answer, through hooks and the transcript. T1's edit replaces lines 10-21, the others lines 12-19, eight lines out and eight in. T1 stops at 20:02:00 and T19 at 20:38:00, as SM says. T2 stops at 20:04:00, and T3 to T18 run 75 s.
- `os.focus` for every browser check: 20:02:30 to 20:02:50 and 20:41:10 to 20:41:40 at SM's times, 20:04:00.3 to 20:04:30.3, 20:38:00.3 to 20:38:30.3, 20:46:00.3 to 20:47:00.3, 20:53:00.3 to 20:54:00.3, and 80 s to 95 s after each loop prompt. VS Code from 20:39:15.3 and from 20:54:00.3.
- WakaTime heartbeats from VS Code: opening `Navbar.css` at 20:39:20 (`NAV`), his `Hero.css` fix at 20:41:00, a small hand edit at 20:55:00 and its save at 20:57:00. His commit at 20:58:00, `actor` `human`.

## The missing field

`agent.tool` has no field for edit hunks (issue #2), and SM 4.2's rewrite rule needs them: `overlap(k)` matches lines by file and hunk range. Until `pkg/event` has the field, `hunks.json` holds what it would carry for each of the 21 edits, keyed by the edit's `event_id`: the file key, as in `files`, and the unified-diff ranges `old_start`, `old_lines`, `new_start`, `new_lines`, as in Claude Code's `structuredPatch`. T1 changes lines 10-21, T2 to T19 lines 12-19, and T20 and T21 add 12 lines to `Hero.css` and 18 to `Footer.css`. gen.py writes the file and computes the rewrite repeats from it. When the field exists, the hunks move into `events.jsonl` and `hunks.json` goes.

`internal/timelines` doesn't read `hunks.json`, so CI checks only that gen.py still runs. The other four files are checked byte for byte as usual.

## Timing choices SM leaves open

- Input. Pointer moves every 10 s while he watches or reads, scrolls in the browser, key presses while he types, so every slot of the hour has input and is `yes`. Windows can't tell key presses from other input, so the whole of each lead needs only presence.
- The loop prompts. SM quotes T2, T4 and three of the prompts "mostly in new words". `prompts.json` has all 22, the invented ones marked `invented`. T7, T12 and T16 repeat T2's text, T7 and T12 exactly and T16 with a comma for the full stop. The other twelve are paraphrases. gen.py checks with `neardup.py` that every two different texts have a Jaccard similarity under 0.5, so a 64-permutation MinHash can't call them near-duplicates, and that the re-roll attempts are 2 for T3, 3 for T4, and 4, 5 and 6 for T7, T12 and T16. "pls fix the overlap" has 19 characters, so T8 is `low_effort`.
- Codes. T3 is a rewrite repeat (attempt 2) and gets `X.rewrite_loop`. T4 is attempt 3 and a rewrite repeat, and gets `X.reroll`, as SM's 20:07 row says. The exact repeats get `X.reroll` and the paraphrases `X.rewrite_loop`, T19 included, which is what SM's 20:38 row needs.
- Turn lengths. T2 stops at 20:04:00, so its last 20 s are `SUP` and the browser check from 20:04:00 is a lookup with no agent running. T3 to T18 run 75 s, into the minute after their prompt. With 60 s runs the even minutes from 20:06 to 20:36 would have no running agent and no credit, so no bucket (SM 5.1), and their discount seconds would be in no upload, the point FX1's 6.6 erratum raises. The 75 s runs keep every loop minute in a bucket and SM's 2,010 s whole.
- The 20:43 to 20:59 range. SM gives 940 s credited, `X.agent_only` 80 and the codes. This fixture's split, by its own timeline:

  | Minutes | What happens | Credited | Uncredited |
  |---|---|---|---|
  | 20:43-20:44 | T20 runs. Its 20 s base, nothing new until a 12-line edit at 20:44:00 | `SUP` 80 | `X.agent_only` 40 |
  | 20:45 | Reads T20's answer | `READ` 60 | |
  | 20:46 | The desktop view | `LOOKUP` 60 | |
  | 20:47-20:48 | T21's lead | `P` 120 | |
  | 20:49-20:50 | T21 runs, the same shape, an 18-line edit at 20:50:00 | `SUP` 80 | `X.agent_only` 40 |
  | 20:51-20:52 | Reads T21's answer | `READ` 120 | |
  | 20:53 | The desktop view again | `LOOKUP` 60 | |
  | 20:54-20:56 | A small hand edit in `Hero.css`, heartbeats at 20:55:00 and 20:57:00 | `E` 180 | |
  | 20:57 | The commit's lead | `G` 60 | |
  | 20:58-20:59 | T22's lead, the next request | `P` 120 | |

  gen.py checks the buckets behind the `SUP` and `READ` seconds and that no agent runs during either lookup. The row marks `credit` as derived.
- WakaTime sends one heartbeat for opening `Hero.css`, typing the rule and saving it at 20:41:00, and no other between 20:39:20 and 20:55:00. A second heartbeat there would be another `NAV` or `E` anchor and would move 20:39, 20:40 or 20:41's reasons.

## What the rows rely on

- 20:01 is T2's `P`: its lead, 120 + 35 / 1.5 = 143.3 s, starts at 20:00:36.7 and beats T1's bucket.
- T3's discount means its lead opens no window, so 20:03 and 20:04 are credited by T2's bucket and the 20:04:00 lookup, and 20:03:20-:40 is `X.agent_only` because nothing is on screen. SM counts the loop from 20:05:00, T3's submit.
- The `NAV` lead of the 20:39:20 heartbeat would start at 20:37:20 but can't reach back past T19's stop at 20:38:00, and 20:38:00-:30 is in the browser, so `NAV` starts at 20:38:30.
- 20:41's lookup holds two whole slots, 20:41:15 and 20:41:30, because focus counts per slot (SM 2.5). 20:02's covers 20 s but only the slot 20:02:30, which is the lookup-slots erratum.
- Hackatime's replay of this fixture's agent events gives 2,700 s of `ai coding` inside the hour and its editor heartbeats 340 s of `coding`. SM says about 50 and 5 minutes. `expected.json` keeps SM's figures under `approx`.
