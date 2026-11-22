# Relayed: T3 Code messages typed on another computer

Lena, 16, runs T3 Code on a small Linux server at home, where Claude Code works on her `weather-cli` project, and drives it from T3's web client on her laptop. pairtime runs on the server. The server has no desktop, so pairtime samples no presence there, and Claude Code, which T3's server starts through the Agent SDK, has no TTY. Before D-relayed, SM 2.2 rule 4 took every prompt of the session and none of this half hour counted.

She sends three messages from the laptop. T3's store records each as a user-role message with `createdBy` `user`, and pairtime's T3 reader writes a `relay.message` for it, which names the prompt it matched (SM 2.2 rule 9). In between, an agent working in another thread sends this thread a message through T3's MCP tools. T3 records that one with `createdBy` `agent`, so the reader writes nothing for it, and rule 4 takes it as before. The case credits 212 s, all level C with `via_relay`.

No SM section describes this case. Every number below comes from SM 2.2 rule 9, 2.3, 2.4 and 2.8 as [D-relayed](../../../../docs/errata/2026-11-22-relayed-prompts.md) writes them, and SM 8's parameters, worked by hand.

| | |
|---|---|
| Source | Invented for D-relayed. The server's hook records and the T3 reader's records, in the order the agent wrote them |
| Rules | SM 2.2 rule 9, 2.3's relayed row, 2.4's relayed reading, 2.8's shapes, and SM 8's `RELAY_MATCH_S`, `RELAY_MIN_GAP_S`, `COMPOSE_CPS` and `UNKNOWN_BUCKET_MAX_S` |
| Date and zone | Monday 2026-11-23, America/Chicago, UTC−06:00. 20:00 local is 02:00Z on 2026-11-24 |
| Evidence | Level C, label `via_relay`, witness `witnessed`. No `os.presence` or `os.focus` record exists, so every slot's presence is `unknown` |
| Window | 20:00:00 to 20:30:00. The session started at 19:56:10 with `entrypoint` and `parent_class` `unknown` and no TTY |
| Errata | [relayed prompts](../../../../docs/errata/2026-11-22-relayed-prompts.md), the decision; [lookback](../../../../docs/errata/2026-11-19-sm-2.8-lookback.md) point 2, for the codes of seconds the 120 s cap cuts and of seconds nothing covers; [6.6 codes](../../../../docs/errata/2026-11-19-sm-6.6-uncredited-codes.md) point 1, for the seconds only the agent's turn runs in |

## The records

| Message | Sent (`sent_at`) | Typed | Prompt reaches the agent | Relay record | Turn | Output |
|---|---|---|---|---|---|---|
| M1 | 20:01:00 | 90 | 20:01:01 | 20:01:03, names the prompt | T1, to 20:03:30 | 60 words, a 30-line edit, 150 words |
| M2 | 20:07:00 | 45 | 20:07:01 | 20:07:03, names the prompt | T2, to 20:09:00 | 45 words, 30 words |
| the agent's | none | 240 | 20:12:00 | none: `createdBy` `agent` | T3, to 20:15:00 | 90 words, an 8-line edit, 60 words |
| M3, `yes` | 20:26:00 | 3 | 20:26:01 | 20:26:03, names the prompt | T4, to 20:27:30 | 45 words |

Each relayed message's lead is its typing time, characters / 1.5, ending at its stamp: 60 s, 30 s and 2 s. M3 is a go-ahead, whose relayed lead has no 20 s think part. T1 fills 20 + 20 + 225 + 50 = 315 s, which the 120 s cap holds to 120. T2 fills 20 + 15 + 10 = 45 s. The agent's turn adds 30 + 60 + 20 = 110 s to T2's 45 without a reset, since an automated turn doesn't reset the bucket, and the cap holds it at 120.

| Stop | Next relayed message | Gap | Reads |
|---|---|---|---|
| T1, 20:03:30 | M2, 20:07:00 | 210 s | 120 s, from the stop |
| T2, 20:09:00 | M3, 20:26:00 | 1,020 s | nothing: more than 600 s |
| T3, 20:15:00 | M3, 20:26:00 | 660 s | nothing: more than 600 s |
| T4, 20:27:30 | none in the window | | nothing |

## Credit, minute by minute

