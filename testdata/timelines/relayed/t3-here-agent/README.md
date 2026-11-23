# Relayed: an agent's message while Lena types in T3 Code

[t3-here](../t3-here/)'s MacBook, the next evening. Lena's MacBook runs T3 Code's desktop app, which runs its own server, which runs Claude Code on `weather-cli`. She sits down at 20:57:30 with T3's window in front, open on her `weather-cli` thread, and starts typing a long message into its composer. She is still typing it, a key a second, when the case ends at 21:10, and she sends nothing.

At 21:04:40 a message she didn't write reaches the thread's agent. An agent reviewing `weather-cli` in another T3 thread sends this thread 240 characters through T3's MCP tools. T3 stores the message with `createdBy` `agent`, so pairtime's T3 reader writes no `relay.message` for it, and T3 starts a run that hands it to Claude Code as a `prompt.submit`. Nothing witnesses that prompt, so SM 2.2 rule 4 takes it. The session's nearest non-shell ancestor is T3's server, `parent_class` `agent_app`, and rule 4 makes every unwitnessed prompt of such a session `headless`, whatever window hosts it. This one is `headless` though this computer saw at its `t` all that rule 6 asks for: Lena's key press 0.7 s before it, T3's window, which hosts the session, in front, and a plausible length. It opens nothing, its turn's 200 s are `X.automated`, and nothing reads the turn's output. The case credits 0 s. Without `agent_app` on rule 4's list it would credit 395 s at level A.

No SM section describes this case. Every number below comes from SM 2.2 rules 4, 6 and 9, 2.3, 2.4, 5.1 and 5.2 as [D-relayed](../../../../docs/errata/2026-11-22-relayed-prompts.md) writes them, and SM 8's parameters, worked by hand.

| | |
|---|---|
| Source | Invented for D-relayed. The MacBook's hook, presence and focus records, in the order the agent wrote them. The T3 reader writes none |
| Rules | SM 2.2 rule 4 with `agent_app` on its list, rule 9's witness, and rule 6 read at the prompt's `t` for what this computer saw; 2.4's bucket; 5.1's minute bucket; 5.2's codes; and SM 8's `KEYS_PRE_S`, `TYPING_MAX_CPS`, `R_TURN_BASE_S` and `READ_HORIZON_S`. The counterfactual adds 2.3's substantive prompt row and 2.4's presence rules |
| Date and zone | Tuesday 2026-11-24, America/Chicago, UTC−06:00. 21:00 local is 03:00Z on 2026-11-25 |
| Evidence | No second is credited, so none has a level. Witness `witnessed`. macOS reports key presses, and Lena's typing puts some in every slot |
| Window | 21:00:00 to 21:10:00. The session started at 20:56:10 with `entrypoint` `unknown`, `parent_class` `agent_app` and no TTY. T3's window has been in front since 20:57:30, hosting the session and resolving to `weather-cli`, and Lena types from 20:58:00.3, a key a second, so every slot is present and focused |
| Errata | [relayed prompts](../../../../docs/errata/2026-11-22-relayed-prompts.md), the decision |

## The records

| Time | Record | What it says |
|---|---|---|
| 20:56:10 | `session.start` | T3's server starts Claude Code for the thread: `entrypoint` `unknown`, `parent_class` `agent_app`, no TTY. Lena's last key press was at 20:48:58.1 |
| 20:57:30 | `os.focus` | T3 Code's window, `agent_app`, hosting the session, on `weather-cli`, after a click at 20:57:29.6. No other window comes to the front |
| 20:58:00.3 to 21:09:59.3 | key presses | Lena's draft in T3's composer, a key a second. She sends nothing |
| every 15 s | `os.presence` | `idle_keys_s` 0.7 s at every tick from 20:58:15 |
| 21:04:40 | `prompt.submit`, 240 characters | The agent's message, which its run hands to Claude Code. No relay record names it, and it names none |
| 21:05:10 to 21:08:00 | A1's output | 45 words, an 8-line edit at 21:06:30, 60 words at the stop |

The sending agent's thread runs on a provider pairtime has no hooks for, so the MacBook records nothing of it. A1, the turn the message starts, fills the bucket with 15 + 60 + 20 = 95 s and no base, since an automated turn doesn't reset it (SM 2.4).

What rule 6 would see at the prompt's `t`, were rule 4 to let the prompt through:

| Prompt | Key press before `t` | Window in front at `t` | Plausible length | Rule 6 at `t` |
|---|---|---|---|---|
| A1's, 21:04:40 | 21:04:39.3, 0.7 s before | T3's, hosting the session | 240 ≤ 15 × 285, with key presses in all 19 slots the 280 s lead would touch | would hold, with nothing pasted |

