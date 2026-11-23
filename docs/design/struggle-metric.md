# Struggle metric

Status: draft complete, ready for design review

Methods spec for how pairtime turns what happens on a teen's machine into credited seconds of productive struggle. Written 2026-11-19 for design review. Requirement IDs (R1-R11, A) refer to [docs/BOUNTY.md](../BOUNTY.md).

This is the rulebook, not a survey. Engineers should be able to implement it from this page, and a reviewer should be able to take any credited minute and recompute it by hand from the events listed against it.

Research docs are cited by short name and section:

| Short name | File |
|---|---|
| PA | [research/prior-art-and-measurement.md](../research/prior-art-and-measurement.md) |
| HS | [research/ai-harness-signals.md](../research/ai-harness-signals.md) |
| HC | [research/hackatime-and-hack-club.md](../research/hackatime-and-hack-club.md) |
| CP | [research/client-platform.md](../research/client-platform.md) |
| TP | [research/trust-and-privacy.md](../research/trust-and-privacy.md) (still a draft) |
| SP | [research/server-platform.md](../research/server-platform.md) |

## Summary

- The unit is a human second, not agent time and not wall clock. Each second of a user's day is credited to at most one project, at a weight of 1, 0.5 or 0, with one reason code.
- A second counts only when three things hold. A human act opened a window that covers it (a typed prompt, a permission answer, an interrupt, a typed edit, a typed command, a commit). The OS saw input recently enough to call the person present. Focus was on that project's terminal, editor or agent window, or on a short, bracketed lookup in between. Where the desktop doesn't say which window is in front, a second can still count, at evidence level C (2.5).
- One exception has no OS evidence behind it. A message a person sends through an agent app such as T3 Code, often from another device, counts on the app's own word that a person sent it, once a prompt the agent received confirms it (2.2 rule 9). It earns its typing time and at most 120 s of reading per turn, at evidence level C, which never reaches anything public.
- Reading the agent's output is credited from a per-session reading bucket. The agent's visible output fills it at 180 words a minute of prose and 8 lines a minute of diff. Present, focused seconds drain it. Watching a spinner adds nothing, and an hour of unread output can't be banked past 10 minutes.
- Dark flow is handled by four deterministic rules that zero out credit: re-rolls (a third near-identical prompt with nothing changed in between), the agent rewriting the same lines a third time, the same error coming back a third time while the person neither steps in nor adds reasoning, and runs of go-aheads sent faster than the output could be read. A separate progress rule halves credit after two hours with no progress of any kind, and gives it back when a commit, a passing test or a fixed error arrives.
- Agent-only time is reported next to credited time and never credited. For agent-assisted windows the output also reports what Hackatime would have logged as `ai coding`, so a reviewer can see in one line whether pairtime credits more or less than the handbook's one third, and which minutes justify it.
- Everything here is a v1 guess until the pilot in section 7 fits it to reviewer decisions. Every credited minute records the `scoring_version` that produced it.

## 1. Event model

The scorer never reads a harness's own format. Adapters (hooks, log readers, an OTLP receiver, OS watchers, editor and shell integrations, a `.git` watcher) turn whatever each tool exposes into the normalized events below and append them to the local spool (CP 3, CP 6; HS 16). The scorer consumes only the spool. That is what makes R7's plugin approach work: a new harness needs an adapter, never a scorer change (HS 17.6).

Raw prompt text, agent output, file paths and window titles stay in tier 0 on the device (TP B6). The scorer runs on the device and reads tier 0. What it uploads is the per-minute output in section 5, which has none of that content.

### 1.1 Envelope fields on every event

| Field | Type | Meaning |
|---|---|---|
| `event_id` | UUIDv7 | Unique, time-ordered |
| `device_id` | random id | Per install, never a hardware hash (TP B1) |
| `seq` | uint64 | Per-device sequence, gapless; the server uses it for exactly-once ingest (SP 8.2) |
| `t` | int64 ms, UTC | Client wall time when the adapter saw the event. Hook payloads mostly carry no timestamp, so the hook binary stamps arrival time (HS 3.1, 4.1, 6.1) |
| `mono` | int64 ms | Monotonic reading at the same moment; the server re-anchors `t` from it within one boot (SP 8.4) |
| `boot_id` | string | Linux `boot_id`, or an id generated per boot elsewhere |
| `source` | enum | `hook`, `log`, `otel`, `os`, `editor`, `shell`, `git`, `cli` (the AGENTS.md `pairtime track` fallback), `mcp` |
| `harness` | enum | `claude_code`, `cursor`, `copilot`, `codex`, `gemini`, `devin`, `cline`, `opencode`, ... or `none` |
| `harness_version` | string | As reported; parsers are pinned per version (HS 17.7) |
| `session_key` | hash | Harness session id, salted per device. `none` for OS and git events |
| `project_key` | local id | Resolved by the client from `cwd`, the repo's root-commit set, or a hashed folder path (SP 4.2) |
| `chain_hash` | 32 bytes | Hash-chain link over the previous event; heads go to the server every 5 minutes (TP A3 T8, SP 8.5) |

### 1.2 Event kinds

Fields marked "local" never leave tier 0. Everything else may appear in tier 2 evidence for submitted hours only.

