# SM 4.2: alternating two prompts, A, B, A, B, A

SM 4.2's re-roll rule ends: "The count runs over all earlier prompts since the last change, not just the previous one, so alternating between two prompts (A, B, A, B, A) is caught at the third A." This fixture is that case. Five prompts go out three minutes apart with no change event between them. The third A is attempt 3 and discounted as `X.reroll`, and the other four are credited. SM gives no minute rows for it, so every row here follows from SM's rules applied to the timeline below, and each row marks its fields `derived`.

| | |
|---|---|
| SM text | 4.2, the re-roll rule and "What a discount does"; 2.4 for the bucket after a discount |
| Date and zone | Monday 2026-11-23, Australia/Melbourne, UTC+11:00. 19:31 local is 08:31Z |
| OS and evidence | macOS with Terminal, `idle_keys_s` set, so every credited second is level A. Witnessed |
| Window | 19:31:00 to 19:46:00. Records from 19:29:35 start the session |
| Project | An invented `dashboard` |
| Files | gen.py uses `../6.5/neardup.py` for SM 4.2's normalization and attempt count |

## Events

- 19:29:35: a click on Terminal, `claude` typed, Claude Code's `SessionStart` at 19:29:42 with `permission_mode` `acceptEdits`, and the terminal now hosts the session.
- Prompt A at 19:33:00, 19:39:00 and 19:45:00, prompt B at 19:36:00 and 19:42:00, each typed in the 40 s before it is sent.
- Every turn the same way: a 30-word plan 5 s in, one 20-line edit 25 s in, a 110-word answer at 58 s, the stop at 60 s. A's turns edit `sidebar.css` and B's `topbar.css`.

## Timing choices

- The texts are invented, since SM names the prompts A and B only. A is "make the sidebar collapse into an icon rail on small screens" and B is "keep the top bar pinned to the top of the page as you scroll". Each repeat is identical to the first, and A and B share no shingle.
- Both have 60 characters, so every lead is 120 + 60 / 1.5 = 160 s and starts 20 s after the prompt before it. Each turn's first 20 s are its bucket's 20 s base, credited as `SUP`, and the rest is the next prompt's lead, `P`.
- Nothing changes between the prompts: no hand edit, typed command, commit, permission answer, interrupt, error or test run. The agent's own edits aren't change events (SM 4.2).
- A's turns and B's edit different files. The rewrite rule compares a turn with the one before it, which always edited the other file, so the overlap is 0 every time and only the re-roll count can catch the loop.
- He sends each prompt two minutes after a 110-word answer, so no turn is unread and the autopilot rule stays out.

## What the rows rely on

- The attempts are 1, 1, 2, 2 and 3. A3's count includes A1 although B1 and B2 came between them. A rule that compared each prompt with the previous one only would never fire here.
- A3 opens no window, so its lead from 19:42:20 credits nothing and B2's bucket is read instead: `SUP` while B2 runs until 19:43:00, then `READ` until 19:45:00. B2 filled it with 20 + 10 + 150 + 36.7 s, enough for those 180 s.
- A3 resets the bucket to zero and adds nothing to it (SM 2.4), so the minute A3 runs, 19:45, is `X.reroll` 60. Without the reset, B2's last 36.7 s would be read there.
- Row 19:45 is `X.reroll` by SM 4.2. Scored with no turn discounted, A3's own bucket, its 20 s base and then its plan, edit and answer, would have credited every second of the minute, so the minute is what the loop cost and takes A3's code. Without that rule SM 5.2 would make it `X.agent_only`, since A3's agent runs and nothing credits a second. B2's bucket credits A3's lead either way, so no other row depends on the rule.
- `excluded_loops_s` is the rows' loop-code seconds, which gen.py adds up: A3's minute, 60 s.
