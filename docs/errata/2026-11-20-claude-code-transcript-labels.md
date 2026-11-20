# Erratum: Claude Code 2.1.327 transcripts state the entrypoint and where each prompt came from

Date 2026-11-20. Raised by WP-AD2. Status: decided 2026-11-20 by the orchestrator for Karim: Claude Code's labels only ever withhold credit, and a mark saying a person sent a prompt adds nothing (2.1.327 marks step 3's paste typed). (a) holds. A log reader writing session.start for a session no hook reported maps the transcript's token: sdk-cli, sdk-ts and sdk-py become sdk, remote, remote_desktop, remote_mobile and claude-in-slack become cloud, and no other token sets anything, since only a walk gives ide or desktop. It writes one at the first entry that states a token and wherever the token changes. turnOrigin scheduled gives origin_declared cron and task_notification gives subagent, in every version whose entries carry them, and a value no capture has shown is a parse failure. (b) is decided another way. A hooked session takes nothing from the transcript or OTel, whose tokens arrive after the walk. Instead pairtime hook sends the CLAUDE_CODE_ENTRYPOINT its own environment holds, which all 90 captured calls name, and the adapter applies the same table to the walk's session.start: sdk-cli under any host, sdk-ts or sdk-py unless an IDE or agent desktop app decides parent_class. This starts once a capture records the value. Which enqueues are agent.queue is the queued-turn erratum's question. Correction: both claude -p prompts have turnOrigin sdk, not none. HS 3.2 stays as researched. No code in WP-AD2 depends on it. It bears on E-2.8-lookback's SM wording, which the orchestrator's docs PR is about to apply, and on WP-AD3 and WP-AD4.

## Input

The four transcripts of WP-AD1's Claude Code 2.1.327 capture on Linux, under `testdata/fixtures/claude_code/2.1.327/linux/cli/transcripts`. Every user, assistant, attachment and system entry has an `entrypoint` field. It is `cli` in the two interactive sessions, `617e16a3` and `b0423624`, and `sdk-cli` in the two `claude -p` sessions, `2197af56` (step 20's nested run) and `e5edd056` (step 18). User entries also carry `promptSource`, `turnOrigin` and `origin`:

| Prompts | `promptSource` | `turnOrigin` | `origin` |
|---|---|---|---|
| every typed prompt, 12 in `617e16a3` and 1 in `b0423624` | `typed` | `human` | `{"kind": "human"}` |
| step 10's task notification, line 151 | `system` | `task_notification` | `{"kind": "task-notification", "producer": "session-task", ...}` |
| step 12's three `/loop` runs, lines 201, 209 and 217 | `system` | `scheduled` | none, and `isMeta` true |
| step 21's queued message, line 264 | `queued` | `human` | `{"kind": "human"}` |
| the prompt of each `claude -p` session | `sdk` | `sdk` | none |

Line numbers count from 0 in `617e16a3-ddb4-44ca-af68-72cf58ed1a34.jsonl`.

## Expected

E-2.8-lookback's text for SM 1.2's `session.start` row: "A Claude Code transcript states none of these (HS 3.2), and lookback reads only the entrypoint (2.8)." Its text for SM Risks 10: "A Claude Code transcript records no entrypoint, parent or TTY (HS 3.2), and no log holds `Stop.session_crons`, so a `claude -p` loop, an Agent SDK session, a `claude -p` that another agent started and unattended `/loop` runs read as typed prompts unless the log marks them." Its erratum's "Not reached" items ask whether the transcript marks `-p`, `/loop` and cron prompts.

## Computed

2.1.327's transcripts state the entrypoint, and they mark scheduled runs, task notifications, queued messages and `-p` prompts. They don't record the parent process or the TTY. HS 3.2's list of transcript fields comes from two public parsers and predates these fields.

## Reasoning

- **Lookback.** A log reader can state `entrypoint` `sdk` for a `claude -p` session, so SM 2.2 rule 4 catches it in lookback. Rules 1 and 3 get labels the log declares: `turnOrigin` `scheduled` is rule 1's "the session's scheduler fired it", and a task notification is a subagent's report (rule 3). The two E-2.8 sentences above are wrong for 2.1.327 and should say what this version states. HS 3.2 warns that the entry format "is internal to Claude Code and changes between versions", so WP-AD3's reader should read these fields only for the versions whose captures show them (HS 17.7).
- **Live sessions.** E-2.8 has log readers write `session.start` only for sessions no hook reported. Step 18's `claude -p` fired hooks, so the transcript's `sdk-cli` never reaches rule 4 through a log record. From the hooks alone nothing says `sdk`: `SessionStart` carries no entrypoint, and the walk finds `claude` under `bash` under `tmux`, which is `parent_class` `multiplexer` with a TTY. So rule 4 doesn't fire, and the session's prompts go to rule 6. OTel's `app.entrypoint` is `sdk-cli` for that session in this capture's OTLP bodies, so WP-AD4's receiver can fill it. Until WP-AD4 lands, a hooked `claude -p` started from a terminal reads as an interactive session, and only rule 6's input and focus tests stand between it and credit. Step 20's nested run is caught regardless, by `parent_class` `agent`.

## Decision

Claude Code's own labels only ever withhold credit, and no mark saying a person sent a prompt adds any. E-2.8's rule stands: a log reader writes session.start only for a session no hook reported. When it does, it maps the transcript's entrypoint token: sdk-cli, sdk-ts and sdk-py become sdk; remote, remote_desktop, remote_mobile and claude-in-slack become cloud; every other token, cli and claude-vscode included, sets nothing, because only a walk supplies ide or desktop. It writes the record at the first entry that states a token and again wherever the token changes. turnOrigin scheduled gives origin_declared cron, task_notification gives subagent, and rules 1 to 3 read every hook and log copy of a prompt. The hook-environment route is in: pairtime hook sends the CLAUDE_CODE_ENTRYPOINT value its own environment holds, and the adapter applies the same table to the session.start it records. sdk-cli counts under any host, sdk-ts and sdk-py count unless an IDE or agent desktop app decides parent_class, and the token never undoes a walk's sdk or exec; this starts once a capture records the variable's value. Hooked sessions take nothing from the transcript's or OTel's token, both of which arrive after the walk, so startBefore and CC9 stay as they are, WP-AD4 doesn't read app.entrypoint, and the install sets no OTEL_METRICS_INCLUDE_ENTRYPOINT. Which enqueues become agent.queue is E-cc-queued-turn's question, HS 3.2 stays as researched, and no fixture or SM number moves.
