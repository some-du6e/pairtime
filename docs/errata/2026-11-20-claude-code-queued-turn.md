# Erratum: a queued message's own turn has no prompt.submit

Date 2026-11-20. Raised by WP-AD2. Status: decided 2026-11-20 by the orchestrator for Karim: option 1. The turn a queued message starts takes the message's origin and class and pairs with it one to one in time order, and the adapter treats any prompt_id that an earlier call of the session carried as a running turn's.

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

So Claude Code runs `UserPromptSubmit` when the person queues a message, 25 ms before it writes the enqueue, and the hook carries the `prompt_id` of the turn that is running. The turn the message starts later has a `prompt_id` of its own, and no hook names it until that turn's `Stop`. `PROVENANCE.md` says the hook "came when Claude took it up". The times put it at the enqueue, 4.5 s before the pickup (17:07:40.604 to 17:07:45.084).

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

## Decision

Decided 2026-11-20 by the orchestrator for Karim (E-cc-queued-turn).

- Option 1. The turn a queued message starts is that message's turn, neither an orphan nor `self_continue`, and takes its origin and class (SM 2.2). Every record stays one Claude Code reported.
- A queued message gets its origin as a prompt does. The adapter applies rules 1 and 3 to its text, so a scheduled prompt or a task notification is never an `agent.queue`. Rules 4 and 5 read its session. Rules 6 to 8 read its enqueue, with rule 6's input test, key presses where the OS reports them, in place of "input in its slot" (SM 2.3). In lookback, 2.8 treats it as a prompt.
- In time order, each `agent.queue` waits for a later turn key that no record named at or before it, that no `prompt.submit` names and that no stop hook continued. Each such turn takes the oldest waiting message, one to a turn, and a `prompt.submit` that rules 1 to 3 leave unlabeled ends every earlier wait. Two messages queued in a row pair with two turns in order, or leave the second waiting until the next prompt if Claude Code takes both up as one turn. A message queued during a picked-up turn waits for the turn after it.
- The turn opens no anchor, adds no base and resets nothing: the message's `Q` anchor at the enqueue and its reset at pickup credit it once. For SM 4.2 the message is that turn's prompt.
- The adapter takes a prompt as queued when any earlier call of the session carried its `prompt_id`, not only a prompt. A message queued in a picked-up turn before any of that turn's calls is still a `prompt.submit` until WP-AD3 learns the turn's key from the transcript's `promptSource` `queued` entry.
- CC1, CC2 and CC9 read queued messages (DESIGN 7.3), and `pkg/score` takes a hook's and a log reader's `agent.queue` of one message, within `TRACK_LOG_MATCH_S`, as one corroborated act.

No fixture, check scenario or SM example has an `agent.queue`, so no number moves. Over this capture, turn `7ca5940f` pairs with `hooks/000076`'s message, and CC9 finds no orphan.
