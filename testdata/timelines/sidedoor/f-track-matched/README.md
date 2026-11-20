# Side door (f): a track call 4 s after the hook prompt it copies

Karim's AGENTS.md entry doing its job beside hooks. On a Windows laptop, a person types a 150-character prompt into Claude Code in Windows Terminal. Claude's first tool call is a Bash call that runs `pairtime track` with the message 4 s later, as the entry asks. pairtime already sees the session through its hooks and transcript, so the hook prompt is the anchor and the track call is `track_matched`. It opens nothing, resets nothing and fills nothing. The session earns exactly what it would with no track call: 280 s at level B.

| | |
|---|---|
| Plan | WP-FX4 (f) |
| Rules | DESIGN 5.2, 5.4, 6.2 D19, 6.4 |
| Date and zone | Wednesday 2026-11-25, America/Chicago, UTC-06:00 |
| OS and evidence | Windows 10, `idle_keys_s` null. Every credited second is level B. Witnessed |
| Window | 14:00:00 to 14:08:00 |

## Events

- `os.focus` at 13:58:00 on Windows Terminal, then `session.start` from Claude Code's hook at 13:58:30 and `os.focus` again with the session in `hosted_sessions`, as testdata/timelines/README.md describes.
- `prompt.submit` from `UserPromptSubmit` at 14:04:40: 150 characters, none pasted.
- `cli.track` at 14:04:44, harness `claude_code`, the session's key. `text_len` 141, `env_hints` `CLAUDE_CODE_CHILD_SESSION` and `CLAUDE_CODE_SESSION_ID`, and `log_match` `matched`, because the transcript tailer already holds the user entry.
- `agent.tool` `bash` from `PostToolUse` at 14:04:44.3, the call that ran `pairtime track`, with no output lines.
- `agent.output` from the transcript, 120 words, and `agent.stop` from the `Stop` hook, both at 14:05:30.

## Timing choices

- The person's touchpad moves every 10 s, and they type the prompt from 14:03:30.7 to the Enter at 14:04:39.2, a key every 0.5 s. Windows can't tell the two apart. The presses land on .2 and .7, so none falls on a slot boundary (testdata/timelines/README.md).
- The model passes 141 of the 150 characters. `text_len` doesn't decide anything here.
- The Bash call starts at 14:04:43.9 and `pairtime track` runs inside it at 14:04:44.0. DESIGN 5.1 registers `PostToolUse` for `Bash`, so the call leaves an `agent.tool` record that counts toward `logged_s`.

## How the numbers follow

The hook prompt passes SM 2.2 rule 6 and is substantive, with a lead of 120 + 150 / 1.5 = 220 s. It arrived via `hook` with `input_any` only, so it is level B (DESIGN 6.4).

The track call comes from a session pairtime sees through hooks and a log reader, 4 s after that session's hook prompt. That is within `TRACK_DEDUPE_S` = 30 s, so DESIGN 5.4 records it as `track_matched`: it opens nothing and feeds the compliance counter. It is within `TRACK_LOG_MATCH_S` = 10 s too, so it raises no `track_without_prompt`.

| | Credited |
|---|---|
| The prompt's lead, 14:01:00 to 14:04:40 | 220 s `P` |
| The 20 s base while the agent runs, 14:04:40 to 14:05:00 | 20 s `SUP` |
| The 120-word answer, 40 s from 14:05:30 to 14:06:10 | 40 s `READ` |
| In all, level B | 280 s |

From 14:05:00 to 14:05:30 the agent runs with nothing to read: `X.agent_only`, as in SM 6.2's 07:41.

`logged_s` and `ai_logged_s` are both 50 s: 4.3 s from the prompt to the Bash call's `PostToolUse`, then 45.7 s to the stop. The `cli.track` record isn't in either series (D19). The transcript's `agent.output` isn't one of SM 5.4's agent events, and it shares the stop's millisecond anyway.

A scorer that opened an anchor for the call would reset the bucket at 14:04:44 and credit the drain after it at level C, the level of the nearest anchor (DESIGN 6.4), so this case catches that.
