# Capture scripts

One script per harness. Each lists the exact actions that make the harness fire every event its pairtime adapter maps (DESIGN 5.1), so a capture is complete and two captures of the same version can be compared. Read [README.md](README.md) first; it says what the kit does and doesn't do on your machine.

Type or paste each prompt exactly as written. The scrub flags any prompt line that isn't in this file, because a fixture should hold no text of yours. If you type something private by mistake, stop, delete that run's folder, and start the run again under a new name.

Step numbers are shared across harnesses: step 4 is always "a permission accepted", even where a harness skips step 3. Before each step, type its number and Enter into the sink's terminal, so `STEPS.txt` says which step every hook call belongs to.

## Before any capture

- You need Python 3.8 or later (`py` instead of `python3` on Windows), git, the harness signed in, and a clone of this repo.
- Pick a capture folder outside any git repo, such as `~/pairtime-capture`. Below it's `ROOT`. Raw captures live there and never enter the repo; only the scrubbed copy does.
- Close other sessions of the harness while you capture. Claude Code's hooks are confined to the scratch project, but for some harnesses below the hooks are user-wide and would capture every session.
- In the scratch project, open nothing of yours, and don't let the agent read outside it. If it asks to, answer no.

## Claude Code

Captured by WP-AD1. Run it once in the CLI (run `cli`) and once in the VS Code extension (run `vscode`), on Windows and on macOS or Linux. Optional third run: the Desktop app's Code tab (run `desktop`).

### Setup

1. From your clone of this repo:

   ```sh
   python3 tools/capture/prepare.py claude_code ROOT cli
   ```

   It makes `ROOT/project` (a tiny made-up Python project with one failing test, and a made-up git identity), `ROOT/runs/cli`, and `ROOT/project/.claude/settings.local.json`, which points every DESIGN 5.1 hook at the dumper: `SessionStart`, `UserPromptSubmit`, `Stop`, `StopFailure`, `Notification`, `PermissionRequest`, `PostToolUse` (matcher `Edit|Write|Bash`), `SubagentStart`, `SubagentStop`, `SessionEnd`. It prints the commands below with your paths filled in.
2. Terminal 1: start the sink, `python3 tools/capture/otlp_sink.py ROOT/runs/cli`.
3. Terminal 2: set the telemetry variables `prepare.py` printed (OTLP over `http/json` to the sink, `OTEL_METRICS_INCLUDE_ENTRYPOINT=true`, prompt, response and tool logging off), then `cd ROOT/project`. Claude Code ignores telemetry variables set in project settings, so they have to come from this shell.

If no `hooks/` folder appears in `ROOT/runs/cli` after step 2 below, open `/hooks` in Claude Code to see whether the hooks loaded. If your version doesn't accept `args` in a hook, run `prepare.py` again with `--shell-form` under a new run name.

### Steps

| # | Do this | It should fire |
|---|---|---|
| 1 | Run `claude`. Accept the folder trust dialog if it appears: hooks don't run in a folder until it's accepted | `SessionStart` (`source` startup) |
| 2 | Type, don't paste, the first prompt below | `UserPromptSubmit`, `Stop` |
| 3 | Copy the three-line block below and paste it as one prompt, then press Enter | `UserPromptSubmit` with the pasted text expanded, `Stop` |
| 4 | Type the step 4 prompt. When Claude asks to edit `wordcount.py`, wait 10 seconds without touching anything, then accept once, not "always" | `PermissionRequest`, `Notification` (permission prompt), `PostToolUse` (Edit), `Stop` |
| 5 | Type the step 5 prompt and accept the new file | `PermissionRequest`, `PostToolUse` (Write), `Stop` |
| 6 | Type the step 6 prompt. When it asks to run `rm`, answer no | `PermissionRequest`, and no `PostToolUse` |
| 7 | Type the step 7 prompt and accept the command | `PostToolUse` (Bash), `Stop` |
| 8 | Type the step 8 prompt and accept the command. One test fails | `PostToolUse` (Bash) with the failure, `Stop` |
| 9 | Type the step 9 prompt. As soon as Claude starts working, press Esc once | `UserPromptSubmit`, and no `Stop` |
| 10 | Type the step 10 prompt and accept what it asks | `SubagentStart`, `SubagentStop`, `Stop`, a subagent transcript |
| 11 | After Claude answers, don't type anything for 75 seconds | `Notification` (idle prompt) |
| 12 | Type the step 12 command, then wait about two and a half minutes without typing, so the loop fires twice. Then type the step 12 follow-up | `UserPromptSubmit` per loop run with no human mark, `Stop` with `session_crons` |
| 13 | Type the step 13 command and let it run to the end, accepting edits and test runs | `Stop` with `stop_hook_active` on the continued turns |
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

Quit VS Code completely, including any window in the background. Run `prepare.py claude_code ROOT vscode`, restart the sink on `ROOT/runs/vscode`, set the telemetry variables in a terminal, and start VS Code from that terminal with `code ROOT/project`, so the extension inherits them. Open the Claude Code panel and do steps 1, 2, 4, 7, 9, 10 and 16. Close the panel's session from its menu for step 16.

