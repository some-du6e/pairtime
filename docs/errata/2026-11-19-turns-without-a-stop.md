# Erratum: whether a turn with no `agent.stop` is running

Date 2026-11-19. Raised by WP-FX4. Status: waiting for Karim. Until Karim decides, the side-door cases (a), (b) and (e) in `testdata/timelines/sidedoor` use the rule proposed below, and every row that depends on it names this erratum. Their credited seconds, levels, reading fills and `logged_s` don't depend on it.

## Input

Two kinds of turn never record an `agent.stop`:

- A track turn. A `pairtime track` call that opens an anchor starts a human-owned turn whose reading fill is `R_TURN_BASE_S`, "because no record shows the agent's output" (DESIGN 5.4). No record shows the agent at all: no output, no tool call, no stop.
- A bridge turn. The WakaTime bridge turns transcript-derived heartbeats into a `prompt.submit` and `agent.tool` edits (DESIGN 5.7). No heartbeat becomes an `agent.stop`.

Three rules ask whether such a turn is running:

- SM 2.4: drained seconds are `SUP` "while the agent is still running" and `READ` "after its stop".
- SM 5.1: `agent_running_s` is the "seconds in this minute during which an agent turn was running".
- SM 5.2: `X.agent_only` is a second where "an agent turn ran and nothing credited the second", and `X.no_anchor` one that is present and focused with no window. SM 6.1's 14:04 and 6.2's 07:41 put `X.agent_only` on seconds where the person watches a running agent with nothing new to read.

## Expected

Each second of these turns gets one reason code, and each minute one `agent_running_s`.

## What the rules give

SM 1.3 builds a turn from `prompt.submit` and `agent.*` events, with `t_submit` and `t_stop`, and `pkg/event`'s `Turn` says "TStop is 0 while the turn is still running." A turn that never records a stop would run for ever: every later drain `SUP`, every idle second `X.agent_only`, and `agent_running_s` growing all night after one `pairtime track` call. Nothing in SM or DESIGN says when such a turn stops.

Case (a) shows what that costs. Ten track calls come from a loop with no agent behind them. Turns that ran for ever would put agent time on every minute after 14:04, and the chess clock would show agent work that never happened.

## What the fixtures do

They use the rule below. In (a) and (b) the track turns never run. Their drains are `READ`, the idle seconds after them are `X.no_anchor`, `agent_running_s` is 0, and 14:08 has no bucket. In (e) each bridge turn runs from its prompt at :03 to its edit at :06, so its 20 s base drains as 3 s of `SUP` and 17 s of `READ`, `agent_running_s` is 3 in each turn's minute, and the idle seconds after it are `X.no_anchor`.

The rows that depend on it: (a) 14:06 to 14:08, (b) 14:07 and 14:08, (e) 14:03 to 14:08. Credited seconds don't move, because `SUP` and `READ` both credit, at the same level.

## A rule to decide it

A turn runs from its prompt to its `agent.stop`. A turn with no `agent.stop` runs to its last agent record, and a turn with no agent record after its prompt doesn't run. So a track turn never runs, and a bridge turn runs until its last bridge edit. A later prompt in the session ends a turn either way.

Two alternatives:

1. A turn with no `agent.stop` doesn't run at all. Simpler. (e) would then drain 20 s of `READ` per turn, with `agent_running_s` 0.
2. Such a turn runs until the session's next prompt. That puts agent time on minutes no agent record covers, as in the paragraph above.

## For Karim

1. Accept the rule above, or pick another. It belongs in SM 2.4 and 5.1, next to what a running turn is.
2. The rows in (a), (b) and (e) then lose the erratum, or change in a PR with the `oracle-change` label.
