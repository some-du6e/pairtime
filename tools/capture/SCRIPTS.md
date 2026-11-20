# Capture scripts

One script per harness. Each lists the exact actions that make the harness fire every event its pairtime adapter maps (DESIGN 5.1), so a capture is complete and two captures of the same version can be compared. Read [README.md](README.md) first; it says what the kit does and doesn't do on your machine.

Type or paste each prompt exactly as written. The scrub flags any prompt line that isn't in this file, because a fixture should hold no text of yours. If you type something private by mistake, stop and quit the harness, delete the whole `ROOT` folder, which holds what you typed in its runs, history and transcripts, and start over with a new `ROOT`.

Step numbers are shared across harnesses: step 4 is always "a permission accepted", even where a harness skips a step before it. Every script has step 3, the pasted prompt, right before step 4, whose prompt refers back to it. Steps 19 to 21 came later than the rest, so their numbers are higher than the steps around them: do every step in the order its script lists it. Before each step, type its number and Enter into the sink's terminal, so `STEPS.txt` says which step every hook call belongs to.

## Before any capture

- You need Python 3.8 or later (`py` instead of `python3` on Windows), git, the harness installed, and a clone of this repo.
- Pick a new, empty capture folder outside any git repo, such as `~/pairtime-capture`. Below it's `ROOT`. Raw captures live there and never enter the repo; only the scrubbed copy does.
- The harness runs with a settings folder of its own, which `prepare.py` makes in `ROOT` and points the harness at with one variable. That keeps your own instructions, hooks, plugins, skills and MCP servers out of the capture; otherwise their text, or what they print, would end up in a public fixture. You sign in to the harness once more inside that folder. Cursor, Copilot in VS Code, Devin and Cline can't be pointed at one, so for them `prepare.py` refuses to start while a file of yours that they would load holds something. It names each file and prints the commands to move it aside for the capture and back afterwards.
- Close other sessions of the harness while you capture. Claude Code's hooks are confined to the scratch project, but for some harnesses below the hooks are user-wide and would capture every session.
- In the scratch project, open nothing of yours, and don't let the agent read outside it. If it asks to, answer no.

## Claude Code

Captured by WP-AD1. Run it once in the CLI (run `cli`) and once in the VS Code extension (run `vscode`), on Windows and on macOS or Linux.

### Setup

1. From your clone of this repo:

   ```sh
   python3 tools/capture/prepare.py claude_code ROOT cli
   ```

   It makes `ROOT/projects/cli` (a tiny made-up Python project with one failing test, and a made-up git identity), `ROOT/runs/cli`, `ROOT/claude-home`, Claude Code's settings folder for the capture, and `ROOT/projects/cli/.claude/settings.local.json`, which points every DESIGN 5.1 hook at the dumper: `SessionStart`, `UserPromptSubmit`, `Stop`, `StopFailure`, `Notification`, `PermissionRequest`, `PostToolUse` (matcher `Edit|Write|Bash`), `SubagentStart`, `SubagentStop`, `SessionEnd`. Every run gets a scratch project of its own, so the VS Code run starts from the same files the CLI run did. It prints the commands below with your paths filled in.
2. Terminal 1: start the sink, `python3 tools/capture/otlp_sink.py ROOT/runs/cli`.
3. Terminal 2: set the variables `prepare.py` printed, then `cd ROOT/projects/cli`. `CLAUDE_CONFIG_DIR` points Claude Code at `ROOT/claude-home` instead of your `~/.claude`, and `CLAUDE_CODE_DISABLE_CLAUDE_MDS=1` stops it reading CLAUDE.md files from the folders above the project, where it would find `~/.claude/CLAUDE.md` again. The rest are telemetry: OTLP over `http/json` to the sink, `OTEL_METRICS_INCLUDE_ENTRYPOINT=true`, prompt, response and tool logging off. Claude Code ignores telemetry variables set in project settings, so they have to come from this shell.

If no `hooks/` folder appears in `ROOT/runs/cli` after step 2 below, open `/hooks` in Claude Code to see whether the hooks loaded. On macOS and Linux each hook names the dumper itself as its command, with the run folder and the event as its `args`. A version that doesn't take `args` either rejects the hooks or runs the dumper without them, and without them it records nothing and reads nothing. Either way, run `prepare.py` again with `--shell-form` under a new run name. On Windows every hook is already one `powershell.exe` command line, so there is no `args` to drop.

### Steps

