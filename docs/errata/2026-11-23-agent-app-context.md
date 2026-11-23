# Erratum: a desktop stops sampling while T3 Code waits for a phone message, so the message's minutes read as slept through

Date 2026-11-23. Raised by the review of #219 (issue #221). Status: decided 2026-11-23 by the orchestrator for Karim (D-relayed-context): DESIGN 3.3's project context gains "an agent app that drives sessions, such as T3 Code's server, is running", so the presence sampler keeps sampling while relayed credit is possible. The agent's project-context test counts a running agent app's server the way it counts a running harness. DESIGN 3.3, the agent and their tests take the change together, in the PR that adds this file (WP-ADE1, PR 3).

## Input

- DESIGN 3.3's `presence` row samples only while a project context is live: a harness session process runs, a terminal, editor or agent app had focus in the last 600 s, or a checked editor heartbeat came in the last 600 s. Outside one, nothing is sampled or recorded (D14).
- SM 2.1, as `pkg/score` reads it: a gap between two samples longer than `SLEEP_GAP_S` (30 s) is sleep, unless the first of them showed `CONTEXT_IDLE_S` (600 s) with no input and the gap is at most the 120 s that sampling once a minute leaves. A slot a sleep covers takes no credit, and its seconds are `X.absent`.
- SM 2.2 rule 9 and 2.3: a message a person sent from T3 Code's phone app, `client` `mobile`, credits its typing lead before the app's stamp, and the session's reading after the turn it started, at level C, in any slot that isn't asleep, whatever this device saw of the person.
- T3 Code's server isn't a harness process, so the process scan counts nothing for it. The Claude Code it drives fires hooks only once a message's run has started.

## What happens

The person leaves the desktop with T3 Code's server running and sends messages from the phone. Once 600 s pass with no focus on a terminal, editor or agent app, no hook and no running turn, the context ends and the sampler stops. The next message's run starts a turn, whose prompt hook makes the context live again and takes a sample. The message's lead ends at the app's stamp, a moment before that sample, so it falls in the stretch with no samples. `pkg/score` reads that stretch as sleep: the lead earns nothing and its seconds are `X.absent`. Reading after a turn's stop goes the same way once the hook's 600 s have passed.

A server never samples, so `pkg/score` derives its whole minutes from the records, and there the same message earns its lead (D-relayed, `t3-away`). A desktop earns less for the same message than a server does, because it samples.

Expected: while T3's server runs, the desktop keeps sampling, once a minute after 600 s with no input, which the scorer tells apart from sleep. A phone message's lead and reading then earn level C credit, as on a server.

## Why

D14 stops sampling outside a project context so that pairtime never records general computer use. A running agent app is a place where work can arrive at any moment, from a device pairtime doesn't run on. While it runs, relayed credit is possible, and only samples tell the scorer an awake machine from a sleeping one. The cost is a sample a minute while the person is away and T3 runs, which is what a harness session left running costs today.

## Decision

D-relayed-context, as the Status line says.

## What changes

- DESIGN 3.3's `presence` row lists "an agent app that drives sessions, such as T3 Code's server, is running" among the signs of a project context.
- The agent: the apps component tells presence how many agent apps run (`presence.Sampler.AgentApps`), as the process scan tells it how many harness session processes run (`HarnessProcesses`), and any one makes the context live. For T3 Code, running means what DESIGN 3.3's `apps` row already reads it as: the store's `-wal` file exists, which T3's server holds open while it runs. The apps component checks on every read, every 10 s while T3 runs and every minute while it doesn't, so the context goes live within a minute of T3 starting and ends within a minute of it stopping. T3 counts as running whether or not `sqlite3` is there to read its store and whether or not the probe accepts its schema.
- Tests: the presence gate and the sampler keep sampling past a terminal's 600 s while an agent app runs, and stop at the 600 s without one. The apps component makes the context live on a read that finds T3 running, and ends it on one that finds T3 stopped or no store. `pkg/score` credits a phone message's lead in minutes sampled once a minute with no input, and none in a stretch with no samples.

## What stays

- The process scan counts harness session processes alone. This file reads "the way it counts a running harness" as a count that presence keeps, which the apps component gives from T3's store. A process name proves nothing, since a script can set its own (DESIGN 5.1), and the store is how DESIGN 3.3 already tells that T3 runs. A scan of `t3 serve`'s process would give the same context once `appclass.json` names it, if the orchestrator prefers that.
- With no agent app running and no other sign of a context, nothing is sampled.
- D14's slow cadence holds in an agent app's context too: after 600 s with no input and no running turn, one sample a minute.