## Credit, minute by minute

Every slot has key presses, and T3's window, on `weather-cli`, is in front, so no presence or focus code applies. An uncredited second is `X.automated` while A1 runs, since its origin is `headless`, and `X.no_anchor` otherwise (SM 5.2). Nothing reads the bucket, so no second is `X.bucket_cap`.

| Minute | Credited | Reasons | Arithmetic |
|---|---|---|---|
| 21:00-21:03 | 0 | none | Lena types her draft with T3's window, hosting the session, in front. A key press is presence, not an act, and opens no window. No credit and no agent in any of the four minutes, so no bucket |
| 21:04 | 0 | none | Lena types with no window open until 21:04:40: `X.no_anchor` 40. Then the agent's message reaches the agent with no relay record, rule 4 makes it `headless`, and A1 runs, automated: `X.automated` 20 |
| 21:05 | 0 | none | A1 runs: `X.automated` 60. Its 45 words at 21:05:10 put 15 s in the bucket, and nothing reads them |
| 21:06 | 0 | none | A1 runs: `X.automated` 60. The 8-line edit at 21:06:30 adds 60 × 8 / 8 = 60 s |
| 21:07 | 0 | none | A1 runs until 21:08:00: `X.automated` 60 |
| 21:08 | 0 | none | A1 stops at 21:08:00 with 60 words, 20 s more, so 95 s sit in the bucket. No human anchor is within `READ_HORIZON_S`, and nothing reads them. A1 ran until 21:08:00, so no credit and no agent, and no bucket |
| 21:09 | 0 | none | Lena types on and sends nothing. No credit and no agent, so no bucket |

## Totals

| Total | Seconds | Arithmetic |
|---|---|---|
| `credited_s` | 0 | 0 |
| `level_c_s` | 0 | 0 |
| `via_relay_s` | 0 | 0 |
| `agent_running_s` | 200 | 20 + 3 × 60 |
| `X.automated` | 200 | 20 + 3 × 60 |
| `X.no_anchor` | 40 | 40 |
| `headless_prompts` | 1 | 1 |
| `relayed_prompts` | 0 | 0 |
| `typed_prompts` | 0 | 0 |

## Without `agent_app` on rule 4's list

This is the counterfactual the case exists to show. Strike `agent_app` from rule 4's list and keep every record. The session still has no TTY, but T3's window is an agent desktop app (SM 3.2 rule 3) and the session runs under it. So the session has a desktop-app host, and rule 4's last clause, for a session with neither a TTY nor such a host, doesn't take the prompt either. Rules 1 to 3 find nothing, since the hook declares no origin. Rule 6 then holds at 21:04:40 (above), so the prompt is `human_typed`, `substantive` at 240 characters, and level A, since macOS reports key presses (SM 5.1).

Its lead is SM 2.3's substantive prompt row, 120 + 240 / 1.5 = 280 s, ending at the prompt. The think part comes first and needs presence only. The 160 s typing part, from 21:02:00, needs key presses in each of its slots, and Lena's typing gives them. The reading follows SM 2.4's presence rules: A1's prompt resets the bucket to its 20 s base when A1 starts, and the bucket drains as `SUP` while A1 runs and as `READ` after its stop, within `READ_HORIZON_S` of the prompt. A1 is a turn a person started, so its other seconds are `X.agent_only`. gen.py checks these rows against sidedoor's `Sweep`.

| Minute | Without `agent_app` | Reasons | Arithmetic |
|---|---|---|---|
| 21:00 | 60 | `P` 60 | The lead, from 21:00:00 to the prompt at 21:04:40. Its think part comes first |
| 21:01 | 60 | `P` 60 | The think part, to 21:02:00 |
| 21:02 | 60 | `P` 60 | The typing part, with key presses in every slot |
| 21:03 | 60 | `P` 60 | The typing part |
| 21:04 | 60 | `P` 40, `SUP` 20 | The typing part ends at 21:04:40. A1's 20 s base drains from then to 21:05:00 |
| 21:05 | 15 | `SUP` 15 | `X.agent_only` 10 until the 45 words' 15 s arrive at 21:05:10 and drain to 21:05:25, then `X.agent_only` 35 |
| 21:06 | 30 | `SUP` 30 | `X.agent_only` 30 until the edit adds 60 s at 21:06:30, which drain from then |
| 21:07 | 30 | `SUP` 30 | The edit's last 30 s, then `X.agent_only` 30 until A1 stops |
| 21:08 | 20 | `READ` 20 | A1 stops at 21:08:00 with 60 words, 20 s, read by 21:08:20, 220 s after the prompt. `X.no_anchor` 40 |
| 21:09 | 0 | none | No credit and no agent |

