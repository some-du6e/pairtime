# Erratum: whether a turn with no `agent.stop` is running

Date 2026-11-19. Raised by WP-FX4. Status: waiting for Karim. Until Karim decides, the side-door cases (a), (b) and (e) in `testdata/timelines/sidedoor` use the rule proposed below, and every row that depends on it names this erratum. Their `agent_running_s` totals depend on it too, as does the split of credit between `SUP` and `READ` and of uncredited seconds between `X.agent_only` and `X.no_anchor`. Their credited seconds, levels, reading fills and `logged_s` don't.

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