| # | Do this | It should fire |
|---|---|---|
| 1 | Run `claude`. The first time, it asks you to pick a theme and sign in, because `ROOT/claude-home` is new: do both. Accept the folder trust dialog if it appears: hooks don't run in a folder until it's accepted | `SessionStart` (`source` startup) |
| 2 | Type, don't paste, the first prompt below | `UserPromptSubmit`, `Stop` |
| 3 | Copy the three-line block below and paste it as one prompt, then press Enter | `UserPromptSubmit` with the pasted text expanded, `Stop` |
| 4 | Type the step 4 prompt. When Claude asks to edit `wordcount.py`, wait 10 seconds without touching anything, then accept once, not "always" | `PermissionRequest`, `Notification` (permission prompt), `PostToolUse` (Edit), `Stop` |
| 5 | Type the step 5 prompt and accept the new file | `PermissionRequest`, `PostToolUse` (Write), `Stop` |
| 6 | Type the step 6 prompt. When it asks to run `rm`, answer no | `PermissionRequest`, and no `PostToolUse` |
| 7 | Type the step 7 prompt and accept the command | `PostToolUse` (Bash), `Stop` |
| 8 | Type the step 8 prompt and accept the command. One test fails | `PostToolUse` (Bash) with the failure, `Stop` |
| 9 | Type the step 9 prompt. As soon as Claude starts working, press Esc once | `UserPromptSubmit`, and no `Stop` |
| 10 | Type the step 10 prompt and accept what it asks | `SubagentStart`, `SubagentStop`, `Stop`, a subagent transcript |
| 20 | Type the step 20 prompt and accept the command. Claude starts a second Claude Code with its Bash tool | `PermissionRequest` and `PostToolUse` (Bash) for the command, `Stop`. From the nested run: `SessionStart`, `UserPromptSubmit`, `Stop` and `SessionEnd` with a `session_id` of their own, whose `parents` show claude under claude, and a transcript of its own |
| 11 | After Claude answers, don't type anything for 75 seconds | `Notification` (idle prompt) |
| 12 | Type the step 12 command, then wait about two and a half minutes without typing, so the loop fires twice. Then type the step 12 follow-up | `UserPromptSubmit` per loop run with no human mark, `Stop` with `session_crons` |
| 13 | Type the step 13 command. While it's still working, do step 21, then let it run to the end, accepting edits and test runs | `Stop` with `stop_hook_active` on the continued turns |
| 21 | While step 13 is still working, type the step 21 prompt into Claude Code and press Enter. It waits in Claude Code's queue until Claude takes it up | A `queue-operation` entry with `operation` enqueue in the transcript, and later its dequeue (HS 3.2). Whether a `UserPromptSubmit` comes with it is one of the things this capture finds out |
| 14 | Type `/clear` | `SessionEnd` (`reason` clear), `SessionStart` (`source` clear) |
| 15 | Optional: turn the network off, type the step 15 prompt, wait for the error, turn the network on | `StopFailure` |
| 16 | Type `/exit` | `SessionEnd` |
| 17 | Run `claude --continue`, type the step 17 prompt, then `/exit` | `SessionStart` (`source` resume), `UserPromptSubmit`, `Stop`, `SessionEnd` |
| 18 | In the same terminal, run the step 18 command | the same hooks from a headless run; `app.entrypoint` in telemetry |

Step 2:

```text
What does count_words do? Answer in one sentence.
```

Step 3, pasted as one block:

```text
Here is a spec. Say in one sentence whether it is clear, and don't write any code yet.
count_lines(text) returns how many lines of text are not empty.
A line holding only spaces counts as empty.
```

Step 4:

```text
Add count_lines to wordcount.py, following that spec.
```

Step 5:

```text
Create a file named notes.txt holding the single line: capture test
```

Step 6:

```text
Delete README.md with a shell command.
```

Step 7:

```text
Run git status and sum it up in one line.
```

Step 8:

```text
Run the unit tests and tell me which test fails. Don't fix anything.
```

Step 9:

```text
Rewrite every function in this project with a long comment explaining each line.
```

Step 10:

```text
Use a subagent to review wordcount.py, and report back in two sentences.
```

Step 20, which Claude runs as a nested, headless Claude Code:

```text
Run this exact command with your Bash tool and tell me what it printed: claude -p "Say nested."
```

Step 12, then the follow-up once the loop has fired twice:

```text
/loop 1m Reply with the word tick and nothing else.
```

```text
Stop the loop.
```

Step 13:

```text
/goal Make the failing test pass, then stop.
```

