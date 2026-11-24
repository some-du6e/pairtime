---
harness: claude_code
harness_version: 2.1.330
os: Ubuntu 24.04.4 LTS, kernel 7.0.14-28-pve (a Linux container on a Proxmox host)
captured_on: 2026-11-24
captured_by: an AI agent (pairtime's WP-AD1 build agent) driving a tmux pty, not a person
synthetic: false
scrubbed_by: pairtime fixtures scrub built by CI from #284 at ef49d23a7e6c (run 45321204585), then Anthropic's system prompt, tool definitions and other text of Claude Code's own replaced with placeholders by an agent's script, as the section on Anthropic's text below says; every file read by an agent, not by a person
kit: tools/capture at 6dc46b890a10, main at the time
script: tools/capture/SCRIPTS.md at 6dc46b890a10, Claude Code, steps 1, 2, 20, 16 and 18, in that order; steps 3 to 15, 17 and 21 to 23 skipped
otlp: http/json to tools/capture/otlp_sink.py, prompt, response and tool logging off
runs: cli (tmux 3.4 pty)
---

# Fixture provenance

## Why this capture is short

This capture is for the kit's `flags` and `entrypoint` files, which `tools/capture/dump-hook.sh` has written since #116. The 2.1.327 capture came before #116, so until this one no fixture had them. One interactive session ran steps 1, 2, 20 and 16 (`/exit`), then step 18, the headless run, ran from the same terminal. Every other step was skipped: 3 to 15, 17 and 21 to 23. The 2.1.327 capture has those.

## No human timing

No person typed anything in this capture. An AI agent drove Claude Code 2.1.330 through a tmux 3.4 pty on Linux, following SCRIPTS.md. It typed each prompt one character at a time, 60 to 160 ms apart, and pressed the keys that answer each dialog and the one permission prompt. So no interval in this fixture is a person's: not the typing, not the time before the prompt was answered, not the gaps between steps. An adapter or scorer test may use these files for what the harness sends and when it sends it, but never as evidence of how a person works.

## How the run was set up

- The capture ran in a Linux container on a Proxmox host, which is why the kernel reads 7.0.14-28-pve. The account was `root`, and `HOME` was a scratch folder, with the kit copied into it by `git archive` at 6dc46b890a10, the way a person has a clone. The scrub turned that folder into `/home/capture`, so the hook commands read `/home/capture/pairtime/tools/capture/...` and the capture folder (`ROOT`) is `/home/capture/pairtime-capture`, as in the 2.1.327 capture.
- Claude Code didn't sign in to a claude.ai account. It used `ANTHROPIC_AUTH_TOKEN` and `ANTHROPIC_BASE_URL`, pointing at an Anthropic-compatible router, so step 1 showed no sign-in screen. Every hook's `env` names both variables, telemetry has `anthropic.custom_base_url` `true` and no account or organization attributes, and tool_use ids start with `toolu_bdrk_`.
- A Python wrapper started tmux with only `HOME`, a plain `PATH`, the two auth variables, `DISABLE_AUTOUPDATER=1`, `LANG`, `USER`, `LOGNAME`, `SHELL` and `TERM`, so no variable of the agent's own harness reached the captured one. Against the 2.1.327 capture's `env` files, the only name missing is `XDG_DATA_DIRS`, which the wrapper didn't pass. `DISABLE_AUTOUPDATER=1` kept the version at 2.1.330 for the whole run. tmux adds `TMUX`, `TMUX_PANE`, `TERM_PROGRAM` and `TERM_PROGRAM_VERSION`, telemetry has `terminal.type` `tmux`, and the interactive session's `parents` end `bash`, `tmux: server`, `systemd`, `systemd`.
- The transcripts' `date` attachments say 2026-11-23 while every timestamp says 2026-11-24, around 03:15Z, because Claude Code takes the date from the machine's local clock, where it was still the evening of 2026-11-23.
- The kit's settings start the session in manual permission mode (`permissions.defaultMode` `default`).

## What the flags and entrypoint files show

| Session | Steps | Hook calls | `entrypoint` | `flags` | Transcript `entrypoint` | Telemetry `app.entrypoint` |
|---|---|---|---|---|---|---|
| Interactive `claude` | 1, 2, 20, 16 | 000001 to 000007, 000012 to 000014 | `cli` | `name=claude`, `print=no`, `stream_json=no`, `bare=no` | `cli` | `cli` |
| The nested `claude -p` that step 20 runs through Bash | 20 | 000008 to 000011 | `sdk-cli` | `name=claude`, `print=yes`, `stream_json=no`, `bare=no` | `sdk-cli` | none: it sent no telemetry |
| Headless `claude -p` | 18 | 000015 to 000019 | `sdk-cli` | `name=claude`, `print=yes`, `stream_json=no`, `bare=no` | `sdk-cli` | `sdk-cli` |

For 000006, the `PermissionRequest`, the hook command was `allow-once.sh`, so the nearest parent in its `parents` is that script's shell, and `flags` names the nearest harness above it, the interactive `claude`. The `-p` runs' transcripts also mark their turns `promptSource` `sdk` and `turnOrigin` `sdk`, where the interactive session's say `typed` and `human`.

## What happened at each step

- Step 1: the theme picker, the security notes and the folder trust dialog. The trust dialog now warns that the folder "configures hooks that can run commands without asking, declared in .claude/settings.local.json", and its default is still "No, exit", so the agent pressed Down, then Enter. Then `SessionStart` with `source` `startup` (000001).
- Step 2: Claude ran `grep` through Bash and read `wordcount.py`, both without asking (`claude_code.tool_decision` `source` `config`), and answered in one sentence. Hooks: `UserPromptSubmit`, `PostToolUse` for Bash, `Stop` (000002 to 000004).
- Step 20: `UserPromptSubmit` for the step 20 prompt (000005). Claude then asked to run `claude -p "Say nested."` through Bash. That fired `PermissionRequest` (000006), which `allow-once.sh` left to the person since the payload doesn't name step 22's file, and a `Notification` `permission_prompt` 6.1 s later (000007). The prompt offered "Yes", "Yes, and don't ask again for: claude *", "Yes, and switch to auto mode" and "No". The agent chose "Yes" 15 s after the request (`tool_decision` `source` `user_temporary`). The nested run fired `SessionStart` `startup`, `UserPromptSubmit` with the prompt `Say nested.`, `Stop` and `SessionEnd` `reason` `other` (000008 to 000011), with a `session_id` of its own. Its `parents` show claude under bash under the interactive claude, the first two without a terminal. It printed `nested`, which came back as the Bash result (`PostToolUse`, 000012), and Claude answered "It printed `nested`." (`Stop`, 000013). The nested run sent no telemetry: no OTLP body carries its `session.id`. No hook's `env` names an `OTEL_*` variable in any session here, the interactive one and step 18 included, though both exported telemetry. So Claude Code keeps those variables out of a hook's environment, and these files can't show whether the nested run had the exporter settings. The 2.1.327 capture's nested run sent no telemetry either, and its PROVENANCE.md wrongly says that run's hooks' `env` names every telemetry variable the outer session had.
- Step 16: `/exit` gave `SessionEnd` `reason` `prompt_input_exit` (000014). The transcript holds the command, its local command caveat and `Catch you later!`.
- Step 18: from the same shell, `SessionStart` `startup`, `UserPromptSubmit`, `PostToolUse`, `Stop` and `SessionEnd` `reason` `other` (000015 to 000019). The `PostToolUse` is for one Bash call that Claude ran without asking (`source` `config`), with two greps: one counts the lines that start a `def`, the other lists the lines with `def` or `lambda`. It printed `1`. Its four OTLP bodies have `app.entrypoint` `sdk-cli`.

## Scrub

`pairtime fixtures scrub` was the scrub. CI built the linux/amd64 binary in run 45321204585 from #284 at ef49d23a7e6c, which fixes #104. The run's head, 3dc0ff2f8aba, is ef49d23a7e6c plus one workflow file on a throwaway branch that only built and uploaded the binary. The branch is gone. GitHub keeps the run's log for this repository's 90 days, until about 2027-02-22, and the binary artifact only until about 2026-11-27. What stays here is the run id, the commit, the binary's SHA-256, 93a6a1f8867d0c0b7ec8ab2a83dc0c969b2926e7d01c8bb268cf75f75da770ec, and the version line it printed, `pairtime v0.0.0-ad1-scrub (commit 3dc0ff2f8aba, go1.26.11, linux/amd64)`. So no fraction of an OTLP `asDouble` value was taken for a card number. The scrub ran over a copy of the run with `--home` for the scratch home folder, and this machine's account, home, hostname and git identity found on its own. It replaced 203 spellings of the home folder and 126 `user.id` telemetry values, in 84 of the 201 files, and found nothing else: no key, no email address, no card, SSN or Aadhaar number. Each `user.id` placeholder is 28 bytes shorter than the value it replaced, so the `bytes_received` and `bytes_saved` in the `otlp/*.meta` files are the bodies' sizes before the scrub. It reported the one name that spells the scratch path, Claude Code's project folder under `transcripts/`, which was renamed by hand to the spelling of `/home/capture`, as in the 2.1.327 capture. A second run replaced nothing, and all 157 JSON documents parse.

After this build, #284 changed its rule for decimals at 9af13f360c00 (#289): a match right after a digit and a `.` is now dropped only when it is all digits, so a separated number such as `1.4111 1111 1111 1111` is found. No file here has a number of that shape, and a port of the final rule finds no card, SSN or Aadhaar number in any file, so the result would be the same.

A script that read the values from the environment searched every file and found no trace of the auth token, the router's URL or host name, this machine's hostname, the real home folder, the git identity, the path of the repo checkout, or any line of the agent's own instruction files. The agent read every file: each hook call's eight files, every distinct attribute and value in the OTLP bodies and their `.meta` files, and every string, number and boolean in the transcripts and the history. The only email address left is `noreply@anthropic.com`, in the `Co-Authored-By` trailer of the `remote_session_change` attachments. `.gitattributes` beside this file keeps git from changing line endings in the captured bytes.

## Anthropic's text

Claude Code 2.1.330 writes what it sends the model into its transcripts, as 2.1.327 does: the system prompt, every tool's definition, the lists of its built-in skills and agents, and the reminders it adds to the conversation. That is Anthropic's writing, and this repository is public under the AGPL and can't relicense it, so an agent replaced it with placeholders before this capture merged. The rule is the table in [the 2.1.327 capture's PROVENANCE.md](../../2.1.327/linux/PROVENANCE.md), applied with the same scripts. A placeholder reads `[<what it was>, removed: <N> characters]`, where N counts the Unicode code points that went. Keys, nesting, array lengths, tool names, parameter names, JSON-schema types, enums, patterns, defaults and limits are unchanged, and so is every byte outside the replaced strings.

