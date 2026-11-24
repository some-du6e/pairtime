# Focus unknown (a): SM 6.1's first cycle on GNOME on Wayland

Rowan's first 12-minute cycle from SM 6.1, the same acts at the same times, on a laptop running GNOME on Wayland without pairtime's GNOME extension. GNOME tells other programs nothing about the window in front, so the agent records one `os.focus` with `app_class` `unknown`, and Mutter's idle monitor gives `idle_any_s` but no `idle_keys_s`. SM 2.5 decides the rest. 695 of the 720 s are credited, as in SM 6.1, but every one of them at level C with `focus_unknown`, and SM 6.1's 120 s of `LOOKUP` become `READ` 90, `NAV` 10 and `G` 20.

| | |
|---|---|
| Source | SM 6.1's table from 14:00 to 14:11, and WP-FX8's (a) (docs/PLAN.md) |
| Rules | SM 2.5's focus-unknown paragraph, with SM 2.1 to 2.7, 3.3 and 5.2 and the parameters of SM 8 |
| Date and zone | Saturday 2026-11-21, Asia/Kolkata, UTC+05:30, as in the 6.1 fixture. 14:00 local is 08:30Z |
| OS and evidence | Linux, GNOME on Wayland without pairtime's extension. `idle_keys_s` is null in every sample. Every credited second is level C with the label `focus_unknown`. Witnessed |
| Window | 14:00:00 to 14:12:00. Records from 13:58:05.5 set up the session, and records to 14:12:45 hold the next cycle's first heartbeat, whose lead credits 14:10:30 to 14:10:40 and 14:11:40 to 14:12:00 |
| Project | `cs2go`, one Claude Code session in a terminal |

## The events

The cycle's acts are SM 6.1's, at the 6.1 fixture's times to the millisecond. What differs is what the agent can see:

- **One `os.focus`.** Before 13:58 no project context is live: the agent can't see VS Code or the terminal come to the front, and no harness runs yet (DESIGN 3.3). Claude Code's SessionStart hook at 13:58:05.5 makes a context live, so the agent records then the one `os.focus` it can, `app_class` `unknown` with no other field, and starts sampling every 15 s. Focus stays unknown, so nothing ends that record. Rowan's Alt-Tabs to VS Code at 13:59:44.9, to the terminal at 14:01:59.9, to the browser at 14:08:59.9, back to the terminal at 14:10:59.9 and to VS Code at 14:11:59.9 leave no record.
- **No `idle_keys_s`.** Mutter's idle monitor reports any input, so every sample has `idle_any_s` and a null `idle_keys_s`, and the typing part of a lead needs only presence (SM 2.3).
- **Heartbeats via `editor`.** WakaTime's VS Code plugin sends navigation heartbeats at 14:00:30 and 14:01:50, and the next cycle's at 14:12:30. On Linux the agent's peer-process check finds wakatime-cli under VS Code, so they arrive via `editor` (DESIGN 5.7), and each takes a presence sample at once (DESIGN 3.3).
- **The rest as in SM 6.1.** The 300-character prompt at 14:04:00, typed. 45 words at 14:04:30 and at 14:05:00. A permission request at 14:05:10, answered at 14:05:25. Edits of 30 lines at 14:05:30 and 14:06:00 with no visible output. The stop at 14:06:30 with a 160-word summary. The commit at 14:11:40, `actor` `human`.

Input follows the 6.1 fixture's pattern: keys while he navigates and types, pointer moves every 5 to 9 s while he reads, keys in the browser. No silence in the window is longer than 34 s, so every slot is `yes` or `bridged`.

## Credit, minute by minute

Every slot's focus is unknown, so every focus test of SM 2 and 3 passes for `cs2go` (SM 2.5), and every credited second is level C with `focus_unknown`.

