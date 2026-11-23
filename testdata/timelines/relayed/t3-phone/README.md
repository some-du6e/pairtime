# Relayed: T3 Code messages sent from its phone app

Mateo, 15, runs T3 Code's desktop app on his Windows PC at home, where Claude Code works on his `tide-tables` project. The PC has no key counter, so pairtime there sees input without knowing whether it came from the keyboard or the mouse. A USB mouse jiggler stays plugged in to keep the PC awake while agents run. This Saturday he leaves T3's window in front, goes out, and drives the thread from the bus with T3's phone app.

T3's desktop app runs its own server, which runs Claude Code, so T3's window hosts the session (SM 3.2 rule 3). At each message's stamp the PC sees what it would see if Mateo had typed the message into that window: the jiggler's input 3.1 s earlier, and T3's window, hosting the session, in front. SM 2.2 rules 6 and 7, tested at the stamp, would make both prompts `human_typed`, at level B on a PC without the key counter. T3 stores the messages with `creationSource` `mobile`, though, so the reader's records say `client` `mobile`, and a message from T3's phone app skips rules 6 and 7. Both prompts are `relayed`. The case credits 197 s, all level C with `via_relay`. With `client` `web` in the same records it would credit 487 s at level B.

No SM section describes this case. Every number below comes from SM 2.2 rules 4, 6, 7 and 9, 2.3, 2.4 and 5.2 as [D-relayed](../../../../docs/errata/2026-11-22-relayed-prompts.md) writes them, and SM 8's parameters, worked by hand.

| | |
|---|---|
| Source | Invented for D-relayed. The PC's hook, presence and focus records and the T3 reader's records, in the order the agent wrote them |
| Rules | SM 2.2 rules 4, 6, 7 and 9, with rules 6 and 7 tested at the app's stamp and skipped for a phone message; 2.3's relayed row; 2.4's relayed reading; 5.2's codes; and SM 8's `KEYS_PRE_S`, `TYPING_MAX_CPS`, `RELAY_MATCH_S`, `RELAY_SEEN_LAG_S`, `COMPOSE_CPS` and `UNKNOWN_BUCKET_MAX_S`. The counterfactual adds 2.3's substantive prompt row and 2.4's presence rules |
| Date and zone | Saturday 2026-11-28, Europe/Madrid, UTC+01:00. 13:00 local is 12:00Z |
| Evidence | Level C, label `via_relay`, witness `witnessed`. Every slot is present by `input_any`, and `idle_keys_s` is null in every sample. Relayed credit reads neither presence nor focus (SM 2.3) |
| Window | 13:00:00 to 13:15:00. The session started at 12:56:10 with `entrypoint` `unknown`, `parent_class` `agent_app` and no TTY, under T3's window, which is in front from then to the end. The jiggler moves the pointer every 12 s, at :08.9, :20.9, :32.9, :44.9 and :56.9 of every minute, so every slot has input |
| Errata | [relayed prompts](../../../../docs/errata/2026-11-22-relayed-prompts.md), the decision |

## The records

| Time | Record | What it says |
|---|---|---|
| 12:56:10 | `session.start` | T3's server starts Claude Code for the thread: `entrypoint` `unknown`, `parent_class` `agent_app`, no TTY |
| 12:56:10 | `os.focus` | T3 Code's window, `agent_app`, hosting the session, on `tide-tables`. No other window comes to the front |
| every 15 s | `os.presence` | The jiggler's last move in `idle_any_s`, and `idle_keys_s` null |
| 13:03:01 | `prompt.submit`, 90 characters | M1's run, 1 s after T3's stamp |
| 13:03:03 | `relay.message`, `client` `mobile`, sent at 13:03:00, 90 characters | The T3 reader's witness, naming the prompt |
| 13:03:30 to 13:06:00 | T1's output | 45 words, an 8-line edit at 13:04:30, 30 words at the stop |
| 13:10:01 | `prompt.submit`, 48 characters | M2's run, 1 s after its stamp |
| 13:10:03 | `relay.message`, `client` `mobile`, sent at 13:10:00, 48 characters | The witness, naming the prompt |
| 13:10:40 to 13:12:00 | T2's output | 30 words, then 60 words at the stop |