571 strings changed, 357,699 characters in all, in the three transcripts. No hook payload changed. A check put the old string literals back into each changed file and got the old bytes exactly.

| Kind | Strings | Characters removed |
|---|---:|---:|
| tool description | 105 | 167,685 |
| Anthropic's system prompt | 66 | 82,456 |
| parameter description | 344 | 46,630 |
| system reminder | 36 | 36,695 |
| skill listing | 3 | 18,630 |
| agent listing line | 16 | 5,431 |
| local command caveat | 1 | 172 |

The 2.1.327 table's other kinds come from steps this capture skipped: the `/loop` and `/goal` instructions, the task notification note, the tool rejection message and the background agent notice. 2.1.330 wrote no entry type or attachment that 2.1.327 didn't. Afterwards no kept string shared a 40-character run with the removed text, except the capture's own paths and the two attribution trailers, and the agent read every remaining distinct string of 40 characters or more.

Kept, as in 2.1.327:
- The capture's own content: the prompts from SCRIPTS.md, Claude's replies, its tool calls and their output, and the scratch project's files. Claude's one `thinking` block holds only a signature, with no text.
- Names and values in fields of their own: tool, parameter, skill and agent names, the environment's `snapshot`, the model's `identity`, the date, and the two attribution trailers in `remote_session_change`, `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` and the pull request line, which are settings values.
- Short lines Claude Code writes about what just happened, such as `Catch you later!`.
- The OTLP bodies, unchanged. With prompt, response and tool logging off they carry no prompt or tool text. The only text of Anthropic's in them is the one-line `description` of each of the four metrics, such as `Number of tokens used`.
- `history.jsonl`, which holds only what was typed.

