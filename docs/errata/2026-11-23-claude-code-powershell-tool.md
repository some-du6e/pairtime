# Erratum: on Windows Claude Code runs commands through its PowerShell tool, which DESIGN 5.1's tool hooks don't match

Date 2026-11-23. Raised by the orchestrator's review of E-cc-tool-failure. Status: waiting for a decision.

## Input

Claude Code's tools reference (read 2026-11-23): on Windows without Git Bash the PowerShell tool is on and the Bash tool isn't registered; with Git Bash it is on by default for claude.ai and Console accounts; once it is on, Claude treats PowerShell as the primary shell. The hooks reference says to match `Bash|PowerShell` in hooks that inspect shell commands, since one that matches only `Bash` never fires there. A PowerShell command that ran and failed gives the same first line `Exit code N`, and the tool can count exit 1 as a normal result from `grep`, `rg`, `egrep`, `fgrep`, `findstr`, `git grep` and `git diff`.

## Expected

DESIGN 5.1 installs `PostToolUse` and `PostToolUseFailure` under `Edit|Write|Bash`, and SM 4.2 and 4.3 read the agent's test and build runs.

## Computed

On such a machine neither tool hook fires for the agent's commands. The adapter maps only `Bash`. The transcript reader writes a `PowerShell` call as `tool_class` `other` with no command, lines or error signature, in hooked sessions too. So SM 4.2 and 4.3 read nothing from the agent's test and build runs there, and an agent's git command run through PowerShell never reaches the git watcher, so its commit goes out as the person's unless a trailer names the agent.

## Reasoning

The two tool hooks keep one matcher (E-cc-tool-failure), so a new matcher covers both. The adapter and the reader would class a PowerShell command as they class a Bash one and apply E-cc-tool-failure's pass rule with PowerShell's own exit-1 list. WP-AD1's Windows run, still owed, would pin the payloads.