Each relay record comes 3 s after its stamp, inside `RELAY_SEEN_LAG_S`, and each prompt 1 s after it, inside `RELAY_MATCH_S`. T1 fills its bucket with 20 + 15 + 60 + 10 = 105 s, and T2 with 20 + 10 + 20 = 50 s, both under the 120 s cap.

What rules 6 and 7 would see at each stamp if the records didn't say `mobile`:

| Message | Input before the stamp | Window in front at the stamp | Plausible length | Rule 6 at the stamp |
|---|---|---|---|---|
| M1, 13:03:00 | 13:02:56.9, 3.1 s before | T3's, hosting the session | 90 ≤ 15 × 180, with input in every slot of the 180 s lead | holds |
| M2, 13:10:00 | 13:09:56.9, 3.1 s before | T3's, hosting the session | 48 ≤ 15 × 165, with input in the 11 slots the 152 s lead touches | holds |

| Stop | Next relayed message | Sent after the stop | Pairs after the stop | Reads |
|---|---|---|---|---|
| T1, 13:06:00 | M2, sent 13:10:00, paired 13:10:03 | 240 s, inside 600 s | 243 s, inside 840 s | 105 s, from the stop |
| T2, 13:12:00 | none in the window | | | nothing |

## Credit, minute by minute

Every slot has input, and T3's window, on `tide-tables`, is in front, so no presence or focus code applies. An uncredited second is `X.agent_only` while a person's turn runs and `X.no_anchor` otherwise (SM 5.2). T1's 105 s stay under the 120 s cap, so no second is `X.bucket_cap`.

| Minute | Credited | Reasons | Arithmetic |
|---|---|---|---|
| 13:00 | 0 | none | The jiggler's input and T3's window in front open nothing. Mouse-only presence never opens a window (SM 2.1), and M1's relayed lead starts at 13:02:00. No credit and no agent, so no bucket |
| 13:01 | 0 | none | The same, no bucket |
| 13:02 | 60 | `P` 60 | M1's lead, 90 / 1.5 = 60 s, from 13:02:00 to its stamp at 13:03:00. It is the thread's first message, so no earlier one caps the lead |
| 13:03 | 0 | none | 13:03:00 is in neither the lead nor a turn: `X.no_anchor` 1. T3 hands M1 to the agent at 13:03:01, and T1 runs from then. A relayed session doesn't read while its agent runs: `X.agent_only` 59 |
| 13:04 | 0 | none | T1 runs: `X.agent_only` 60 |
| 13:05 | 0 | none | T1 runs: `X.agent_only` 60 |
| 13:06 | 60 | `READ` 60 | T1 stops at 13:06:00. M2 is sent 240 s later and pairs 243 s later, so the session reads from the stop. 105 − 60 = 45 left |
| 13:07 | 45 | `READ` 45 | The bucket empties at 13:07:45. `X.no_anchor` 15 |
| 13:08 | 0 | none | No credit and no agent, so no bucket |
| 13:09 | 32 | `P` 32 | `X.no_anchor` 28, then M2's lead, 48 / 1.5 = 32 s, from 13:09:28. M1 was sent 420 s before M2, so the cap at the seconds since the thread's previous message doesn't bind |
| 13:10 | 0 | none | 13:10:00 is in neither the lead nor a turn: `X.no_anchor` 1. T2 runs from 13:10:01: `X.agent_only` 59 |
| 13:11 | 0 | none | T2 runs: `X.agent_only` 60 |
| 13:12 | 0 | none | T2 stops at 13:12:00, and no relayed message follows, so nothing reads its 50 s. No credit and no agent, so no bucket |
| 13:13-13:14 | 0 | none | No credit and no agent |

