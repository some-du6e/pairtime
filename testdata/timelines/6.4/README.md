# SM 6.4: a `/goal` agent running unattended overnight

Jordan, 16, on Linux with X11 and the key counter on, types a `/goal` prompt into Claude Code at 23:40, watches for four minutes and goes to bed. The agent works alone until 06:10. At 07:30 he unlocks, reads, and sends a follow-up at 07:37. Every SM row is used: 810 s credited, all of it AI-assisted, with 23,400 s of agent time and 23,180 s of it agent-only.

| | |
|---|---|
| SM rows | 23:37 to 23:43, the range 23:44 to 07:29, 07:30 to 07:36, and the totals paragraph |
| Date and zone | Friday 2026-11-20 into Saturday, Europe/Berlin, UTC+01:00. 23:37 local is 22:37Z |
| OS and evidence | Linux, X11 with the opt-in key counter, so `idle_keys_s` is set and every credited second is level A. Witnessed |
| Window | 23:37:00 to 07:37:00. Records from 23:29:50 set up the session and aren't checked |
| Project | `pixel-forge` |
| Erratum | [2026-11-19-sm-6.4-prompt-lengths](../../../docs/errata/2026-11-19-sm-6.4-prompt-lengths.md): the quoted prompts have 73 and 60 characters, SM counts 75 and 61. The events use SM's counts, so the rows are SM's |

## Events

- 23:29:50: he clicks the terminal (`os.focus`), types `claude`, and Claude Code's `SessionStart` hook fires at 23:29:58 (`entrypoint` `cli`, `parent_class` `terminal_app`, a TTY). The agent re-reads the focused window, which now hosts the session. He leaves at 23:30:03 and sits down again at 23:37:00.
- 23:40:00: `prompt.submit` with `slash_command` `/goal`, 75 characters typed.
- The night: nine turns of 2,600 s each. T1 is his prompt; T2 to T9 are stop-hook continuations, which have no `prompt.submit` and whose `agent.stop` carries `stop_hook_active` true. Claude Code allows at most 8 continuations in a row (HS 3.1), which is why there are nine turns and not more. Tool calls (`via` `hook`) come every 55 to 110 s, assistant text from the transcript (`via` `log`) after every third call, 31 commits by the agent 750 s apart from 23:52 to 06:07 (`git.change` with `actor` `agent`, each 0.3 s after the `bash` call that made it), test runs failing with three error signatures until the first green run at 03:12:00, and Lighthouse runs after it. The last turn's 240-word summary arrives at 06:09:55 and its stop at 06:10:00.
- `os.power` `lock` at 23:53:10, 600 s after his last touch, and `unlock` at 07:30:00.
- 07:37:00: `prompt.submit`, 61 characters typed, the last record.

## Timing choices SM leaves open

- Presence samples every 15 s while the session runs, then once a minute from 06:10 to 07:30, when there's no input and no running turn (DESIGN 3.3, DESIGN 6.2 D14), plus one at each hook and power event.
- Input: mouse moves and scrolls at 23:37:00.4 and every 13 s or so while he looks at the repo; key presses every 1.5 s from 23:38:00.4 to the Enter at 23:39:59.8, so every slot of the lead's typing part (23:39:10 to 23:40:00) has keyboard input; scrolls every 10 s while he watches; the last touch at 23:43:10.0 exactly, so the tail ends at 23:43:40; the password from 07:29:52 to 07:29:59.4, on the locked screen; scrolls every 8 s from 07:30:03.5; key presses every 1.4 s from 07:35:30.4 to 07:36:59.8.
- `permission_mode` `auto`. SM doesn't name it, and a `/goal` run can't work through the night in the default mode, which stops at the first permission prompt.
- The first four minutes of output (a 150-word plan, two edits of 16 and 12 lines, two test runs with 4 visible lines each, a 40-word message) fill the bucket faster than he reads, so it never runs dry before 23:43:40. gen.py checks that with SM 2.4's fill and drain rules, and that at 07:30 the bucket holds the newest 600 s of output, all of it from after 05:34:19, so nothing expires while he reads.
- No agent event is more than 120 s from the next, so Hackatime's replay of the night is the 23,400 s run plus 120 s before the morning prompt: 23,520 s, which SM calls about 23,500.

## What the rows rely on

- 23:43:40 to 07:30:00 is `X.absent` even while the agent runs: SM's 23:43 row puts the 20 s after the tail under `X.absent`, not `X.agent_only`. The seconds still count as agent-only time, so `agent_only_s` is 23,400 − 220.
- 06:10 to 07:29 has no minute buckets. Nothing is credited and no agent runs, and SM 5.1 writes a bucket only when one of the two happens.
- The lead of the 07:37:00 prompt starts at 07:34:19. That is 120 + 61 / 1.5 = 160.67 s rounded to 161, as SM rounds it. Without the rounding it would start at 07:34:19.33.
