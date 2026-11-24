# Focus unknown (b), the X11 twin: the same script with the browser in front

[b-pty-game](../b-pty-game/)'s acts at its times, on a desktop running X11 whose window manager keeps `_NET_ACTIVE_WINDOW`, without the opt-in key counter. Here the agent sees the browser in front the whole time, so each scripted prompt fails SM 2.2 rule 6's focus test and is `no_input`. Nothing is credited, against 1,140 s at level C where focus is unknown.

| | |
|---|---|
| Source | WP-FX8's (b) (docs/PLAN.md), the twin |
| Rules | SM 2.2 rules 6 to 8 and SM 5.2, with SM 2.5 and the parameters of SM 8 |
| Date and zone | Wednesday 2026-11-25, Europe/London, UTC+00:00, as b-pty-game |
| OS and evidence | Linux, X11 without the key counter. `idle_keys_s` is null in every sample. Nothing is credited; `evidence` B is the level this machine's credit would have. Witnessed |
| Window | 20:00:00 to 20:20:00, as b-pty-game |
| Project | `idle-farm` |

## The events

b-pty-game's, with two `os.focus` records in place of its one. The SessionStart hook at 19:55:04 makes a project context live, and the agent records the window in front: the terminal Kai ran the script in, with no `hosted_sessions`, since Claude Code runs under the tmux server, and so no `focus_project`. At 19:55:20 the browser comes to the front, and the agent records it, `app_class` `browser`. Nothing else comes to the front before 20:20:30.

## What the rules make of each prompt

Rule 6 needs "focus at submit on a window whose `hosted_sessions` includes this session". The browser hosts none. Rule 7 needs the same focus, so it fails too, and rule 8 makes each prompt `no_input`: no anchor, and a turn whose running seconds would be `X.no_input` (SM 2.2, 5.2). Four `no_input` prompts out of four raise the reviewer flag `no_input_prompts` (SM 8.7).

## Credit, minute by minute

T is a cycle's prompt: 20:03:00, 20:08:00, 20:13:00 and 20:18:00. A minute has a bucket only while the script's turn runs, from T to T + 100 (SM 5.1).

| Minute | Credited | Reasons | Arithmetic |
|---|---|---|---|
| 20:00-20:02 | 0 | none | The game. No anchor and no running turn, so no bucket |
| 20:03 | 0 | none | The first turn runs from 20:03:00 with the browser in front. A browser slot outside a lookup is X.other_focus, which SM 5.2 puts before the turn step: X.other_focus 60 |
| 20:04 | 0 | none | The turn runs to 20:04:40: X.other_focus 60 |
| 20:05-20:07 | 0 | none | No anchor and no running turn, so no bucket |
| 20:08 | 0 | none | As 20:03: X.other_focus 60 |
| 20:09 | 0 | none | As 20:04: X.other_focus 60 |
| 20:10-20:12 | 0 | none | No bucket |
| 20:13 | 0 | none | As 20:03: X.other_focus 60 |
| 20:14 | 0 | none | As 20:04: X.other_focus 60 |
| 20:15-20:17 | 0 | none | No bucket |
| 20:18 | 0 | none | As 20:03: X.other_focus 60 |
| 20:19 | 0 | none | As 20:04: X.other_focus 60 |

## Totals

| Total | Seconds | Arithmetic |
|---|---|---|
| `credited_s` | 0 | 0 |
| `X.other_focus` | 480 | 4 × 2 × 60 |
| `agent_running_s` | 400 | 4 × 100 |
| `agent_only_s` | 400 | 400 − 0 |
| `no_input_prompts` | 4 | 4 |
| `human_typed_prompts` | 0 | 0 |

## Readings the case relies on

- **The twin changes only focus.** Every act, time and amount is b-pty-game's, through the same builder ([`../ptygame.py`](../ptygame.py)). Only the `os.focus` records and the device differ.
- **No lookup.** The browser run has no credited slot on either side, so it is no lookup under SM 2.5, and E-lookup-lookahead has nothing to decide.
- **The bucket fills but never drains.** The turns aren't human-owned, so they reset nothing and add no 20 s base, while their output fills the bucket (SM 2.4). From the fourth turn's summary it holds more than 600 s, so the cap evicts the oldest output. Nothing drains it, since every slot's focus is the browser, and so no second is `X.bucket_cap`.
