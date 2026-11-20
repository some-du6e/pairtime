# Lookback: SM 6.4 from its transcript alone

Jordan's night (SM 6.4) as lookback would read it from the Claude Code transcript. The events are those of [`6.4`](../../6.4/) with every OS, editor and git event removed, and SM 2.8 scores the rest. Lookback credits 91 s, the typing time of his two prompts, against the live fixture's 810. The agent's 23,400 s are all agent-only.

| | |
|---|---|
| Source | `testdata/timelines/6.4/events.jsonl`, harness events only. gen.py reads that file, so this fixture follows any change to it |
| Rules | SM 2.8, with SM 2.2 to 2.4 and the parameters of SM 8 where 2.8 doesn't change them |
| Date and zone | Friday 2026-11-20 into Saturday, Europe/Berlin, UTC+01:00, as in 6.4 |
| Evidence | Level C, witness `backfilled`, presence `unknown` in every slot |
| Window | 23:37:00 to 07:37:00. The session starts at 23:29:58 and isn't checked |
| Errata | [lookback](../../../../docs/errata/2026-11-19-sm-2.8-lookback.md), point 2; WP-FX1's [prompt lengths](../../../../docs/errata/2026-11-19-sm-6.4-prompt-lengths.md) and [6.6 codes](../../../../docs/errata/2026-11-19-sm-6.6-uncredited-codes.md), point 1 |

## What the transcript holds

The `/goal` prompt at 23:40:00, the agent's nine turns to 06:10:00, and the follow-up at 07:37:00. T2 to T9 are stop-hook continuations, which have no prompt and whose stops carry `stop_hook_active`, so SM 2.2 rule 2 labels them `self_continue` in lookback too. Every record arrives `via` `log`.

Gone with the OS and git records: the lock at 23:53:10, the unlock at 07:30:00, Jordan's typing and scrolling, and the agent's 31 commits. The `bash` calls that made the commits are still there as tool calls.

## Credit, minute by minute

| Minute | Credited | Reasons | Arithmetic |
|---|---|---|---|
| 23:37-23:38 | 0 | none | No lead reaches back this far and no agent runs, so there is no bucket. The live fixture credits these minutes from the think lead, which lookback drops |
| 23:39 | 50 | `P` 50 | The `/goal` prompt has 75 typed characters by SM's count. SM 2.8 keeps its typing time, 75 / 1.5 = 50 s, from 23:39:10. SM decides no code for 23:39:00 to 23:39:10 |
| 23:40-00:22 | 0 | none | T1 runs to 00:23:20, and lookback credits no reading while an agent runs. `X.agent_only` 43 × 60 = 2,580 |
| 00:23-06:09 | 0 | none | T1's last 20 s, then T2 to T9 to 06:10:00: 347 × 60 = 20,820 s of agent time, none credited. Whether the continuations' seconds are `X.automated` or `X.agent_only` is open |
| 06:10-07:35 | 0 | none | No agent runs and nothing is credited, so there is no bucket. The night's output would count as reading only if a prompt came within 600 s of a stop. The last stop is at 06:10:00 and the next prompt at 07:37:00, 5,220 s later |
| 07:36 | 41 | `P` 41 | The follow-up has 61 typed characters by SM's count: 61 / 1.5 = 40.67 s, rounded up to 41 as SM rounds the live lead's 160.67 s to 161, so it starts at 07:36:19. SM decides no code for the 19 s before it |

## Totals

| Total | Seconds | Arithmetic |
|---|---|---|
| `credited_s` | 91 | 50 + 41 |
| `P` | 91 | 50 + 41 |
| `agent_running_s` | 23,400 | 2,580 + 20,820 |
| `agent_only_s` | 23,400 | 23,400 − 0 |
| `X.agent_only` | 2,580 | 43 × 60 |

`agent_only_s` is the agent's running time less what was credited inside it, which is nothing. `X.agent_only` counts only T1's seconds to 00:23:00, the rows whose codes SM decides.

| | Live 6.4 | Lookback |
|---|---|---|
| Credited | 810 | 91 |
| `P` | 331 | 91 |
| `SUP` | 220 | 0 |
| `READ` | 259 | 0 |
| Agent-only | 23,180 | 23,400 |

The live fixture's reading needs presence. Its `SUP` from 23:40 to 23:43:40 happens while the agent runs, which lookback never credits, and its `READ` from 07:30 has no prompt within 600 s of the stop before it. Without presence, lookback can't tell Jordan watching the plan from Jordan already in bed. It gives the night the same nothing either way, which is what SM 6.4 wants from an unattended `/goal`.

## Readings the rows rely on

- **Prompt lengths.** The events carry SM's counts, 75 and 61, as the live fixture's do. The quoted texts have 73 and 60 characters (WP-FX1's prompt-lengths erratum). With those, 23:39 would credit 73 / 1.5 = 48.67 s, rounded up to 49, and 07:36 would credit 40, so the night would credit 89.
- **Rounding.** 61 / 1.5 is 40.67 s. The lead is rounded up to the whole second, as SM rounds the live lead, which the prompt-lengths erratum also asks Karim to confirm.
- **Codes.** Seconds outside every window with no agent running have no SM 5.2 code in lookback (the lookback erratum's point 2). A stop-hook turn's uncredited seconds could be `X.automated` or `X.agent_only` (WP-FX1's 6.6 erratum, point 1). Those rows have `uncredited` null and list the candidate codes.

DESIGN 6.2 D5's cap for level C, min(credited, ai_logged / 3), is the 91 s credited: the agent events replay to `ai_logged_s` 23,520.