| Kind | Fields | Filled from (examples) |
|---|---|---|
| `session.start` | `start_kind` (`startup`, `resume`, `clear`, `compact`, `fork`); `entrypoint` (`cli`, `ide`, `desktop`, `sdk`, `exec`, `mcp`, `cloud`, `unknown`); `automation_flags[]`; `parent_class`: the nearest ancestor that isn't a plain interactive shell, classed as `terminal_app`, `ide`, `multiplexer` (tmux, screen), `remote` (sshd), `script_runtime` (python, a non-IDE node, a shell running a script file), `scheduler` (cron, systemd, launchd, Task Scheduler), `agent` (another harness), `agent_app` (an agent app's own server process, which runs the harness for the app's clients, such as T3 Code's `t3 serve`) or `unknown`; `has_tty`; `permission_mode` | Claude `SessionStart` + OTel `app.entrypoint`; Codex `session_meta.source`, `originator`, `thread_source`; Cursor `is_background_agent`; the hook binary's own walk up the process tree (HS 3.5, 6.2, 12.1). The daemon also lists harness processes once a minute and on focus changes, so `--bare` runs, which fire no hooks (HS 3.1), still get classified |
| `session.end` | `reason` | `SessionEnd`, `sessionEnd` |
| `prompt.submit` | `turn_key`; `len_chars`; `pasted_chars`; `typed_len_est` = `len_chars - pasted_chars`; `minhash[64]` (local); `template_hash` (local); `error_sig` (local, see 4.2); `slash_command` (e.g. `/loop`, `/goal`); `origin_declared` (`cron`, `loop`, `goal`, `subagent`, `cross_session`, `followup`, `none`); `matches_pending_cron`; `answers_question`; `low_effort`; `relay` (the `relay.message` the agent paired the prompt with, when that record came first, 2.2); `guest` (set when the app's reader says a guest's message started the prompt's run, 2.2); `text` (local, encrypted; never kept for a `guest` prompt) | `UserPromptSubmit`, `beforeSubmitPrompt`, `userPromptSubmitted`, `BeforeAgent`; transcripts for lookback; a Claude Code 2.1.327 transcript's `turnOrigin` gives `origin_declared` `cron` for `scheduled` and `subagent` for `task_notification`, as the hook's `<task-notification>` opening does. `human` and `sdk`, and every `promptSource` (`typed`, which a paste also gets, `queued`, `system`, `sdk`), declare nothing, and a value no capture has shown declares nothing and counts as a parse failure. Claude marks pastes with `<pasted_content>` lines in some sessions (PA 3.2 S3) |
| `agent.output` | `turn_key`; `words` (displayed assistant text only, not hidden thinking); `code_lines` (lines inside fenced blocks); `is_final`; `asks_question`; `offers_options` | Transcript assistant entries; `Stop.last_assistant_message`; `afterAgentResponse`; Codex `agent_message` |
| `agent.tool` | `tool_class` (`edit`, `write`, `read`, `bash`, `test`, `build`, `search`, `web`, `subagent`, `other`); `files[]` (hashed); `lines_added`; `lines_removed`; `cmd_hash` (normalized command, local); `exit_code`; `error_sig`; `visible_output_lines`; `t_start`; `t_end` | `PreToolUse`/`PostToolUse`; Claude `toolUseResult.structuredPatch`; Codex `exec_command_end`, `patch_apply_end`; Cursor `afterFileEdit` |
| `agent.permission` | `requested_at`; `answered_at`; `decision` (`accept`, `reject`, `edit`, `cancel`); `decided_by` (`user`, `config`, `hook`) | `PermissionRequest` paired with the next tool event; Claude `tool.blocked_on_user` span and `code_edit_tool.decision`; Codex `approval.requested` (HS 3.3, 6.3) |
| `agent.interrupt` | `turn_key` | Codex `Interrupt`; Claude transcript interrupt entries; Copilot `session.abort`; Cursor `stop.status = aborted` |
| `agent.queue` | `turn_key`; `len_chars` | Claude `queue-operation` `enqueue` (a message typed while the agent works) (HS 3.2) |
| `agent.stop` | `turn_key`; `stop_reason` (`end_turn`, `question`, `interrupted`, `error`, `limit`); `stop_hook_active`; `pending_crons[]` (prefix hashes of `Stop.session_crons[].prompt`, plus schedule) | `Stop`, `agentStop`, `AfterAgent`, Cursor `stop` (`loop_count`) |
| `agent.subagent` | `phase` (`start`, `stop`); `agent_key` | `SubagentStart`/`SubagentStop` |
| `editor.activity` | `editor`; `act` (`edit`, `navigate`, `save`, `open`, `debug`, `task`); `file_hash`; `lines_changed`; `edit_source` (`human`, `tab`, `agent`, `paste`, `unknown`) | WakaTime heartbeats sent to the local daemon through wakatime-cli's `[api_urls]` (HC 1.5, SP 8.8); Cursor `afterTabFileEdit` and `afterFileEdit`; pairtime's own editor extension (v2, HS 13.1) |
| `shell.command` | `cmd_class` (`test`, `build`, `run`, `git`, `install`, `other`); `cmd_hash` (local); `exit_code`; `t_start`; `t_end`; `interactive`; `error_sig` | Shell hook or OSC 133/633 marks (v1.5); zsh `EXTENDED_HISTORY` and fish history timestamps for lookback (HS 13.2) |
| `git.change` | `op` (`commit`, `amend`, `rebase`, `reset`, `checkout`, `merge`, `revert`, `push`, `stash`); `sha`; `parents[]`; `files_changed`; `insertions`; `deletions`; `actor` (`human`, `agent`, `unknown`); `agent_trailer` | `.git` watcher (`HEAD`, `index`, `logs/HEAD`) (CP 3.2); `actor = agent` when an `agent.tool` bash call ran the same git command within 2 s; trailers per HS 13.3 |
| `os.presence` | `idle_any_s`; `idle_keys_s` (null where unavailable); `locked` | One sample every 15 s (CP 3.3). `idle_keys_s` comes from `CGEventSourceSecondsSinceLastEventType` with the key-down event type on macOS, the same permission-free call ActivityWatch makes with the any-input type (HS 13.4; that the key-down variant also needs no permission is to be verified on current macOS); on Windows and X11 it needs an opt-in key counter that records counts only (Raw Input, XI2); Wayland has none (HS 13.4) |
| `os.focus` | `app_class` (`terminal`, `editor`, `agent_app`, `browser`, `chat_app`, `other`, `none`, `unknown`); `app_hash` (local); `hosted_sessions[]` (harness `session_key`s whose process runs under this window's process); `focus_project`. `unknown` means the client can't tell which window is in front; such a record has no `app_hash`, `hosted_sessions` or `focus_project` (2.5) | Event-driven foreground hooks (CP 3.3), and on GNOME on Wayland pairtime's GNOME Shell extension, which reports the process of the window in front. Where no source answers (GNOME on Wayland without the extension, KDE and wlroots, an X11 window manager that doesn't keep `_NET_ACTIVE_WINDOW`, an X11 connection being re-made), one record with `unknown` each time focus turns unknown, ended by the next window report. Window titles are never read unless the user opts in |
| `os.power` | `op` (`suspend`, `resume`, `lock`, `unlock`) | `PBT_APMSUSPEND`, IOKit sleep, logind `PrepareForSleep`, session lock notifications (CP 2.2) |
| `otel.active_time` | `type` (`user`, `cli`); `seconds` | Claude Code `claude_code.active_time.total` (HS 3.3). Used as a calibration reference only, never as credit |
| `cli.track` | `label` | AGENTS.md fallback. A self-declared claim from the model; never an anchor on its own (HS 11.3) |
| `relay.message` | `app` (`t3_code`); `client` (`web` or `mobile`: the app's word for where the message was typed, T3 Code's `creationSource`, 2.2); `sent_at` (the app's own stamp of the message, when it took it from the person); `thread_key` (the app's thread, keyed per device); `declared` (`none`, or what the app says sent the message: `cron` for its scheduler or a webhook, `cross_session` for another thread); `len_chars` (the typed length of the app's own text, 2.2); `low_effort`; `prompt` (the `prompt.submit` the agent paired it with, when that record came first). The envelope's `session_key` is the harness session the app bound the message's run to, when the reader knew it; the app's own ids, texts and folders never enter the record | An agent app's store: for T3 Code, a user-role message T3 stores with `createdBy` `user` and `creationSource` `web` or `mobile` (2.2). A claim: any process running as the person can write one, and it opens no anchor unless it pairs (2.2, 2.3) |

### 1.3 Derived records

The scorer builds these from the events, in this order. Each is kept on the device; only the last two are uploaded.

| Record | Fields | Built from |
|---|---|---|
| `slot` | `start` (aligned to :00, :15, :30, :45), `input_any` (bool), `input_keys` (bool or null), `present` (`yes`, `bridged`, `tail`, `returned`, `no`, `unknown`), `focus_class`, `focus_project`, `asleep`, `locked` | `os.presence`, `os.focus`, `os.power` (2.1) |
| `turn` | `session_key`, `turn_key`, `t_submit`, `t_stop`, `origin` (2.2), `prompt_class` (`substantive`, `low_effort`, `answer`, `error_paste`), `attempt` (position in a re-roll chain), `words_out`, `diff_lines`, `unread` (bool) | `prompt.submit`, `agent.*` |
| `anchor` | `anchor_id`, `kind` (2.3), `t`, `project_key`, `lead_s`, `trail_s`, `discounted_by` | Human acts that pass the evidence checks |
| `credit_slot` | `slot`, `project_key`, `credited_s` (0-15), `weight`, `reason`, `anchor_ids[]` | Section 2 and 4 rules |
| `minute_bucket` | section 5 | Four `credit_slot`s |
| `rollup` | section 5 | Buckets |

A turn runs from `t_submit` until its first `agent.stop` or `agent.interrupt`, and no later than its session's next prompt or `session.end`. Its agent records are the `agent.*` events with its `turn_key`, and those without one that its session records from its `t_submit` to the next prompt. In a session that the harness's own hooks or logs report, they will report how a turn ends, so the turn runs on until then. A turn in any other session runs only to its latest agent record, and one with none doesn't run. Either way a long gap in a turn's agent records, counted from `t_submit`, ends its run, and no later record starts it again, not even its stop. In a session the hooks or logs report, a gap of more than `BUCKET_TTL_S` ends the run `BUCKET_TTL_S` after the gap begins; in any other, a gap of more than `READ_HORIZON_S` ends it where the gap begins. Every scoring rule that asks whether a turn is running, or what came after it ended (2.4, 2.5, 2.6, 2.8, 4.2, 5.1, 5.2, 5.3, 5.4), reads this span.

The 15-second slot is the atomic unit because that is the presence sample rate. Event times keep millisecond precision. Two lengths round to the nearest second, halves up: a lead's typing part, `typed_len_est / COMPOSE_CPS` (2.3, 2.8), and the reading part of `RETURN_CREDIT` (2.1). So 61 typed characters at 1.5 a second are 41 s of typing, and 35 are 23 s. Other lengths keep their milliseconds, such as a permission wait or a command's run, and reading-bucket fills keep their fractions (2.4). A second belongs to a span the rules define, such as an anchor's window, a turn's run, a tail or the reading horizon, when the second's start is at or after the span's start and before its end. So the scorer applies windows in whole seconds inside a slot before it sums the slot.

## 2. Engagement intervals

A second is engaged when a human act opened a window that covers it, the person was present, and their attention was on that project. This section defines each of those three tests, then the one rule that combines them (2.7).

### 2.1 Presence

The client samples OS idle time every 15 s (`SAMPLE_S`; CP 3.3). Each 15-second slot gets `input_any` (any keyboard or mouse input during the slot) and, where the OS allows it, `input_keys` (keyboard input during the slot). Then each slot gets one `present` value:

| Value | Rule | Can be credited by |
|---|---|---|
| `yes` | Input during the slot | any window |
| `bridged` | No input, but the no-input run containing the slot is at most `PRESENCE_GAP_S` = 120 s long and has input on both sides | any window |
| `tail` | Within `PRESENCE_TAIL_S` = 30 s after the last input before a longer gap | any window |
| `returned` | Inside a longer no-input run (up to `RETURN_GAP_MAX_S` = 600 s) whose first following anchor on record within 600 s of the run's start is substantive and in the same project, with no lock, sleep or focus change in between. At most `RETURN_CREDIT` seconds of the run qualify (below) | prose reading (2.4) at the start of the run and that anchor's think lead at the end |
| `no` | Anything else, including any no-input run longer than 600 s | nothing |
| `unknown` | The sampler wasn't running: no daemon yet (lookback), or a machine with no desktop to sample, such as a server | the lookback rules (2.8), for records the lookback reader backfilled. A live prompt in such a slot goes through 2.2's rules as anywhere. A prompt no app message witnesses falls to one of rules 1 to 5, such as rule 4 in a session with no TTY, or to rule 8. Only a relayed anchor (2.2 rule 9) credits a live second with no presence |

Hard breaks override all of the above. A slot that overlaps a screen lock, a suspend, or a gap between consecutive samples longer than `SLEEP_GAP_S` = 30 s (twice the sample interval) is `no`, and no bridged or returned run can cross it (CP 2.2). Relayed anchors (2.2 rule 9) don't read this device's presence. Their windows are realized in a slot of any presence, a locked one included, except one the machine slept through, since a sleeping machine runs no agent. The slot's own presence value stays as sampled. Gaps are computed from wall-clock samples, never from Go's monotonic `time.Since`, which skips suspended time on Linux and macOS (CP 2.2).

The return rule. Someone who reads a long answer without touching anything, thinks, then types a careful reply should not lose that time; someone who got a drink should. pairtime can't tell them apart from the OS, so it credits a silent run only when it ends in a substantive act, and then only a bounded amount:

```text
RETURN_CREDIT = 60 * unread_prose_words / READ_WPM  +  THINK_LEAD_S
```

`unread_prose_words` counts only the prose of agent output that arrived since the person last had the session in focus with input. Diffs and tool output are left out, because nobody reviews a diff without scrolling. The reading part is placed at the start of the run, right after the tail (`READ~r`). The think part is the anchor's own think lead, where it reaches back into the run (`P~r`, `E~r`, ...). Anything between the two is `X.absent`. The anchor must be on record within `READ_HORIZON_S` (600 s) of the run's start, so the silence and the act that ends it fit in ten minutes, grit's limit from an answer to the reply (PA 1.14), and the rule never looks further ahead than reading does (2.4). An act is on record at the later of its own time and its event's `t` (1.1), so a typed command, whose `shell.command` is written when it ends, counts from its end. An act on record later doesn't count for the run, and the first one on record in time decides. "Substantive" here means a substantive prompt, answer or queued message, a typed edit, a typed command or a human commit; editor navigation, go-aheads and relayed messages don't qualify. So a 6-minute silence after a 160-word answer, followed straight away by a typed prompt, earns at most 53 + 120 = 173 s, which is close to the 120 s Hackatime would give any long gap. The same silence followed by a click in the editor earns only the 30-second tail.

Mouse-only presence never opens a window. It can only keep a window that a human act opened. That is the main defense against jigglers (TP A3 T7): a jiggler can hold `input_any` forever, but it can't submit a prompt, answer a permission request or type in the editor.

Why these numbers, compared with Hackatime. Hackatime credits `min(gap, 120 s)` between any two heartbeats, whatever happened in between (HC 1.4). That over-credits agent-only time, because transcript heartbeats arrive while nobody is there, and under-credits reading, because a long agent answer read without touching anything loses everything past 2 minutes (HC 1.4). pairtime keeps 120 s as the bridge, because reviewers already calibrate against it and it matches reading one screen of output (250-300 words at 150-240 wpm) without input. It adds two things Hackatime can't do. It requires an anchor, so agent activity alone earns nothing. And it allows a longer silent gap, up to 600 s, only when the gap ends in a substantive act and there was output to read. Six hundred seconds is grit's think-time limit and Lookout's auto-pause (PA 1.10, 1.14). Beyond that, PA 3.4 is blunt: over-crediting idle gaps is what Stardance reviewers had to deflate.

### 2.2 Prompt origin: typed, pasted, or automated

`UserPromptSubmit` fires for `/loop` iterations, background subagents and messages from other sessions, with no field saying which (HS 3.1). So every `prompt.submit` gets an `origin` from these rules, first match wins. Rules 1-5 use what the harness declares: rules 1 to 3 read every hook and log copy of the prompt, and rules 4 and 5 the session's `session.start` before it (1.2). A declaration only ever withholds credit, and no mark saying a person sent a prompt adds any. They are public, deterministic labels (the MRC "general invalid traffic" tier, TP A5). Rules 6-8 use human-side evidence.

| # | Origin | Rule | Sources |
|---|---|---|---|
| 1 | `scheduled` | Prompt text matches, by prefix on its first 1,000 characters, a `pending_crons[]` entry from an earlier `agent.stop` in the same session; or the session's scheduler fired it (`/loop`, `CronCreate`, `ScheduleWakeup`) | Claude `Stop.session_crons[].prompt`, truncated at 1,000 chars with a `… [+N chars]` marker (HS 3.1); a Claude Code transcript's `turnOrigin` `scheduled` (1.2) |
| 2 | `self_continue` | The turn continues without a new user prompt, or the prompt is a hook-generated continuation | `stop_hook_active: true` (Claude `/goal`, Copilot autopilot), Codex goal steering, Cursor `stop.loop_count > 0` with `followup_message`, Gemini `AfterAgent` deny-retry (HS 3.5, 4.5, 5.5, 6.5, 7.1) |
| 3 | `subagent` | The event comes from inside a subagent | `agent_id` on Claude events, `isSidechain` in transcripts, Codex `source.subagent` or `thread_source = subagent` (HS 12.1); Claude Code's `<task-notification>` prompt, which a transcript marks `turnOrigin` `task_notification` (1.2) |
| 4 | `headless` | The session's `entrypoint` is `sdk` or `exec`, or `parent_class` is `script_runtime`, `scheduler`, `agent` or `agent_app`, or the prompt is marked `guest`, or the session has neither a TTY nor an IDE or desktop-app host (an IDE extension has no TTY and is not headless). Shells in between are skipped, so `python farm.py` spawning `bash` spawning `claude` is still `script_runtime`. A headless session's prompts count as human only when `input_keys` shows typing in the 10 s before `session.start` and the parent is an interactive shell (a person typing `claude -p "..."` by hand). Where `input_keys` is unavailable, headless prompts never count. A prompt an app message witnesses (rule 9) skips this rule unless it is marked `guest`, and a message witnesses only a prompt of a session whose nearest non-shell ancestor is the app's own server (`parent_class` `agent_app`). Every other prompt of such a session is `headless`, whatever window hosts it | OTel `app.entrypoint` (`sdk-cli`, `sdk-ts`, `sdk-py`), Codex `source = exec`/`mcp`, `originator`; the hook binary's process-tree walk (HS 3.5, 6.5, 12.2) |
| 5 | `cloud` | The session ran on a vendor VM | Codex Cloud, Claude Code on the web, Copilot coding agent, Cursor cloud agents: no local events at all, only commits (HS 10.2) |
| 6 | `human_typed` | Input in the `KEYS_PRE_S` = 10 s before submit or the `INPUT_RELEASE_GRACE_MS` = 250 ms after it (`input_keys` where available, else `input_any`), focus at submit on a window whose `hosted_sessions` includes this session (or the editor window, for IDE harnesses), and the typed length is plausible: `typed_len_est <= TYPING_MAX_CPS * active_input_s`, where `active_input_s` is 15 s times the number of input slots in the lead window and `TYPING_MAX_CPS` = 15 chars/s | `os.presence`, `os.focus`, `prompt.submit` |
| 7 | `human_pasted` | As rule 6, grace after submit included, but the plausibility test fails or `pasted_chars > 0`. The submit is still a human act. The typed length used for compose credit is cut to `min(typed_len_est, TYPING_MAX_CPS * active_input_s)` | as above |
| 8 | `no_input` | None of the above, and rule 9 doesn't make the prompt `relayed`. Not an anchor. Counted toward the reviewer flag `no_input_prompts` (grit's rule: at least 2 prompts and a quarter of all prompts) | |
| 9 | `relayed` | An app message witnesses the prompt (below), the prompt isn't marked `guest`, its session's nearest non-shell ancestor is the app's own server (`parent_class` `agent_app`), and none of rules 1 to 7 labels it. It comes before rule 8, which takes the prompts it leaves. An anchor with the shapes of 2.8, at level C (2.3) | The agent app's own store (T3 Code) |

Declared flags (rules 1-5) catch honest automation, which is most of it, and they label it instead of calling it fraud (HS 12.1). They don't survive a determined cheater, who can drive the interactive TUI with `tmux send-keys` or an LLM-controlled terminal (HS 12.1). Rule 6 is what stops that: those tools write to the pty, not the OS input queue, so OS idle keeps rising and the prompt has no input before it (TP A3 T3). Rule 6 is weaker where only `input_any` exists (Windows and X11 without the opt-in key counter, all of Wayland), because a jiggler satisfies it. Section 5 marks those minutes `evidence = B`, and the Risks section comes back to this. Where the client can't tell which window is in front (an `os.focus` with `app_class` `unknown`, 2.5), rule 6's focus test can't be applied and passes for every session, so a script typing into a terminal while the person uses another app passes too; every second it supports is evidence level C (2.5, 5.1).

The grace after submit in rules 6 and 7 is for the key that sends the prompt. The hook binary stamps `t` when it starts (1.1), after that key's press, and the agent takes a presence sample for the hook a few milliseconds later. A sample only says when the last input before it came. If the key's release lands between the two, the sample dates the last input after `t` and hides the typing before it, so a typed prompt would score `no_input`. The release comes less than one key press after `t`, and nothing tells it apart from the typing. Nothing else takes the grace: rule 4's test before `session.start`, focus at submit, the plausibility test, presence, the typing part of a lead (2.3) and the other anchors' evidence tests read input as before. For a script driving the pty, the grace adds a quarter of a second to the 10 s in which a real key press would have to come.

**Relayed prompts.** An agent app runs agents on one machine and takes messages for them from its desktop window, a browser or a phone, often on another device. T3 Code works this way. This device's OS sees none of that typing, and rule 4 takes every prompt of a session the app's server runs (`parent_class` `agent_app`). Rule 9 counts the ones a person sent, on the app's word, under these rules:

- **What witnesses a prompt.** A `relay.message` (1.2) of `declared` `none`. For T3 Code that is a user-role message that passes T3's own test for a message a person sent from a client: `createdBy` `user`, `creationSource` `web` or `mobile`, no `scheduledTaskId`, and a run of its own, which a message T3 steered into a running turn doesn't have. It must also carry none of T3's other marks of a message nobody typed (`idleCompaction`, `senderThreadId`, `linkedSender`, `workflowScript`, `notification`, `delegatedCompletion`) and no guest's `context.guest`. And the reader must have first seen it at most `RELAY_SEEN_LAG_S` after T3 created it, which turns away rows T3 imported from old sessions and rows written with an old stamp. A message T3 made itself, with `creationSource` `server`, never witnesses anything: feedback on a request, an answer to the agent's question, an imported session. Nor does a message whose row changed after the reader first saw it, or whose `updated_at` was already later than its `created_at` when the reader first saw it: T3 lets an agent edit a queued message's text, keeps its author and its stamp, and stamps only `updatedAt`. A guest's message never witnesses anything either: another person's typing doesn't credit the owner. The reader tells the agent which runs a guest's message started, and the prompt such a run delivers is marked `guest`: it keeps no text on this device, and rule 4 takes it whatever its session, even when a message witnesses it.
- **The app's own declarations.** The adapter records only the messages T3 Code stores with `createdBy` `user` and `creationSource` `web` or `mobile`, and gives each the `client` its source names. An agent's message, `createdBy` `agent`, gets no record, so nothing witnesses its prompt and rule 4 takes it. Among the recorded ones, T3 marks a message its scheduler or a webhook fired with `scheduledTaskId`, and one another thread sent with `senderThreadId` or `linkedSender`. The adapter records those with `declared` `cron` and `cross_session`, and rules 1 and 3 read them as they read the harness's own declarations: the prompt such a message pairs with is `scheduled` or a cross-session `subagent`. T3's own test for a message nobody attended is the same one, `createdBy` other than `user` or a `scheduledTaskId`.
- **Pairing.** By the app's own structure, never by text. A message pairs with the prompt its own run delivered. T3 Code starts a run for the message, the run goes to a provider thread whose Claude Code session id T3 chose, and the run reaches the harness as the first `prompt.submit` in that session at or after the run starts. A message whose run the app can't tie to a session witnesses nothing. One run gives one prompt, so one message pairs with one prompt and one prompt with one message, and the prompt must come no earlier than the message's stamp `sent_at` and at most `RELAY_MATCH_S` after it. The agent writes the pairing on whichever of the two records arrives second, as the prompt's `relay` or the message's `prompt` (1.2), and the scorer reads only that link, checking its session and times. Whether Claude Code reports a message T3 steers into a running turn at all is unverified, so a steered message earns nothing until a capture shows what Claude Code records for it.
- **Rule 4 and the app's server.** A message witnesses only a prompt of a session whose nearest non-shell ancestor is the app's own server process, `parent_class` `agent_app` (1.2). Rule 4 lists that class, so every prompt of such a session is `headless` unless a message witnesses it, and a witnessed one skips rule 4 unless it is marked `guest`. A chain that reaches a script runtime, a scheduler or another harness first stays headless, so a script that posts each of its prompts to the app and also runs them through `claude -p` still earns nothing. A row a script writes for a tmux or TTY session witnesses nothing: rules 6 to 8 read that prompt as they read any. The class comes from the server's process name, which a script can give itself, so this test sorts honest setups and bounds nothing on its own (Risk 12).
- **Rules 6 and 7, at the stamp.** For a witnessed prompt, rules 6 and 7 read the app's stamp in place of the prompt's `t`: input in the `KEYS_PRE_S` before `sent_at` or the `INPUT_RELEASE_GRACE_MS` after it, focus at `sent_at` on a window that hosts the session, and the plausibility test over a lead that ends at `sent_at`. The app's own window on this computer hosts every session its server drives (3.2). So a message typed into T3 Code's desktop app here is `human_typed` or `human_pasted`, at the usual level, with its lead ending at the stamp. A message whose record says `client` `mobile`, T3 Code's `creationSource` for its phone app, skips rules 6 and 7, since no input here typed it, and is relayed whatever this device's input and focus say.
- **Otherwise the prompt is `relayed`.** Its typed length and its class come from the app's own text, never from the prompt the hook saw, which carries what the app adds. The app's machine strings come off first: for T3 Code, a leading `Ultrathink:` line, the placeholder it stores for a message with attachments and no text, and the tokens of its context references. The length never exceeds the hook's `typed_len_est`. T3 Code records no paste flag, so the length is an upper bound on what was typed.
- **What a relayed prompt is.** A person's prompt for its own turn: the turn resets the bucket at pickup (2.4), its running seconds are `X.agent_only`, and 4.2's detectors count it. For everything else it is no human anchor at all (2.3).
- **What isn't a message.** An approval, an answer to the agent's question or a stop given in the app's own interface reaches the harness without a message, so it earns nothing in v1.

Each origin label is attached to the turn and shown in the timeline. Only `human_typed`, `human_pasted`, `relayed` and the human acts inside other turns (a permission answer during a `/loop` turn, say) create anchors.

Then each human prompt gets a `prompt_class`, from local rules on the text:

- `low_effort`: the normalized prompt (lowercased, punctuation and whitespace stripped) is under 4 characters or on the go-ahead list (`yes`, `y`, `ok`, `okay`, `sure`, `continue`, `go`, `go on`, `go ahead`, `do it`, `proceed`, `next`, `keep going`, `lgtm`, `looks good`, `sounds good`, `thanks`, `try again`, `again`, `retry`, `fix it`, `more`, and a pick such as `1`, `2`, `option 2`), and the previous agent output did not ask a question. The list is grit's, extended (PA 1.14).
- `answer`: the previous `agent.output` had `asks_question`, or the harness showed a question dialog (Copilot's `elicitation_dialog` notification, HS 5.1). A one-word answer to a real question is a decision, not a go-ahead (PA 2.2, autonomy study; vibe-log's rubric scores such answers 80-100, PA 1.5).
- `error_paste`: the prompt contains an error signature (4.2) and fewer than 40 typed characters outside it.
- `substantive`: everything else with `typed_len_est >= SUBSTANTIVE_CHARS` = 20. A human prompt under 20 typed characters that matches none of the above counts as `low_effort`.

The class decides the lead window (2.3) and feeds the dark-flow rules (4.2). It never multiplies credit. A content classifier that scores prompts as "inquiry" or "delegation" (PA 3.2 S4) is reported as a label for the recap and for reviewers, but v1 keeps it out of the number: it would be the one part of the rule a reviewer can't recompute, and HADT found two-thirds of the variance is within each student, by phase (PA 2.2), so a classifier's errors would land unpredictably.

### 2.3 Anchors: the human acts that open windows

An anchor is a human act that passes its evidence test. Each anchor opens a lead window before it and a trail window after it. Both are realized only in slots where the person is present and focused on the anchor's project (2.5).

| Anchor | Evidence test | Lead before `t` | Trail after `t` | Reason code | Activity |
|---|---|---|---|---|---|
| Substantive prompt | origin 6 or 7, class `substantive` or `error_paste` | `THINK_LEAD_S` (120 s) + `min(COMPOSE_MAX_S, typed_len_est / COMPOSE_CPS)` | 0 (the reading bucket covers the agent's start, 2.4) | `P` | `prompting` |
| Answer to an agent question | origin 6 or 7, class `answer` | 60 s + typing time as above | 0 | `ANS` | `prompting` |
| Go-ahead | origin 6 or 7, class `low_effort` | `LOW_LEAD_S` (20 s), presence only | 0 | `GO` | `prompting` |
| Queued message | `agent.queue` `enqueue` with input in its slot and focus on the host window | as a prompt of its class | 0 | `Q` | `prompting` |
| Permission answer | `agent.permission` with `decided_by = user`, presence at `answered_at`, latency at least 300 ms | the whole wait from `requested_at`, up to `PA_MAX_S` (120 s) | 10 s | `PA` | `reviewing` |
| Interrupt | `agent.interrupt` with presence in its slot | 30 s | 10 s | `INT` | `reviewing` |
| Typed edit | `editor.activity` `edit` or `save` with `edit_source` `human`, `tab` or `unknown`, input in its slot (`input_keys` where available), editor focus on the same project | `EDIT_LEAD_S` (120 s) | 0 | `E` | `hand_edit` |
| Editor navigation | `editor.activity` `navigate`, `open`, `debug` or `task` with input in its slot and editor focus | `EDIT_LEAD_S` (120 s) | 0 | `NAV` | `reviewing` |
| Typed command | `shell.command` with `interactive = true` and keyboard input before it; or a user shell action inside a harness, such as Codex's `task.user_shell` (HS 6.3) | `CMD_LEAD_S` (60 s), plus the run itself up to `CMD_WATCH_MAX_S` (300 s) | `CMD_TRAIL_S` (60 s) | `CMD`, or `TEST` when `cmd_class = test` | `testing` for tests and builds, else `debugging` |
| Human commit | `git.change` `commit` with `actor = human` and input in its slot | `COMMIT_LEAD_S` (60 s) | 0 | `G` | `reviewing` |
| Relayed message | origin 9 (2.2) | typing time only, `min(COMPOSE_MAX_S, typed_len_est / COMPOSE_CPS)` of the app's own text, capped at the seconds since the thread's previous `relay.message` of `declared` `none`, and ending at the app's stamp `sent_at` | 0 | `P`, `ANS` or `GO`, by class | `prompting` |

A hook and a log reader can both report a queued message: Claude Code runs `UserPromptSubmit` as the person queues it, and its transcript records the `enqueue` a few milliseconds later. Both copies carry the running turn's `turn_key`, which every message queued in that turn shares, so time and length tell the messages apart. A log reader's `agent.queue` and another path's, of the same session, `turn_key` and `len_chars` and within `TRACK_LOG_MATCH_S` = 10 s of each other, are one message, paired one to one, oldest first. It opens one `Q` anchor, which goes by its most trusted copy and is corroborated when one of its copies came through a hook (DESIGN 6.4). A pair always has a log reader's copy and one from another path, so two hook records are two messages, however close together, and so are two log records.

A relayed anchor's event is its `relay.message`, and the anchor stands at that record's `sent_at`, the app's stamp: when the app took the message from the person, not when the reader saw it or the agent got it. Its lead is typing time only, whatever its class, so a relayed go-ahead earns its typing (`yes`, 2 s) and not `LOW_LEAD_S`, and a relayed answer has no 60 s to decide. The typed length is the app's own, as 2.2 says: T3 Code keeps a paste under 32 KB inline, where it counts as typed, and a longer one as an attachment, which doesn't count. The cap at the seconds since the thread's previous message keeps two long messages sent a minute apart from claiming 300 s each.

Relayed credit lives in this device's slots like any credit, under its anchor's project and reason code, and it ignores everything those slots record about the person. The lead is realized in every second of a slot of any presence except one the machine slept through (2.1), whatever its focus and whatever `input_keys` says. It is the one lead the keyboard rule below doesn't touch, nor `COMPOSE_NEEDS_KEYS`, nor a `track` anchor's typing rule, because the typing happened on another device. Every second it credits is level C with the label `via_relay` (5.1), and its witness label is the live one. It earns no return credit.

A relayed anchor waits for its pairing. The minutes its lead touches can't become final until a prompt pairs with the message or `RELAY_MATCH_S` passes, and if none has paired by then, the anchor opens nothing. A lead starts at most `COMPOSE_MAX_S` before `sent_at`, so the wait never holds a minute past the time it would be final anyway (8.1). So a message an app queued behind a running turn for longer than `RELAY_MATCH_S` earns nothing. A pairing reaches back from `sent_at`, not from the later of its two records: the lead and the reading a message vouches for (2.4) both lie within `READ_HORIZON_S` before `sent_at`, so that bound never cuts them. A pairing changes nothing in a minute that is already final.

A relayed anchor credits only its own lead and its own session's reading (2.4). It is no human anchor for anything else: not for another session's reading or its level, not for a returned run (2.1), not for a lookup's brackets (2.5).

How a lead is realized. A lead has two parts: the think part (the fixed allowance) comes first and needs only presence; the typing part (`typed_len_est / COMPOSE_CPS`, rounded to the nearest second as 1.3 says) comes last, just before the anchor. Where `input_keys` exists, typing seconds are realized only in slots with keyboard input, except a relayed anchor's (above). Otherwise a jiggler plus one real 300-character prompt every few minutes would buy 200 s of "typing" that never happened. Where `input_keys` doesn't exist, the whole lead needs only presence, and the minutes are level B (5.1).

WSL needs one extra hop. A harness inside WSL2 runs its hooks as Linux processes, and WSL has no input devices, so pairtime runs a Linux agent inside WSL that hands records to the Windows agent through a spool directory on the Windows side, and the Windows agent supplies presence and focus (CP 6.4). The Windows terminal window hosting `wsl.exe` counts as hosting the WSL sessions.

Some reasons behind the windows:

- The 120 s think lead is the same allowance Hackatime gives every heartbeat gap, given here only to substantive acts. Planning before a prompt, reading code before an edit and deciding before a command are where much of the struggle is (PA 2.1), and they leave no event of their own.
- Typing time uses 1.5 characters a second, which is 18 words a minute at 5 characters a word. Karat, Halverson, Horn and Karat (CHI 1999) measured keyboard entry at 33 wpm for transcription and 19 wpm for composition ([HFI summary](https://www.humanfactors.com/newsletters/human_interaction_speeds.html); the ACM PDF returned 403). Writing a prompt is composition. The 300 s cap matches grit's 4-minute ceiling with a minute of slack (PA 1.14).
- Go-aheads get 20 s, not 0. Deciding that the agent's plan is fine is a small act of review; grit gives bare go-aheads up to 90 s of think credit in total (PA 1.14), and 20 s plus the reading bucket comes out close to that.
- Permission answers and interrupts are the strongest single signals of supervision. Experienced users interrupt in about 9% of turns and auto-approve more (PA 2.2, autonomy study), so pairtime credits the decision itself but never counts auto-approval against anyone. An answer faster than 300 ms is a script, not a person, and is dropped and flagged.
- Editor anchors mirror Hackatime for hand coding. WakaTime plugins send a heartbeat at most every 120 s in one file (HC 1.4), so a 120 s lead gives continuous typing the same credit Hackatime would, but only while the editor is focused and input is arriving.
- A command's run is credited up to 5 minutes. Watching your own test suite is verification (PA 2.2, Barke); watching a 20-minute build is waiting.

### 2.4 Reading and review: the per-session reading bucket

Verification is the largest human activity in AI-assisted coding: 22.4% of session time in CUPS (PA 2.2). Reading leaves almost no events, so pairtime can't observe it directly. It estimates how much reading the agent's output could plausibly take, and credits present, focused seconds up to that amount.

Each harness session has a reading bucket `b`, a FIFO queue of `(fill_time, seconds)` entries. Visible agent output fills it while the agent works:

```text
fill = R_TURN_BASE_S                              (20 s, once per human-owned turn, at submit)
     + 60 * prose_words  / READ_WPM               (180 words a minute)
     + 60 * code_lines   / DIFF_LPM               (8 lines a minute; fenced code in replies)
     + 60 * diff_lines   / DIFF_LPM               (lines added + removed by edit tools)
     + 60 * min(visible_output_lines, TOOL_LINES_MAX) / DIFF_LPM   (4 lines per tool call)
```

- Only displayed text counts: assistant prose, code blocks, diffs, the few lines of tool output a harness shows by default. Hidden thinking and collapsed tool output don't.
- Each human prompt in the session resets `b` to its own 20 s base when its turn starts (for a queued message, when the agent picks it up). Sending the next prompt means the person has moved on, so the budget for reading older output goes. Automated turns (`/loop`, `/goal` continuations) don't reset it, so output from an unattended run accumulates for whoever comes back to read it.
- `b` is capped at `BUCKET_MAX_S` = 600 s. New output past the cap evicts the oldest entries, so the bucket always holds the most recent output, which is what a person reads first. Entries older than `BUCKET_TTL_S` = 2 h are dropped. Draining takes the oldest entry first.
- `b` drains one second per second in slots where the person is present (`yes`, `bridged` or `tail`) and focused on any window of the session's project, and where no anchor window already covers the second (2.7). In `returned` slots only prose entries drain, within the return rule's limit (2.1). Drained seconds are credited as `SUP` (activity `supervising`) while the agent is still running and `READ` (activity `reading_output`) after its stop.
- Draining only counts within `READ_HORIZON_S` = 600 s of a human anchor in the same project, before or after. Discounted anchors (4.2) don't count for this. Reading that follows a prompt is checking the answer; reading that comes right before a prompt, edit or command is acting on it. Reading followed by nothing and preceded by nothing is not credited, which is what keeps a jiggler plus a `/loop` from earning anything (TP A3 T2, T7).
- A discounted turn (4.2) resets `b` to zero instead of to the base, and adds nothing to it. Re-rolling means the person has already judged the last answer and thrown it away.
- A relayed anchor's session reads in 2.8's shape, whatever this device's presence and focus say. The relayed prompt resets the bucket to its 20 s base when the agent picks the prompt up. In a session an agent app's server runs, relayed reading draws on at most `UNKNOWN_BUCKET_MAX_S` = 120 s of the bucket from the session's first fill on, whatever reset came last. It drains the bucket, as `READ`, only after one of the session's stops, and only when a relayed anchor (2.3) of the thread vouches for that stop: a message whose prompt is relayed, not one that paired with nothing or whose prompt another rule took. A stop's reading is vouched for by the first relayed anchor sent after the stop among those that pair, when the later of its record and its prompt arrives, within 840 s of the stop: a minute inside the 900 s for which the stop's own minute stays open to late records, so that minute is still pending. That anchor must be sent within 600 s of the stop, or nothing vouches. One that pairs later still earns its own lead, but it neither vouches for that stop's reading nor ends it. The reading ends when the anchor that vouches for it is sent or the session's next turn starts, whichever comes first. A message sent before the stop, as one the app queued is, doesn't vouch for that stop. Output from an automated turn fills the same bucket, and its stop opens reading the same way. No relayed session reads while its agent runs, so relayed credit has no `SUP`. Every such second is level C with `via_relay`. In the same session, reading that this device's presence and focus back, within `READ_HORIZON_S` of an anchor that isn't relayed, follows the rules above and draws on the whole bucket, up to `BUCKET_MAX_S`.
- Output from automated turns (`scheduled`, `self_continue`, `subagent`) fills the bucket like any other output. The human who comes back to read what a `/loop` did is doing real review. The 600 s cap and the anchor horizon bound how much of it can count.

The rates. Brysbaert's meta-analysis of 190 studies puts adult silent reading of English non-fiction at 238 wpm, with most adults between 175 and 300, and says rates "are lower for children, old adults, and readers with English as second language" ([Brysbaert 2019](https://doi.org/10.1016/j.jml.2019.104047); PA 3.2 S5). US twelfth graders averaged 192 wpm on grade-level text with comprehension checks in 2011 (Spichtig et al., Reading Research Quarterly 2016, [ERIC EJ1094505](https://eric.ed.gov/?id=EJ1094505); the 192 figure is from a [secondary summary](https://www.howtolearn.com/silent-reading-levels-lower-than-1960/), to check against the paper), and younger students read slower. 180 wpm sits just below the twelfth-grade mean. Since it fills a ceiling rather than a quota, a slow reader shouldn't run out of budget. For code, Kemerer and Paulk found 200 LOC/hour or less was "an effective rate for individual reviews", finding more than half of code defects, and treat faster review as ineffective ([IEEE TSE 2009](https://doi.org/10.1109/TSE.2009.27), 371 and 246 PSP programs). Eight lines a minute is 480 LOC/hour, 2.4 times that rate, because a teen reading an agent's diff to decide what to do next is checking intent, not hunting defects. Both rates are v1 guesses to fit in the pilot (7).

Why a bucket instead of a window after each stop. It credits reading whenever it happens: during the run (watching diffs appear), after the stop, or after coming back from a break. It credits nothing for watching a spinner, because a spinner produces no output. And a long, busy run is never penalized: an expert's prompt that sets off 12 actions (PA 2.2) fills the bucket faster, so the person watching it gets more credit, not less.

### 2.5 Focus and lookups

Each slot gets `focus_class` and `focus_project`: the foreground window that held focus for the most time in the slot, all its stretches there added up, and the project it resolves to (3.2). A tie goes to the one in front first. The whole slot counts as that window's, so a lookup is a whole number of slots and a few seconds in another app inside the slot count as the window's too. This slot focus decides where leads and trails are realized, where a bucket drains, which slots form a lookup and which project's candidates survive (3.3). Windows are realized only when the focus is a terminal, editor or agent app resolved to the window's project. Slots with input in an `other` app (a game, a video, a chat client) are `X.other_focus` and credit nothing, even inside a window.

Browsers and desktop chat apps are ambiguous: they might hold documentation or a video. Without titles or URLs, v1 credits them only as short lookups between project work:

- A run of browser or chat-app slots counts as `LOOKUP` (activity `lookup`) for project `p` when it starts within `LOOKUP_BRACKET_S` = 30 s after a credited slot of `p`, ends within 30 s before a credited slot of `p`, and no agent turn in `p` is running during it. Since every credited slot traces back to an anchor, this means there was a human act on both sides. Relayed credit (2.2 rule 9) is never a bracket.
- Nothing the harness reports shows when an agent stops in a session that its hooks or logs don't report (1.3). So in such a session, nothing the session credits, by a window of one of its anchors or by reading drained from its bucket, can be the credited slot a run starts after. Other credit of `p` in the 30 s before the run can, such as a typed edit's lead, and the slot the run ends before can be credited by anything in `p`.
- The run is credited up to `LOOKUP_MAX_S` = 180 s; the rest is `X.lookup_cap`.
- During an agent run, browser time is waiting, not lookup, unless the browser extension (v2) marks the tab as documentation.

Looking up documentation is a named activity in CUPS and one of Barke's exploration-mode validation strategies (PA 2.2), so it should count. For a web project the browser is also where the person checks the running app, and the 180 s cap under-credits long manual tests there; the v2 browser extension fixes that by classifying `localhost` and dev-server tabs as `testing` for the project. But the lookup rule is the easiest one to farm, which is why it needs anchors on both sides and has the shortest cap.

**Focus unknown.** Where the client can't tell which window is in front, it records an `os.focus` with `app_class` `unknown` (1.2), and a slot that record holds longest gets `focus_class` `unknown` and no `focus_project`. Such a slot, and an act whose window in front is unknown, passes every focus test of sections 2 and 3 for every project: rule 6's window at submit (2.2), the focus in 2.3's evidence tests, where windows are realized, where buckets drain (2.4), the focus a returned run needs held through its silence, with an input slot of unknown focus counting as one where every session was in focus (2.1), and rule 1 of 3.3. Presence, input and every other test still apply. Where candidates of several projects want a second, 3.3's rules 2 to 6 choose among them, and buckets of different projects go to the project whose human anchor qualifying the drain (2.4) is nearest the second, the earlier anchor on a tie, then the lower `project_key`. Every second credited in such a slot is evidence level C with the label `focus_unknown`, and so is every second credited from an anchor that passed its focus test only this way (5.1). An uncredited second in such a slot takes 5.2's codes without `X.other_focus` and `X.ambiguous`, and the turn step reads every project's running turns. Such a slot is never a browser slot, so it holds no lookup, and where presence is unknown too, 2.8 applies instead. The level is C because pairtime can't tell project work there from a game or a video in the next window: a script typing into an agent's terminal while the person plays would pass every test that is left (2.2).

### 2.6 The agent running: waiting, watching, walking away

Agent time is never credited as such. During a run, the human is in one of four states, and each is handled by the rules above:

| State | Evidence | Credit for this project |
|---|---|---|
| Watching | Present, focused on the project | Drains the bucket as output appears (`SUP`); a turn that produces only a spinner earns its 20 s base |
| Steering mid-run | Interrupt, permission answer, queued message | That anchor's window (`INT`, `PA`, `Q`) |
| Working elsewhere | Present, focused on another project | Nothing here; the seconds go to the other project if its windows cover them (3) |
| Waiting on something else, or gone | Focus in an `other` app, or no presence, lock or sleep | Nothing (`X.other_focus`, `X.absent`). The run is `agent_only` |

Agent-only seconds (wall time of agent runs that no human second covers) are reported in every rollup, so the user sees the chess clock: your time next to the agent's (PA 4.3). They are never submitted as struggle (HS 17.2).

### 2.7 The rule that combines them

For each second `τ` in the user's global timeline, on each device:

```text
candidates = []
# on(s, p): focus_project(s) == p, or focus_class(s) == unknown (2.5)
for each anchor A that isn't discounted (4.2) and whose lead or trail window contains τ:
    if τ is in the typing part of A's lead and input_keys(slot(τ)) == false:
        continue                                   # keyboard known, and silent (2.3)
    if present(slot(τ)) in {yes, bridged, tail}            and on(slot(τ), A.project)
       or present(slot(τ)) == returned and τ is in A's think part, inside the return credit (2.1):
        candidates += (A.project, A.reason, priority 1)
for each session σ with b_σ > 0 and an undiscounted human anchor in σ.project within READ_HORIZON_S of τ:
    if (present(slot(τ)) in {yes, bridged, tail}
        or present(slot(τ)) == returned and τ is in the reading part of the return credit)
       and on(slot(τ), σ.project):
        candidates += (σ.project, SUP or READ, priority 2)
if slot(τ) is in a qualifying lookup run for project p:
        candidates += (p, LOOKUP, priority 3)
choose one candidate by the attribution order in 3.3; credit τ to it (draining b_σ if READ/SUP)
level = C, label focus_unknown, if focus_class(slot(τ)) == unknown or the anchor passed its focus test only through unknown focus (2.5)
weight = 0.5 under the no-progress cap (4.3), else 1
with no candidate, τ is uncredited under one code (5.2), a loop's code if scoring with no turn discounted would credit it (4.2)
```

Relayed candidates (2.3, 2.4) come after all of these. They credit a second only when no other candidate wants it, whatever the slot's presence and focus, and 3.3 rule 1's focus test doesn't apply to them: this device's own evidence beats the app's word. A second two relayed sessions want goes to the session whose next relayed anchor's lead holds it. When both sessions' next leads hold it, it goes to the session whose anchor is sent soonest after it, and when neither does, to the session whose latest relayed message is the most recent. A session with none yet counts as the oldest, ties go to the lower `project_key` and then the lower `session_key`, and 3.3's rules 2 to 6 then choose among the winner's candidates.

The per-second results are summed into 15-second credit slots, then into minute buckets (5). Each slot keeps the reason codes and anchor ids of the seconds in it, so a reviewer can follow any credited second back to the event that opened its window.

### 2.8 Lookback mode: presence unknown

On first install, pairtime reads up to 30 days of transcripts (Claude Code, Gemini CLI) and older Codex and Copilot logs (HS 2, finding 5) with no OS data behind them. Zach allows a lookback mode if it's good enough (BOUNTY.md). For the records the lookback reader backfilled, whose slots are all `unknown`:

- Prompts can't be checked for input, so origin rules 1-5 still apply and every remaining prompt is treated as `human_typed`, at evidence level C (5.1).
- Leads shrink to typing time only (`typed_len_est / COMPOSE_CPS`, no think lead). Permission answers keep their wait, up to 60 s.
- The bucket cap drops to `UNKNOWN_BUCKET_MAX_S` = 120 s per turn, and reading is credited only between a stop and a human prompt that follows within 600 s.
- Focus is unknown, so a second wanted by two parallel sessions goes to the one with the most recent human prompt.

Everything credited this way is marked `presence = unknown` and `witness = backfilled` (SP 8.4). It shows on the user's dashboard from day one (R3), and programs see it as the lower-trust tier it is (HS 17.14).

These rules are for backfilled records only. A live slot whose presence is unknown, such as every slot on a server with no desktop, scores its prompts by 2.2 as written, and nothing lookback does with rule 4 reaches it. Relayed anchors (2.2 rule 9) borrow this section's lead and reading shapes for live slots, as 2.3 and 2.4 say, with the live witness label and with credit for their own session's reading only.

## 3. Multi-project attribution

R4's third story is the one most trackers get wrong: "When you are multitasking and jump between 5 different projects in an afternoon, Hackatime accurately counts that time for each project." With agents it is worse, because five agents can run at once while one person looks at one of them. METR stopped trusting its own task timings once developers ran agents concurrently (PA 2.2).

### 3.1 One timeline, one attention stream

pairtime keeps one timeline per user. Every second is credited to at most one project, so per-project totals always add up to the user's total, with nothing counted twice (HC Design implication 7). Hackatime's dashboard breaks this: its per-project path computes gaps inside each project, so heartbeats A@0 s, B@30 s, A@60 s, B@90 s give A 60 s and B 60 s for 90 s of wall time, and Flavortown keeps an audit table of the difference (HC 1.4). pairtime does what Hackatime's `attributed_durations_by` does instead: one global order, each second goes to exactly one bucket.

Agents don't get a share of the stream. Five agents running in five repos produce five buckets of unread output, but only the project in front of the person can drain its bucket. The others wait, up to their 600 s cap and 2 h TTL, for the person to come and read them.

### 3.2 Resolving the project of a slot

`project_key` comes from the session's `cwd`, resolved to a repo by its root-commit set, or a hashed folder path when there's no git (SP 4.2). `focus_project` for a slot is resolved in this order:

1. **Terminal.** The focused window's process tree is walked once per focus change: the window's process comes from the foreground calls in CP 3.3, its descendants from a process snapshot (Toolhelp32 on Windows, `proc_pidinfo` on macOS, `/proc` on Linux). Harness sessions whose process runs under it are its `hosted_sessions`. One hosted session gives its project. Several (tabs, panes, tmux) are harder, because tab switches are invisible to the OS. The slot goes to the session whose next human anchor in this window comes soonest, if the slot lies inside that anchor's lead; otherwise to the session with the most recent human anchor within `ATTRIB_STICKY_S` = 120 s; otherwise the slot is `ambiguous` (anchors still count, the bucket doesn't drain). Looking ahead is right more often than looking back: the seconds before a prompt is sent are almost always spent in the tab it is sent from. A terminal hosting no harness resolves through the shell hook's reported `cwd` when that is installed (v1.5), else `ambiguous`.
2. **Editor.** The project of the most recent editor heartbeat with input in the same or previous slot. Heartbeats come from the focused window, because they need activity, so this tracks which of several editor windows is in front without reading titles.
3. **Agent desktop app** (Claude desktop's Code tab, Copilot app, T3 Code's window): the `cwd` of the session it hosts, by the same process-tree walk. An app hosting several sessions, as T3 Code's window hosts every session its own server drives, resolves a slot the way rule 1 resolves a terminal with several.
4. **Browser, chat app.** No project. Only the lookup rule (2.5) can credit these slots.
5. **Other.** No project, never credited.
6. **Unknown.** An `os.focus` with `app_class` `unknown`: the client can't tell which window is in front. The slot has no project; every project's windows and buckets may use it, at evidence level C (2.5).

### 3.3 When two candidates want the same second

After 2.7 has collected candidates for a second, the second goes to one of them:

1. Only candidates whose project equals the slot's `focus_project` survive, except lookups (resolved by their brackets), returned slots (focus had to be on the anchor's project throughout the gap anyway) and slots whose focus is unknown, where every candidate survives and 2.5 says which project's bucket drains.
2. Anchor windows beat the reading bucket, which beats lookups. Being in the middle of an act is better evidence than having output to read.
3. Among anchor windows, the lead of the next anchor at or after the second wins over the trail of an earlier one. Time just before an act is preparation for it.
4. Among buckets in the same project, the oldest entry drains first (FIFO across sessions).
5. Lookup runs must have both brackets in the same project. A browser run between project A and project B is not credited to either.
6. Anything still tied goes to the lower `project_key`, so the result is deterministic.
7. Relayed candidates (2.2 rule 9) skip rule 1 and come after every other candidate, as 2.7 says, which also says how two relayed sessions share a second.

### 3.4 Across devices

Each device scores its own slots. The server merges them: one human-minute per wall-clock minute per account across all devices (TP A3 T11). When two devices credit the same slot, the server keeps the slot with more credited seconds, then the one with `input_keys`, then the lower `device_id`, drops the other, and raises `concurrent_devices` for the review queue. Two devices with simultaneous keyboard input usually means two people on one account (TP A3 T17).

An SSH or cloud dev box is the known gap. The person types on the laptop, so the remote machine's daemon sees prompts with no input and labels them `no_input`. v1 doesn't credit them. A v1.5 join, where the server matches the remote device's prompt times to the local device's keyboard input with an SSH client focused, is in Open questions.

Agent apps close part of the gap. A person who drives agents on a server through T3 Code's desktop app, its web client or its phone app types on another device, and the server's daemon sees the prompts with no input. Where the app's own store says a person sent the message, the prompt is `relayed` (2.2 rule 9) and earns 2.8's shapes on the server, at level C. In v1 a server's relayed slot merges like any other device's (above): a laptop and the server crediting the same minute raise `concurrent_devices`, though one person drove the agent from the laptop. A merge that tells an all-relayed slot apart needs a slot to say so, and waits for v1.5 with the join below. The other half of the join is v1.5: a linked device of the same account that saw keyboard input with the app's client in focus just before the message would raise it to level B. That is Open question 11's remaining half.

### 3.5 Time attached to commits

Attribution decides the project. Commits come after it: credited seconds in a project between a commit's parent and the commit are that commit's `credited_seconds_since_parent`, and time after the last commit shows as uncommitted (SP 4.3, 4.4). Rebases move the time to the new SHA, and merges never count it twice, because commits only label seconds that the global timeline already assigned once (SP 4.4).

## 4. Productive and unproductive time

Sections 2 and 3 decide whether a person was engaged with a project. This section decides whether that engagement was struggle worth crediting. The vision post draws the line: "if you are in dark flow or doing the same thing over and over again, that shouldn't count" (BOUNTY.md).

### 4.1 Working definition

Struggle is effort toward something not yet understood but within reach (Hiebert and Grouws, PA 2.1). In AI-assisted work the human's part of it is steering, verifying, integrating and debugging (PA 2.2, Lee et al.; PA 3.1). The credit rule already restricts credit to those acts and the reading around them. What it still has to remove is engagement that is real but empty: the loops where a person keeps pulling the lever without changing anything, which is what Dixon et al. called dark flow at the slot machine and Thomas applied to vibe coding (PA 2.2; HC 5).

Kapur's four quadrants (PA 2.1) set the policy for each case:

| Quadrant | In logs | Policy |
|---|---|---|
| Productive failure | Errors, then the human reasons, edits, tests | Full credit, even with no success yet |
| Productive success | Specific prompts, quick accepts, steady progress (Barke's acceleration mode) | Full credit; speed is not disengagement |
| Unproductive success | A one-shot that works | Earns its few minutes and nothing more, by construction |
| Unproductive failure | Re-rolls, rewrite and fix-it loops, unread go-aheads, hours with nothing to show | Zero for the loops (4.2), half for long stretches with no progress (4.3) |

### 4.2 Dark-flow loops

All four detectors run on the device, on prompt and diff data that never leaves it. Only their outcomes (reason codes, counts, similarity scores) are uploaded.

**Normalization and similarity.** Before comparing prompts, the client lowercases the text, removes pasted blocks and error text (kept separately as an error signature), replaces numbers with `<n>`, paths with `<path>`, URLs with `<url>`, hex strings with `<hex>` and backticked identifiers with `<id>`, strips the punctuation from both ends of each word except those placeholders, drops apostrophes, and collapses whitespace. So `mobile.`, `mobile,` and `mobile!!` are one word, and `doesn't` is `doesnt`. Retry-type go-aheads (`try again`, `again`, `retry`, `fix it`, `still broken`, `doesn't work`, `still not working`) all become the single token `<retry>`. docs/spec/ledger-v1.md 10.4 gives every step exactly. Then it computes a MinHash signature with 64 permutations over word 3-shingles, or character 5-shingles for prompts under 6 words (256 bytes per prompt, microseconds to compute; TP A4, Broder 1997). Two prompts are near-duplicates when the estimated Jaccard similarity is at least `SIM_JACCARD` = 0.8 or the normalized texts are identical. The signatures are tier 0 and never leave the device.

**Change events.** A loop is broken by anything that changes what the agent will see next: a typed edit (`E`), a typed command (`CMD`, `TEST`), a human commit (`G`), a rejected or edited permission request, an interrupt, a new error signature, or a test or build command whose outcome changed. Undo, rewind and `git checkout -- .` are not change events: they put the code back where it was, so the next identical prompt is a re-spin of the same wheel. A different prompt is not a change event either; the re-roll count just doesn't grow for it. A change event counts from when it is on record: the later of its own time and its event's `t` (2.1). Each of the four counts below reads only the change events, error signatures, outputs and prompts on record before the prompt it tests, so the re-roll, fix-it and autopilot discounts are decided at the prompt. A typed command still running when a prompt is sent doesn't free that prompt; it frees the prompts sent after its record. A `run_green` or `error_resolved` counts from the end of its command, and a rejected or edited permission from the record that pairs it with the next tool event. If shell integration later records a command's start (OSC 133/633), a typed command counts from that start record.

**Re-roll.** For each human prompt `P`:

```text
attempt(P) = 1 + count of earlier human prompts Q in the same session where
             Q is after the last change event, t(P) - t(Q) <= REROLL_WINDOW_S (1,800 s),
             and near_duplicate(P, Q)
```

Attempts 1 and 2 are credited normally. From attempt 3 on, the turn is discounted with reason `X.reroll`. One free retry is deliberate: comparing two generations is part of Barke's exploration mode (PA 2.2), and a single regenerate after a flaky answer is normal. A third identical spin with nothing changed is the slot machine. The count runs over all earlier prompts since the last change, not just the previous one, so alternating between two prompts (A, B, A, B, A) is caught at the third A.

**Fix-it loop (the same error recurring).** An error signature is the first error line of a failure, normalized: Python's final `XError: message` line of a traceback, `Error:` and `TypeError:` lines in JavaScript, `error[E0xxx]` in Rust, `file:line:col: error:` from compilers, `FAILED <test>` and `--- FAIL:` from test runners, `panic:` in Go, each with numbers, paths, line and column numbers, addresses and timestamps replaced as above, then hashed. Signatures are read from agent tool output (test and build runs) and from pasted errors in prompts. Human terminal output is not read in v1. Then:

```text
occurrences(e) = times signature e appeared in this project since the later of the last
                 human change event in the project and the last time e was cleared
                 (agent edits don't reset it: they are the attempted fixes)
```

Once `occurrences(e) >= SAME_ERROR_LIMIT` (3), every following human turn whose prompt is `error_paste` or `low_effort` is discounted with reason `X.same_error`, until a human change event happens or `e` is cleared. The human change events are the change events the person makes, each an anchor that passes its evidence test in 2.3: a typed edit (`E`), a typed command (`CMD`, `TEST`), a human commit (`G`), a permission answer (`PA`) that rejects or edits, and an interrupt (`INT`). Each sets every count in its project back to 0. A `run_green` of the command that produced `e`, or `e`'s `error_resolved` (4.3), clears `e` and sets its count back to 0, so an error that comes back later starts over. Nothing else resets a count: not agent edits or agent commits, not a new error signature or another command's outcome, and not an answer given by a config rule or a hook. A turn is judged by the count before its prompt, so an error pasted in a prompt counts toward the turns after it, not its own. Substantive prompts are never discounted by this rule, even while the error recurs. "Maybe it's the lock order, check whether `drop` runs before `await`" is the person debugging, the pattern the skill study links to higher quiz scores; "still broken" plus the same traceback is "iterative AI debugging", the pattern that scored 24% (PA 2.2).

**Autopilot.** A human turn is `unread` when the agent's final message had at least `NOREAD_MIN_WORDS` (100) words and the next human prompt arrived less than `NOREAD_FRAC` (0.25) times the time needed to read it at 238 wpm:

```text
unread(turn) = words_final >= 100 and (t_next_prompt - t_stop) < 0.25 * 60 * words_final / 238
```

For a 400-word answer that is 25 s, which is reading at about 950 wpm, four times Brysbaert's adult mean and three times the top of his normal range (PA 3.2 S5). Diff lines are left out of this test on purpose: plenty of careful people verify by running the code instead of reading every line (Barke's acceleration mode, PA 2.2). From the third consecutive `low_effort` prompt that follows an `unread` turn, with no change event in between, turns are discounted with reason `X.autopilot`. This is the "intention that the code will not be read" and the menu-following that Thomas describes (PA 2.2). The judgment-calls study shows how easily it happens: analysts went with the agent's randomly assigned lean 86% of the time (PA 2.2). Go-aheads sent after reading stay credited; that is acceleration, not autopilot.

**Rewrite loop (paraphrased re-rolls).** Near-duplicate hashing misses a person who keeps asking for the same fix in new words: "still overlapping on mobile", "the navbar still covers the hero", "pls fix the overlap" (HS 12.2 lists paraphrase as exactly what it misses). The code gives it away: the agent keeps rewriting the same lines. For each human-owned turn `k` with agent edits:

```text
overlap(k) = lines the agent changed in turn k that it also changed in turn k-1
             / lines the agent changed in turn k
```

Lines are matched by file and hunk range from the edit tools' patches (Claude's `structuredPatch`, Codex `patch_apply_end`, Cursor `afterFileEdit`; HS 3.2, 4.1, 6.2), with turn `k-1`'s ranges shifted by any diffs applied in between. Turn `k` is a rewrite repeat when `overlap(k) >= REWRITE_OVERLAP` (0.5) and no change event happened since turn `k-1`. The rule reads turn `k`'s agent edits on record within `READ_HORIZON_S` - `THINK_LEAD_S` - `COMPOSE_MAX_S` after prompt `k` (180 s with the values in 8), and turn `k-1`'s edits on record before prompt `k`. A later edit doesn't count toward `overlap(k)`. So a turn's repeat status is decided at most 180 s after its prompt, and every second of its lead within `READ_HORIZON_S`. From the second consecutive repeat (the third turn on the same lines), turns are discounted with reason `X.rewrite_loop`. Generate, undo, regenerate is caught the same way, because undo is not a change event and the regenerated edit lands on the same lines. Building a feature over several prompts mostly adds lines rather than rewriting the last turn's, and a hand edit, a test run or a new error between turns resets the count. Asking the agent three times in a row to nudge one value ("a bit smaller", "smaller", "no, bigger") does get discounted; changing the number by hand would have been the struggle. A small on-device sentence-embedding model (all-MiniLM-L6-v2 class, about 23 MB as int8; TP A4) could catch paraphrases directly, and is a v1.1 candidate once its cost is measured on an old laptop.

**What a discount does.** A discounted turn opens no anchor windows and adds nothing to the reading bucket, so the minutes spent on it earn 0 unless something else covers them. A permission answer inside a discounted turn counts only if it is a rejection, which is a change event and ends the loop anyway. The lead of a later, credited anchor never reaches back past the stop of the most recent discounted turn in the same project, so the first good act after a loop can't buy back the loop's minutes. A loop is a run of discounted turns in one project with no undiscounted human prompt between them. The seconds each loop cost carry its discount codes, so the reviewer and the user see exactly how much it cost:

- **Which seconds.** The scorer scores the same records again with no turn discounted, keeping each slot's focus as it resolved it the first time. A second that this scoring credits to a project and the first scoring leaves uncredited is a second that project's loops cost. It counts at the weight the second scoring gives it (4.3), and the rest of a half-weighted second is `X.noprog`. Every other uncredited second keeps its usual code (5.2), inside a discounted turn too. A second when the person was away, or when nothing was on screen and no window would have covered it, is no part of the cost.
- **Which code.** Such a second takes the code of the latest discounted turn in the project that started at or before it, unless an undiscounted human prompt in the project came between them. Otherwise it takes the code of the next discounted turn in the project, or of the latest one if none follows. So each second belongs to one loop.
- **Two detectors on one turn.** The turn takes the first of `X.reroll`, `X.same_error`, `X.autopilot` and `X.rewrite_loop`. The first three are decided when the prompt is sent, and the rewrite rule only once the turn's edits land, so a code set at the prompt never changes.

The user's recap shows it too: "you re-rolled the same prompt 7 times between 21:10 and 21:40", which is the self-improvement feedback R3 asks for (HS 17.5).

**Repetition across sessions.** The same template sent in several sessions (a daily "run the tests and fix what fails") is often a legitimate habit, so it never changes credit. grit's `repeated_prompts` rule (the same prompt or template 3 or more times and in at least 30% of a session's prompts) becomes a reviewer flag (PA 1.14).

### 4.3 Progress

Progress events, per project:

| Event | Definition |
|---|---|
| `commit` | A `git.change` `commit` by anyone (human or agent) with at least one changed line, except a revert (`op = revert`) |
| `push` | A commit reaches a remote; forge verification comes later (SP 4.3) |
| `run_green` | A test or build command, matched by its normalized `cmd_hash`, that failed on its last run in this project and now passes. Pass and fail come from the exit status where the harness reports it, else from the runner's summary line (`N passed`, `N failed`, `test result: ok.`, `ok`/`FAIL`) |
| `error_resolved` | A signature that occurred at least twice is absent from the next two runs of the command that produced it |
| `edit_survives` | A file the human edited by hand (`E` anchors) is in the next commit's file list. With git-ai notes present, the check is per line on `h_` attestations (PA 1.3) |

How progress changes credit:

1. **It breaks loops.** `run_green` and `error_resolved` are change events, so they end re-roll, rewrite and autopilot discounts. They end a fix-it discount only for the error they clear: a `run_green` of the command that produced `e`, or `e`'s own `error_resolved`, clears `e` and sets its count back to 0 (4.2).
2. **The no-progress half rate.** Each project tracks `since_progress`, the credited seconds since its last progress event. Past `NO_PROGRESS_LIMIT_S` (7,200 s, two hours of credited time), further seconds are credited at weight 0.5 with reason `X.noprog`. When a progress event arrives within `PROGRESS_RESTORE_S` (86,400 s) of a half-weighted second, that second goes back to weight 1 and its reason gets the suffix `+restored`. Until then the minute shows as `provisional`. Bjork's caveat is the reason: difficulty without progress is not desirable difficulty, and a tracker that ignores progress "rewards being lost" (PA 2.1). Half rather than zero because Kapur's productive failure is real even when the fix never comes. Two hours because a hard bug can take that long with nothing to commit, and the restore means a person who finally fixes it loses nothing.
3. **Commits anchor submissions.** At submission time, credited time after the last commit in the window counts only up to `UNCOMMITTED_CAP_S` (3,600 s) (TP A7 E4; SP 4.4).

Two things deliberately never subtract time on their own. **Undo, revert and churn** (lines an agent added that are removed again before the next commit; `/rewind`, Codex `task.undo`, Gemini `rewind`; HS 6.3, 7.3) never subtract time, because throwing away a bad approach is part of productive failure. They act indirectly: churned lines don't make `edit_survives` true and a reverted commit isn't new progress, so a stretch whose only output was churn runs into the half rate. The two rates shown to reviewers and in the recap are defined per work session:

```text
undo_rate   = (human undo, rewind, revert and reset events) / human-owned turns
churn_share = agent-added lines removed again before the next commit / agent-added lines
```

Undo still matters for credit, through 4.2: it is not a change event, so undo followed by the same prompt is a re-roll, and generate-undo-regenerate is a rewrite loop. **Surviving lines** at the submitted commit (git blame per commit, computed by the server for public repos; TP A4) are reviewer metadata. A commit with more than an hour attached and under 10% of its added lines surviving gets the flag `low_survival`. TP A4 puts it well: progress signals bound hours, they don't prove them.

**Shipped features** are judged once per submission, by a reviewer, against the vision post's "The project must do what it says it does" (TP A7 E8). No minute rule can check that.

### 4.4 One-shots

The handbook rejects "a project that was generated in a single prompt with no meaningful iteration, testing, or refinement" outright (HC 3.7). pairtime doesn't need a rule to keep one-shots from earning hours, because a one-shot has only one prompt's worth of anchors. A 30-word prompt (about 165 characters) that produces 2,000 lines earns at most its lead, 120 + 165 / 1.5 = 230 s, plus the 600 s reading cap. That is the vision post's "one-shotting takes 2 minutes of productive struggle" plus the time to look at what came out, and even that much only if the person was present and focused for all of it. The flag `one_shot_session` (one substantive prompt, no `E`, `CMD` or `TEST` anchors, no follow-up prompts, more than 200 diff lines) is shown to reviewers so the handbook rule can be applied quickly (HC Design implication 5).

### 4.5 What stays out of the number

Some signals change the review path but never the credited number, because a reviewer couldn't recompute them from the timeline, or because publishing them would teach people to beat them:

- prompt-kind labels from a content classifier (2.2);
- typing cadence and injected-input scores (TP A4);
- regular timing (coefficient of variation of prompt spacing under 0.1, or burstiness `B < -0.5`, over 6 or more prompts; TP A4), `no_input_prompts`, `repeated_prompts`, `periodic_input`, `marathon`, `concurrent_devices`, `low_survival`, `relayed_share`, VM labels, cross-user anomalies.

These are tier-2 flags (TP A5, A8). They live in a private server module, as Hack Club's own fraud rules do (HC 1.9, 3.6), and they set review priority and audit probability (TP A6). The credit rule itself (sections 2-4 and the parameter table) is public and deterministic, which R10 needs. The split is MRC's: deterministic public filters for known automation, private multi-signal scoring for the rest (TP A5).

Also kept out, because the research says they measure the agent: token counts, lines of code, files touched, agent runtime (PA 2.3; HC 5).

## 5. Output: credited seconds, reason codes, rollups

### 5.1 The minute bucket

The scorer's output is one record per user, device and UTC minute in which anything was credited, an agent was running or a loop cost a second (4.2). This is the tier-1 upload (TP B6): numbers, codes and event ids, no content.

```json
{
  "v": "1.0.0",
  "params": "sha256:9c1e04…",
  "minute": "2026-11-19T16:42:00Z",
  "device": "d_7f3a",
  "slots": [
    {"p": "p_9f2c", "cr": 5,  "why": "READ", "kind": "reading_output", "ev": "A"},
    {"p": "p_9f2c", "cr": 15, "why": "READ", "kind": "reading_output", "ev": "A"},
    {"p": "p_9f2c", "cr": 15, "why": "GO",   "kind": "prompting",      "ev": "A"},
    {"p": "p_9f2c", "cr": 15, "why": "GO",   "kind": "prompting",      "ev": "A"}
  ],
  "credited_s": 50,
  "uncredited": {"X.agent_only": 10},
  "agent_running_s": 16,
  "anchors": ["e_01JDQ4…41", "e_01JDQ4…57"],
  "state": "final",
  "witness": "witnessed"
}
```

| Field | Meaning |
|---|---|
| `v`, `params` | `scoring_version` and the SHA-256 of the parameter table (8) that produced this record |
| `minute`, `device` | UTC minute start and the device that scored it. Here: 10 s of the agent running with nothing new on screen until it stops at 16:42:10, then reading its answer, then from 16:42:34 the 20 s lead of a go-ahead sent at 16:42:54, whose turn's 20 s base covers the last 6 s |
| `slots[4]` | One entry per 15-second slot: project, credited seconds (0-15, one decimal for half-weight seconds), the dominant credited reason code, the activity kind, and the evidence level |
| `credited_s` | Sum of `cr` over the four slots, at most 60 |
| `uncredited` | Seconds in this minute that were looked at and not credited, by reason code |
| `agent_running_s` | Seconds in this minute during which an agent turn was running in any project |
| `anchors` | Ids of the events whose windows covered credited seconds here. The events themselves are tier 0; their metadata is revealed only for submitted windows (TP B6, tier 2) |
| `state` | `final`, `provisional` (half-weight seconds waiting for a progress event, 4.3) or `restored` |
| `witness` | `witnessed` if the chain head covering this minute reached the server within 15 minutes, else `backfilled` (SP 8.4) |

Activity kinds are the server's six (SP 10.2) plus `supervising` and `lookup`: `prompting`, `reading_output`, `supervising`, `reviewing`, `testing`, `debugging`, `hand_edit`, `lookup`.

Evidence levels:

| Level | Meaning |
|---|---|
| `A` | Witnessed; present by OS input; anchors backed by keyboard input (`input_keys`); focus known (2.5) |
| `B` | Witnessed; present by OS input; keyboard-specific input unavailable (`input_any` only); focus known (2.5) |
| `C` | Backfilled, presence `unknown` (lookback, 2.8), focus unknown (2.5), or relayed (2.2 rule 9) |

Relayed credit carries the label `via_relay` beside its level C. Like lookback, it shows on the person's own dashboard with its label and never counts toward a streak day or anything public, and a program takes it only by opting in to level C.

### 5.2 Reason codes

Credit reasons. A `~r` suffix means the second was credited in a `returned` slot, with no input, under 2.1's return rule.

| Code | Window | Activity |
|---|---|---|
| `P` | Lead of a substantive or error-paste prompt (think + typing) | `prompting` |
| `ANS` | Lead of an answer to the agent's question | `prompting` |
| `GO` | Lead of a go-ahead | `prompting` |
| `Q` | Lead of a message queued while the agent worked | `prompting` |
| `PA` | Wait and trail of a permission answer | `reviewing` |
| `INT` | Lead and trail of an interrupt | `reviewing` |
| `E` | Lead of a typed edit | `hand_edit` |
| `NAV` | Lead of editor navigation | `reviewing` |
| `CMD` | Lead, run and trail of a typed command | `debugging` |
| `TEST` | Lead, run and trail of a typed test or build command | `testing` |
| `G` | Lead of a human commit | `reviewing` |
| `SUP` | Reading bucket drained while the agent runs | `supervising` |
| `READ` | Reading bucket drained after the agent stopped | `reading_output` |
| `LOOKUP` | Bracketed browser or chat-app run | `lookup` |

Uncredited reasons:

| Code | Meaning |
|---|---|
| `X.absent` | No presence: no input past the bridge and tail, lock, sleep |
| `X.other_focus` | Input went to an app outside the project |
| `X.ambiguous` | Focus couldn't be resolved to one project (3.2) |
| `X.no_anchor` | Present and focused, but no window covered the second (a jiggler on an open terminal ends up here) |
| `X.agent_only` | An agent turn ran and nothing credited the second |
| `X.automated` | The turn's origin was `scheduled`, `self_continue`, `subagent`, `headless` or `cloud` |
| `X.no_input` | A prompt arrived with no input before it (origin 8) |
| `X.reroll`, `X.rewrite_loop`, `X.same_error`, `X.autopilot` | Dark-flow discounts, on the seconds a loop cost (4.2), which also gives the code when two detectors fire |
| `X.noprog` | The half not credited under the no-progress rule (4.3) |
| `X.lookup_cap`, `X.bucket_cap`, `X.day_cap` | Caps (2.4, 2.5, 5.3) |

In a live slot whose presence is unknown, such as every slot of a server with no desktop, the presence and focus codes don't apply. An uncredited second there is `X.bucket_cap` where only the 120 s cap kept a session from reading, else `X.agent_only` while a turn a person started runs, or a `no_input` one, else `X.automated` while an automated turn runs, else `X.no_anchor`.

A reviewer can check any minute by hand: take the anchors, look up their times and windows in the parameter table, intersect with the presence and focus slots, and the credited seconds must come out the same. The server re-runs exactly this check on submitted windows, using the revealed event metadata (SP 10.4).

### 5.3 Rollups

Each level sums the one below. Credited and uncredited seconds roll up separately, by reason code and by activity kind, so the "what was left out" breakdown survives every level.

| Level | Definition | Extra fields |
|---|---|---|
| Work session | A maximal run of credited minutes in one project, split where two credited minutes are more than `SESSION_GAP_S` (1,200 s) apart. Not the same thing as a harness session | Start, end, credited by kind, agent-only time, discounted time by code, counts of each anchor kind, progress events, flags, share of credited seconds at each evidence level |
| Day | Per user and local day in the profile time zone (SP 8.4) | Credited per project. Past `DAY_CAP_S` (43,200 s), further seconds are `X.day_cap`, and anything past 10 hours raises a review flag. A streak day needs 900 credited seconds, Hackatime's 15 minutes applied to credited time instead of heartbeats (PA 4.3) |
| Project | All of a project's credited time | Per commit: `credited_seconds_since_parent` split by kind; uncommitted time (SP 4.3) |
| Submission | A frozen window for one program project (SP 10.2) | Rows, one per commit interval, each with `captured_s`, kinds, anchor counts, flags and an evidence link; the totals below |

The cap of 12 hours a day is a bound, not an estimate. Lookout ends a session at 720 captures (12 hours) and Stardance pays at most 10 hours per devlog (HC 3.3, 4.2), and nobody does more than 12 hours of real struggle in a day. The cap turns unbounded attacks into bounded ones (TP A6).

Submission totals use the server's names (SP 10.2), defined here:

| Total | Definition |
|---|---|
| `logged_s` | Hackatime-equivalent time for the window: every editor heartbeat and agent event in the submitted projects as one time-ordered series, summed as `min(gap, 120 s)` (HC 1.4). What the same activity would have shown in Hackatime |
| `credited_s` | Sum of credited seconds in the window, after the uncommitted cap (4.3) |
| `witnessed_s`, `backfilled_s` | `credited_s` split by `witness` |
| `agent_only_s` | Seconds in agent turns that nothing credited |
| `excluded_loops_s` | `X.reroll` + `X.rewrite_loop` + `X.same_error` + `X.autopilot` |
| `idle_s` | `X.absent` seconds inside spans where an agent session was open |

### 5.4 Answering the one-third rule

The handbook says time Hackatime categorizes as `ai coding` "must be deflated to at most ⅓ of its original amount unless there is significant evidence proving that more time should be credited" (HC 3.7). Ari implements the flat third (HC 3.6). pairtime's job is to be that significant evidence, in both directions. For every submission it reports:

| Quantity | Definition |
|---|---|
| `ai_logged_s` | The `logged_s` rule applied to agent events only (prompt submits, tool calls, agent edits, stops): what wakatime-cli's transcript sync would have sent Hackatime as `ai coding` (PA 1.1). Relay records (1.2) aren't agent events and never count; the harness's own records of a relayed turn count like any turn's |
| `coding_logged_s` | `logged_s - ai_logged_s` |
| `handbook_s` | `coding_logged_s + ai_logged_s / 3`: the most a reviewer could approve from Hackatime data alone under the handbook. The handbook's own example is 4 h coding + 3 h `ai coding` = 5 h |
| `ai_credited_s` | pairtime credit with reasons `P`, `ANS`, `GO`, `Q`, `PA`, `INT`, `SUP`, `READ`, plus any credited second inside an agent turn |
| `hand_credited_s` | All other credit (`E`, `NAV`, `CMD`, `TEST`, `G`, `LOOKUP` outside agent turns) |
| `ai_ratio` | `ai_credited_s / ai_logged_s`: above 0.333 means pairtime credits more than the handbook's third, below means less |

Then the evidence, one line per claim:

- **Crediting more than a third.** Every AI-assisted second pairtime credits has a human act within its window and OS presence behind it, or falls inside the bounded return credit of 2.1, apart from relayed credit (2.2 rule 9), which has the app's word for its act and no presence behind it, and is level C for that reason. The submission shows the counts that make that concrete: typed prompts, relayed messages, counted apart from typed prompts, answers, permission decisions (with rejections called out), interrupts, hand edits, test runs per AI hour; the share of `ai_credited_s` at evidence level A and B; the share that is reading (`SUP` and `READ`), which is bounded by output size; and the progress events in the window. A reviewer who doubts a stretch opens its minutes. Fifteen typed prompts, fifteen permission decisions and five commits in three hours, each prompt followed by reading time that fits the length of the answer (6.1), is the kind of significant evidence the handbook asks for.
- **Crediting less than a third.** `agent_only_s` and `idle_s` show the agent running while nobody was there; `X.automated` shows turns no human started; `excluded_loops_s` shows the loops. An agent left on `/goal` overnight has an `ai_ratio` near zero, and the flat third would have paid it a third of the night (6.4).
- **Where the evidence is thin.** For level-C seconds (backfilled, no presence data, focus unknown as in 2.5, or relayed), pairtime recommends `min(credited, ai_logged / 3)` for that portion, so lookback can never beat the handbook rule without presence data behind it. Levels A and B are recommended as credited. Whether Quality and Integrity treat level B (no keyboard-specific signal) as "significant evidence" is their call (Open questions).

The submission's handbook text (SP 10.6) states the three numbers side by side, for example: "pairtime credited 5.9 h. Hackatime would log 9.1 h for this window, 7.4 h of it as AI coding, so the handbook's third allows at most 4.2 h. Of the 7.4 h of agent-assisted time pairtime credited 4.6 h (62%), backed by 212 typed prompts, 38 permission decisions and 9 interrupts, and 12 messages relayed from another device, counted apart; 97% of credited time was witnessed. Review link: …". The `Alternate Tracking Method` field gets the method summary, the review URL goes in `Additional Justification` (HC 3.7).

## 6. Worked examples

Each example applies the v1 parameters (8) to a made-up but realistic timeline. Times are local; seconds are exact under the rules above, and a reviewer should be able to reproduce every row. Hackatime figures are my replay of the same events through Hackatime's `min(gap, 120 s)` rule (HC 1.4); they are approximate because they depend on exactly when WakaTime plugins fire.

### 6.1 Three hours of deep flow with Claude Code

Rowan, 17, on macOS, so keyboard input is visible and his minutes are evidence level A. Claude Code runs in one iTerm window, VS Code has the WakaTime plugin, the project is `cs2go`, the permission mode is the default. From 14:00 to 17:00 he works in fifteen 12-minute cycles of the same shape. The first one:

| Minute | What happens | Credited | Reasons |
|---|---|---|---|
| 14:00 | Reads code in VS Code, moving through files with the keyboard (quick open, arrow keys). Heartbeats at 14:00:30 and 14:01:50 become `NAV` anchors | 60 | `NAV` |
| 14:01 | Same. From 14:01:50 the lead of his next prompt takes over; its typing part needs keyboard input, which his file-hopping provides | 60 | `NAV` 50, `P` 10 |
| 14:02 | Switches to iTerm and starts typing a 300-character prompt | 60 | `P` |
| 14:03 | Still typing; sends at 14:04:00. Its lead is 120 + 300 / 1.5 = 320 s | 60 | `P` |
| 14:04 | The agent starts; the bucket resets to 20 s. A 45-word message (+15 s) at 14:04:30. Nothing new to read from 14:04:20 to :30 and from :45 to 14:05:00 | 35 | `SUP` 35; `X.agent_only` 25 |
| 14:05 | Another 45 words (+15 s). Permission request at 14:05:10, accepted at 14:05:25. The first edit (30 lines, +225 s) lands at 14:05:30 | 60 | `SUP` 10, `PA` 25, `SUP` 25 |
| 14:06 | Second edit (+225 s) at 14:06:00; the agent stops at 14:06:30 with a 160-word summary (+53 s) | 60 | `SUP` 30, `READ` 30 |
| 14:07 | Reads the summary, scrolls back through both diffs | 60 | `READ` |
| 14:08 | Same. The bucket still holds 303 s at the end of the minute | 60 | `READ` |
| 14:09 | Plays the change in the browser on `localhost` | 60 | `LOOKUP` |
| 14:10 | Same. The browser run is 120 s, under the 180 s cap, with credited slots on both sides and no agent running | 60 | `LOOKUP` |
| 14:11 | Back in iTerm at 14:11:00: `git add -p`, commit at 14:11:40 (`G`, lead 60 s). Then the lead of the next cycle's first `NAV` | 60 | `G` 40, `NAV` 20 |

That cycle credits 695 of its 720 seconds. The other fourteen are the same shape with three variations:

- In cycles 3, 6, 9, 12 and 15 minute 11 is a commit, as above. In the others it is a small hand edit in VS Code, which credits the same 60 s as `E` and `NAV`. Five commits in three hours keep `since_progress` far below the 2-hour limit.
- In cycle 6 Rowan gets a drink. He leaves at 15:06:40, ten seconds after the agent stops, and is back at 15:12:40, where the first thing he does is click in VS Code. The 30 s tail credits reading until 15:07:10. The silent run is 6 minutes long and its first anchor is `NAV`, which doesn't qualify for the return rule, so 15:07:10 to 15:12:40 is `X.absent`: 290 s lost in cycle 6 and 40 s in cycle 7. Had he sat down and typed a prompt straight away, the return rule would have credited at most 53 + 120 = 173 s of the silence (2.1).
- In cycles 9 and 10 the agent's test run fails three times in a row with the same `AssertionError`, twice in cycle 9 and once in cycle 10. Cycle 10's prompt is a new hypothesis ("the hitbox updates before the position in `tick()`, swap them"); the agent tries it and the test still fails. His own edit at the end of cycle 10 fixes it, and the next run is a `run_green`. Nothing is discounted: cycle 9's commit and that edit are human change events, so the error's count is back at 0 before each of his next prompts and never reaches `SAME_ERROR_LIMIT` (4.2). Had it reached the limit, his hypothesis would still count, since the fix-it rule only discounts error pastes and go-aheads.

| Total | Seconds | Notes |
|---|---|---|
| Wall time | 10,800 | 3 h |
| Credited | 10,095 | 2 h 48 min, 93% of wall time |
| of which AI-assisted (`P`, `PA`, `SUP`, `READ`) | 5,965 | |
| of which hand work (`NAV`, `E`, `G`, `LOOKUP`) | 4,130 | |
| `X.agent_only` | 375 | 25 s per cycle with nothing new on screen |
| `X.absent` | 330 | the drink |
| Hackatime-equivalent `logged_s` | about 8,300 | |
| of which `ai_logged_s` | about 4,050 | each run plus the 120 s gap credit before each prompt, 270 s a cycle |
| `handbook_s` | about 5,600 | 1 h 33 min |

pairtime credits about 1.8 times what the handbook rule would allow from Hackatime's data, and every extra minute sits in the window of a typed prompt, a permission answer, an editor click or a commit. `ai_ratio` is 5,965 / 4,050 = 1.47: pairtime credits more human time around the agent than Hackatime logged as AI time at all. Composing a prompt and reading the answer happen outside the spans where the agent is busy, and Hackatime's `ai coding` only sees those spans. The one-third rule assumes AI time overstates the human. For someone working like Rowan it understates them. R4's first story holds: three hours of flow count with nothing to set up, and the only losses are a six-minute break and the moments when there was nothing new on screen.

### 6.2 Five minutes before school

Maya, 14, has a 2016 Windows 10 laptop without the opt-in key counter, so pairtime can't tell keyboard from touchpad and her minutes are evidence level B. She uses Cursor in agent mode on a project called `remind-bot`. She opens the lid at 07:40:00 and closes it at 07:44:45 to catch the bus.

| Minute | What happens | Credited | Reasons |
|---|---|---|---|
| 07:40 | The laptop resumes with Cursor in front. She touches the touchpad at 07:40:00, types a 120-character prompt from 07:40:10 and sends it at 07:41:00. Origin `human_typed`: input in the 10 s before submit, Cursor hosts the session. The lead is 120 + 80 = 200 s, but nothing before 07:40:00 counts because the machine was asleep | 60 | `P` |
| 07:41 | Bucket reset to 20 s, drained by 07:41:20. Nothing to read until the agent's 40-line edit lands at 07:41:50, inside the lead of her coming hand edit | 40 | `SUP` 20, `E` 20; `X.agent_only` 20 |
| 07:42 | The agent stops at 07:42:30 with a 120-word summary. She reads the diff in Cursor's review view | 60 | `E` |
| 07:43 | Fixes a message string by hand at 07:43:40. Cursor fired neither `afterFileEdit` nor `afterTabFileEdit` for it, so the edit is hers | 60 | `E` 40, `G` 20 |
| 07:44 | Commits from Cursor's source-control panel at 07:44:30 (`G`, lead 60 s); reads the summary until her last touch at 07:44:40; the lid closes at 07:44:45 | 45 | `G` 30, `READ` 15; `X.absent` 15 |

Credited: 265 s, 4 min 25 s of the 4 min 45 s the lid was open. She didn't start or stop anything. The suspend notification ended credit and made the daemon flush its spool and send its chain head (CP 2.2), so the minutes are witnessed; had the upload failed, they would show as backfilled until the next wake. The 325 s left in the bucket expire with the 2-hour TTL instead of turning into credit after school. Five minutes don't make a streak day (900 s), which is Hackatime's rule as well (HC 1.4), but they count toward her project, which is R4's second story.

### 6.3 The five-project afternoon

Sam, 16, on macOS (evidence A), has five projects open from 13:00 to 17:00:

| Key | Project | Tooling |
|---|---|---|
| A | `portfolio` | Claude Code in iTerm window 1 |
| B | `discord-bot` | Codex CLI in iTerm window 2 |
| C | `homework-helper` | Copilot agent mode in VS Code (worked on after 15:00) |
| D | `plant-sensor` | Arduino code in VS Code with WakaTime, by hand |
| E | `rusty-cli` | Cursor: its agent plus hand edits |

From 14:00 to 14:30 he juggles four of them while two agents run in the background:

| Minute | Focus | What happens | Credited | Reasons |
|---|---|---|---|---|
| 14:00 | A | Types a 180-character prompt | A 60 | `P` |
| 14:01 | A | Sends at 14:01:30 (lead 240 s); watches A's agent start | A 60 | `P` 30, `SUP` 30 |
| 14:02 | B | Window 2: rereads B's answer from 13:58 and starts typing at 14:02:50. All of it is inside the lead of the prompt he is about to send | B 60 | `P` |
| 14:03 | B | Types the rest of a 150-character prompt | B 60 | `P` |
| 14:04 | B | Sends at 14:04:30 (lead 220 s); watches B's agent start | B 60 | `P` 30, `SUP` 30 |
| 14:05 | E | Cursor: hand edits, heartbeats at 14:06:00, 14:07:30, 14:08:40 | E 60 | `E` |
| 14:06 | E | Same | E 60 | `E` |
| 14:07 | E | Same. B's agent stops at 14:07:00; its output waits in B's bucket | E 60 | `E` |
| 14:08 | E | Last edit at 14:08:40, then asks Cursor's agent to run the tests without fixing anything (50 characters), sent 14:09:00 | E 60 | `E` 40, `P` 20 |
| 14:09 | E | `cargo test`: one failure, exit 101. A's agent stops at 14:09:00, unread | E 60 | `E` (lead of the 14:11:00 edit) |
| 14:10 | E | Reads the failure | E 60 | `E` |
| 14:11 | E | Fixes the test by hand (edits at 14:11:00 and 14:12:30) | E 60 | `E` |
| 14:12 | E | Sends "run the tests again" (a go-ahead, lead 20 s) at 14:13:00 | E 60 | `E` 30, `READ` 10, `GO` 20 |
| 14:13 | E | Tests pass at 14:13:40 (`run_green`); commits at 14:14:00 | E 60 | `G` |
| 14:14 | A | Window 1: reads A's summary and scrolls the diff. A's bucket is at its 600 s cap; the 300 changed lines alone would have been 2,250 s | A 60 | `READ` |
| 14:15 | A | Same | A 60 | `READ` |
| 14:16 | A | Same | A 60 | `READ` |
| 14:17 | A | Still reviewing, now inside the lead of the follow-up he sends at 14:20:30 (135 characters, lead 210 s) | A 60 | `P` |
| 14:18 | A | Same | A 60 | `P` |
| 14:19 | A | Types the follow-up | A 60 | `P` |
| 14:20 | A, then B | Sends at 14:20:30, moves to window 2 and reads B's answer (255 s left in B's bucket) | A 30, B 30 | `P`; `READ` |
| 14:21 | B | Reads | B 60 | `READ` |
| 14:22 | B | Inside the lead of his reply (90 characters, sent 14:25:00, lead 180 s) | B 60 | `P` |
| 14:23 | B | Same | B 60 | `P` |
| 14:24 | B | Types the reply | B 60 | `P` |
| 14:25 | D | VS Code, Arduino sketch, hand coding with heartbeats every 60-80 s | D 60 | `E` |
| 14:26-14:29 | D | Same | D 240 | `E` |

Half an hour of wall time, 1,800 s credited: A 510, B 450, E 540, D 300. They add up because every second went to one project. Meanwhile A's agent ran 570 s and B's 180 s that nobody watched; those 750 s are agent time, shown on each project's chess clock and credited to nothing. Hackatime's per-project view would count 14:05-14:09 three times, as `ai coding` for A and B from their transcripts and as `coding` for E from Cursor's heartbeats, and it would miss most of the eleven minutes Sam spent reading A's and B's answers, which leave no heartbeat except the prompt submits (HC 1.4).

If A and B were two tabs of one iTerm window, the OS couldn't see the switch at 14:02. The look-ahead rule (3.2) would give 14:01:30-14:02:00 to B, because the next anchor in that window is B's prompt and the slot is inside its lead. Thirty seconds move from A to B and the total doesn't change.

The whole afternoon, with the same rules (illustrative totals):

| Project | Credited | That project's agent-only time |
|---|---|---|
| A `portfolio` | 52 min | 41 min |
| B `discord-bot` | 38 min | 22 min |
| C `homework-helper` | 41 min | 18 min |
| D `plant-sensor` | 29 min | 0 |
| E `rusty-cli` | 47 min | 12 min |
| All | 207 min | 93 min |

The 33 uncredited minutes of the 240 are a snack (`X.absent`, 15), a video (`X.other_focus`, 6), browser runs past the lookup cap (`X.lookup_cap`, 5), watching agents with nothing new on screen (`X.agent_only`, 4) and present-but-unanchored time (`X.no_anchor`, 3). The agents' 93 minutes overlap Sam's credited minutes in other projects, which is exactly why they can't be counted as his.

### 6.4 A `/goal` agent running unattended overnight

Jordan, 16, runs Linux on X11 with the key counter on (evidence A). Claude Code in a terminal, project `pixel-forge`.

| Minute | What happens | Credited | Reasons |
|---|---|---|---|
| 23:37 | Sits down at 23:37:00 and looks at the repo; the lead of his prompt starts at 23:37:10 | 50 | `P` 50; `X.no_anchor` 10 |
| 23:38 | Types `/goal all tests pass, and lighthouse performance above 90 on the home page.` (75 characters) | 60 | `P` |
| 23:39 | Sends at 23:40:00. A typed `/goal` with an objective is a substantive prompt; lead 120 + 50 = 170 s | 60 | `P` |
| 23:40 | Watches the plan (150 words) and the first edits come in; the bucket fills faster than he reads | 60 | `SUP` |
| 23:41 | Same | 60 | `SUP` |
| 23:42 | Same | 60 | `SUP` |
| 23:43 | Last touch at 23:43:10, then bed. The tail covers until 23:43:40 | 40 | `SUP` 40; `X.absent` 20 |
| 23:44 to 07:29 | The agent carries on alone. Every continuation is a Stop-hook turn (`stop_hook_active`), so its origin is `self_continue` (HS 3.5). The screen locks at 23:53:10. Tests go green at 03:12 and the agent commits 31 times; it finishes at 06:10 | 0 | `X.absent`; turns `X.automated`; agent time `X.agent_only` |
| 07:30 | Unlocks (typing a password is input) and reads the final summary, scrolling back through the last diffs. The bucket holds the newest 600 s of output; the night's older entries were evicted or expired | 60 | `READ` |
| 07:31 | Same | 60 | `READ` |
| 07:32 | Same | 60 | `READ` |
| 07:33 | Same | 60 | `READ` |
| 07:34 | From 07:34:19, the lead of his next prompt | 60 | `READ` 19, `P` 41 |
| 07:35 | From 07:35:30, types "nice. explain what you changed in the service worker and why?" (61 characters) | 60 | `P` |
| 07:36 | Sends at 07:37:00; lead 120 + 41 = 161 s | 60 | `P` |

Credited: 810 s, 13.5 minutes, all of it AI-assisted. The agent ran 23,400 s (6 h 30 min), and 23,180 s of that is agent-only. Hackatime would log about 23,500 s of `ai coding` for the night, so the flat third would credit about 7,800 s (2 h 11 min) for eight hours of sleep. `ai_ratio` is about 0.03. This is Karim's point that agent activity can't be the measure, because anyone can set a `/goal` or a 20-minute check and walk away (BOUNTY.md).

The morning reading counts because the next prompt came within 600 s of it. The green tests and 31 commits don't add a second. They are progress for the project, which lifts the no-progress half rate for Jordan's own later minutes, and they appear on the review page. With a mouse jiggler left running all night, presence would have stayed `yes`, but no human act happens after 23:40 and the reading horizon closes at 23:50, so at most 6 min 20 s more `SUP` could have counted.

### 6.5 An hour of dark-flow re-rolling

Alex, 15, on Windows 10 without the key counter (evidence B), Claude Code in Windows Terminal, project `pantry-app` (React). From 20:00 to 21:00 he fights one layout bug.

| Minute | What happens | Credited | Reasons |
|---|---|---|---|
| 20:00 | Types "the navbar overlaps the hero on mobile, can you fix the css so the hero starts below the navbar" (95 characters); sends at 20:01:00 | 60 | `P` |
| 20:01 | T1 rewrites lines 10-21 of `Navbar.css` and stops at 20:02:00. He is already inside the lead of his next prompt (143 s) | 60 | `P` |
| 20:02 | Reads, checks the phone view in the browser (20:02:30-:50, a bracketed lookup of one slot, since the terminal has most of the slot from 20:02:45; 2.5), types "still overlapping on mobile. fix it" | 60 | `P` 45, `LOOKUP` 15 |
| 20:03 | Sends at 20:03:00; a new text, so attempt 1. T2 rewrites lines 12-19, all inside T1's lines: rewrite repeat 1, which is free. Nothing new on screen 20:03:20-:40, which the lead of his next prompt would have covered had the loop not discounted it (4.2) | 40 | `SUP` 40; `X.rewrite_loop` 20 |
| 20:04 | Browser check (lookup), then reads T2's answer | 60 | `LOOKUP` 30, `READ` 30 |
| 20:05 | Sends the identical prompt at 20:05:00 (attempt 2, still free as a re-roll). T3 rewrites lines 12-19 again: rewrite repeat 2, so the turn is discounted and the bucket goes to 0 | 0 | `X.rewrite_loop` |
| 20:06 | Browser check, retypes | 0 | `X.rewrite_loop` |
| 20:07 | "still overlapping on mobile, fix it!!" normalizes to the same text, since normalization strips the punctuation at the ends of words: attempt 3. The rewrite loop continues too, and `X.reroll` comes first (4.2) | 0 | `X.reroll` |
| 20:08 to 20:37 | The same two minutes 15 more times, with prompts at 20:09, 20:11 ... 20:37, mostly in new words: "the navbar STILL covers the hero", "pls fix the overlap", "it's not working, try again". Every turn rewrites the same eight lines, and there's no hand edit, command, test or new error in between. "pls fix the overlap" has 19 characters, so it is a go-ahead with a 20 s lead, and the 20 s with nothing on screen in the turn before it would not have been credited even without the loop | 0 | `X.rewrite_loop`, `X.reroll`; `X.agent_only` 20 |
| 20:38 | T19 stops at 20:38:00. Browser check until 20:38:30, then he sits in the terminal. From 20:38:30, the lead of his coming editor click, which can't reach back past T19's stop | 30 | `NAV` 30; `X.rewrite_loop` 30 |
| 20:39 | Opens `Hero.css` in VS Code (heartbeat at 20:39:20) and works out the margin the hero needs | 60 | `NAV` 20, `E` 40 |
| 20:40 | Edits `Hero.css` by hand at 20:41:00 (`margin-top: var(--nav-h)`). A change event: the loop is over | 60 | `E` |
| 20:41 | Checks the phone view (20:41:10-:40): fixed | 60 | `P` 30, `LOOKUP` 30 |
| 20:42 | Types a new request: "now the hero is right on mobile but there's a big gap on desktop. only apply the margin under 768px" | 60 | `P` |
| 20:43 to 20:59 | Sends at 20:43:00. Normal work in the shape of 6.1: two more turns that add new lines, a check of the desktop view, a commit at 20:58 | 940 | `P`, `SUP`, `READ`, `LOOKUP`, `E`, `G`; `X.agent_only` 80 |

| Total | Seconds |
|---|---|
| Credited | 1,490 (24 min 50 s) |
| `excluded_loops_s` | 2,010 (33 min 30 s), in 20:03:20-:40 and in 20:05:00 to 20:38:30 apart from the loop's 20 s of `X.agent_only` |
| `X.agent_only` | 100 |

The lexical re-roll rule alone would have caught only the turns whose wording repeated exactly. The autopilot rule didn't fire either: Alex took about a minute to reply each time, far more than the 7 s that would mark a 110-word answer unread. What caught the paraphrases was the code: the agent rewrote the same eight lines seventeen times while nothing else changed (4.2).

Hackatime would log about 50 minutes of `ai coding` and 5 of `coding` for this hour, and the handbook's third would allow about 22 minutes. pairtime's 25 minutes is a similar total made of different minutes: none of the loop, all of the five-minute start and the twenty minutes after his own fix. Those are the minutes a reviewer can defend. Alex's recap says it without a score: "Between 20:05 and 20:38 you asked for the same navbar fix 17 times and the agent rewrote the same 8 lines each time. Your own edit at 20:41 fixed it."

### 6.6 A scripted `claude -p` loop pretending to be a person

Casey has a Windows 10 laptop, Git Bash in Windows Terminal, a $10 USB mouse jiggler, and this script:

```bash
while true; do
  p=$(python gen_prompt.py)                       # an LLM writes a varied, plausible prompt
  claude -p "$p" --permission-mode acceptEdits
  sleep $((RANDOM % 240 + 60))                    # 1 to 5 minutes
done
```

It runs from 22:00 to 02:00, with the terminal in front and the lid open.

| Minute | What happens | Credited | Reasons |
|---|---|---|---|
| 22:00 | `claude -p` starts at 22:00:05 with "the leaderboard sorts ties wrong, make it stable and add a test". The hook binary and the daemon's process list agree: `claude -p`, nearest non-shell ancestor `bash farm.sh`. `entrypoint = sdk`, `parent_class = script_runtime`, so origin `headless`, and this laptop has no key counter, so headless prompts never count (2.2 rule 4) | 0 | `X.automated` |
| 22:01 | The agent edits two files | 0 | `X.agent_only` |
| 22:02 | The agent stops at 22:02:10; the script sleeps 167 s. The jiggler keeps presence `yes` | 0 | `X.agent_only`, `X.no_anchor` |
| 22:03 to 22:04 | Sleeping | 0 | `X.no_anchor` |
| 22:05 | The next iteration starts at 22:04:57 | 0 | `X.automated` |
| 22:06 to 01:59 | 47 more iterations; the agent commits once an hour | 0 | same |

Credited: 0 s for four hours. Hackatime would log about 3 h 10 min of `ai coding` (two minutes of agent events per iteration plus the 120 s gap credit before each one), and the flat third would pay about 64 minutes. The server's private flags put the account in front of a reviewer anyway: a 100% headless share, regular timing (the prompt spacing has a mean near 300 s and a standard deviation near 75 s, so burstiness `B` is about −0.6, past the −0.5 line; TP A4), input that resets at the jiggler's fixed period, and four hours of agent commits with no hand edit, command or typed prompt.

What Casey can try next, and what happens:

- **Run with `--bare`, so no hooks fire** (HS 3.1). The daemon's once-a-minute process list still classifies each session, and the transcripts still land on disk unless `--no-session-persistence` is also set. Still 0.
- **Type the loop straight into the interactive shell**, so there's no script file in the process tree. The nearest non-shell ancestor is now Windows Terminal, but `claude`'s arguments still say `-p`, and without a key counter headless prompts never count. Still 0. On a Mac, where the key counter exists, the first iteration, which followed real typing, would count; the rest wouldn't.
- **Post each prompt through T3 Code before each `claude -p`.** A message witnesses only a prompt of a session whose nearest non-shell ancestor is the app's own server, and here it is `bash farm.sh`, so rule 4 still takes every prompt: 0. Having T3 Code itself run the agent, with a T3 token the script holds, does work, and so does a script that takes the name of T3's server and writes T3's store itself. T3 records each message as a person's, and each earns the relayed shapes, up to all of wall time at level C on Casey's own dashboard. Level C never reaches anything public, and a program takes it only by opting in to level C, at most a third of `ai_logged_s` (5.4), which the farm's own agent turns raise.
- **Drive the interactive TUI with an auto-typer** (AutoHotkey or `pyautogui` sending keystrokes into the focused terminal; TP A3 T5). Now every prompt arrives with real OS keyboard input, in the right window, at a plausible rate, and the tier-1 rules credit each turn's lead and reading as if a person had typed it. This is the point where no rule can tell, as TP A6 says of the full puppeteer. What remains is tier 2 (cadence aggregates, the injected-input flag where the opt-in hook is on, regular timing, hours of agent commits with no hand edit or command, prompts that never react to what the agent just said), witnessing, and the risk-weighted live interview, where Casey would have to explain and change the code on a screen share (TP A6). The cost of cheating has gone from one shell script to a convincing auto-typer, a prompt generator that reacts to the agent and a project Casey can defend in conversation, for about $5 an hour of prize value (TP A1).

## 7. Calibration and evaluation

Every constant in sections 2-4 is a reasoned guess. R2's checklist item already states the test: "On a labeled set of real sessions, our hours match what human reviewers would approve" (BOUNTY.md). This section says how to build that set, what to measure, and how the numbers get fitted and versioned. It is also the clearest way to do better than grit, whose constants are hand-tuned with no validation set (PA 1.14, 6.13).

### 7.1 What is being estimated

The target is the number of minutes a trained Hack Club reviewer, applying the handbook ("genuine effort", "When in doubt, deflate"; HC 3.7), would approve for a session as productive struggle. Two label levels:

- **Minute labels.** Each minute of a session gets one of: `steering` (prompting, deciding), `verifying` (reading output, reviewing diffs, testing), `integrating` (hand edits), `debugging`, `lookup`, `waiting` (present, nothing to do), `loop` (dark flow, repetition), `away`, `off_task`. The first five count as struggle, the rest don't. These are CUPS-style states, merged to what a reviewer can see in a recording (PA 2.2).
- **Session hours.** The reviewer's approved minutes for the whole session, decided the way they would decide a devlog (HC 3.3).

### 7.2 The labeled dataset

| Part | What | Size target | Why |
|---|---|---|---|
| Pilot sessions | Real sessions from consenting pilot users: the full local event log plus an opt-in, Lookout-style timelapse (a frame every 10 s; HC 4.2) recorded only for sessions the user volunteers for labeling | 300 sessions, about 300 hours, from at least 40 people | Enough for the session-level error to be estimated to about ±1% and for leave-one-person-out validation |
| Strata | At least 40% Windows, with some laptops from before 2017; Claude Code, Cursor, Codex, Copilot and Gemini CLI; each of the six shapes in section 6 | quotas per stratum | The rules must hold on the machine R8 describes and in every R4 story, not only on a developer's Mac |
| Reviewer labels | Two independent reviewers per session label minutes and approve hours from the timelapse plus the event timeline; a third adjudicates disagreements | every pilot session | The two-rater agreement is the ceiling any metric can reach |
| Self-labels | For a subset, the participant labels their own recording afterwards, as the CUPS participants did (PA 2.2) | 60 sessions | The only source of truth for thinking away from the keyboard |
| Red-team sessions | Scripted versions of T1-T15 (TP A3) run on test accounts, including the variants in 6.6 | 50 sessions | Ground truth is known: zero, or the loop's free attempts |
| Historical data | Stardance per-project submitted and approved hours, and the Hackatime heartbeats behind them, if Hack Club shares them (Open questions) | thousands of projects | The prior in 7.4, and the replay for the handbook comparison |

Privacy: timelapses and transcripts in the labeled set are encrypted, visible only to assigned labelers, and deleted 30 days after labeling. Labels and derived features are kept. Participation is opt-in per session, with parental consent where the law requires it, and no one under 13 (TP B2, B3).

Reviewers first label a shared calibration set of 20 sessions. If their minute-level Cohen's κ is under 0.6, the label guide is revised before the main labeling starts. Labels from two people who disagree that much can't be used to fit anything.

### 7.3 Metrics

| Level | Metric | v1 ship target |
|---|---|---|
| Minute | Cohen's κ between "pairtime credited this minute" (at least 30 s credited) and "the adjudicated label is struggle" | ≥ 0.6, and within 0.1 of the reviewers' own κ |
| Minute | Precision and recall of credited minutes; confusion broken down by reason code, so the rule that disagrees most is visible | precision ≥ 0.85 |
| Session | Mean absolute error in minutes, relative to reviewer hours | ≤ 15% |
| Session | Mean signed error (bias) | between −5% and +3%: erring low is acceptable, erring high is not |
| Session | Share of sessions within ±15% of the reviewer; Bland-Altman limits of agreement | ≥ 70% within ±15% |
| Submission | Reviewer-approved hours / pairtime credited hours, the R9 test ("an hour submitted ... should equal 1 hour") | mean ≥ 0.9, and 80% of submissions between 0.8 and 1.05 |
| Adversarial | Credit on red-team sessions | 0 for T1, T2, T3 (with `input_keys`), T7 and T14; 0 witnessed seconds for T8; at most the free attempts for T15 |

Every metric is reported against four baselines run on the same sessions: Hackatime raw (`min(gap, 120 s)`), Hackatime plus the handbook third (`handbook_s`), grit's published rules (PA 1.14), and Claude Code's `active_time{type="user"}` where it was exported (HS 3.3). pairtime has to beat the handbook third on both error and bias, because that is what reviewers do today.

The fit uses an asymmetric loss: an over-credited minute costs twice an under-credited one. Over-crediting is what gets programs fined at spot-check (HC 3.7), and it is what turned 123,400 submitted hours into 27,600.

### 7.4 Stardance as a prior

Stardance reviewers approved 27,600 of 123,400 submitted Hackatime hours, 22.4% (BOUNTY.md R9). Three caveats decide how it can be used:

- "So far" means some hours were still unreviewed, so 22.4% is a lower bound on the approval rate of reviewed hours.
- The hours were Hackatime's, with `ai coding` at full weight, since Stardance calls the stats API with `no_ai_coding=false` (HC 1.4).
- The deflation mixes causes Zach names: people not pushing themselves, undeclared AI, some fraud (BOUNTY.md R9).

So it is a prior on one ratio, reviewer-approved minutes over Hackatime-logged minutes for a Stardance-like population, not a minute-level label. Its uses:

1. **A sanity check per cohort.** On pilot sessions, compute `credited_s / logged_s`. If pairtime credits much more of Hackatime-equivalent time than reviewers approve of it (the adjudicated `approved / logged`), the rules over-credit, whatever the minute-level κ says.
2. **A regularizer for the fit.** Put a Beta prior with mean 0.25 and a wide spread (Beta(2.5, 7.5), standard deviation about 0.13) on the population `approved / logged` ratio, and update it with pilot labels. Until the pilot has enough sessions, parameter choices that move the cohort ratio far from it need stronger evidence. As labels accumulate, they dominate.
3. **The handbook comparison**, if Hack Club shares per-project hours with heartbeats: replay those heartbeats through the `handbook_s` formula and compare with approved hours. That measures how well today's rule predicts reviewers, which is the bar pairtime has to clear.

It is never a target. Tuning the rules until pairtime credits 22% of logged time would bake the review backlog and the undeclared-AI cases into the metric.

### 7.5 Pilot plan with real teens

1. **Recruit.** 40 to 60 Hack Clubbers aged 13 to 18, through a pairtime-native YSWS, which Zach allows (BOUNTY.md), or as an opt-in inside an existing program that already has to apply the one-third rule (HC 6). Quotas as in 7.2.
2. **Week 0, shadow mode.** Install, back-fill with lookback, score everything, show the user nothing but the recap. Numbers stay hidden until the rules are calibrated, so nobody learns to steer them first (PA 4.4, Goodhart).
3. **Weeks 1-2, labeled sessions.** Each participant volunteers at least five sessions for labeling with the timelapse on. Reviewers label as in 7.2. The red team runs its scripts on test accounts in parallel.
4. **Week 3, fit and freeze.** Sensitivity analysis first: perturb each parameter by ±25% and measure the change in session error, then fit only the roughly ten parameters that move it most (expected: `THINK_LEAD_S`, `PRESENCE_GAP_S`, `READ_WPM`, `DIFF_LPM`, `BUCKET_MAX_S`, `LOOKUP_MAX_S`, `NO_PROGRESS_LIMIT_S`, `REWRITE_OVERLAP`, `SIM_JACCARD`, `COMPOSE_CPS`). Coordinate descent on a grid, each parameter kept inside the range its source supports (for example `READ_WPM` between 150 and 240), minimizing the asymmetric loss subject to the κ target, with leave-one-participant-out cross-validation so the fit can't learn one person's habits. Publish the parameter table, the sensitivity table and the evaluation against the four baselines, then tag `1.0.0`.
5. **Weeks 4-8, live.** Participants submit to the pilot program. Program reviewers review as usual, seeing pairtime's rows and reasons, and the approved/credited ratio per submission is the R9 result. Every reviewer cut, with its reason, becomes a new label for the next version, as Overwatch verdicts became VACnet's training labels (TP A5).

### 7.6 Versioning

- `scoring_version` is `MAJOR.MINOR.PATCH`. MAJOR changes a rule (a new detector, a new priority order). MINOR changes a parameter value. PATCH fixes a bug without changing any output on the regression corpus. The server doc's example `2026.11.2` (SP 10.2) is a placeholder for this.
- Every minute bucket carries `v` and `params`, the SHA-256 of the canonical parameter table (JSON, sorted keys). Every submission freezes both (SP 10.2).
- The regression corpus is the six timelines in section 6, encoded as event fixtures, plus pilot timelines with all tier-0 content stripped. CI runs the scorer on it in GitHub Actions; any change in output fails the build until the version is bumped and the expected files are updated in the same PR.
- Client and server run the same Go scoring package (SP 1). For a submitted window, the server re-scores from the revealed event metadata and must reproduce the client's buckets exactly; a mismatch flags the submission as a possible modified client (TP A3 T9).
- When the version changes, open submissions are re-scored and decided ones keep their numbers, as ari-webhooks does with `CurrentEvidenceVersion` (HC 3.6).
- Any version can be rebuilt from its tag in the AGPL repo, so an outside auditor can re-run it on a submission's evidence bundle (SP 10.4, R10).

## 8. Parameters

Every tunable value in the credit rule, with its v1 setting and the reason for it. The table is public and its SHA-256 is recorded with every minute bucket (7.6). Values marked "fit" are the first candidates for the pilot fit (7.5).

### 8.1 Presence and origin

| Parameter | v1 | Reason |
|---|---|---|
| `SAMPLE_S` | 15 s | Idle-time sample interval. grit samples every 15 s; ActivityWatch polls every 5 s; one user32 or CoreGraphics call per sample is negligible on an old laptop (CP 3.3) |
| `SLEEP_GAP_S` | 30 s | Two samples further apart than twice the interval mean the machine slept or the agent froze (CP 2.2) |
| `PRESENCE_GAP_S` (fit) | 120 s | Silence bridged inside a present stretch. Hackatime's per-gap cap, which reviewers already calibrate against (HC 1.4); ActivityWatch's AFK timeout is 180 s (PA 1.9); one screen of output takes 75-120 s to read |
| `PRESENCE_TAIL_S` | 30 s | Credit kept after the last input before a long silence: two slots, enough to finish the line being read |
| `RETURN_GAP_MAX_S` | 600 s | The longest silence the return rule can bridge. grit's think-time limit and Lookout's auto-pause (PA 1.10, 1.14) |
| `KEYS_PRE_S` | 10 s | Input needed this close before a prompt submit. Short enough that an earlier, unrelated touch doesn't vouch for a scripted prompt |
| `INPUT_RELEASE_GRACE_MS` | 250 ms | How long after a prompt submit input still counts for rules 6 and 7, for the key that sends the prompt, whose release can land between `t` and the hook's sample and hide the typing (2.2). The hook starts after the press, so the release comes less than one key press after `t`. A key press lasts 116 ms on average across Dhakal et al.'s 168,960 typists, with a standard deviation of 24 ms between typists (TP A4). Range 150 to 250 ms. Shorter misses the release of a long press, and longer catches hardly any more releases while letting later input vouch for the prompt |
| `TYPING_MAX_CPS` | 15 chars/s | Above this, typed length is implausible and the excess counts as pasted. About 180 wpm, three and a half times the 51.6 wpm mean of 168,000 typists in Dhakal et al. (TP A4) |
| `SUBSTANTIVE_CHARS` | 20 | Typed characters for a prompt to count as substantive. Short enough that "use a min-heap for the queue" qualifies |
| `ERROR_PASTE_TEXT_MAX` | 40 chars | Typed text outside a pasted error below which the prompt is an error paste, not a hypothesis |
| `CRON_PREFIX_CHARS` | 1,000 | Prefix length for matching a prompt to `Stop.session_crons[].prompt`, which Claude truncates at 1,000 characters (HS 3.1) |
| `PA_MIN_LATENCY_MS` | 300 ms | Permission answers faster than this are a script, not a person |
| `RELAY_MATCH_S` | 600 s | How long after an app message's stamp the prompt it pairs with may reach the agent (2.2 rule 9). The minutes of a relayed lead stay open until the pairing or this deadline (2.3). A lead starts at most `COMPOSE_MAX_S` before the stamp, and a minute stays open to late records for 900 s after it ends, so every minute of the lead is still open this long after the stamp: 900 s less `COMPOSE_MAX_S`. That leaves a pairing no margin for late records: one that arrives after a lead's minute is final changes nothing in it. Reading has a tighter bound, a pairing within 840 s of the stop (2.4). T3 Code holds a message sent while a turn runs until the turn ends, with no bound, so a message queued longer than this earns nothing |
| `RELAY_SEEN_LAG_S` | 60 s | The oldest an app message's stamp may be when pairtime first reads it (2.2 rule 9). The reader looks well inside this, so an honest message is always younger. T3 Code's importers write old messages under new rows, and a row anyone writes can carry any stamp |

### 8.2 Anchor windows

| Parameter | v1 | Reason |
|---|---|---|
| `THINK_LEAD_S` (fit) | 120 s | Think allowance before a substantive act. The same 120 s Hackatime gives any gap, spent only where an act follows (HC 1.4) |
| `COMPOSE_CPS` (fit) | 1.5 chars/s | Typing allowance. 18 wpm, just under the 19 wpm Karat et al. measured for composition (2.3) |
| `COMPOSE_MAX_S` | 300 s | Cap on the typing part of a lead. grit uses 4 minutes (PA 1.14) |
| `LOW_LEAD_S` | 20 s | Lead of a go-ahead. With the reading bucket it comes to roughly grit's 90 s go-ahead cap (PA 1.14) |
| `ANSWER_LEAD_S` | 60 s | Think part of an answer to the agent's question: a real decision (PA 2.2, autonomy study), usually a quick one |
| `PA_MAX_S` | 120 s | Longest credited wait between a permission request and its answer |
| `PA_TRAIL_S` | 10 s | Seeing the result of a decision |
| `INT_LEAD_S`, `INT_TRAIL_S` | 30 s, 10 s | Watching before deciding to interrupt, then the redirect |
| `EDIT_LEAD_S` | 120 s | Lead of a typed edit or editor navigation. WakaTime plugins send at most one heartbeat per 120 s per file, so this credits continuous editing as Hackatime would (HC 1.4) |
| `CMD_LEAD_S`, `CMD_TRAIL_S` | 60 s, 60 s | Typing a command and reading its result |
| `CMD_WATCH_MAX_S` | 300 s | Longest credited run of a command the person started. Watching a test suite is verification; watching a long build is waiting |
| `COMMIT_LEAD_S` | 60 s | Staging and writing the message |

### 8.3 Reading bucket and lookups

| Parameter | v1 | Reason |
|---|---|---|
| `R_TURN_BASE_S` | 20 s | Watching the agent pick up a prompt; also the floor for a turn that shows nothing but a spinner |
| `READ_WPM` (fit) | 180 wpm | Prose reading. Below US twelfth graders' 192 wpm (Spichtig et al. 2016) and Brysbaert's 238 wpm adult mean, since younger and second-language readers are slower (2.4) |
| `DIFF_LPM` (fit) | 8 lines/min | Diff and code reading. 480 LOC/hour, 2.4 times the 200 LOC/hour Kemerer and Paulk found effective for careful review, because this is reading for intent (2.4) |
| `TOOL_LINES_MAX` | 4 lines | Tool output a harness shows by default per call; the rest is collapsed |
| `BUCKET_MAX_S` (fit) | 600 s | Most reading one turn can bank. Bounds what a jiggler can collect after a huge diff; equal to grit's 10-minute think limit |
| `BUCKET_TTL_S` | 7,200 s | Output older than two hours is no longer "the answer you are reading" |
| `READ_HORIZON_S` | 600 s | Reading counts only within ten minutes of a human act in the project, before or after. The return rule uses the same bound, counted from the start of the silence (2.1) |
| `LOOKUP_BRACKET_S` | 30 s | How close project work must be on each side of a browser run |
| `LOOKUP_MAX_S` (fit) | 180 s | Longest credited browser or chat-app run. A documentation lookup, not an evening of video |

### 8.4 Attribution

| Parameter | v1 | Reason |
|---|---|---|
| `ATTRIB_STICKY_S` | 120 s | How long a multi-session terminal keeps the project of its last anchor when no upcoming lead applies. Matches the presence bridge |
| `PROC_SCAN_S` | 60 s | Interval of the daemon's harness process list, which classifies `--bare` sessions that fire no hooks (HS 3.1) |

### 8.5 Dark flow and progress

| Parameter | v1 | Reason |
|---|---|---|
| `MINHASH_PERMS` | 64 | 256 bytes per prompt, microseconds per comparison (TP A4) |
| `SHINGLE` | word 3-shingles; character 5-shingles under 6 words | Short prompts have too few word shingles for a stable estimate (TP A4) |
| `SIM_JACCARD` (fit) | 0.8 | Near-duplicate threshold from TP A4 |
| `REROLL_WINDOW_S` | 1,800 s | Re-rolls are counted within 30 minutes |
| `REROLL_FREE_ATTEMPTS` | 2 | The original and one retry are credited; Barke's exploration mode compares alternatives (PA 2.2), "over and over" starts at three |
| `REWRITE_OVERLAP` (fit) | 0.5 | Share of a turn's changed lines that the previous turn also changed, for a rewrite repeat |
| `REWRITE_FREE_REPEATS` | 1 | Same allowance as re-rolls: the third turn on the same lines is discounted |
| `SAME_ERROR_LIMIT` | 3 | Occurrences of one error signature before error pastes and go-aheads stop counting |
| `NOREAD_MIN_WORDS` | 100 | Shorter answers can be read in a few seconds, so speed proves nothing |
| `NOREAD_FRAC`, `NOREAD_WPM` | 0.25, 238 wpm | Unread means replying in under a quarter of the time at Brysbaert's mean rate, about 950 wpm (PA 3.2 S5) |
| `AUTOPILOT_RUN` | 3 | Consecutive unread go-aheads before discounting starts |
| `NO_PROGRESS_LIMIT_S` (fit) | 7,200 s | Credited time without any progress event before the half rate. Long enough for a hard bug |
| `NO_PROGRESS_WEIGHT` | 0.5 | Productive failure is real (Kapur), difficulty without progress isn't desirable (Bjork): half, not zero (PA 2.1) |
| `PROGRESS_RESTORE_S` | 86,400 s | A progress event within a day restores half-weighted seconds |
| `ERROR_RESOLVED_RUNS` | 2 | Runs without the signature before an error counts as resolved |

### 8.6 Rollups, submissions, lookback, witnessing

| Parameter | v1 | Reason |
|---|---|---|
| `SESSION_GAP_S` | 1,200 s | Credited minutes more than 20 minutes apart start a new work session (display only) |
| `DAY_CAP_S` | 43,200 s | Hard cap of 12 credited hours a day: Lookout's 12-hour session limit and Stardance's 10-hour devlog cap (HC 3.3, 4.2), and a bound on any attack (TP A6) |
| `DAY_FLAG_S` | 36,000 s | Over 10 credited hours in a day raises a review flag |
| `STREAK_DAY_S` | 900 s | Hackatime's 15-minute streak threshold, applied to credited time (PA 4.3) |
| `UNCOMMITTED_CAP_S` | 3,600 s | Credited time after the last commit in a submission window (TP A7 E4) |
| `UNKNOWN_BUCKET_MAX_S` | 120 s | Reading cap per turn when presence is unknown (lookback), and for a relayed anchor's session (2.4) |
| `UNKNOWN_PA_MAX_S` | 60 s | Permission-wait cap in lookback |
| `WITNESS_LAG_S` | 900 s | A chain head seen by the server within 15 minutes is witnessed; later is backfilled (SP 8.4; grit) |
| `CHAIN_HEAD_INTERVAL_S` | 300 s | How often the client sends its chain head (TP A3 T8) |

### 8.7 Tier-2 flags: starting points only

These change review priority, never credit (4.5). The values below are the public starting points from the research docs; the deployed thresholds live in the private server module and will drift from these (TP A8).

| Flag | Starting point | Source |
|---|---|---|
| `regular_timing` | 6 or more prompts with coefficient of variation of spacing under 0.1, or burstiness `B < -0.5` | grit; Goh and Barabási (TP A4) |
| `no_input_prompts` | At least 2 prompts and a quarter of all prompts with origin `no_input` | grit (PA 1.14) |
| `repeated_prompts` | Same prompt or template 3 or more times and in at least 30% of a session's prompts; templates under 12 characters ignored | grit (PA 1.14) |
| `one_shot_session` | One substantive prompt, no `E`, `CMD` or `TEST`, no follow-ups, more than 200 diff lines | This spec (4.4), after the handbook's "AI slop" (HC 3.7) |
| `low_survival` | Commit with more than 1 h attached and under 10% of added lines surviving | This spec (4.3); the surviving-lines measure is from TP A4 |
| `concurrent_devices` | Two devices credited in the same slot | TP A3 T11, T17 |
| `marathon` | More than 4 credited hours with no gap over 10 minutes | Ari's `MARATHON_SESSION` (TP A4) |
| `periodic_input` | Idle time that resets at a near-constant period, with no keyboard input where `input_keys` exists | Jigglers and Caffeine-style keepers (TP A3 T7) |
| `relayed_share` | More than half of a submission's credited seconds, or of any day's, rest on relayed anchors | This spec (2.2 rule 9, Risk 12) |

## Risks

1. **Level-B machines are the soft spot.** Where the OS gives only "some input happened" (Windows and X11 without the opt-in key counter, all of Wayland), a mouse jiggler satisfies the presence and origin tests, and a prompt driven through a pty or `tmux send-keys` can pass as typed. The rules still require a prompt per window and cap every window, but the per-turn credit is real. "The average Hack Clubber is on a 10 year old Windows laptop" (BOUNTY.md R8), so this is the common case, not the edge. Mitigations: key counting (counts only, no key identity) as the recommended default on Windows, the `periodic_input` and `regular_timing` flags, and audits weighted toward level-B hours. Whether the key counter can be on by default is an R6 decision (Open questions).
2. **A full puppeteer beats every rule.** An auto-typer with human cadence typing LLM-written prompts into the real terminal produces real OS input in the right window (6.6). No tier-1 rule can tell. Tier 2, witnessing and the live interview are the only defenses, as TP A6 says of any design.
3. **Leads credit time the rules can't see.** A lead credits up to 420 s before a long prompt to whoever was present and focused. On level-B machines that presence can be a jiggler, and an honest person may have been staring out of the window. Calibration may show `THINK_LEAD_S` should shrink, or that leads should scale with something other than prompt length.
4. **Honest work the rules under-credit.** Code reading in the editor with only the scroll wheel (scrolling isn't among the VS Code plugin's heartbeat triggers, HC 1.5), thinking away from the keyboard beyond the return rule's 120 s, manual testing of a web app beyond the 180 s lookup cap, work in a browser chat or an app builder (no local signal until the v2 extension, HS 10), SSH or cloud dev boxes (prompts arrive with no local input, 3.4), and the approvals, answers and stops a person gives in an agent app's own interface, which aren't messages (2.2). Each is a known downward bias. The pilot measures how large.
5. **Dark-flow false positives.** Tuning a value three times in a row, comparing three generations on purpose, or a long research stretch with no commit can be productive and still get discounted or halved. The codes make it visible, but under Ari's rule reviewers can only cut, not restore (HC 3.6), so a wrong discount stays wrong unless restoring is allowed (Open questions).
6. **Paraphrased loops that don't touch code.** Asking the same conceptual question five ways in a chat, with no edits, escapes all four detectors. A small on-device embedding model is the v1.1 fix; until then it is credited.
7. **Format drift.** Claude Code's and Cursor's transcript formats are internal and change between versions (HS 3.2, 17.7). A parser that silently breaks drops anchors, which under-credits without any error. Fixture tests per harness version and parse-failure counters on the user's dashboard are the guard.
8. **Goodhart.** The credit rule is public, so people will learn to keep the terminal focused, type longer prompts, or commit more often. Caps, the presence gate, private tier-2 flags and audits limit the gain; recaps that show what someone did, not a score to push up, limit the motive (PA 4.2, 4.4).
9. **No calibration data, no accuracy claim.** If Hack Club can't share review data and the pilot can't recruit enough labeled sessions, every number here stays a guess, and R2 can't be shown to pass.
10. **Lookback inflation.** Level-C hours are unverifiable by construction. If programs treat them like witnessed hours, the system inherits Hackatime's weaknesses. The handbook-third fallback in 5.4 exists for this, and it only works if programs keep the split visible.
11. **Desktops that hide focus.** GNOME on Wayland, the default desktop of Ubuntu and Fedora, whose newest releases offer no GNOME on X11 session (to verify), says nothing about which window is in front without pairtime's GNOME extension, and KDE and wlroots compositors say nothing in v1. Their minutes are level C (2.5): on the teen's own dashboard, never in public totals, build days or boards, and in a submission only where a program opts in to level C, at the handbook third, which pays hand coding on such a desktop nothing (5.4; Open question 8). The extension brings GNOME back to level B, but nothing checks that its report is true: an edited or patched extension can name the terminal while a game has focus, as an X client can by writing `_NET_ACTIVE_WINDOW` on X11 (DESIGN 7.12).
12. **Relayed prompts are the app's word.** An agent app's store vouches for typing on another device, and nothing on this device can check it. Any client holding the app's credentials can send messages the app records as a person's, and any process running as the person can mint such a credential or write the app's store. The `agent_app` class a witnessed prompt needs rests on the server's process name, which a script can give itself. So a script that drives real agents through the app, or fakes the app outright, reaches all of wall time at level C on the owner's own dashboard. What contains it is level C: never public, never a streak day, and in a submission only under a program's level-C opt-in, inside commit intervals whose closing commit is forge-verified, at most a third of `ai_logged_s` (5.4). The relay records add nothing to `ai_logged_s`, but the farm's own agent turns do, so the cap is the handbook's third of the agent time the farm causes, which the flat third would have paid anyway. The `relayed_share` flag and audits weighted toward relayed time are the rest. The v1.5 join of Open question 11 is how honest relayed time would rise above level C.

## Open questions

Only Karim or Hack Club staff can answer these.

1. **Is level-B evidence "significant evidence"?** Quality and Integrity must decide whether minutes backed by any-input presence (no keyboard-specific signal) justify crediting more than the handbook's third, or whether Windows users need the key counter on for submitted hours. (Hack Club staff; affects 5.4.)
2. **May pairtime count keystrokes by default on Windows and X11?** Counts only, per 15-second slot, never which keys. Antivirus heuristics and privacy-conscious users both dislike global keyboard hooks (HS 13.4, TP A4). (Karim, with Hack Club's privacy reviewers; R6.)
3. **Does watching and reading agent output count?** v1 credits it up to what the output could take to read (2.4). The vision post counts "actively solving problems"; Hack Club may want reading capped harder or not counted while the agent is still running. (Hack Club staff.)
4. **Thinking away from the keyboard.** Will reviewers credit silent thinking, and how much per gap? v1 gives 120 s plus prose reading after a silence that ends in a real act. (Hack Club staff; PA 7 question 3.)
5. **Can reviewers restore discounted time?** Ari only lets reviewers cut. If a reviewer decides a rewrite loop was legitimate tuning, can they add it back with a reason, or should pairtime's discounts become cuttable rows with the discounted seconds shown as captured? (Ari maintainers and Quality and Integrity.)
6. **Calibration data.** Will Hack Club share Stardance's per-project submitted and approved hours, the approval rate over reviewed hours only, and the Hackatime heartbeats behind them, under what privacy terms? (Hack Club staff; 7.4.)
7. **Who labels the pilot, and where does it run?** Can Hack Club reviewers label pilot sessions (paid or volunteer), and can the pilot be a pairtime-native YSWS with its own grants? (Karim and Hack Club staff; 7.5.)
8. **Level-C hours.** Are level-C hours acceptable for YSWS at all, and is capping them at the handbook's third the right rule? They come from lookback and side doors, and from desktops that hide which window is in front, whose minutes have witnessed presence behind them and might earn the handbook value of the same time, so that hand coding there isn't paid less than Hackatime pays it. (Hack Club staff; 2.5, 2.8, 5.4.)
9. **One number or two?** Do programs want AI-assisted and hand-work hours reported separately, or a single credited total with the split as evidence? (Hack Club staff; PA 7 question 7.)
10. **Caps.** Do the 12-hour daily cap, the 60-minute uncommitted cap and the two-hour no-progress half rate match what reviewers would do, or should they follow Stardance's 10 hours per devlog? (Hack Club staff.)
11. **Remote and WSL setups.** Are SSH dev boxes, Codespaces and WSL in scope for v1? WSL needs the two-agent hand-off (CP 6.4), and SSH needs a server-side join of keyboard input on one device with prompts on another. (Karim.) Half answered on 2026-11-22: agent apps such as T3 Code count, at level C, through 2.2 rule 9. A chat bot that hands a person's messages to an agent needs a design of its own and is left for later. Still open: SSH and Codespaces, WSL, and the v1.5 join that would raise a relayed prompt to level B when another linked device of the same account saw keyboard input with the app's client in focus just before the message.
12. **Private flag thresholds.** Can tier-2 thresholds live in a private module while the scorer and server stay AGPL-3.0, as Ari and Stardance keep their fraud rules out of public repos? (Karim, with legal advice if needed; HC open question 11.)
13. **The browser extension.** Is a browser extension that classifies `localhost` tabs as testing and documentation sites as lookups acceptable under R6, given that it can see URLs? (Karim and Hack Club's privacy reviewers.)
