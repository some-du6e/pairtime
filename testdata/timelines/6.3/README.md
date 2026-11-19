# SM 6.3: the five-project afternoon

Sam, 16, on macOS, has five projects open and works on four of them between 14:00 and 14:30 while two agents run in the background. Every SM row is used: 1,800 s credited, A 510, B 450, E 540 and D 300, plus 570 s of A's agent time and 180 s of B's that nobody watched.

| | |
|---|---|
| SM rows | 14:00 to 14:25, the range 14:26-14:29, and the paragraph after the table. The whole-afternoon table is illustrative and not used |
| Date and zone | Saturday 2026-11-28, America/Los_Angeles, UTC-08:00. 14:00 local is 22:00Z |
| OS and evidence | macOS. `idle_keys_s` comes from the key-down idle time, so it is set in every sample and every credited second is level A. Witnessed |
| Window | 14:00:00 to 14:30:00. Records from 13:54:45 set up B's 13:58 answer and A's session. Records to 14:31:00 hold the D heartbeat whose lead covers 14:29 |
| Projects | A `portfolio` (Claude Code in iTerm window 1), B `discord-bot` (Codex CLI in iTerm window 2), D `plant-sensor` (VS Code with WakaTime, by hand), E `rusty-cli` (Cursor). C `homework-helper` has no records, since Sam works on it after 15:00 |
| Files | `afternoon.py` builds this timeline and the one in [6.3-tabs](../6.3-tabs/), which differs only in `os.focus`. `gen.py` calls it |

## Events

- 13:54:45, iTerm window 2: Sam starts Codex. Its `SessionStart` hook fires at 13:54:49 and the agent re-reads the focused window, which now hosts B's session. He asks B something at 13:55:40, and its answer and `Stop` come at 13:58:00. That is "B's answer from 13:58".
- 13:59:00, window 1: he types `claude`. Claude Code's `SessionStart` fires at 13:59:05 and window 1 now hosts A's session. He starts typing A's 180-character prompt at 13:59:30.4.
- Prompts come from each harness's prompt hook: A's at 14:01:30 and 14:20:30, B's at 14:04:30 and 14:25:00, E's at 14:09:00 and 14:13:00. Tool calls and stops come from hooks too. Assistant text comes from Claude Code's transcript and Codex's rollout file (`via` `log`) and from Cursor's `afterAgentResponse` hook.
- A's first turn makes five edits from 14:02:31 to 14:07:50, 300 changed lines in all, and stops at 14:09:00 with a 210-word summary. B's first turn edits 20 lines, runs the tests and stops at 14:07:00 with a 210-word answer.
- `os.focus` at each switch: window 2 at 14:02:00.2, Cursor at 14:05:00.3, window 1 at 14:14:00.4, window 2 at 14:20:30.5, VS Code at 14:25:00.5. Each iTerm window hosts one session, so each resolves to that session's project.
- Cursor, project E: WakaTime heartbeats at SM's times, 14:06:00, 14:07:30, 14:08:40, 14:11:00 and 14:12:30. A new agent chat at 14:08:41.5 (`sessionStart`, `entrypoint` `ide`). The agent's `cargo test` fails with exit code 101 at 14:09:30 and passes at 14:13:40 with the same `cmd_hash`, which makes it a `run_green`. Sam's commit at 14:14:00, `actor` `human`.
- VS Code, project D: heartbeats at 14:25:00.8 and then every 70 to 75 s until 14:30:55.

## Timing choices SM leaves open

- Input. Key presses whenever he types: prompts, code, the commit message, Cmd-Tab at 14:14:00.3 and Cmd-` at 14:20:30.4. Pointer moves and scrolls every 9 to 11 s while he reads or watches. Every slot of the half hour has input, so every slot is `yes`, and every slot of each prompt's typing part has key presses.
- B's earlier turn, which SM only mentions: a 64-character prompt at 13:55:40, a 40-word message, a read, an edit of 18 lines, a test run, a 160-word answer and the stop at 13:58:00. He rereads the answer at 14:02, but those seconds are inside B's lead, so they are `P` and the bucket isn't drained.
- The buckets, which gen.py checks with SM 2.4's rules:
  - A's first turn opens with a 90-word plan at 14:01:38, so the bucket doesn't run dry while he watches until 14:02:00. The 300 lines fill it to its 600 s cap long before 14:14.
  - B's first turn opens with a 45-word plan at 14:04:37 for the same reason, then adds 150 s for the 20-line edit, 30 s for the test run's 4 visible lines and 70 s for the 210-word answer. 20 + 15 + 150 + 30 + 70 - 30 drained = 255 s are left at 14:20:30, SM's number.
  - E's first Cursor turn shows the failing run's 4 lines and a 40-word message, which leaves 63.3 s at 14:12:30, more than the 10 s of `READ` there.
- How long the agents run. SM says A's agent stops at 14:09:00 and B's at 14:07:00, and that A's ran 570 s and B's 180 s that nobody watched. That fixes the second turns: A's stops at 14:23:00 and B's at 14:26:00. SM doesn't time E's Cursor turns. They run from 14:09:00 to 14:09:36 and from 14:13:00 to 14:13:44, inside E's credited seconds.
- Editor heartbeats. WakaTime sends one when the file changes, on a save, and otherwise at most every two minutes per file. So in Cursor he scrolls without moving the cursor until his first keystroke at 14:05:59.9, and the heartbeats at 14:07:30, 14:08:40 and 14:12:30 are saves. A heartbeat at any other time would be another `E` or `NAV` anchor and could move a reason boundary. In VS Code he types right after the switch, so the slot 14:25:00 has a D heartbeat of its own.
- Cursor and VS Code each have one window open, on E and D, so their `os.focus` events carry `focus_project`, as 6.2's do.

## What the rows rely on

- 14:01:30-14:02:00 is A's `SUP`. B's lead has started at 14:00:50, but focus is on window 1, so it isn't realized.
- 14:08:40-14:09:00 is `P`. The 50-character request's lead is 120 + 50 / 1.5 = 153.3 s and starts at 14:06:26.7, but before 14:08:40 the lead of the edit at 14:08:40 wins, because it is the next anchor (SM 3.3 rule 3).
- 14:12:30-14:12:40 is `READ`, because no window covers it between the edit's lead and the go-ahead's. "run the tests again" has 19 characters, under `SUBSTANTIVE_CHARS`, so it is `low_effort` with a 20 s lead (SM 2.2).
- A's `READ` from 14:14:00 to 14:17:00 and B's from 14:20:30 to 14:22:00 are within `READ_HORIZON_S` of the follow-up at 14:20:30 and the reply at 14:25:00.
- `agent_only_s.<project>` in the totals is a project's agent time minus the seconds that project credits during it, the chess clock of SM 2.6: A 570 and B 180, SM's 750 s together, and E 0. A plain `agent_only_s` would have to be at most `agent_running_s`, which `internal/timelines` checks. Here it can't be: `agent_running_s` counts each second once, 740 s, and A's and B's runs overlap from 14:04:30 to 14:07:00.
- SM assumes the agent sees the switch between iTerm's two windows and knows which session each one hosts. DESIGN's macOS focus source, `NSWorkspace` app activation (CP 3.3), reports app changes only, and both windows belong to one iTerm process, so the agent DESIGN describes would record what 6.3-tabs records. This fixture follows SM. The [two-tab erratum](../../../docs/errata/2026-11-19-sm-6.3-two-tabs.md) raises the point.
