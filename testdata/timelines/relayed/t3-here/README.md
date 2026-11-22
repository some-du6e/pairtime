# Relayed: the same message typed into T3 Code on this computer

[t3-away](../t3-away/)'s first message, typed on the computer that runs the agent instead of on another one. Lena's MacBook runs T3 Code's desktop app, which runs its own server, which runs Claude Code on `weather-cli`. She types the same 90-character message into T3's window and sends it at 20:01:00. pairtime runs on the MacBook too, so this time the OS saw her typing.

T3's store records the message exactly as on the server, so the T3 reader writes the same kind of `relay.message`, and the prompt is witnessed. That makes SM 2.2 skip rule 4, which would otherwise take the prompt: the session has no TTY, and its nearest non-shell ancestor is T3's server. Rules 6 and 7, tested at the app's stamp, then hold. There were key presses in the 10 s before 20:01:00, T3's window was in front, and it hosts every session its own server drives (SM 3.2 rule 3). So the prompt is `human_typed` at its usual level, A on macOS, with its full lead ending at the stamp and its reading under SM 2.4's own rules. The case credits 495 s, against the 180 s that t3-away credits for the same message typed elsewhere.

No SM section describes this case. Every number below comes from SM 2.2 rules 6 and 9, 2.3 and 2.4 as [D-relayed](../../../../docs/errata/2026-11-22-relayed-prompts.md) writes them, and SM 8's parameters, worked by hand.

| | |
|---|---|
| Source | Invented for D-relayed: t3-away's M1 and T1, on a MacBook running T3 Code's desktop app |
| Rules | SM 2.2 rules 4, 6 and 9 (rules 6 and 7 tested at the app's stamp), 2.3's substantive prompt row, 2.4's reading bucket, and SM 8's parameters |
| Date and zone | Monday 2026-11-23, America/Chicago, UTC−06:00 |
| Evidence | Level A: macOS reports key presses, and there are some in the anchor's evidence window. Witness `witnessed` |
| Window | 19:58:00 to 20:07:00. The session started at 19:56:10 with `parent_class` `agent_app` and no TTY. T3's window has been in front since 19:57:30, with pointer input every 12 s, so every slot is present and focused on `weather-cli` |
| Errata | [relayed prompts](../../../../docs/errata/2026-11-22-relayed-prompts.md), the decision |

## The records

| Time | Record | What it says |
|---|---|---|
| 19:57:30 | `os.focus` | T3 Code's window, `agent_app`, hosting the session, on `weather-cli` |
| 20:00:00.3 to 20:00:59.8 | key presses | 60 keys a second apart, then Return |
| 20:01:01 | `prompt.submit`, 90 characters | Claude Code's hook, 1 s after T3's stamp |
| 20:01:03 | `relay.message`, sent at 20:01:00, 90 characters | the T3 reader's witness, naming the prompt |
| 20:01:20 to 20:03:30 | T1's output | 60 words, a 30-line edit at 20:02:00, 150 words at the stop |

## Credit, minute by minute

The lead is the usual substantive prompt's: 120 s to think and 90 / 1.5 = 60 s to type, 180 s ending at the stamp, 20:01:00, so it starts at 19:58:00. Its typing part, the last 60 s, has key presses in every slot. The turn's 20 s base goes into the bucket when T1 starts at 20:01:01, and SM 2.4's rules drain it while Lena is present and focused: `SUP` while T1 runs, `READ` after.

| Minute | Credited | Reasons | Arithmetic |
|---|---|---|---|
| 19:58 | 60 | `P` 60 | The think part of the lead, from 19:58:00 |
| 19:59 | 60 | `P` 60 | The think part |
| 20:00 | 60 | `P` 60 | The typing part, 20:00:00 to 20:01:00, with key presses in every slot |
| 20:01 | 40 | `SUP` 40 | 20:01:00 is after the lead and before T1: `X.no_anchor` 1. From 20:01:01 the 20 s base and the 60 words' 20 s are read by 20:01:41: 19 + 21 = 40. Nothing new until 20:02:00: `X.agent_only` 19 |
| 20:02 | 60 | `SUP` 60 | The edit adds 60 × 30 / 8 = 225 s at 20:02:00. 225 − 60 = 165 left |
| 20:03 | 60 | `SUP` 30, `READ` 30 | `SUP` until the stop at 20:03:30, 165 − 30 = 135 left. The 150 words add 50: 185. Then `READ` 30, 155 left |
| 20:04 | 60 | `READ` 60 | 95 left |
| 20:05 | 60 | `READ` 60 | 35 left |
| 20:06 | 35 | `READ` 35 | The bucket empties at 20:06:35. `X.no_anchor` 25 |

## Totals

| Total | Seconds | Arithmetic |
|---|---|---|
| `credited_s` | 495 | 3 × 60 + 40 + 60 + 60 + 60 + 60 + 35 |
| `P` | 180 | 120 + 60 |
| `SUP` | 130 | 40 + 60 + 30 |
| `READ` | 185 | 30 + 60 + 60 + 35 |
| `level_a_s` | 495 | 180 + 130 + 185 |
| `agent_running_s` | 149 | 59 + 60 + 30 |
| `X.no_anchor` | 26 | 1 + 25 |
| `X.agent_only` | 19 | 19 |
| `typed_prompts` | 1 | 1 |
| `relayed_prompts` | 0 | 0 |

## Readings the case relies on

- **Rules 6 and 7 read the app's stamp.** The hook fired at 20:01:01, but the input test, the focus test and the plausibility test all look at 20:01:00, when T3 took the message, and the lead ends there (SM 2.2).
- **T3's window hosts the session.** Its desktop app starts the server that starts Claude Code, so the session runs under the window's process. pairtime's app table classes the window as `agent_app` (DESIGN 5.1), which is what makes `hosted_sessions` list it.
- **The witness skips rule 4 and nothing else.** With no relay record, rule 4 would make the prompt `headless`, and nothing would count. The relay record doesn't make it `relayed`, because rule 6 comes first.
- **The same message elsewhere.** On the server, with nothing to see Lena type, this message earns 180 s at level C in [t3-away](../t3-away/): 60 s of typing and 120 s of reading. Here it earns 495 s at level A, with the think part, the reading while the agent works, and reading past the 120 s cap.