Step 21, typed while step 13 is still working:

```text
When you're done, say which test you fixed.
```

Step 15:

```text
Say hello.
```

Step 17:

```text
Say done.
```

Step 18:

```sh
claude -p "Reply with the number of functions in wordcount.py and nothing else."
```

### The VS Code run

Quit VS Code completely, including any window in the background. Run `prepare.py claude_code ROOT vscode`, restart the sink on `ROOT/runs/vscode`, set the variables it printed in a terminal, and start VS Code from that terminal with `code ROOT/projects/vscode`, so the extension inherits them and uses `ROOT/claude-home` too. Open the Claude Code panel and do steps 1, 2, 3, 4, 7, 9, 10 and 16. Close the panel's session from its menu for step 16.

The Desktop app's Code tab doesn't read a terminal's variables, so it can't be pointed at `ROOT/claude-home` and would load your own instructions and plugins. Don't capture it with this kit: `collect.py` refuses any session whose transcript is outside `ROOT/claude-home`.

## After any capture

1. Quit the harness, so every transcript is complete. Stop the sink with Ctrl+C.
2. Copy the transcripts the hooks named, and only the lines of the prompt history that came from the captured sessions. For Claude Code the history is in its settings folder for the capture:

   ```sh
   python3 tools/capture/collect.py ROOT/runs/cli --history ROOT/claude-home/history.jsonl
   ```

   `prepare.py` printed this command with your paths, and each harness's section below says which history file it has. For harnesses whose hooks don't say where the log is, add `--extra PATH` for that one session's file. It shows the list and asks first. Never pass a whole store such as Cursor's `state.vscdb`: it holds all your chats. When the harness had a settings folder of its own, `collect.py` copies nothing from outside it, since a session file from anywhere else ran with your own settings; start over with a new `ROOT` if it says so.
3. Scrub into the repo, with the scrub command `prepare.py` printed: it has your run's paths and the fixture folder filled in. Add `--also` for anything private the kit can't know: your real name, your school, another handle. For the Claude Code CLI run it looks like this:

   ```sh
   python3 tools/capture/scrub.py ROOT/runs/cli testdata/fixtures/claude_code/<version>/<os>/cli --also "Your Name"
   ```

   The fixture folder is `testdata/fixtures/<harness>/<version>/<os>/<run>`. `<harness>` is the name you gave `prepare.py` (`claude_code`, `codex`, `cursor`, `copilot`, `gemini`, `devin` or `cline`) and `<run>` the run's name. `<version>` is the version `prepare.py` found with the harness's `--version`. If it printed `<version>` instead, use the number the harness shows in its About box or, for an extension such as Cline, in VS Code's Extensions view. `<os>` is `windows`, `macos` or `linux`.
4. Read the report it prints. Anything under STILL PRESENT is a known value inside a longer word. If the longer word is private too, such as `karimsLaptop`, add it with `--also`. If it's your account name or hostname inside an ordinary word or a harness field name, such as `will` in `willing` or `mark` in `markdown`, add `--common` with that name instead: it's then replaced only in paths and as `name@host`, never as a word on its own. The report lists the names it already treats that way, such as `User` and `admin`. Then scrub again. While the report lists anything under STILL PRESENT or FROM YOUR OWN INSTRUCTION FILES, the scrub writes nothing into the repo. If you've checked every STILL PRESENT line and none is private, scrub again with `--accept-leftovers`. Anything under FROM YOUR OWN INSTRUCTION FILES means the harness read your own instructions, so the run gets redone instead. Every prompt it lists as not from this file must be machine text (a loop run, a subagent's report) or the run gets redone.
5. Read every file the scrub wrote, by eye. Look for names, paths, hostnames, anything from outside the scratch project, and prompts that aren't in this file.
6. Copy `testdata/fixtures/PROVENANCE.template.md` to `testdata/fixtures/<harness>/<version>/<os>/PROVENANCE.md` and fill it in, with `synthetic: false`.
7. Check it: `go run ./tools/ci/provenance testdata/fixtures`, or, without Go, push your branch and run `gh workflow run fixtures.yml --ref <branch>`.
8. Commit only `testdata/fixtures/<harness>/**` and open a pull request. Its description names the steps you skipped.
9. Delete `ROOT`: the scratch project, the raw runs, the scrub reports, which hold the original values, and the harness's settings folder with the sign-in you made there. On macOS, Claude Code also keeps that sign-in in your login keychain, where Keychain Access can delete it. Move back everything `prepare.py` had you move aside: each time it runs, it prints the commands for whatever is still aside. Remove any hook entry you added outside `ROOT` (the sections below say where).