## Totals

| Total | Seconds | Arithmetic |
|---|---|---|
| `credited_s` | 197 | 60 + 60 + 45 + 32 |
| `P` | 92 | 60 + 32 |
| `READ` | 105 | 60 + 45 |
| `level_c_s` | 197 | 92 + 105 |
| `via_relay_s` | 197 | 92 + 105 |
| `agent_running_s` | 298 | 59 + 60 + 60 + 59 + 60 |
| `X.agent_only` | 298 | 59 + 60 + 60 + 59 + 60 |
| `X.no_anchor` | 45 | 1 + 15 + 28 + 1 |
| `relayed_prompts` | 2 | 1 + 1 |
| `typed_prompts` | 0 | 0 |

## With client web

This is the counterfactual the case exists to show. Give both relay records `client` `web`, as T3 would store a message typed into its window on this PC, and keep every other record. Rules 6 and 7 then read each stamp, and rule 6 holds there (above), so both prompts are `human_typed`, at level B, since the PC has no key counter (SM 5.1). Each lead is SM 2.3's substantive prompt row, 120 s to think plus the typing time, ending at the stamp. Without `input_keys` the whole lead needs presence only, which the jiggler gives. The reading follows SM 2.4's presence rules. Each prompt resets the bucket to its 20 s base when its turn starts, and the bucket drains as `SUP` while the turn runs and as `READ` after the stop, up to 600 s, within `READ_HORIZON_S` of a human anchor. The uncredited codes are the same as above. gen.py checks these rows against sidedoor's `Sweep`.

| Minute | With client web | Reasons | Arithmetic |
|---|---|---|---|
| 13:00 | 60 | `P` 60 | M1's lead, 120 + 90 / 1.5 = 180 s, from 13:00:00 to the stamp at 13:03:00. Its think part comes first |
| 13:01 | 60 | `P` 60 | The think part, to 13:02:00 |
| 13:02 | 60 | `P` 60 | The typing part, 60 s, with presence in every slot |
| 13:03 | 35 | `SUP` 35 | `X.no_anchor` 1 at 13:03:00. T1's 20 s base drains from 13:03:01 and the 45 words' 15 s from 13:03:30, 20 + 15. Nothing is left to read in between and after: `X.agent_only` 9 + 15 |
| 13:04 | 30 | `SUP` 30 | `X.agent_only` 30 until the edit's 8 lines add 60 × 8 / 8 = 60 s at 13:04:30, which drain from then |
| 13:05 | 30 | `SUP` 30 | The edit's last 30 s, then `X.agent_only` 30 |
| 13:06 | 10 | `READ` 10 | T1 stops at 13:06:00 with 30 words, 10 s, read by 13:06:10. `X.no_anchor` 50 |
| 13:07 | 32 | `P` 32 | `X.no_anchor` 28, then M2's lead, 120 + 48 / 1.5 = 152 s, from 13:07:28 |
| 13:08 | 60 | `P` 60 | M2's think part |
| 13:09 | 60 | `P` 60 | The think part to 13:09:28, then the typing part |
| 13:10 | 30 | `SUP` 30 | `X.no_anchor` 1. T2's base drains from 13:10:01 and the 30 words' 10 s from 13:10:40, 20 + 10. `X.agent_only` 19 + 10 |
| 13:11 | 0 | none | Nothing is left to read while T2 runs: `X.agent_only` 60 |
| 13:12 | 20 | `READ` 20 | T2 stops at 13:12:00 with 60 words, 20 s, read by 13:12:20, 140 s after M2's stamp. `X.no_anchor` 40 |
| 13:13-13:14 | 0 | none | No credit and no agent |

