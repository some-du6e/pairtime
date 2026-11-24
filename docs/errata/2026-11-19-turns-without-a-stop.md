# Erratum: whether a turn with no `agent.stop` is running

Date 2026-11-19. Raised by WP-FX4. Status: decided 2026-11-20 by the orchestrator for Karim: a turn runs to its first `agent.stop` or `agent.interrupt`, and no later than its session's next prompt or `session.end`. Before then, a turn in a session that the harness's own hooks or logs report runs on until a gap of more than `BUCKET_TTL_S` in its agent records, and any other turn runs only to its latest agent record, ended for good by a gap of more than `READ_HORIZON_S`. Nothing credited from a session that hooks and logs don't report can open a lookup. SM 1.3 and 2.5 state the rules, and no fixture number changes (Decision, below).

## Input

Two kinds of turn never record an `agent.stop`:

- A track turn. A `pairtime track` call that opens an anchor starts a human-owned turn whose reading fill is `R_TURN_BASE_S`, "because no record shows the agent's output" (DESIGN 5.4). No record shows the agent at all: no output, no tool call, no stop.
- A bridge turn. The WakaTime bridge turns transcript-derived heartbeats into a `prompt.submit` and `agent.tool` edits (DESIGN 5.7). No heartbeat becomes an `agent.stop`.

These rules ask whether such a turn is running:

- SM 2.4: drained seconds are `SUP` "while the agent is still running" and `READ` "after its stop".
- SM 2.5: a run of browser or chat-app slots counts as `LOOKUP` for `p` only if "no agent turn in `p` is running during it". During a run, browser time is waiting.
- SM 5.1: a minute gets a bucket when "anything was credited or an agent was running", and `agent_running_s` is the "seconds in this minute during which an agent turn was running".
- SM 5.2: `X.agent_only` is a second where "an agent turn ran and nothing credited the second", and `X.no_anchor` one that is present and focused with no window. SM 6.1's 14:04 and 6.2's 07:41 put `X.agent_only` on seconds where the person watches a running agent with nothing new to read.
- SM 5.4: `ai_credited_s` adds "any credited second inside an agent turn", and `hand_credited_s` takes `LOOKUP` only outside agent turns.

No case here has a lookup run, and (a), (b) and (e) credit only `P`, `SUP` and `READ`, which are AI credit either way. So SM 2.5 and 5.4 decide no row here, but the scorer needs the same answer for them.

## Expected

Each second of these turns gets one reason code, and each minute one `agent_running_s`.

## What the rules give

SM 1.3 builds a turn from `prompt.submit` and `agent.*` events, with `t_submit` and `t_stop`, and `pkg/event`'s `Turn` says "TStop is 0 while the turn is still running." A turn that never records a stop would run for ever: every later drain `SUP`, every idle second `X.agent_only`, and `agent_running_s` growing all night after one `pairtime track` call. Nothing in SM or DESIGN says when such a turn stops.

Case (a) shows what that costs. Ten track calls come from a loop with no agent behind them. Turns that ran for ever would put agent time on every minute from 14:04 on, and the chess clock would show agent work that never happened.

## What the fixtures do

They use the rule below. In (a) and (b) the track turns never run. Their drains are `READ`, the idle seconds after them are `X.no_anchor`, `agent_running_s` is 0, and 14:08 has no bucket. In (e) each bridge turn runs from its prompt at :03 to its edit at :06, so its 20 s base drains as 3 s of `SUP` and 17 s of `READ`, `agent_running_s` is 3 in each turn's minute, and the idle seconds after it are `X.no_anchor`.

The rows that depend on it: (a) 14:04 to 14:08, (b) 14:04 to 14:08, (e) 14:03 to 14:08. In (a) and (b) the first track turn starts at 14:04:00, so read literally, or under alternative 2 below, the track turns would cover every second from then to the end of the window: 60 s of `agent_running_s` in each of those minutes, 300 s in each case's total, and a bucket for 14:08. The `agent_running_s` totals of all three cases depend on it. Credited seconds don't move, because `SUP` and `READ` both credit, at the same level.

## A rule to decide it

A turn runs from its prompt to its `agent.stop`. A turn with no `agent.stop` runs from its prompt to the latest of its agent records the scorer has received so far, and doesn't run while it has none. A later prompt in the session ends a turn either way. So a track turn never runs, and a bridge turn runs until its latest bridge edit.

"Received so far" matters because a minute is final 900 s after it ends (DESIGN 3.7, 6.2 D1). An agent record that arrives later re-scores only the minutes still pending, so it extends the turn over those and leaves final minutes as they were. In (e) each edit comes 3 s after its prompt, long before any minute it touches is final, so the rows are what hindsight would give. When two of a turn's agent records arrive more than about 900 s apart, they aren't: the turn runs in the minutes still pending when the later record arrives, but not in the final minutes before them.

