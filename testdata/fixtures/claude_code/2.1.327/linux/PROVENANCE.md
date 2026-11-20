---
harness: claude_code
harness_version: 2.1.327
os: Ubuntu 24.04.4 LTS, kernel 7.0.14-28-pve (a Proxmox VM)
captured_on: 2026-11-20
captured_by: an AI agent (pairtime's WP-AD1 build agent) driving a tmux pty on swiss, not a person
synthetic: false
scrubbed_by: pairtime fixtures scrub v0.0.0-snapshot.49 (CI-built at e3621dd0b101, release.yml run 44773070395), then tools/capture/scrub.py at b2dac8f74bd0 as a dry-run second check; two false positives restored by hand; checked by an agent with the searches listed below, not read by a person
kit: tools/capture at b2dac8f74bd0 (branch wp-ad1-kit-step22, which adds steps 22 and 23 to main's kit)
script: tools/capture/SCRIPTS.md at b2dac8f74bd0, Claude Code, steps 1 to 14, 16 to 18 and 20 to 23; step 15 skipped
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
- Step 20: the nested run has a `session_id` of its own (`SessionStart` `startup`, `UserPromptSubmit`, `Stop`, `SessionEnd` `reason` `other`), and its `parents` show claude under claude.
- Step 11: `Notification` `idle_prompt` about 60 s after the answer.
- Step 12: the loop fired three times, not twice: the follow-up was typed after the third run had finished, so the two wouldn't overlap. Each loop run's `Stop` has one entry in `session_crons`.
- Steps 13 and 21: `STEPS.txt` marks step 21 twice. The first try, at 17:06:59 UTC, was typed while a permission dialog had focus, so the text never reached the input box; its final Enter accepted the pending edit of `wordcount.py` (option 1, Yes), which step 13 says to accept anyway. The second, at 17:07:39 UTC, is the real step 21: the message was queued, and its `UserPromptSubmit` came when Claude took it up. The goal was met at its first check, so no continued turn ran and every `Stop` has `stop_hook_active` false.
- Step 14: `SessionEnd` `reason` `clear`, then `SessionStart` `source` `clear`.
- Step 15 was skipped, so there is no `StopFailure`. It needs the network off, and on swiss that would have cut off every other agent on the machine.
- Steps 16 and 17: `/exit` gives `SessionEnd` `reason` `prompt_input_exit`. `claude --continue` resumed the session that `/clear` started (`SessionStart` `source` `resume`).
- Step 18: `SessionStart` `startup` through `SessionEnd` `reason` `other`, and `app.entrypoint` in telemetry.

## Scrub

`pairtime fixtures scrub` was the scrub: the linux/amd64 binary from release.yml run 44773070395, built from commit e3621dd0b101, a merge onto main at 84ca8eb whose `cmd/pairtime` scrub code and `pkg/redact` are the same as main's. It ran over a copy of the run with `--home` for the scratch home folder, and this machine's account, home, hostname and git identity found on its own. It replaced 880 spellings of the home folder and 574 `user.id` telemetry values. It also took the fractional digits of two `asDouble` values in OTLP metric bodies for card numbers (`4.614999999999999` in `otlp/000022.json` and `1.3239999999999998` in `otlp/000081.json`), which would have left invalid JSON; both were put back by hand. Claude Code's project folder name under `transcripts/`, which spells the scratch path, was renamed by hand to the spelling of `/home/capture`, as `scrub.py` renames it.

Then `scrub.py --dry-run` over the scrubbed copy found nothing to replace. It found no line of this machine's own instruction files in the capture, and the only prompt not in SCRIPTS.md is the `<task-notification>` of step 10. A search of every file for the auth token, the router's URL and host name, this machine's hostname, the real home folder, the project owner's name and handles, and the path of the repo checkout found none. All 619 JSON documents parse. The agent read every user and assistant message in the transcripts, the environment and git status blocks Claude Code adds, every distinct absolute path, URL and email address (only `noreply@anthropic.com` remains, from Claude Code's commit instructions), and the values of the telemetry attributes that could carry identity. `.gitattributes` beside this file keeps git from changing line endings in the captured bytes.