| Without `agent_app` | Seconds | Arithmetic |
|---|---|---|
| `credited_s` | 395 | 5 × 60 + 15 + 30 + 30 + 20 |
| `P` | 280 | 120 + 160 |
| `SUP` | 95 | 20 + 15 + 30 + 30 |
| `READ` | 20 | 20 |
| `level_a_s` | 395 | 280 + 95 + 20 |
| `agent_running_s` | 200 | 20 + 3 × 60 |
| `X.agent_only` | 105 | 10 + 35 + 30 + 30 |
| `X.no_anchor` | 40 | 40 |
| `typed_prompts` | 1 | 1 |
| `headless_prompts` | 0 | 0 |

## Readings the case relies on

- **Rule 4 lists `agent_app`, whatever window hosts the session.** The session's nearest non-shell ancestor is T3's server, so rule 4 makes every prompt of the session `headless` unless an app message witnesses it (SM 2.2 rule 4 and its "Rule 4 and the app's server" bullet; the erratum's decision 1). The rules go in order and the first match wins (SM 2.2), so rule 6 is never asked.
- **An agent's message has no witness.** T3 stores what an agent sends through its MCP tools with `createdBy` `agent`. The T3 reader records only messages T3 stores with `createdBy` `user` and `creationSource` `web` or `mobile`, so no `relay.message` exists and the prompt carries no `relay` (SM 2.2, "The app's own declarations"; the erratum's decision 1, which names this case).
- **What this computer saw at `t` changes nothing.** At 21:04:40 rule 6 would see a key press 0.7 s before, T3's window hosting the session in front, and 240 characters, plausible over 285 s of input (SM 2.2 rule 6; SM 8.1's `KEYS_PRE_S` and `TYPING_MAX_CPS`). [t3-here](../t3-here/)'s message passed the same tests at its stamp, but there a relay record witnessed the prompt, so it skipped rule 4. Here nothing does, so the typing of Lena's unsent draft can't vouch for an agent's prompt (the erratum's red team, item 8).
- **Rule 4's own exception needs a shell.** A headless session's prompts count as human only with typing in the 10 s before `session.start` and an interactive shell for the parent (SM 2.2 rule 4). T3's server started this session at 20:56:10, 431.9 s after the last key press, and T3's server is no shell.
- **Typing opens nothing.** Lena's key presses make every slot present (SM 2.1), but only an act opens a window (SM 2.3), and her draft isn't sent before the case ends. So 21:00 to 21:03 and 21:08 to 21:09 have no credit and no agent, and no bucket (SM 5.1). The 40 s before the prompt in 21:04 are `X.no_anchor`.
- **The turn is automated.** A1's 200 s are `X.automated`, SM 5.2's code for a turn whose origin is `headless`, though Lena is present and focused on its project throughout.
- **The bucket fills and nothing reads it.** A1 adds 15 + 60 + 20 = 95 s with no base, since an automated turn doesn't reset the bucket. Draining needs a human anchor in the project within `READ_HORIZON_S` (SM 2.4), and the window has none. So nothing drains, and with no human act no second is `X.bucket_cap`.
- **Codes where the person is present and focused.** An uncredited second is `X.automated` while A1 runs and `X.no_anchor` otherwise, the codes SM 5.2's table gives a present, focused second, as in [t3-phone](../t3-phone/). With no second capped and no turn a person started, that is also the order the erratum's reading 3 gives a slot whose presence is unknown, so the codes don't depend on which of the two applies.
- **The counterfactual, in numbers.** Without `agent_app` on rule 4's list, and with T3's window, which the session runs under (SM 3.2 rule 3), as the desktop-app host rule 4's last clause asks about, rule 6 holds at 21:04:40. The prompt is `human_typed` and `substantive`, at level A (SM 2.2, 5.1). Its lead is 120 + 240 / 1.5 = 280 s from 21:00:00, with key presses in every slot of its 160 s typing part (SM 2.3). Its turn reads under SM 2.4's presence rules, the 20 s base and A1's 95 s of output: `SUP` 20 + 15 + 30 + 30 = 95 while A1 runs, `READ` 20 after the stop. That is 280 + 95 + 20 = 395 s at level A where the case credits 0, and A1's 200 s would be `SUP` 95 and `X.agent_only` 105 in place of `X.automated` 200.
- **The draft stays unsent.** The rows are what the records give with nothing after 21:10:00. A message Lena sent then would be t3-here's case, and its lead and the reading before it could still reach these minutes, which stay pending for `PENDING_WINDOW_S` after they end (SM 2.3, 2.4; DESIGN 6.2 D1).