Two alternatives:

1. A turn with no `agent.stop` doesn't run at all. Simpler, and settled when the prompt arrives, so no late record can leave a turn running in some minutes and not others. (e) would then drain 20 s of `READ` per turn, with `agent_running_s` 0.
2. Such a turn runs until the session's next prompt. That puts agent time on minutes no agent record covers, as the literal reading does: in (a) and (b), every minute from 14:04 on.

## For Karim

1. Accept the rule above, or pick another. It belongs in SM 1.3, where turns are built, so that SM 2.4, 2.5, 5.1, 5.2 and 5.4 all read the same answer.
2. The rows in (a), (b) and (e) then lose the erratum, or change in a PR with the `oracle-change` label.

## Decision

Decided 2026-11-20 by the orchestrator under Karim's delegation, together with the hooked-turn-runs erratum that #52 added (docs/errata/2026-11-20-hooked-turn-runs.md). SM 1.3 and 2.5 state the rules, and DESIGN 5.2, 5.4 and 5.7 apply them to `track`, plugin and bridge sessions.

- A turn runs from `t_submit` until its first `agent.stop` or `agent.interrupt`, and no later than its session's next prompt or `session.end`. Claude Code fires no `Stop` on an interrupt (HS 3.1), so an interrupt has to end a turn too.
- Until then, a turn in a session that the harness's own hooks or logs report runs on, because they will report how it ends. A turn running a 40-minute build is agent time throughout. Under the last-record rule its minutes would go final as if the agent had stopped.
- A turn in any other session runs only to its latest agent record, the rule proposed above, and one with none doesn't run. So a `track` turn never runs and a bridge turn runs to its latest edit.
- A long gap in a turn's agent records ends its run, and no later record starts it again. The gap is more than `BUCKET_TTL_S` in a hooks-or-logs session and more than `READ_HORIZON_S` in any other. That replaces "received so far": the run no longer depends on when records arrived, so a Step per minute and a re-score of the same records agree.
- In a session that hooks and logs don't report, nothing the session credits can be the credited slot a browser run starts after (SM 2.5). Its agent may be working with nothing to show it, and browser time while an agent works is waiting. A typed edit or another session's act just before the run can still open it. For a teen: if pairtime sees your agent only through `pairtime track`, a plugin or the WakaTime bridge, a browser break counts as a lookup only when something else you did in the project, like an edit, came just before it.

Why the alternatives lost. Alternative 2 makes up agent time that no record shows, 300 s in each of (a) and (b), and never ends when no next prompt comes. Alternative 1 isn't settled when the prompt arrives either. A hooked turn has no stop until its Stop hook fires, so its minutes would go final as if the agent had stopped.

Why the lookup rule covers the whole session's credit. A rule that only kept the latest prompt's lead and its reading from opening a lookup would rarely apply. When the next prompt comes within one lead, which is three minutes for a 90-character call, SM 3.3 rule 3 gives the seconds right after a prompt to the next prompt's lead, and that lead would open the lookup. A plugin's own `agent.stop` can't lift the rule, since nothing backs it (DESIGN 5.2). Hooked sessions need no such rule, because their turns run until the stop comes. The rule keeps waiting from counting as a lookup. It doesn't lower what a scripted loop can earn, which stays at the wall time case (b) shows.

What it costs. A teen whose harness pairtime sees only through a side door gets no lookup for a browser break when nothing but their own prompt or its reading came just before it, at most `LOOKUP_MAX_S` a break. A Claude Code turn whose interrupt the transcript reader misses runs until the next prompt, `session.end` or a `BUCKET_TTL_S` gap. That shows as agent time and withholds the project's lookups in the meantime.

No fixture number changes. (a) credits 387 s, `P` 309 and `READ` 78, with 33 s of `X.no_anchor`. (b) credits 389 s, `P` 369 and `READ` 20, with 31 s. Both have `agent_running_s` 0 and no bucket for 14:08. (e) credits 240 s, `P` 180, `SUP` 9 and `READ` 51, with 120 s of `X.no_anchor` and `agent_running_s` 9. The literal reading's figures above also check out: 60 s of agent time in each minute from 14:04 to 14:08 in (a) and (b), 300 s in all, and a bucket for 14:08. Every other turn inside the window of the SM, lookback and side-door fixtures has a stop. No fixture has an interrupt, a queued message or a subagent, the longest gap inside a turn is 150 s, and every browser run follows a stopped turn of a hooked session. The rows of (a), (b) and (e) no longer name this erratum and cite SM 1.3 instead.