## Codex

Captured by WP-AD6. Codex runs a hook only after you trust that exact hook in `/hooks`, so this script captures the same steps with hooks trusted and untrusted.

Setup: run `prepare.py codex ROOT codex` to make the scratch project, the run folder and `ROOT/codex-home`, Codex's settings folder for the capture. In the terminal you run Codex from, set the `CODEX_HOME` line it printed, so your own `~/.codex` (its `AGENTS.md`, config, hooks and MCP servers) stays out of the capture, then run `codex login` once, since the new folder has no sign-in. Then write `ROOT/projects/codex/.codex/hooks.json`, with one entry per event below. Codex reads project hooks only in a trusted folder; accept its folder trust prompt.

```json
{
  "hooks": {
    "SessionStart": [{ "hooks": [{ "type": "command", "command": "/bin/sh 'KIT/dump-hook.sh' 'ROOT/runs/codex' SessionStart" }] }]
  }
}
```

Repeat the entry for `UserPromptSubmit`, `PreToolUse`, `PermissionRequest`, `PostToolUse`, `Stop`, `Interrupt`, `SubagentStart`, `SubagentStop` and `SessionEnd`, changing the last word to match. `KIT` is this folder's absolute path. On Windows, the command is `powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "KIT\dump-hook.ps1" "ROOT\runs\codex" SessionStart`.

Steps, with hooks trusted: run `codex` in `ROOT/projects/codex`, open `/hooks`, trust every hook, then `/exit` and start `codex` again so step 1 fires. Do steps 1, 2, 3, 4 (Codex edits through `apply_patch`), 6, 7, 8, 9 (Esc fires `Interrupt`), 10, 13 and 16, with the Claude Code prompts. Then step 18 as `codex exec "Reply with the number of functions in wordcount.py and nothing else."`. If the IDE extension is installed, quit VS Code completely, start it from the same terminal with `code ROOT/projects/codex` so the extension uses `ROOT/codex-home` too, and do steps 1, 2, 7 and 16 there.

Steps, with hooks untrusted: untrust the hooks in `/hooks`, then do steps 1, 2, 7 and 16. No hook fires, so the rollout file is the only record: find it under `ROOT/codex-home/sessions/YYYY/MM/DD/` by its time and pass it with `--extra`.

Collect with `--history ROOT/codex-home/history.jsonl`. Rollouts the hooks named are copied on their own.

## Cursor

Captured by WP-AD8. Cursor also runs Claude-format hooks, so this capture puts both on record for the same prompts.

Setup: run `prepare.py cursor ROOT cursor`. Cursor can't be pointed at a settings folder of its own, so `prepare.py` refuses while your `~/.cursor/hooks.json`, the hooks in your `~/.claude/settings.json`, your `~/.cursor/mcp.json` or a skills folder holds something, and prints the commands to move each aside. Then open Cursor's settings, find Rules, and check that User Rules is empty. If it isn't, copy the text somewhere safe, clear the box for the capture, and paste it back afterwards. The scratch project's `.claude/settings.local.json` is the Claude-format half: Cursor loads it while Settings > Agents > Third-Party Imports is on, its default. Then write `ROOT/projects/cursor/.cursor/hooks.json`:

```json
{
  "version": 1,
  "hooks": {
    "sessionStart": [{ "command": "/bin/sh 'KIT/dump-hook.sh' 'ROOT/runs/cursor' sessionStart" }]
  }
}
```

Repeat the entry for `beforeSubmitPrompt`, `afterAgentResponse`, `afterFileEdit`, `afterTabFileEdit`, `stop` and `sessionEnd`. On Windows, Cursor runs hook commands through PowerShell: use `powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "KIT\dump-hook.ps1" "ROOT\runs\cursor" sessionStart`.

On macOS and Linux, check after step 2 that Cursor ran both halves: `awk 'FNR == 2' ROOT/runs/cursor/hooks/*/argv | sort | uniq -c` counts the events, and Claude Code's (`UserPromptSubmit`, `Stop`) should be there beside Cursor's (`beforeSubmitPrompt`, `stop`). If Claude Code's aren't, while Third-Party Imports is on, Cursor ran those hooks without their `args`, so the dumper recorded nothing for them. Quit Cursor, run `prepare.py cursor ROOT cursor2 --shell-form`, write `ROOT/projects/cursor2/.cursor/hooks.json` for `ROOT/runs/cursor2`, and start again there.

