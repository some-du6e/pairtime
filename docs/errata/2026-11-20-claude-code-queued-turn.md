# Erratum: a queued message's own turn has no prompt.submit

Date 2026-11-20. Raised by WP-AD2. Status: waiting for Karim. The Claude Code adapter writes what the hooks report, so `testdata/checks`' CC9 would fail every honest session in which the person types while the agent works, until this is decided.

## Input

WP-AD1's Claude Code 2.1.327 capture on Linux, step 21 (`testdata/fixtures/claude_code/2.1.327/linux/cli`):

| Call or entry | Time (UTC) | What it says |
|---|---|---|
| `hooks/000069` | 17:06:55.553 | `UserPromptSubmit`, `/goal Make the failing test pass, then stop.`, `prompt_id` `8eb66f7b` |
| `hooks/000076` | 17:07:40.604 | `UserPromptSubmit`, `When you're done, say which test you fixed.`, `prompt_id` `8eb66f7b` |
| transcript line 257 | 17:07:40.629 | `queue-operation` `enqueue` of that message |
| `hooks/000077` | 17:07:41.629 | `Stop`, `prompt_id` `8eb66f7b` |
| transcript line 260 | 17:07:45.049 | `queue-operation` `dequeue` |
| transcript line 264 | 17:07:45.084 | user entry with the message, `promptId` `7ca5940f`, `promptSource` `queued` |
| `hooks/000078` | 17:07:47.689 | `Stop`, `prompt_id` `7ca5940f` |

The transcript lines are in `transcripts/.../617e16a3-ddb4-44ca-af68-72cf58ed1a34.jsonl`, counting from 0.

So Claude Code runs `UserPromptSubmit` when the person queues a message, 25 ms before it writes the enqueue, and the hook carries the `prompt_id` of the turn that is running. The turn the message starts later has a `prompt_id` of its own, and no hook names it until that turn's `Stop`. `PROVENANCE.md` says the hook "came when Claude took it up". The times put it at the enqueue, almost five seconds before the pickup.

## What the adapter does

`internal/adapters/claudecode` writes `hooks/000076` as `agent.queue` in the running turn, with the message's `len_chars`. That is SM 2.3's `Q` anchor, which SM 1.2 fills from the enqueue. It can't write a `prompt.submit` with turn key `8eb66f7b`: `pkg/score`'s `buildTurns` groups the `prompt.submit` records of one session and turn key as copies of one prompt, so the message would vanish behind the `/goal` prompt, and `checks.py`'s `cc1` keeps one hook prompt per turn key. `hooks/000078` becomes `agent.stop` with the key of `7ca5940f`, which is the key the transcript reader will give line 264, since the hooks' `prompt_id`, the transcript's `promptId` and OTel's `prompt.id` are one id.

## Expected

`testdata/checks/README.md`, CC9: "Every turn an `agent.output`, `agent.interrupt`, `agent.queue` or `agent.stop` names has a `prompt.submit` in the session, or ends with a stop hook's continuation."

## Computed

Turn `7ca5940f` has an `agent.output` and an `agent.stop`, no `prompt.submit`, and its stop has `stop_hook_active` false. `checks.py`'s `cc9` counts it in `orphan_turns` and fails the session. `pkg/score` reads it as a continuation that starts at the previous stop (`testdata/timelines/README.md`, Turns), so its origin is `self_continue`, not a person's.

## Reasoning

The person's act is credited once either way: the `Q` anchor at the enqueue carries the message's lead. What goes wrong is the bookkeeping of the turn the message starts. I see three ways out:

1. CC9 also accepts a turn whose first record is the session's first new turn key after an `agent.queue`. The adapter and `pkg/score` stay as they are, and every record stays one that Claude Code reported. I'd pick this one.
2. The adapter writes a `prompt.submit` for the new turn when its first record arrives, declared `followup` so that it opens no second anchor. That is a record no hook stated, at a time no hook reported, which E-2.8-lookback rules out for log readers and which I'd rather not start for hooks.
3. The transcript reader (WP-AD3) writes the turn's `prompt.submit` from line 264. That covers only sessions a log reader covers, and CC1 would then find a log prompt with no hook prompt.

Whichever stands, WP-AD3's reader must take a `promptSource` `queued` entry as the pickup of a message that already has its `agent.queue`, not as a new prompt. Otherwise the scorer credits the message's typing twice, `Q` at the enqueue and `P` at the pickup, and CC1 counts a log-only prompt.