| With client web | Seconds | Arithmetic |
|---|---|---|
| `credited_s` | 487 | 3 × 60 + 35 + 30 + 30 + 10 + 32 + 60 + 60 + 30 + 20 |
| `P` | 332 | 180 + 152 |
| `SUP` | 125 | 35 + 30 + 30 + 30 |
| `READ` | 30 | 10 + 20 |
| `level_b_s` | 487 | 332 + 125 + 30 |
| `via_relay_s` | 0 | 0 |
| `agent_running_s` | 298 | 59 + 60 + 60 + 59 + 60 |
| `X.agent_only` | 173 | 24 + 30 + 30 + 29 + 60 |
| `X.no_anchor` | 120 | 1 + 50 + 28 + 1 + 40 |
| `typed_prompts` | 2 | 1 + 1 |
| `relayed_prompts` | 0 | 0 |

## Readings the case relies on

- **Only `client` decides.** Both stamps pass rule 6's three tests: input in the `KEYS_PRE_S` before `sent_at`, focus at `sent_at` on a window that hosts the session, and a plausible length. Nothing was pasted, so it would be rule 6, not rule 7. A record that says `client` `mobile` skips rules 6 and 7 whatever this device's input and focus say, so both prompts are `relayed` (SM 2.2, "Rules 6 and 7, at the stamp"; the erratum's decision 1 and its answer to question 1). gen.py checks what rule 6 would see at each stamp before it scores the case.
- **A jiggler can't pass a phone message off as typed.** On a PC with `input_any` only, rule 6 can't tell the jiggler from Mateo. T3's word on where the message was typed is what keeps it at level C here (the erratum's red team, item 8).
- **Relayed credit ignores the PC's evidence.** The leads and the reading land in present slots with T3's window in front, and they still count at level C with `via_relay`, not level B (SM 2.3, 5.1).
- **The jiggler and the window open nothing.** Mouse-only presence never opens a window (SM 2.1), and a relayed anchor is no human anchor for this device's own reading rules (SM 2.3, 2.4). So 13:00, 13:01 and 13:08 have no bucket, and nothing drains as `SUP` while T1 or T2 runs.
- **Rule 4 is skipped only because the records witness the prompts.** Rule 4 lists `parent_class` `agent_app`, so without the relay records both prompts would be `headless`, whatever window hosts the session, and the case would credit 0 s (SM 2.2 rule 4; the erratum's decision 1).
- **The leads end at the stamps.** 13:03:00 and 13:10:00 lie between each lead and its turn, in no window, so each is `X.no_anchor` (SM 2.3).
- **M2 vouches for T1's reading.** It is sent 240 s after T1's stop, inside 600 s, and pairs at 13:10:03, 243 s after it, inside `PENDING_WINDOW_S` − 60 s = 840 s. The session reads T1's 105 s from the stop, and the reading would have ended at M2's stamp (SM 2.4; the erratum's decisions 2 and 3).
- **Nothing reads T2.** No relayed message follows T2's stop, so its 50 s go unread (SM 2.4).
- **Codes where the person is present and focused.** An uncredited second is `X.agent_only` while T1 or T2 runs and `X.no_anchor` otherwise, the codes SM 5.2's table gives a present, focused second, as in [t3-here](../t3-here/). With no second capped, that is also the order the erratum's reading 3 gives a slot whose presence is unknown, so the codes don't depend on which of the two applies.
- **The counterfactual, in numbers.** With `client` `web` the case would credit 487 s at level B: `P` 180 + 152 = 332, `SUP` 125 and `READ` 30. As relayed it credits 197 s at level C: `P` 60 + 32 = 92 and `READ` 105. Of the 290 s between them, 2 × 120 = 240 s are the two think parts, which a relayed lead never has (SM 2.3). The other 50 s are T2's output, which 2.4's presence rules read after the stop because M2's anchor is within `READ_HORIZON_S`, while relayed reading needs a later relayed message to vouch for it. T1's 105 s are read either way, as `SUP` 95 and `READ` 10 with `web` and as `READ` 105 relayed.
