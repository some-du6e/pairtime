# Focus unknown (e): the one `os.focus` an hour before the work

Sam, 14, starts Claude Code for `quiz-app` on Ubuntu's GNOME on Wayland, without pairtime's extension, then watches videos for an hour before he types a prompt. The agent records its one `os.focus`, `app_class` `unknown`, at 12:59:52, and nothing about focus after it. So every checkpoint between that record and the work, at 13:00, 13:15, 13:30, 13:45 and 14:00, is followed by no `os.focus` at all. A re-score that starts from one of them (DESIGN 6.5) reproduces these rows only if the checkpoint carries the `os.focus` in force. The hour credits nothing, and the work credits 400 s at level C with `focus_unknown`.

| | |
|---|---|
| Source | WP-FX8's (e) (docs/PLAN.md). Invented |
| Rules | SM 2.5's focus-unknown paragraph, with SM 2.2 to 2.4 and 5.2, the parameters of SM 8, and DESIGN 6.5's checkpoints |
| Date and zone | Sunday 2026-11-29, America/New_York, UTC−05:00. 13:00 local is 18:00Z |
| OS and evidence | Linux, GNOME on Wayland without pairtime's extension. `idle_keys_s` is null in every sample. Every credited second is level C with `focus_unknown`. Witnessed |
| Window | 13:00:00 to 14:08:00. The session starts at 12:59:52, and samples run to 14:08:15 |
| Project | `quiz-app`, one Claude Code session in a terminal |

## The events

- **12:59:52.** Claude Code's SessionStart hook makes a project context live, so the agent records the `os.focus` it can, `app_class` `unknown` (SM 2.5, DESIGN 3.3), and samples. A harness session process runs from then on, so the context stays live and the samples come every 15 s.
- **12:59:55 to 13:59:50.** Videos in the browser, which pairtime can't see: a click or a scroll every 10 to 50 s, so every slot is `yes` or `bridged`. At 13:59:49.9 he clicks the terminal, which pairtime can't see either.
- **14:00 to 14:03.** He reads `quiz.js` in the terminal, scrolling, then types a 90-character prompt and sends it at 14:03:00.
- **14:03 to 14:05.** The turn: 30 words at 14:03:30, a 20-line edit at 14:04:00 with no visible output, and a 120-word summary with the stop at 14:05:00. He watches and scrolls from 14:03:04 while the turn runs.
- **14:05 to 14:08.** He reads the summary and the diff, scrolling every 5 to 9 s.

## Credit, minute by minute

| Minute | Credited | Reasons | Arithmetic |
|---|---|---|---|
| 13:00-13:59 | 0 | none | Present, and focus unknown passes every focus test, but there is no anchor, no reading and no running turn, so nothing is credited: no bucket in any of the 60 minutes (SM 5.1) |
| 14:00 | 60 | P 60 | The prompt's lead, 120 + 90 / 1.5 = 180 s, from 14:00:00 |
| 14:01 | 60 | P 60 | The think part, to 14:02:00 |
| 14:02 | 60 | P 60 | The typing part, which needs only presence where `idle_keys_s` is null (SM 2.3). SM 2.2 rule 6's focus test passes on unknown focus, so the prompt is human_typed, its anchor level C with focus_unknown |
| 14:03 | 30 | SUP 30 | The prompt resets the bucket to 20 s, drained to 14:03:20, and 30 words add 10 s at 14:03:30, drained to 14:03:40: 20 + 10. Nothing is left to read from 14:03:20 to 14:03:30 and from 14:03:40 to the edit at 14:04:00 while his turn runs: X.agent_only 10 + 20 = 30 |
| 14:04 | 60 | SUP 60 | The edit adds 20 × 60 / 8 = 150 s: SUP to the stop at 14:05:00, which leaves 90 |
| 14:05 | 60 | READ 60 | The 120-word summary adds 40 s, so 130 are left: READ from the stop |
| 14:06 | 60 | READ 60 | Reading |
| 14:07 | 10 | READ 10 | The bucket runs dry at 14:07:10, 130 s after the stop. No turn runs and nothing is left to read: X.no_anchor 50 |

