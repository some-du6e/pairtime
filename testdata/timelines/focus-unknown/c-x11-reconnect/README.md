# Focus unknown (c): 30 s of unknown focus on X11, inside a prompt's lead

Lee, 16, works on `weather-cli` with Claude Code in a terminal, on Linux Mint's Cinnamon desktop on X11, without the opt-in key counter. Every credited second here is level B, except in the two slots that unknown focus holds longest. At 10:02:20, as Lee types a prompt, the X connection the agent follows focus on drops, and the new connection's first report names the terminal again at 10:02:50. Only the seconds of those two slots drop to level C, 30 s of the 575 credited.

| | |
|---|---|
| Source | WP-FX8's (c) (docs/PLAN.md). Invented |
| Rules | SM 2.5's focus-unknown paragraph and its slot rule, with SM 2.2 to 2.4, 3.3 and 5.2 and the parameters of SM 8 |
| Date and zone | Thursday 2026-11-26, Australia/Sydney, UTC+11:00. 10:00 local is 23:00Z the day before |
| OS and evidence | Linux, X11 without the key counter, so `idle_keys_s` is null in every sample and credit is level B. The 30 s from 10:02:15 to 10:02:45 are level C with `focus_unknown`. Witnessed |
| Window | 10:00:00 to 10:10:00. Records from 09:59:20 set up the session, and samples run to 10:10:15 |
| Project | `weather-cli`, one Claude Code session in the terminal |

## The events

- **09:59:20.** Lee clicks the terminal. Focus on a terminal makes a project context live, so the agent records the `os.focus` and starts sampling (DESIGN 3.3). The terminal hosts no harness yet.
- **09:59:30.** Claude Code's SessionStart hook. The session now runs under the focused window, so the agent walks it again and records a second `os.focus` with the session in `hosted_sessions` and `focus_project` `weather-cli` (SM 3.2 rule 1).
- **10:00 to 10:02.** He reads the bug report in the terminal, scrolling every 5 to 9 s.
- **10:02 to 10:04.** He types a 180-character prompt, a key every 300 to 500 ms, and sends it at 10:04:00.
- **10:02:20.** The X connection the agent follows `_NET_ACTIVE_WINDOW` on drops. The agent records `os.focus` `unknown` with no other field and re-makes the connection (internal/platform/linux). Idle sampling has an X connection of its own, which stays up, so presence is sampled throughout.
- **10:02:50.** The new connection's first report names the terminal. The walk finds the session again, so the record is the terminal's with the session hosted, as before the drop.
- **10:04 to 10:06.** The turn: 45 words at 10:04:30, edits of 30 lines at 10:05:00 and 10:05:30 with no visible output, and a 120-word summary with the stop at 10:06:00. He watches, moving the pointer about every 29 s.
- **10:06 to 10:10.** He reads, scrolling every 5 to 9 s.

## Which slots are of unknown focus

SM 2.5 gives each slot the window that held focus longest in it, counting `os.focus` records of one `app_class` and `focus_project` as one window, so the terminal before the drop and after it are one window.

| Slot | Terminal | Unknown | Slot's focus |
|---|---|---|---|
| 10:02:15 | 5 s, to 10:02:20 | 10 s | unknown |
| 10:02:30 | 0 s | 15 s | unknown |
| 10:02:45 | 10 s, from 10:02:50 | 5 s | the terminal |

So 30 s of unknown focus that start and end mid-slot leave exactly two slots, 30 s, of unknown focus.

## Credit, minute by minute