Steps: open `ROOT/projects/cursor` in Cursor and start a new agent chat (step 1). Do steps 2, 3, 4 (`afterFileEdit`), 7, 8 and 9 (stop the generation). For a Tab edit, open `wordcount.py`, type `def count_chars(` on a new line at the end and accept a Tab suggestion. Close the chat (step 16). Start a background agent if your plan has them; its hooks run on Cursor's machines, so record in the notes whether any local hook fired. Then in the `agent` CLI, in `ROOT/projects/cursor`: steps 1, 2 and 16, and step 18 as `agent -p "Reply with the number of functions in wordcount.py and nothing else."`.

Collect without `--history`. Transcripts the hooks named are copied on their own; don't add `state.vscdb`.

## GitHub Copilot

Captured by WP-AD10. pairtime installs Copilot's hooks in `~/.copilot/hooks/`; the capture uses the repo-level folder Copilot also reads, so only the scratch project is captured. The camelCase event names are the ones whose payloads carry timestamps.

Setup: run `prepare.py copilot ROOT copilot`. It makes `ROOT/copilot-home`, the Copilot CLI's settings folder for the capture, and prints the `COPILOT_HOME` line that points the CLI at it. VS Code reads your own `~/.copilot` all the same, so `prepare.py` refuses while its hooks or instructions hold something, and prints the commands to move them aside. Copilot also runs Claude Code-format hooks from a repo's `.claude/settings.json` and `.claude/settings.local.json` (HS 5.1), but pairtime installs only Copilot's own hooks for it, so `prepare.py` writes none of those into this run's project. Then write `ROOT/projects/copilot/.github/hooks/capture.json`:

```json
{
  "version": 1,
  "hooks": {
    "sessionStart": [{
      "type": "command",
      "bash": "/bin/sh 'KIT/dump-hook.sh' 'ROOT/runs/copilot' sessionStart",
      "powershell": "powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File 'KIT\\dump-hook.ps1' 'ROOT\\runs\\copilot' sessionStart",
      "timeoutSec": 30
    }]
  }
}
```

Repeat it for `userPromptSubmitted`, `preToolUse`, `postToolUse`, `permissionRequest`, `agentStop`, `subagentStart`, `subagentStop`, `notification`, `errorOccurred` and `sessionEnd`.

Steps: start VS Code with a profile of its own, so your VS Code instructions, prompt files and MCP servers stay out: `code --profile pairtime-capture ROOT/projects/copilot`. Install GitHub Copilot Chat in that profile, sign in, and in agent mode do steps 1, 2, 3, 4, 6, 7, 8, 9, 10 and 16. In the Copilot CLI, in the terminal where you set `COPILOT_HOME`, in `ROOT/projects/copilot`: start it as `copilot --no-custom-instructions` and sign in with `/login` if it asks, then steps 1, 2, 7 and 16; one autopilot run with `/autopilot` and the step 13 goal; step 18 as `copilot --no-custom-instructions -p "Reply with the number of functions in wordcount.py and nothing else."`.

Collect: the CLI's `ROOT/copilot-home/session-state/<sessionId>/events.jsonl` for each `sessionId` in the hook payloads, and for VS Code the one `workspaceStorage/<hash>/chatSessions/` folder whose `workspace.json` names `ROOT/projects/copilot`, each with `--extra`.

## Gemini CLI

Captured by WP-AD12.

Setup: run `prepare.py gemini ROOT gemini`. It makes `ROOT/gemini-home`, which Gemini CLI uses in place of your home folder once you set the `GEMINI_CLI_HOME` line it printed, so your `~/.gemini` (`GEMINI.md`, settings, hooks, extensions and MCP servers) stays out of the capture. Sign in again when Gemini asks. Then write `ROOT/projects/gemini/.gemini/settings.json`. Gemini fingerprints project hooks and asks before running new ones; accept once.

```json
{
  "hooks": {
    "SessionStart": [{ "matcher": "*", "hooks": [{ "type": "command", "name": "capture", "command": "/bin/sh 'KIT/dump-hook.sh' 'ROOT/runs/gemini' SessionStart", "timeout": 60000 }] }]
  }
}
```

Repeat it for `BeforeAgent`, `BeforeTool`, `AfterTool`, `Notification`, `PreCompress` and `SessionEnd`, changing the last word to match. `AfterAgent` runs `deny-once.sh` instead of the dumper, so the capture holds one deny-retry at step 19:

