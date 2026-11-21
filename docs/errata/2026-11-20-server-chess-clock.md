# Erratum: the server can't draw DESIGN 3.8's chess clock, because a tier-1 bucket carries neither agent-only seconds nor the agent's project

Date 2026-11-20. Raised by WP-DB0 (the dashboard wave), from the review of #125. Status: decided 2026-11-20 by the orchestrator for Karim: option 1 (D-bucket-agent-only). Every tier-1 minute bucket gains `agent_only{p: s}`, the seconds in the minute a turn of each project ran that weren't credited to that project. WP-EV3 adds it to DESIGN 4.3 and to `pkg/event`'s minute bucket, whose `Tier1BucketFields` lists the tier-1 fields. docs/schema/event.schema.json describes events, not buckets, so it doesn't change. WP-SC4 fills it, ingest accepts it, the uploader sends it, and the server's chess clock reads it. Until those land, `/demo`'s sample already shows agent-only time per project, and no real dashboard is served.

## Input

- DESIGN 3.8, the chess clock: "You 1h 12m, Claude 2h 40m" per project and day, as information, never as a target ratio (PA 4.3).
- SM 2.6: "Agent-only seconds (wall time of agent runs that no human second covers) are reported in every rollup, so the user sees the chess clock: your time next to the agent's (PA 4.3)." So the agent's clock counts only seconds no human second covers, and the two clocks never run at once, as on a real chess clock.
- DESIGN 4.3, the tier-1 minute bucket: `{v, params, minute, device, slots[4]{p, cr, why, kind, ev, lbl}, credited_s, uncredited{code: s}, agent_running_s, state, witness, rev}`. SM 5.1: `agent_running_s` is "seconds in this minute during which an agent turn was running in any project".
- E-6.6-codes: an uncredited second takes `X.absent`, `X.other_focus` or `X.ambiguous` first, and `X.agent_only` only when none applies and a person started the turn running in the slot's focus project.

## Expected

The server's chess clock shows, per project and day, the owner's credited time beside the agent-only time: the seconds an agent ran in that project that no credited second covers.

## Computed

Neither number is in a bucket. `agent_running_s` counts seconds the owner was credited too (SUP, PA, INT, Q happen while the agent runs), so it overlaps the owner's time. `uncredited["X.agent_only"]` leaves out every agent second the owner spent away or in another app, which carry `X.absent` or `X.other_focus`. What the bucket bounds is `max(0, agent_running_s - credited_s)` at least and `min(agent_running_s, 60 - credited_s)` at most, per minute. And `agent_running_s` names no project, so a minute whose slots name two projects can't say which one the agent ran in.

## For Karim

Pick one before the dashboard is the place teens read their week.

1. **Add agent-only seconds by project to the bucket.** Each bucket gets `agent_only{p: s}`: for each project key, the seconds in the minute a turn of that project ran and the second wasn't credited to it. The scorer already counts exactly this for each slot, `score.Credit.AgentOnly`, by project (`pkg/score/credit.go`), so the bucket sums four slots. DESIGN 4.3 and the tier-1 field list change through you (PLAN ground rule 10), then `pkg/event`, ingest's shape check, the uploader and `rollup.Bucket`. The server's chess clock then matches the device's, per project. This is the change I'd make: it is the number SM 2.6 says the chess clock shows, it names the project the agent ran in, and it is a count of seconds per opaque key, so it fits tier 1.
2. **Keep running time on the server.** DESIGN 3.8 says the server's dashboard shows the agent's running time beside the owner's, overlapping, and the device's coach keeps the agent-only clock. The per-project split stays the slots' share, which is right when the owner works in one project at a time and wrong for an agent running in one project while the owner's attention is in another.
