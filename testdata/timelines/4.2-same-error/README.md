# SM 4.2: the same error, again and again

SM 4.2's fix-it rule discounts a human turn whose prompt is an error paste or a go-ahead once the same error has appeared `SAME_ERROR_LIMIT` = 3 times since the person last changed something or the error was last cleared. The decision on the [same-error erratum](../../../docs/errata/2026-11-19-sm-6.1-same-error.md) names the human change events and the clearing, and this fixture is its test case. One `AssertionError` comes back while the person pastes it, debugs it and waves the agent on. The error paste and the go-ahead sent at the limit are `X.same_error`. The paste that brings the count to the limit, the hypothesis sent at it, the paste after a human commit and the go-ahead after a passing run are credited. SM gives no minute rows for it, so every row follows from SM's rules applied to the timeline below, and each row marks its fields `derived`.

| | |
|---|---|
| SM text | 4.2, the fix-it rule and "What a discount does"; 2.2 for the classes, 2.3 for the leads, 2.4 for the bucket after a discount |
| Date and zone | Thursday 2026-11-26, Europe/Berlin, UTC+01:00. 16:00 local is 15:00Z |
| OS and evidence | macOS with Terminal, `idle_keys_s` set, so every credited second is level A. Witnessed |
| Window | 16:00:00 to 16:26:00. Records from 15:58:20 start the session |
| Project | An invented `habits` command-line app with a pytest suite |
| Files | gen.py uses `../6.5/neardup.py` for SM 4.2's normalization and attempt count, and `Sweep` from `../sidedoor/sidedoor.py` for SM 2.7 second by second |

## Events