```json
"AfterAgent": [{ "matcher": "*", "hooks": [{ "type": "command", "name": "capture", "command": "/bin/sh 'KIT/deny-once.sh' 'ROOT/runs/gemini' AfterAgent", "timeout": 60000 }] }]
```

`deny-once.sh` records every `AfterAgent` call with the dumper, like the other hooks. The first time a call's prompt is step 19's, it also tells Gemini to turn its answer down with the reason below, and Gemini sends that reason back to the model as a new prompt (HS 7.1). These commands are for macOS and Linux.

Steps: `gemini` in `ROOT/projects/gemini`, steps 1, 2, 3, 4, 6, 7, 8 and 9, then step 19, then step 16. Step 18 twice: `gemini -p "Reply with the number of functions in wordcount.py and nothing else."`, and the same prompt piped in with no `-p`, which is also a non-TTY run.

Step 19, Gemini only: type the prompt below. Gemini answers, the hook turns that answer down once, and Gemini answers again, so it should fire `BeforeAgent`, `AfterAgent`, then a second `AfterAgent` with `stop_hook_active`.

```text
Name one fruit, in one word.
```

The reason `deny-once.sh` sends back, which Gemini treats as the next prompt:

```text
Name a different fruit, in one word.
```

Collect with `--extra` for `ROOT/gemini-home/.gemini/tmp/<hash>/logs.json`, the one whose `<hash>` folder holds the captured chats.

## Devin

Captured by WP-AD14. Devin Desktop and the Devin CLI read Claude-format hooks from the project's `.claude/` settings, so `prepare.py devin ROOT devin` is the whole setup. Devin can't be pointed at a settings folder of its own, so `prepare.py` refuses while your Devin config, rules, skills or MCP servers, or Claude-format hooks in your `~/.claude`, hold something, and prints the commands to move each aside.

Steps: Devin Desktop with `ROOT/projects/devin` open, steps 1, 2, 3, 4, 6, 7, 8, 9 and 16. Then the Devin CLI, run as `devin` in `ROOT/projects/devin`: steps 1, 2, 7 and 16, then step 17 as `devin --continue` with the step 17 prompt, quitting it the same way, and step 18 as `devin -p "Reply with the number of functions in wordcount.py and nothing else."`. Print mode can't show the folder trust prompt, so it only runs once the interactive steps have trusted the folder.

On macOS and Linux, if no `hooks/` folder appears in `ROOT/runs/devin` after step 2, Devin didn't run the hooks, or ran them without their `args`, so the dumper recorded nothing. Check that Devin trusts the folder, then quit Devin, run `prepare.py devin ROOT devin2 --shell-form`, and start again in `ROOT/projects/devin2`.

Collect with `--extra` for this session's files in `~/.local/share/devin/transcripts/`. The Windows path isn't known yet (HS 15); if you find it, put it in the provenance notes.

## Cline

Captured by WP-AD14. Cline's file hooks run on macOS and Linux only, after you tick "Enable Hooks" in its settings. Each hook is an executable named after its event.

Setup: run `prepare.py cline ROOT cline`. Cline can't be pointed at a settings folder of its own, so `prepare.py` refuses while your `~/Documents/Cline/Hooks`, `~/Documents/Cline/Rules` or `~/.agents/AGENTS.md` holds something, and prints the commands to move each aside. If you've added MCP servers in Cline, turn them off for the capture. Then, in `ROOT/projects/cline`:

```sh
mkdir -p .clinerules/hooks
for e in TaskStart TaskResume TaskCancel TaskComplete UserPromptSubmit PreToolUse PostToolUse PreCompact; do
  printf '#!/bin/sh\nexec /bin/sh %s %s %s "$@"\n' "'KIT/dump-hook.sh'" "'ROOT/runs/cline'" "$e" >".clinerules/hooks/$e"
  chmod +x ".clinerules/hooks/$e"
done
```

Steps: open `ROOT/projects/cline` in VS Code and start a Cline task (`TaskStart`). Do steps 2, 3, 4, 7 and 8, cancel the task (`TaskCancel`), resume it (`TaskResume`), and let one task finish (`TaskComplete`). On Windows, do the same steps with no hooks.

Collect with `--extra` for each captured task's folder, `<editor user dir>/globalStorage/saoudrizwan.claude-dev/tasks/<taskId>/` (the `taskId`s are in the hook payloads), and for the CLI `~/.cline/data/sessions/<id>.json`.