| Minute | Credited | Reasons | Arithmetic |
|---|---|---|---|
| 20:00 | 60 | `P` 60 | M1's lead, 90 / 1.5 = 60 s, from 20:00:00 to its stamp at 20:01:00. It is the thread's first message, so no earlier one caps the lead |
| 20:01 | 0 | none | T3 hands M1 to the agent at 20:01:01, so 20:01:00 is in neither the lead nor a turn. T1 runs from 20:01:01: `X.agent_only` 59 |
| 20:02 | 0 | none | T1 runs, and a relayed session reads only after a stop: `X.agent_only` 60 |
| 20:03 | 30 | `READ` 30 | `X.agent_only` 30 until T1 stops at 20:03:30. M2 comes 210 s after the stop, so the session reads from it |
| 20:04 | 60 | `READ` 60 | The same reading |
| 20:05 | 30 | `READ` 30 | The 120 s end at 20:05:30: 30 + 60 + 30. Then 30 s that only the cap kept from being read |
| 20:06 | 30 | `P` 30 | 30 s cut by the cap, then M2's lead, 45 / 1.5 = 30 s, from 20:06:30. M1 came 360 s earlier, so the cap at the seconds since the thread's previous message doesn't bind |
| 20:07 | 0 | none | 20:07:00 is in neither the lead nor a turn. T2 runs from 20:07:01: `X.agent_only` 59 |
| 20:08 | 0 | none | T2 runs: `X.agent_only` 60 |
| 20:09 | 0 | none | T2 stops at 20:09:00, and M3 comes 1,020 s later, so nothing reads T2's 45 s. No credit and no agent, so no bucket |
| 20:10-20:11 | 0 | none | No credit and no agent |
| 20:12 | 0 | none | The agent's message reaches the agent at 20:12:00 with no relay record: rule 4 takes it, `headless`, and its turn runs 60 s, automated |
| 20:13 | 0 | none | The automated turn runs |
| 20:14 | 0 | none | The automated turn runs until 20:15:00 |
| 20:15-20:24 | 0 | none | No credit and no agent. The bucket holds 120 s, but M3 comes 660 s after the agent's stop |
| 20:25 | 2 | `GO` 2 | M3's lead, 3 / 1.5 = 2 s, from 20:25:58 |
| 20:26 | 0 | none | 20:26:00 is in neither the lead nor a turn. T4 runs from 20:26:01: `X.agent_only` 59 |
| 20:27 | 0 | none | T4 runs until 20:27:30: `X.agent_only` 30. Nothing follows in the window |
| 20:28-20:29 | 0 | none | No credit and no agent |

## Totals

| Total | Seconds | Arithmetic |
|---|---|---|
| `credited_s` | 212 | 60 + 30 + 60 + 30 + 30 + 2 |
| `P` | 90 | 60 + 30 |
| `READ` | 120 | 30 + 60 + 30 |
| `GO` | 2 | 2 |
| `level_c_s` | 212 | 90 + 120 + 2 |
| `via_relay_s` | 212 | 90 + 120 + 2 |
| `agent_running_s` | 537 | 59 + 60 + 30 + 59 + 60 + 3 × 60 + 59 + 30 |
| `agent_only_s` | 357 | 59 + 60 + 30 + 59 + 60 + 59 + 30 |
| `X.agent_only` | 150 | 60 + 30 + 60 |
| `automated_s` | 180 | 3 × 60 |
| `capped_s` | 60 | 30 + 30 |
| `relayed_prompts` | 3 | 1 + 1 + 1 |
| `headless_prompts` | 1 | 1 |

`X.agent_only` counts only the rows whose every uncredited second has a code SM settles. The other rows hold seconds that only the cap kept from being read, seconds nothing covers, or seconds only the automated turn ran in, and give their codes without seconds, as the lookback fixtures do. `agent_only_s` counts every second a turn Lena started ran without credit.

## Readings the case relies on

- **The lead ends at the stamp, not at the prompt.** M1's lead ends at 20:01:00, its `sent_at`, though the prompt reached the agent at 20:01:01. The second in between is in no window.
- **No think part.** A relayed lead is typing time only, whatever the class (SM 2.3): M3, a go-ahead, earns its 2 s of typing and not `LOW_LEAD_S`.
- **Reading waits for a stop and a following relayed message.** T1's 120 s are read because M2 follows its stop within 600 s. T2's and the agent turn's output is never read, because M3 comes later than that after both stops.
- **The agent's message.** T3 records it with `createdBy` `agent`, so no relay record exists and rule 4 applies. Its turn's output still fills the bucket, as an automated turn's output does (SM 2.4), and would have been read had M3 come within 600 s of 20:15:00.
- **The relay record names the prompt** because the prompt reached the agent first (SM 2.2 rule 9). The prompt carries no `relay` here.
