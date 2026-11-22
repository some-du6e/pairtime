# Relayed: a chat bot and `pairtime relay`

Lena runs a small chat bot on her server. When she messages it from her phone, it calls `pairtime relay` with her message, its personal information removed, on stdin. Then it hands the whole message to an agent. Usually that goes through T3 Code's MCP tools, into a thread whose Claude Code session T3's server runs. T3 stores that copy with `createdBy` `agent`, so T3's own reader writes nothing for it, and the bot's call is the only witness (SM 2.2 rule 9).

Four messages:

- **B1 and B2** pair with the prompts T3 delivered, so they are relayed. The bot removed an address from B1 before relaying it, so its call's 120 characters are fewer than the prompt's 126. The relayed lead counts the call's own text, which is never longer than the hook's.
- **B3** pairs with nothing: the bot relays it but hands it to no agent pairtime sees. It opens nothing and raises `relay_without_prompt`.
- **B4** is a quick question the bot answers by running `claude -p` itself. The call pairs with that prompt. But the session's nearest non-shell ancestor is the bot's own Node process, a script runtime, not an agent app's server, so rule 4 still takes it, and it earns nothing.

The case credits 210 s, all level C with `via_relay` and `via_plugin`.

No SM section describes this case. Every number below comes from SM 2.2 rule 9, 2.3, 2.4 and 5.2 as [D-relayed](../../../../docs/errata/2026-11-22-relayed-prompts.md) writes them, and SM 8's parameters, worked by hand.

| | |
|---|---|
| Source | Invented for D-relayed. The server's hook records and the agent's records of the bot's `pairtime relay` calls, in the order the agent wrote them |
| Rules | SM 2.2 rule 9 and its `pairtime relay` bullet, 2.3's relayed row, 2.4's relayed reading, 5.2's codes for a slot whose presence is unknown, and SM 8's `RELAY_MATCH_S`, `COMPOSE_CPS` and `UNKNOWN_BUCKET_MAX_S` |
| Date and zone | Wednesday 2026-11-25, America/Chicago, UTC−06:00 |
| Evidence | Level C, labels `via_relay` and `via_plugin`, witness `witnessed`. No `os.presence` or `os.focus` record exists, so every slot's presence is `unknown` |
| Window | 20:58:00 to 21:18:00. T3's session for the bot's thread started at 20:50:05 with `parent_class` `agent_app` and no TTY. The bot's own `claude -p` starts at 21:15:02 with `entrypoint` `sdk` and `parent_class` `script_runtime` |
| Errata | [relayed prompts](../../../../docs/errata/2026-11-22-relayed-prompts.md), the decision |

## The records

| Message | Sent | Call arrives | Relayed characters | Prompt | Prompt's characters | Session | Pairs |
|---|---|---|---|---|---|---|---|
| B1 | 21:00:00 | 21:00:01 | 120 | 21:00:03, names the call | 126 | T3's, `agent_app` | yes: relayed |
| B2 | 21:05:00 | 21:05:01 | 60 | 21:05:03, names the call | 60 | T3's, `agent_app` | yes: relayed |
| B3 | 21:10:00 | 21:10:01 | 90 | none | | | no |
| B4 | 21:15:00 | 21:15:01 | 45 | 21:15:03, names the call | 45 | the bot's `claude -p`, `script_runtime` | yes, but rule 4 takes it |

Every call's send time is 1 s before it arrives, inside the 5 s the plugin rule allows (SM 2.2). T1, B1's turn, fills its bucket with 20 + 30 + 40 = 90 s. T2 fills 20 + 25 = 45 s, but no relayed message follows its stop at 21:06:30, so nothing reads it.

## Credit, minute by minute

Every slot's presence is unknown, so an uncredited second is `X.agent_only` while a person's turn runs, `X.automated` while only automated turns do, and `X.no_anchor` otherwise (SM 5.2).

| Minute | Credited | Reasons | Arithmetic |
|---|---|---|---|
| 20:58 | 20 | `P` 20 | B1's lead, 120 / 1.5 = 80 s, ending at 21:00:00, so from 20:58:40. `X.no_anchor` 40 before it |
| 20:59 | 60 | `P` 60 | The lead |
| 21:00 | 0 | none | 21:00:00 to 21:00:02 are in neither the lead nor a turn: `X.no_anchor` 3. T1 runs from 21:00:03: `X.agent_only` 57 |
| 21:01 | 0 | none | T1 runs: `X.agent_only` 60 |
| 21:02 | 60 | `READ` 60 | T1 stops at 21:02:00, and B2 follows 180 s later, so the session reads from the stop |
| 21:03 | 30 | `READ` 30 | The 90 s end at 21:03:30: 60 + 30. `X.no_anchor` 30 |
| 21:04 | 40 | `P` 40 | `X.no_anchor` 20, then B2's lead, 60 / 1.5 = 40 s, from 21:04:20. B1 came 300 s before it, so the cap doesn't bind |
| 21:05 | 0 | none | `X.no_anchor` 3 to the prompt at 21:05:03, then T2: `X.agent_only` 57 |
| 21:06 | 0 | none | T2 runs until 21:06:30: `X.agent_only` 30. No relayed message follows the stop: `X.no_anchor` 30 |
| 21:07-21:14 | 0 | none | No credit and no agent. B3 pairs with nothing, so its 60 s of typing, 21:09:00 to 21:10:00, earn nothing |
| 21:15 | 0 | none | `X.no_anchor` 3, then the bot's `claude -p` turn, headless by rule 4: `X.automated` 57 |
| 21:16-21:17 | 0 | none | The `claude -p` turn stopped at 21:16:00. No credit and no agent |

## Totals

| Total | Seconds | Arithmetic |
|---|---|---|
| `credited_s` | 210 | 20 + 60 + 60 + 30 + 40 |
| `P` | 120 | 20 + 60 + 40 |
| `READ` | 90 | 60 + 30 |
| `level_c_s` | 210 | 120 + 90 |
| `via_relay_s` | 210 | 120 + 90 |
| `agent_running_s` | 261 | 57 + 60 + 57 + 30 + 57 |
| `X.agent_only` | 204 | 57 + 60 + 57 + 30 |
| `X.automated` | 57 | 57 |
| `X.no_anchor` | 129 | 40 + 3 + 30 + 20 + 3 + 30 + 3 |
| `relayed_prompts` | 2 | 1 + 1 |
| `headless_prompts` | 1 | 1 |
| `relay_without_prompt` | 1 | 1 |

## Readings the case relies on

- **A relay call never opens an anchor of its own.** It can only witness a prompt (SM 2.2), so B3, which pairs with nothing, earns nothing, as a stray `track` call earns nothing.
- **Pairing a call.** A call names no session. It pairs with the first prompt delivered in a session of its project at or after its send time, within `RELAY_MATCH_S`, whose text, redacted the same way, is a near-duplicate of the call's. The case leaves the texts out, as every fixture does, and states each pairing by the prompt's `relay`.
- **The typed length is the call's**, 120 for B1, not the prompt's 126 (SM 2.2).
- **The ancestor decides rule 4.** B1's and B2's sessions run under T3's server, `agent_app`, so their witnessed prompts skip rule 4. B4's runs under the bot itself, `script_runtime`, so rule 4 still takes it: a script that calls `pairtime relay` and runs `claude -p` gets nothing for it.
- **Two labels.** A relayed anchor that only a bot's call vouches for carries `via_relay` for its origin and `via_plugin` for the path the call took (DESIGN 6.4, as the scorer PR writes it).