| Minute | Credited | Reasons | Arithmetic |
|---|---|---|---|
| 10:00 | 60 | P 60 | The prompt's lead, 120 + 180 / 1.5 = 240 s, from 10:00:00. Its think part runs to 10:02:00. The terminal hosting the session holds every slot, so level B |
| 10:01 | 60 | P 60 | The think part |
| 10:02 | 60 | P 60 | The typing part from 10:02:00; with no key counter it needs only presence (SM 2.3). The slots from 10:02:15 and 10:02:30 are of unknown focus, which passes the lead's focus test, so their 15 + 15 = 30 s are level C with focus_unknown. The slots from 10:02:00 and 10:02:45 stay level B |
| 10:03 | 60 | P 60 | The typing part. The prompt is sent at 10:04:00 with the terminal in front, so SM 2.2 rule 6's focus test passes on known focus: human_typed, level B, and no focus_unknown on the anchor |
| 10:04 | 35 | SUP 35 | The bucket resets to 20 s, drained 10:04:00 to 10:04:20, and 45 words add 15 s at 10:04:30, drained to 10:04:45: 20 + 15. Nothing is left to read from 10:04:20 to 10:04:30 and 10:04:45 to 10:05:00 while Lee's turn runs: X.agent_only 10 + 15 = 25 |
| 10:05 | 60 | SUP 60 | The first edit adds 30 × 60 / 8 = 225 s at 10:05:00 and the second 225 more at 10:05:30: SUP to the stop at 10:06:00, which leaves 225 − 30 + 225 − 30 = 390 |
| 10:06 | 60 | READ 60 | The 120-word summary adds 40 s, so 430 are left. READ from the stop, at the level of the anchor that qualifies it, the nearest within 600 s: the prompt, level B (DESIGN 6.4) |
| 10:07 | 60 | READ 60 | Reading |
| 10:08 | 60 | READ 60 | Reading |
| 10:09 | 60 | READ 60 | Reading. 430 − 240 = 190 s are left at 10:10:00 |

## Totals

| Total | Seconds | Arithmetic |
|---|---|---|
| `credited_s` | 575 | 60 + 60 + 60 + 60 + 35 + 60 + 60 + 60 + 60 + 60 |
| `P` | 240 | 4 × 60 |
| `SUP` | 95 | 35 + 60 |
| `READ` | 240 | 4 × 60 |
| `X.agent_only` | 25 | 10 + 15 |
| `agent_running_s` | 120 | 60 + 60 |
| `agent_only_s` | 25 | 120 − (35 + 60) |
| `level_b_s` | 545 | 575 − 30 |
| `level_c_s` | 30 | 15 + 15 |
| `focus_unknown_s` | 30 | 30 |
| `level_a_s` | 0 | 0 |
| `unknown_focus_s` | 30 | 30 |
| `ai_credited_s` | 575 | 240 + 95 + 240 |

`unknown_focus_s` is the time focus was unknown, 10:02:20 to 10:02:50, which happens to equal the seconds at level C.

## Readings the case relies on

- **A second's level, not an anchor's.** SM 2.5 makes level C "every second credited in such a slot" and every second "credited from an anchor that passed its focus test only this way". The prompt passed rule 6 with the terminal in front at 10:04:00, so it is a level-B anchor, and only its lead's seconds in the two slots drop. The reading it qualifies stays B (DESIGN 6.4: a reading second takes the level of the anchor that qualifies the drain). `slots.json` gives each slot's level.
- **No act while focus is unknown.** He only types between 10:02:20 and 10:02:50, so no anchor's evidence test is judged on unknown focus. Had he saved a file in an editor then, that edit's anchor would be level C with `focus_unknown`, and so would every second its lead credits, inside the two slots or not.
- **An edit fills the bucket when it lands.** An `agent.tool` record fills the bucket at its `t`, which is its `t_end`, not its `t_start`, as SM 6.1's "lands at 14:05:30" and 6.2's "until the agent's 40-line edit lands at 07:41:50" read it. Here the edits fill at 10:05:00 and 10:05:30, which the 10:05 SUP rests on. Filled at `t_start`, the bucket would have output from 10:04:52.5 and SUP would start before 10:05:00.
- **No silence, no lookup.** No silence reaches 120 s, so SM 2.1's return rule, which reads focus through a silence, never applies, and no slot is a browser slot.