| Minute | Credited | Reasons | Arithmetic |
|---|---|---|---|
| 14:00 | 60 | NAV 60 | The 14:00:30 heartbeat's 120 s lead covers 14:00:00 to 14:00:30, and the 14:01:50 heartbeat's covers 14:00:30 on. The prompt's lead covers them too, but the lead of the next anchor at or after a second wins (SM 3.3 rule 3). Both heartbeats are NAV anchors: input in their slots, and the editor-focus test passes because focus is unknown |
| 14:01 | 60 | NAV 50, P 10 | NAV to 14:01:50, then the prompt's lead: 120 + 300 / 1.5 = 320 s, from 13:58:40 |
| 14:02 | 60 | P 60 | The prompt's lead |
| 14:03 | 60 | P 60 | The prompt is sent at 14:04:00. SM 2.2 rule 6: input in the 10 s before it, and the focus test passes for every session because focus is unknown, so it is human_typed and its anchor level C with focus_unknown |
| 14:04 | 35 | SUP 35 | The bucket resets to 20 s, drained 14:04:00 to 14:04:20, and 45 words add 15 s at 14:04:30, drained to 14:04:45: 20 + 15. Nothing is left to read from 14:04:20 to 14:04:30 and from 14:04:45 to 14:05:00 while the turn Rowan started runs: X.agent_only 10 + 15 = 25. SM 5.2 skips X.other_focus and X.ambiguous where focus is unknown |
| 14:05 | 60 | PA 25, SUP 35 | 45 words add 15 s at 14:05:00, drained to 14:05:10. The permission's 15 s wait and 10 s trail, 14:05:10 to 14:05:35, beat the bucket (SM 3.3 rule 2): 15 + 10. The first edit adds 30 × 60 / 8 = 225 s at 14:05:30, and SUP runs from 14:05:35: 10 + 25 |
| 14:06 | 60 | SUP 30, READ 30 | The second edit adds 225 s at 14:06:00. SUP to the stop at 14:06:30, then READ. The 160-word summary adds 160 / 3 = 53 1/3 s |
| 14:07 | 60 | READ 60 | Reading the summary and both diffs |
| 14:08 | 60 | READ 60 | The bucket holds 303 1/3 s at 14:09:00, as SM 6.1 says |
| 14:09 | 60 | READ 60 | SM 6.1's LOOKUP. A slot of unknown focus is never a browser slot, so it holds no lookup (SM 2.5), and it passes the bucket's focus test. READ, within 600 s of the 14:04:00 prompt and the 14:05:25 answer |
| 14:10 | 60 | READ 30, NAV 10, G 20 | READ to 14:10:30, 90 s of the 303 1/3, which leaves 213 1/3. Then the lead of the 14:12:30 heartbeat, which the browser's slots kept out in SM 6.1, from 14:10:30 to 14:10:40. From 14:10:40 the commit's 60 s lead, because the next anchor's lead wins (SM 3.3 rule 3) |
| 14:11 | 60 | G 40, NAV 20 | The commit's lead to 14:11:40, then the 14:12:30 heartbeat's |

## Totals

| Total | Seconds | Arithmetic |
|---|---|---|
| `credited_s` | 695 | 60 + 60 + 60 + 60 + 35 + 60 + 60 + 60 + 60 + 60 + 60 + 60 |
| `NAV` | 140 | 60 + 50 + 10 + 20 |
| `P` | 130 | 10 + 60 + 60 |
| `SUP` | 100 | 35 + 35 + 30 |
| `PA` | 25 | 15 + 10 |
| `READ` | 240 | 30 + 60 + 60 + 60 + 30 |
| `G` | 60 | 20 + 40 |
| `X.agent_only` | 25 | 10 + 15 |
| `agent_running_s` | 150 | 60 + 60 + 30 |
| `agent_only_s` | 25 | 150 − (35 + 60 + 30) |
| `level_c_s` | 695 | 720 − 25 |
| `focus_unknown_s` | 695 | 695 |
| `level_b_s` | 0 | 0 |
| `ai_credited_s` | 495 | 130 + 25 + 100 + 240 |
| `hand_credited_s` | 200 | 140 + 60 |

Against SM 6.1's first cycle the total is the same, 695 s. SM 6.1 credits `LOOKUP` 120, `READ` 150, `NAV` 130 and `G` 40 in it. Here `LOOKUP` is gone, `READ` is 150 + 90 = 240, `NAV` 130 + 10 = 140 and `G` 40 + 20 = 60, so `ai_credited_s` rises from 405 to 495 and `hand_credited_s` falls from 290 to 200.

## Readings the case relies on

- **Every second is level C, whatever its anchor's level.** SM 2.5: "Every second credited in such a slot is evidence level C with the label `focus_unknown`". The prompt and the three heartbeats passed their focus tests only because focus was unknown, so `anchors.json` gives them level C and the label. The permission answer and the commit have no focus test: the answer needs presence and the commit input in its slot (SM 2.3). Their own level is B, from `input_any` presence and a `hook` or `git` record (DESIGN 6.4), and every second they credit is still C because of its slot.
- **No lookup, so no lookup horizon.** The browser run of SM 6.1 is no browser run here, since no slot's focus is a browser. E-lookup-lookahead (docs/errata/2026-11-20-lookup-lookahead.md), which counts only acts and prompts on record `READ_HORIZON_S` after a run starts, has nothing to decide.
- **The next anchor's lead.** From 14:10:40 two leads hold each second, the commit's and the 14:12:30 heartbeat's. The case reads SM 3.3 rule 3 as the 6.1 fixture does: the lead of the next anchor at or after the second wins. The next cycle's prompt at 14:16:00, whose 320 s lead would start at 14:10:40, isn't in the records, and would lose to both by the same rule.
- **The record of unknown focus.** It is dated at the SessionStart hook, in the same millisecond, because the focus component writes it when a project context goes live (internal/agent/focus). No act falls in that millisecond.