## Totals

| Total | Seconds | Arithmetic |
|---|---|---|
| `credited_s` | 400 | 60 + 60 + 60 + 30 + 60 + 60 + 60 + 10 |
| `P` | 180 | 3 × 60 |
| `SUP` | 90 | 30 + 60 |
| `READ` | 130 | 60 + 60 + 10 |
| `X.agent_only` | 30 | 10 + 20 |
| `X.no_anchor` | 50 | 50 |
| `agent_running_s` | 120 | 60 + 60 |
| `agent_only_s` | 30 | 120 − (30 + 60) |
| `level_c_s` | 400 | 400 |
| `focus_unknown_s` | 400 | 400 |
| `level_b_s` | 0 | 0 |
| `ai_credited_s` | 400 | 180 + 90 + 130 |
| `checkpoints_after_focus` | 5 | 5 |
| `focus_age_at_work_s` | 3,608 | 60 × 60 + 8 |

`checkpoints_after_focus` counts the checkpoints in the window, all after the only `os.focus`. `focus_age_at_work_s` is how old that record is when the work's first credited second comes, 12:59:52 to 14:00:00.

## Re-scoring from a checkpoint

DESIGN 6.5 seals a checkpoint at every UTC minute divisible by 15 for each project with records in the last 24 hours, so from 13:00 local, 18:00Z. Every checkpoint in the window comes after the only `os.focus`, so a re-score that starts at one reads no `os.focus` at all:

- A submission window starting at 14:00 is re-scored from the latest checkpoint at or before 13:45, `W.start` minus 900 s (DESIGN 6.5's Range). From 13:45 the records hold samples, the prompt and its turn, and no focus record.
- DESIGN 6.5 has each checkpoint carry "the shareable fields of the `os.focus` in force (`app_class`, `hosted_sessions`, `focus_project`), so a range that starts long after the last focus change, as in SM 6.4's night or on a desktop that hides focus, knows what had focus". Here that is `app_class` `unknown` with no other field. Resumed from any of the five checkpoints with it, the scorer gives the rows above. The session's `session.start` also comes before every checkpoint, and SM 2.2 rule 4 reads it. But "a declaration only ever withholds credit" (SM 2.2), so a re-score that doesn't see it still takes the 14:03 prompt to rule 6, and the rows come out the same.
- Without it, a scorer resumed there would start with no focus, as before a client's first `os.focus`, where nothing is credited (WP-SC8 keeps that: "time before the first `os.focus` still credits nothing"). It would credit none of the 400 s, and CC6 would fail.
- Because the range holds slots of unknown focus, DESIGN 6.5's reveal set (f) opens every record of every project in the batches that overlap the range. Here there is only `quiz-app`.

## Readings the case relies on

- **An edit fills the bucket when it lands.** An `agent.tool` record fills the bucket at its `t`, which is its `t_end`, not its `t_start`, as SM 6.1's "lands at 14:05:30" and 6.2's "until the agent's 40-line edit lands at 07:41:50" read it. The edit fills at 14:04:00, its `t_end`, which ends the 20 s with nothing to read in 14:03. Filled at `t_start`, it would fill at 14:03:51.2, and 14:03 would credit 8 s more, from 14:03:52.
- **When the record is written.** The focus component writes the record of unknown focus once a project context is live (internal/agent/focus), so it is dated at the SessionStart hook, in the same millisecond. No act falls in that millisecond.
- **An hour of presence and nothing else.** Unknown focus passes the focus tests, but credit still needs an anchor: a window (SM 2.3), reading within 600 s of one (SM 2.4) or a lookup, which a slot of unknown focus never is (SM 2.5). None of these comes before 14:00, and the reading horizon of the 14:03 prompt reaches back only to 13:53, when the bucket is empty.
- **Two minutes, two codes.** In 14:03 the uncredited seconds fall while the turn runs, so SM 5.2's turn step, which reads every project's running turns where focus is unknown, gives `X.agent_only`. In 14:07 nothing runs, so `X.no_anchor`.