For the Desktop app's Code tab, do the same steps in run `desktop`. The Desktop app doesn't inherit a terminal's variables, so its telemetry may not reach the sink; say so in the provenance notes if it doesn't.

## After any capture

1. Quit the harness, so every transcript is complete. Stop the sink with Ctrl+C.
2. Copy the transcripts the hooks named, and only the lines of your prompt history that came from the captured sessions:

   ```sh
   python3 tools/capture/collect.py ROOT/runs/cli --history ~/.claude/history.jsonl
   ```

   For harnesses whose hooks don't say where the log is, add `--extra PATH` for that one session's file. It shows the list and asks first. Never pass a whole store such as Cursor's `state.vscdb`: it holds all your chats.
3. Scrub into the repo. Add `--also` for anything private the kit can't know: your real name, your school, another handle.

   ```sh
   python3 tools/capture/scrub.py ROOT/runs/cli testdata/fixtures/claude_code/<version>/<os>/cli --also "Your Name"
   ```

   `<version>` is the number from `claude --version`, `<os>` is `windows`, `macos` or `linux`.
4. Read the report it prints. Anything under STILL PRESENT is a known value inside a longer word: add it with `--also`, delete the output folder, and scrub again. Every prompt it lists as not from this file must be machine text (a loop run, a subagent's report) or the run gets redone.
5. Read every file the scrub wrote, by eye. Look for names, paths, hostnames, anything from outside the scratch project, and prompts that aren't in this file.
6. Copy `testdata/fixtures/PROVENANCE.template.md` to `testdata/fixtures/<harness>/<version>/<os>/PROVENANCE.md` and fill it in, with `synthetic: false`.
7. Check it: `go run ./tools/ci/provenance testdata/fixtures`, or, without Go, push your branch and run `gh workflow run fixtures.yml --ref <branch>`.
8. Commit only `testdata/fixtures/<harness>/**` and open a pull request. Its description names the steps you skipped.
9. Delete `ROOT`: the scratch project, the raw runs and the scrub reports, which hold the original values. Remove any hook entry you added outside `ROOT` (the sections below say where).

## Codex

Captured by WP-AD6. Codex runs a hook only after you trust that exact hook in `/hooks`, so this script captures the same steps with hooks trusted and untrusted.

Setup: run `prepare.py claude_code ROOT codex` to make the scratch project and run folder (Codex ignores the Claude settings file it writes). Then write `ROOT/project/.codex/hooks.json`, with one entry per event below. Codex reads project hooks only in a trusted folder; accept its folder trust prompt.

```json
{
  "hooks": {
    "SessionStart": [{ "hooks": [{ "type": "command", "command": "/bin/sh 'KIT/dump-hook.sh' 'ROOT/runs/codex' SessionStart" }] }]
  }
}
```

Repeat the entry for `UserPromptSubmit`, `PreToolUse`, `PermissionRequest`, `PostToolUse`, `Stop`, `Interrupt`, `SubagentStart`, `SubagentStop` and `SessionEnd`, changing the last word to match. `KIT` is this folder's absolute path. On Windows, the command is `powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "KIT\dump-hook.ps1" "ROOT\runs\codex" SessionStart`.

Steps, with hooks trusted: run `codex` in `ROOT/project`, open `/hooks`, trust every hook, then `/exit` and start `codex` again so step 1 fires. Do steps 1, 2, 3, 4 (Codex edits through `apply_patch`), 6, 7, 8, 9 (Esc fires `Interrupt`), 10, 13 and 16, with the Claude Code prompts. Then step 18 as `codex exec "Reply with the number of functions in wordcount.py and nothing else."`. If the IDE extension is installed, do steps 1, 2, 7 and 16 there too.

Steps, with hooks untrusted: untrust the hooks in `/hooks`, then do steps 1, 2, 7 and 16. No hook fires, so the rollout file is the only record: find it under `~/.codex/sessions/YYYY/MM/DD/` by its time and pass it with `--extra`.

Collect with `--history ~/.codex/history.jsonl`. Rollouts the hooks named are copied on their own.

## Cursor

Captured by WP-AD8. Cursor also runs Claude-format hooks, so this capture puts both on record for the same prompts.

Setup: run `prepare.py claude_code ROOT cursor`. Its `.claude/settings.local.json` is the Claude-format half: Cursor loads it while Settings > Agents > Third-Party Imports is on, its default. Then write `ROOT/project/.cursor/hooks.json`:

```json
{
  "version": 1,
  "hooks": {
    "sessionStart": [{ "command": "/bin/sh 'KIT/dump-hook.sh' 'ROOT/runs/cursor' sessionStart" }]
  }
}
```

Repeat the entry for `beforeSubmitPrompt`, `afterAgentResponse`, `afterFileEdit`, `afterTabFileEdit`, `stop` and `sessionEnd`. On Windows, Cursor runs hook commands through PowerShell: use `powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "KIT\dump-hook.ps1" "ROOT\runs\cursor" sessionStart`.

Steps: open `ROOT/project` in Cursor and start a new agent chat (step 1). Do steps 2, 4 (`afterFileEdit`), 7, 8 and 9 (stop the generation). For a Tab edit, open `wordcount.py`, type `def count_chars(` on a new line at the end and accept a Tab suggestion. Close the chat (step 16). Start a background agent if your plan has them; its hooks run on Cursor's machines, so record in the notes whether any local hook fired. Then in the `agent` CLI, in `ROOT/project`: steps 1, 2 and 16, and step 18 as `agent -p "Reply with the number of functions in wordcount.py and nothing else."`.

Collect without `--history`. Transcripts the hooks named are copied on their own; don't add `state.vscdb`.

## GitHub Copilot

Captured by WP-AD10. pairtime installs Copilot's hooks in `~/.copilot/hooks/`; the capture uses the repo-level folder Copilot also reads, so only the scratch project is captured. The camelCase event names are the ones whose payloads carry timestamps.

Setup: run `prepare.py claude_code ROOT copilot`, then write `ROOT/project/.github/hooks/capture.json`:

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

Steps: in VS Code agent mode with `ROOT/project` open, steps 1, 2, 4, 6, 7, 8, 9, 10 and 16. In the Copilot CLI, in `ROOT/project`: steps 1, 2, 7 and 16; one autopilot run with `/autopilot` and the step 13 goal; step 18 as `copilot -p "Reply with the number of functions in wordcount.py and nothing else."`.

Collect: the CLI's `~/.copilot/session-state/<sessionId>/events.jsonl` for each `sessionId` in the hook payloads, and for VS Code the one `workspaceStorage/<hash>/chatSessions/` folder whose `workspace.json` names `ROOT/project`, each with `--extra`.

## Gemini CLI

Captured by WP-AD12.

Setup: run `prepare.py claude_code ROOT gemini`, then write `ROOT/project/.gemini/settings.json`. Gemini fingerprints project hooks and asks before running new ones; accept once.

```json
{
  "hooks": {
    "SessionStart": [{ "matcher": "*", "hooks": [{ "type": "command", "name": "capture", "command": "/bin/sh 'KIT/dump-hook.sh' 'ROOT/runs/gemini' SessionStart", "timeout": 60000 }] }]
  }
}
```

Repeat it for `BeforeAgent`, `AfterAgent`, `BeforeTool`, `AfterTool`, `Notification`, `PreCompress` and `SessionEnd`. If Gemini's telemetry is on, set `telemetry.logPrompts` to `false` first.

Steps: `gemini` in `ROOT/project`, steps 1, 2, 3, 4, 6, 7, 8, 9 and 16. Step 18 twice: `gemini -p "Reply with the number of functions in wordcount.py and nothing else."`, and the same prompt piped in with no `-p`, which is also a non-TTY run. The `AfterAgent` deny-retry needs a hook that denies once, which the kit doesn't have yet; WP-AD12 adds it.

Collect with `--extra` for `~/.gemini/tmp/<hash>/logs.json`, the one whose `<hash>` folder holds the captured chats.

## Devin

Captured by WP-AD14. Devin Desktop and the Devin CLI read Claude-format hooks from the project's `.claude/` settings, so `prepare.py claude_code ROOT devin` is the whole setup.

Steps: Devin Desktop with `ROOT/project` open, steps 1, 2, 4, 7 and 16. The Devin CLI in `ROOT/project`, steps 1, 2, 7 and 16.

Collect with `--extra` for this session's files in `~/.local/share/devin/transcripts/`. The Windows path isn't known yet (HS 15); if you find it, put it in the provenance notes.

## Cline

Captured by WP-AD14. Cline's file hooks run on macOS and Linux only, after you tick "Enable Hooks" in its settings. Each hook is an executable named after its event.

Setup: run `prepare.py claude_code ROOT cline`, then, in `ROOT/project`:

```sh
mkdir -p .clinerules/hooks
for e in TaskStart TaskResume TaskCancel TaskComplete UserPromptSubmit PreToolUse PostToolUse PreCompact; do
  printf '#!/bin/sh\nexec /bin/sh %s %s %s "$@"\n' "'KIT/dump-hook.sh'" "'ROOT/runs/cline'" "$e" >".clinerules/hooks/$e"
  chmod +x ".clinerules/hooks/$e"
done
```

Steps: open `ROOT/project` in VS Code and start a Cline task (`TaskStart`). Do steps 2, 4, 7 and 8, cancel the task (`TaskCancel`), resume it (`TaskResume`), and let one task finish (`TaskComplete`). On Windows, do the same steps with no hooks.

Collect with `--extra` for each captured task's folder, `<editor user dir>/globalStorage/saoudrizwan.claude-dev/tasks/<taskId>/` (the `taskId`s are in the hook payloads), and for the CLI `~/.cline/data/sessions/<id>.json`.
