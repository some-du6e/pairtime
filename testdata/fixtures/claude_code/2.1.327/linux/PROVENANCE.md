---
harness: claude_code
harness_version: 2.1.327
os: Ubuntu 24.04.4 LTS, kernel 7.0.14-28-pve (a Proxmox VM)
captured_on: 2026-11-20
captured_by: an AI agent (pairtime's WP-AD1 build agent) driving a tmux pty on swiss, not a person
synthetic: false
scrubbed_by: pairtime fixtures scrub v0.0.0-snapshot.49 (CI-built at e3621dd0b101, release.yml run 44773070395), then tools/capture/scrub.py at b2dac8fda350 as a dry-run second check; two false positives restored by hand; then Anthropic's system prompt, tool definitions and other text of Claude Code's own replaced with placeholders by an agent's script, as the section on Anthropic's text below says; checked by an agent with the searches listed below, not read by a person
kit: tools/capture at b2dac8fda350, the first commit of PR #98, which adds steps 22 and 23 to main's kit; #98 merged as 3c4d722 after changing only SCRIPTS.md's wording and a comment in prepare.py
script: tools/capture/SCRIPTS.md at b2dac8fda350, Claude Code, steps 1 to 14, 16 to 18 and 20 to 23; step 15 skipped
otlp: http/json to tools/capture/otlp_sink.py, prompt, response and tool logging off
runs: cli (tmux 3.4 pty)
---

# Fixture provenance

## No human timing

No person typed anything in this capture. An AI agent drove Claude Code 2.1.327 through a tmux 3.4 pty on Linux (the host swiss), following SCRIPTS.md: it typed each prompt one character at a time, 60 to 160 ms apart, pasted step 3 as one bracketed paste, pressed the keys that answer each permission prompt, and slept for the waits the script asks for (10 s at step 4, 75 s at step 11, about 2.5 minutes at step 12). So no interval in this fixture is a person's: not the typing, not the time before a prompt was answered, not the gaps between steps. An adapter or scorer test may use these files for what the harness sends and when it sends it, but never as evidence of how a person works.

## How the run was set up

- The account was `root`, and `HOME` was a scratch folder, with the kit copied into it, the way a person has a clone. The scrub turned that folder into `/home/capture`, so the hook commands in the transcripts read `/home/capture/pairtime/tools/capture/...` and the capture folder (`ROOT`) is `/home/capture/pairtime-capture`. `ls` output in the transcripts shows the file owner `root`.
- Claude Code didn't sign in to a claude.ai account. It used `ANTHROPIC_AUTH_TOKEN` and `ANTHROPIC_BASE_URL`, pointing at an Anthropic-compatible router, so step 1 showed no sign-in screen. That shows in the files: every hook's `env` names both variables, telemetry has `anthropic.custom_base_url` `true` and no account or organization attributes, and tool_use ids start with `toolu_bdrk_`. A signed-in account would give other ids and add its account attributes, which the scrub then replaces.
- `DISABLE_AUTOUPDATER=1` was set, so the version stayed 2.1.327 for the whole run. tmux adds `TMUX`, `TMUX_PANE`, `TERM_PROGRAM` and `TERM_PROGRAM_VERSION` to the environment, telemetry has `terminal.type` `tmux`, and each hook's `parents` ends `bash`, `tmux: server`, `systemd`.
- The kit's settings start the session in manual permission mode (`permissions.defaultMode` `default`). Without that, 2.1.327 starts in auto mode, where a classifier answers most of the prompts the script needs.

## What happened at each step

- Step 1: the theme picker, the security notes and the folder trust dialog, then `SessionStart` with `source` `startup`.
- Step 2: Claude ran `grep` through Bash (a `PostToolUse` for Bash) and read the file, without asking.
- Step 4: after the edit was accepted (10 s after its `PermissionRequest`), Claude asked to run a `python3` check of `count_lines`, which the script doesn't mention. It was accepted once. Both prompts sent a `Notification` (`permission_prompt`) about 6 s after their `PermissionRequest`.
- Step 6: before asking to run `rm`, Claude ran a read-only `ls` and `git` command without asking. Answering No to `rm` ended the turn as interrupted, with no `Stop`.
- Step 22: `allow-once.sh` allowed the write 2 s after the `PermissionRequest`, Claude Code showed "Allowed by PermissionRequest hook", and `allow-once` holds the decision. No `Notification` fired. The `claude_code.tool_decision` for it has `source` `hook`; the person's answers have `user_temporary` or `user_reject`, and commands Claude Code allows on its own have `config`.
- Step 23: `connections` names the sink (`python3`) and pid 3467805 `claude`, the pid every hook of the main session names as its nearest parent. The exporter keeps its connection open between exports. While step 20 ran, `lsof` every 0.5 s never showed the nested `claude -p` holding a connection.
- Step 7: `git status` is read-only, so Claude ran it without asking: no `PermissionRequest`.
- Step 9: Esc was pressed about 1.2 s after Enter. Claude Code put the prompt back in the input box, which was emptied with Ctrl+U before the next step.
- Step 10: 2.1.327 ran the subagent in the background. The main turn stopped at once, the subagent's two test runs each asked for permission and were accepted, and when it finished Claude Code sent a `<task-notification>` prompt (machine text, with a `UserPromptSubmit`) and reported back.
- Step 20: the nested run has a `session_id` of its own (`SessionStart` `startup`, `UserPromptSubmit`, `Stop`, `SessionEnd` `reason` `other`), and its `parents` show claude under claude, both without a terminal. It sent no telemetry: no OTLP body carries its `session.id`, although its hooks' `env` names every telemetry variable the outer session had. The headless run at step 18 did send telemetry.
- Step 11: `Notification` `idle_prompt` about 60 s after the answer.
- Step 12: the loop fired three times, not twice: the follow-up was typed after the third run had finished, so the two wouldn't overlap. Each loop run's `Stop` has one entry in `session_crons`.
- Steps 13 and 21: `STEPS.txt` marks step 21 twice. The first try, at 17:06:59 UTC, was typed while a permission dialog had focus, so the text never reached the input box; its final Enter accepted the pending edit of `wordcount.py` (option 1, Yes), which step 13 says to accept anyway. The second, at 17:07:39 UTC, is the real step 21: the message was queued, and its `UserPromptSubmit` came when Claude took it up. The goal was met at its first check, so no continued turn ran and every `Stop` has `stop_hook_active` false.
- Step 14: `SessionEnd` `reason` `clear`, then `SessionStart` `source` `clear`.
- Step 15 was skipped, so there is no `StopFailure`. It needs the network off, and on swiss that would have cut off every other agent on the machine.
- Steps 16 and 17: `/exit` gives `SessionEnd` `reason` `prompt_input_exit`. `claude --continue` resumed the session that `/clear` started (`SessionStart` `source` `resume`).
- Step 18: `SessionStart` `startup` through `SessionEnd` `reason` `other`, and `app.entrypoint` in telemetry.

## Scrub

`pairtime fixtures scrub` was the scrub: the linux/amd64 binary from release.yml run 44773070395, built from commit e3621dd0b101, a merge onto main at 84ca8eb whose `cmd/pairtime` scrub code and `pkg/redact` are the same as main's. It ran over a copy of the run with `--home` for the scratch home folder, and this machine's account, home, hostname and git identity found on its own. It replaced 880 spellings of the home folder and 574 `user.id` telemetry values. It also took the fractional digits of two `asDouble` values in OTLP metric bodies for card numbers (`4.614999999999999` in `otlp/000022.json` and `1.3239999999999998` in `otlp/000081.json`), which would have left invalid JSON; both were put back by hand. Claude Code's project folder name under `transcripts/`, which spells the scratch path, was renamed by hand to the spelling of `/home/capture`, as `scrub.py` renames it.

Then `scrub.py --dry-run` over the scrubbed copy found nothing to replace. It found no line of this machine's own instruction files in the capture, and the only prompt not in SCRIPTS.md is the `<task-notification>` of step 10. A search of every file for the auth token, the router's URL and host name, this machine's hostname, the real home folder, the project owner's name and handles, and the path of the repo checkout found none. All 619 JSON documents parse. The agent read every user and assistant message in the transcripts, the environment and git status blocks Claude Code adds, every distinct absolute path, URL and email address, and the values of the telemetry attributes that could carry identity. The environment and git status blocks have since gone with the rest of Claude Code's own text, as the next section describes. The only email address left is `noreply@anthropic.com`, in the `Co-Authored-By` trailer of the `remote_session_change` attachments. `.gitattributes` beside this file keeps git from changing line endings in the captured bytes.

## Anthropic's text

Claude Code 2.1.327 writes what it sends the model into its transcripts: the system prompt, every tool's definition, the lists of its built-in skills and agents, the reminders it adds to the conversation, and the instructions some slash commands expand into. That is Anthropic's writing, 647,484 characters of it in this capture, most of it repeated with every request. This repository is public under the AGPL and can't relicense it. The fixtures are here to pin the formats, meaning the field names, the nesting and which entries appear when, so before this capture merged an agent replaced that text with placeholders.

A placeholder reads `[<what it was>, removed: <N> characters]`, where N counts the Unicode code points that went. In markup the tags stay, with the line breaks next to them, and only the text between goes, so a rendered reminder reads `<system-reminder>\n[system reminder, removed: 714 characters]\n</system-reminder>`. Keys, nesting, array lengths, tool names, parameter names, JSON-schema types, enums, patterns, defaults and limits are unchanged, and so is every byte outside the replaced strings. 1,071 strings changed in six files, the five transcripts and `hooks/000047/stdin`. A check put the old string literals back into each changed file and got the old bytes exactly.

### What was replaced

| Text | Where it was | Placeholder |
|---|---|---|
| The system prompt | `shape.system[].text` in `api-request-shape` entries; `systemPrompt[]` and `cliPrefix` in `prompt_snapshot` attachments; the text of every `api-request-blob` whose `message.role` is `system` | `Anthropic's system prompt` |
| Tool descriptions | `shape.tools[].description`; `tools[].description` and `tools[].schema.description` in `prompt_snapshot`; `entries[].description` in `deferred_tools_record` | `tool description` |
| Free text in tool input schemas | every string at a `description` key anywhere below an `input_schema` | `parameter description` |
| The built-in skill list | `attachment.content` of `skill_listing` | `skill listing` |
| The built-in agent list | each line of `attachment.addedLines` in `agent_listing_delta` | `agent listing line` |
| System reminders | the text inside every `<system-reminder>` tag, in the attachments' `rendered[].content` and in `api-request-blob` text, and the two attachment fields reminders are made from, `attachment.text` of `model` and `attachment.context.gitStatus` of `session_context` | `system reminder` |
| What `/loop` and `/goal` expand into | the `isMeta` user entry that follows each command, and its copy in an `api-request-blob` | `instructions for /loop`, `instructions for /goal` |
| The local command caveat | the text inside `<local-command-caveat>` | `local command caveat` |
| The note in step 10's task notification | the text inside `<note>` in the `<task-notification>`, in the `prompt` of `hooks/000047/stdin` and in the main transcript's user entry, queue operation and request blob, replaced alike everywhere so the hook and the transcript still agree | `task notification note` |
| Tool results Claude Code writes to steer the model | the message the refused `rm` of step 6 got back, whose `toolUseResult` is `User rejected tool use`, and the notice step 10's background subagent returned at launch, whose `toolUseResult.status` is `async_launched` | `tool rejection message`, `background agent notice` |

The one reminder that wraps a `<task-notification>`, in the request blob that carried step 10's report to the model, keeps the notification. Only its own lead text went, and the note inside.

| Kind | Strings | Characters removed |
|---|---:|---:|
| tool description | 195 | 297,126 |
| Anthropic's system prompt | 103 | 141,496 |
| parameter description | 664 | 88,185 |
| system reminder | 61 | 63,142 |
| skill listing | 5 | 31,000 |
| instructions for /loop | 2 | 11,962 |
| agent listing line | 28 | 10,473 |
| background agent notice | 1 | 1,066 |
| instructions for /goal | 2 | 940 |
| local command caveat | 5 | 860 |
| task notification note | 4 | 784 |
| tool rejection message | 2 | 450 |

The strings add up to 1,072, one more than the strings that changed, because the reminder around step 10's notification holds two placeholders, one for its lead text and one for the note.

### What was kept

- The capture's own content: the prompts from SCRIPTS.md, the `<task-notification>` and scheduled `/loop` prompts Claude Code sent while following them, Claude's replies, its tool calls and their output, the scratch project's files, the `/goal` condition and the goal check's `reason`. Claude's `thinking` blocks hold only signatures, with no text.
- Names and values in fields of their own: tool, parameter, skill and agent names, such as `addedNames`, `names`, `addedTypes` and `builtInTypes`, the environment's `snapshot`, the model's `identity`, the date, and the two attribution trailers in `remote_session_change`, `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` and the pull request line, which are settings values.
- Short lines Claude Code writes about what just happened, which an adapter may match on and none of which runs past one line: `[Request interrupted by user for tool use]`, `User rejected tool use`, `Tool loaded.`, the Write and Edit success lines, CronCreate's `Scheduled recurring job ...` line, `Cancelled job 3cf0fef2.`, `Running scheduled task (...)`, `Goal set: ...`, `Catch you later!` and the `Notification` messages.
- The OTLP bodies, unchanged. With prompt, response and tool logging off they carry no prompt or tool text. The only text of Anthropic's in them is the one-line `description` of each of the six metrics, such as `Number of tokens used`, which belongs to the metric's definition.
- `history.jsonl`, which holds only what was typed.

### What no longer matches

- The hashes Claude Code took over the removed text stay as written, so they don't match the placeholders: `shapeHash`, the `hash` of each `api-request-blob` with the `tail` and `requestDigest` of the `api-request` entries that point at them, and the `digests` of `skill_listing`.
- The task notification prompt is now 1631 characters in `hooks/000047/stdin` and in the transcript, which agree. OTLP's `prompt_length` for it says 1799, its length before either scrub. The home-path scrub had already made it 21 characters shorter.
- The sizes in `transcripts/INDEX.txt` are the source files', from before either scrub.

### Repeating it

The replacement was a Python script an agent ran over the scrubbed run folder, and it isn't in this repo. It parses each JSON document while keeping every string literal's byte range, picks strings by the table above, and writes each placeholder into its literal's place, so nothing else moves. Afterwards no kept string shared a 40-character run with the removed text, except the capture's own paths, git status lines and `/loop` prompt and the two attribution trailers, and the agent read every remaining distinct string of 40 characters or more. A later Claude Code capture can apply the same table, then look for entry types and attachments that this capture doesn't have and that carry text of Claude Code's own.