What no longer matches: the hashes Claude Code took over the removed text stay as written. Those are `shapeHash`, the `hash` of each `api-request-blob` with the `tail` and `requestDigest` of the `api-request` entries that point at them, and the `digests` of `skill_listing`. The sizes in `transcripts/INDEX.txt` are the source files', from before either scrub.

## Files

Everything under `cli/` but `pending.md` is the run folder the kit wrote, scrubbed as above: 201 of its 202 files. `pending.md` was written by hand after the scrub, so `TestConformance` and `pairtime adapters test` report the run as pending.

| Path | What it is |
|---|---|
| `.gitattributes` | `* -text`, so git never changes the captured bytes' line endings |
| `cli/capture.json` | How `prepare.py` set the run up: the harness, the hook form (`exec`), the settings folder |
| `cli/STEPS.txt` | The step numbers typed into the sink before each step, with their times: 1, 2, 20, 16, 18 |
| `cli/pending.md` | Why the Claude Code adapter has no expected records for this run yet, which `adapters.Conform` reads so the run is reported as pending |
| `cli/hooks/000001/` to `cli/hooks/000019/` | One folder per hook call, in arrival order: `argv`, `cwd`, `entrypoint`, `env`, `flags`, `parents`, `stdin` and `time` |
| `cli/otlp/000001` to `cli/otlp/000021`, `.json` and `.meta` | 10 OTLP log bodies and 11 metric bodies, each with its arrival time, path, content type and header names |
| `cli/transcripts/INDEX.txt` | Each copied file with its source path, size and modification time |
| `cli/transcripts/home/pairtime-capture/claude-home/history.jsonl` | The three history entries: the step 2 and step 20 prompts and `/exit` |
| `cli/transcripts/.../fb5eb70a-3021-4407-bbea-6e92b2ced0b7.jsonl` | The interactive session: steps 1, 2, 20 and 16 |
| `cli/transcripts/.../78f79adc-1337-4b01-bcae-8c6780b01d19.jsonl` | The nested `claude -p` of step 20 |
| `cli/transcripts/.../00dc46c4-0269-4c28-a3fd-4c0b6012219a.jsonl` | The headless run of step 18 |

The three session transcripts are in `cli/transcripts/home/pairtime-capture/claude-home/projects/-home-capture-pairtime-capture-projects-cli/`.
