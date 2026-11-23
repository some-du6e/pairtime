# Side door (g): a track call on a hooked session with no prompt near it

Case (f)'s session without the compliant call. The person sends a prompt at 14:04:40, Claude answers in text with no tool call, and stops at 14:05:30. Two minutes later a process started inside the session runs `pairtime track` with no turn behind it. pairtime sees the session through hooks and its transcript, and no prompt of the session is within `TRACK_DEDUPE_S` = 30 s of the call. DESIGN 5.4 says the call opens nothing and raises the tier-2 flag `track_without_prompt`, since it "likely came from outside the agent's turn". The credit is the session's own 280 s at level B.

| | |
|---|---|
| Plan | WP-FX4 (g) |
| Rules | DESIGN 5.2, 5.4, 6.2 D19, 6.4 |
| Date and zone | Wednesday 2026-11-25, America/Chicago, UTC-06:00 |
| OS and evidence | Windows 10, `idle_keys_s` null. Every credited second is level B. Witnessed |
| Window | 14:00:00 to 14:09:00 |

## Events

- `os.focus`, `session.start` and `os.focus` again before the window, as in (f).
- `prompt.submit` from `UserPromptSubmit` at 14:04:40, 150 characters.
- `agent.output` from the transcript, 120 words, and `agent.stop` from the `Stop` hook, at 14:05:30.
- `cli.track` at 14:07:30, harness `claude_code`, the session's key, `text_len` 64, `env_hints` `CLAUDE_CODE_SESSION_ID`, `log_match` `unmatched`. No `agent.tool` comes with it: Claude made no tool call.

## Timing choices

- The call's process descends from the session's `claude` process and inherited `CLAUDE_CODE_SESSION_ID`, as a loop left running in the background would. The transcript tailer covers the session and holds no user entry for it.
- The call is 170 s after the prompt, outside `TRACK_DEDUPE_S` = 30 s, so it isn't `track_matched` and it raises `track_without_prompt`.

## How the numbers follow

The prompt, its lead and the reading are (f)'s: 220 s of `P`, 20 s of `SUP` and 40 s of `READ`, 280 s at level B, with 30 s of `X.agent_only` while the agent runs with nothing to read.

The track call opens nothing, starts no turn and resets no bucket, so 14:07 and 14:08 stay without credit or a bucket. The flag changes review priority and the audit chance, never credit (SM 4.5).

`logged_s` and `ai_logged_s` are both 50 s, from the prompt to the stop. The `cli.track` record counts toward neither (D19).

A scorer that opened an anchor for the call would get it wrong in a way this case shows. On Windows the anchor's typing part, 64 / 1.5 = 42.7 s, is never realized, so its think part runs from about 14:04:47 to 14:06:47. There it would beat the bucket, and its own 20 s base would drain from 14:07:30, all at level C.