- 15:58:20: a click on Terminal, `claude` typed, Claude Code's `SessionStart` at 15:58:27 with `permission_mode` `acceptEdits`, and the terminal now hosts the session. He allowed the test command, `pytest tests/test_streaks.py`, for the project before the window, and `acceptEdits` approves the edits, so no turn asks a permission.
- Seven prompts, each typed just before it is sent: P1 at 16:02:00, P2 at 16:05:00, P3 at 16:08:00, P4 at 16:11:00, P5 at 16:14:00, P6 at 16:19:00 and P7 at 16:24:00. P2, P4 and P6 end with pytest's report of the failing test, pasted, and each of their `prompt.submit`s carries in `error_sig` the key the failing runs carry.
- Every turn: a 30-word plan 5 s in, edits of files no other turn touches, runs of the test command with 4 lines of output each, a 45-word answer and the stop. The runs fail with the one `AssertionError` at 16:02:22 and 16:02:42 (P1's turn), 16:05:40 (P2's), 16:08:40 (P3's), 16:11:40 and 16:13:10 (P4's) and 16:14:40 (P5's). P6's turn fails at 16:19:25 and 16:19:45, passes at 16:20:05, then tidies a helper and fails again at 16:20:30. P7's turn passes at 16:24:40. The turns stop at 16:03:00, 16:06:00, 16:09:00, 16:13:40, 16:15:36, 16:21:00 and 16:25:00.
- 16:15:40: he moves to a second Terminal tab in the project, which hosts no session, types `git commit -am "count check-ins by local day"`, and commits at 16:16:36, `actor` `human`, with key presses in its slot. At 16:16:40 he is back in Claude's tab.

## Timing choices

- The texts are invented, since SM 4.2 quotes none for this rule. P1 has 90 typed characters, P2 33, P3 84, P4 36 and P6 36, so their leads are 180, 142, 176, 144 and 144 s, all whole seconds. P5 is "keep going" and P7 "go on", go-aheads on SM 2.2's list, with the 20 s `LOW_LEAD_S` lead. The commit's lead is `COMMIT_LEAD_S`, 60 s.
- A paste has its typed words, then the pasted report. Each paste has fewer than 40 typed characters, no agent question before it and words that aren't a go-ahead, so it is `error_paste` (SM 2.2). P4's 36 are at least `SUBSTANTIVE_CHARS`, so a scorer that missed its signature would class it `substantive` and credit it. `pasted_chars` above 0 makes each paste `human_pasted` (rule 7).
- The commit is the only human change event: no hand edit, typed command, permission answer or interrupt. The agent's edits and its commits aren't human change events, and neither is the error's first appearance (SM 4.2).
- The other detectors stay out. Under SM 4.2's normalization no prompt's typed words are a near-duplicate of an earlier prompt's since the last change event; pasted text is removed before comparing. Every edit is of a file no other turn touches, so the rewrite rule's overlap is 0. Every answer has 45 words, under `NOREAD_MIN_WORDS`, so no turn is unread.
- A trackpad move every 10 s keeps every slot present, and the keys of each prompt cover every slot of its lead's typing part (SM 2.3).

## The count before each prompt

The count is the error's appearances since the later of the last human change event in the project and the last time the error was cleared. A failing run and a pasted error each add one, and a prompt is judged by the count before it, so a paste counts toward the turns after it (SM 4.2).

| Prompt | Class | Count before it | Why | Credit |
|---|---|---|---|---|
| P1 16:02:00 | `substantive` | 0 | no failure yet | credited |
| P2 16:05:00 | `error_paste` | 2 | P1's turn failed twice | credited, under the limit; its paste brings the count to 3 |
| P3 16:08:00 | `substantive` | 4 | P2's paste, then P2's turn's run | credited: the fix-it rule never discounts a substantive prompt |
| P4 16:11:00 | `error_paste` | 5 | P3's turn's run | `X.same_error` |
| P5 16:14:00 | `low_effort` | 8 | P4's paste, then two runs in P4's turn | `X.same_error` |
| P6 16:19:00 | `error_paste` | 0 | P5's turn's run made it 9, and the commit at 16:16:36 set it to 0 | credited; its paste brings the count to 1 |
| P7 16:24:00 | `low_effort` | 1 | P6's turn's runs made it 2 and 3, the passing run at 16:20:05, a `run_green`, cleared the error, and it came back once at 16:20:30 | credited. Without the clearing the count would be 4 and the go-ahead `X.same_error` |

## What the rows rely on

- From 16:02:00 until P2's lead starts at 16:02:38, P1's turn's bucket drains as `SUP`: its 20 s base, then what its plan and edits filled. P2's turn's bucket does the same until P3's lead starts at 16:05:04. Every other second from 16:00:00 to 16:08:00 is in a lead.
- P4 opens no window (SM 4.2, What a discount does), so its lead from 16:08:36 credits nothing and P3's bucket is read instead: `SUP` while P3's turn runs until 16:09:00, then `READ` until 16:11:00. P3's turn filled 225 s, enough for those 180 s.
- P4 and P5 reset the bucket to zero and add nothing to it (SM 2.4), and they count as no human anchor for reading. Nothing else covers 16:11:00 to 16:15:36, P5's stop. Those 276 s are what the loop cost: scoring the same records with no turn discounted credits every one of them, as `SUP` while P4's and P5's turns run, `READ` and P5's `GO` lead.
- The commit's 60 s lead starts at P5's stop, so it doesn't reach back past the discounted turn's stop (SM 4.2), and P6's 144 s lead starts at the commit.
- P6's turn fills 405 s, which drain as `SUP` while it runs until 16:21:00 and as `READ` until P7's lead at 16:23:40. P7's turn drains as `SUP` until 16:25:00 and as `READ` to the end of the window.
- Rows 16:11 to 16:15 are `X.same_error` by SM 4.2. Scored with no turn discounted, every second of them would be credited, so they are what the loop cost, and each takes the code of the latest discounted turn at or before it, P4's or P5's. Without that rule SM 5.2 would make the seconds inside P4's and P5's turns `X.agent_only`, and 16:13:40 to 16:14:00, with no agent running, `X.no_anchor`.
- Every minute has credit or a running agent, so each has a bucket (SM 5.1).
- `excluded_loops_s` is the rows' loop-code seconds, which gen.py adds up: 276 s.
