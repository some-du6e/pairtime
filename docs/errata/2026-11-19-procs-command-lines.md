# Erratum: SM 6.6 reads command lines that the procs scan doesn't

Date 2026-11-19. Raised by WP-OS6. Status: waiting for Karim. The scan implements the choice below until Karim decides.

## Input

SM 6.6, the 22:00 row, on Casey's Windows 10 laptop: "The hook binary and the daemon's process list agree: `claude -p`, nearest non-shell ancestor `bash farm.sh`. `entrypoint = sdk`, `parent_class = script_runtime`, so origin `headless`."

Its first variant: "Run with `--bare`, so no hooks fire (HS 3.1). The daemon's once-a-minute process list still classifies each session ... Still 0."

Its second variant: "The nearest non-shell ancestor is now Windows Terminal, but `claude`'s arguments still say `-p`."

SM 1.2 classes "a shell running a script file" as `script_runtime`.

## Expected

The process list alone shows `bash farm.sh` as a shell running a script, so `parent_class = script_runtime`, and the `--bare` variant stays at 0 credited seconds on the Windows laptop.

## What the procs scan computes

WP-OS6 reads each process's id, parent id, executable name, start time and owner, and on Linux and macOS its process group, whether it has a terminal and that terminal's foreground group. It never reads another process's command line or environment, because `claude -p "<prompt>"` carries the prompt on its command line and other programs carry tokens there. The command line is the only place `farm.sh` and `-p` appear, so the scan can't see either.

On Linux and macOS it doesn't need them for `bash farm.sh`. An interactive POSIX shell starts each command in a process group of its own, and a shell running a script doesn't, so `claude` sharing its parent shell's group means the shell runs a script. When the script runs `timeout 900 claude -p`, GNU timeout moves claude into a group of its own, and then the terminal's foreground group gives the script away instead: claude is outside it, and the shell above claude was started by the person's shell. The scan finds `script_runtime` there, as SM says (`internal/platform/testdata/procs/sm66-loop.json` and `sm66-bare.json`, the darwin tables; `classify_test.go` for timeout).

Windows has no process groups. Every shell counts as interactive there, and the nearest non-shell ancestor of Casey's `claude` is Windows Terminal, so the scan gives `parent_class = terminal_app` (the windows tables in the same files).

## What that does to credit

The `-p` that SM 6.6 leans on reaches the agent from one place only. The chain can't see it, and no hook payload carries the entrypoint: `SessionStart` has `source`, `model`, `agent_type` and `session_title`, and the common fields have no entrypoint either (HS 3.1). Claude Code's OTel attribute `app.entrypoint = sdk-cli` says it, and Claude Code sends that attribute only when `OTEL_METRICS_INCLUDE_ENTRYPOINT=true` is set (HS 3.3). Even when pairtime's install sets it, a repo's `.claude/settings.json` can turn the metrics exporter off, and so can the environment the script runs claude in (HS 3.3). So on Windows, where the chain says `terminal_app`, OTel's opt-in attribute is the only tier-1 headless signal for any `claude -p` loop, hooked or not.

- SM 6.6 as written: hooks fire, but they carry no entrypoint. With `app.entrypoint = sdk-cli` reaching the agent, `entrypoint = sdk` and rule 4 makes the prompts headless: still 0, and only the `parent_class` label differs from SM. With the exporter switched off by one line in the repo's settings, Casey's loop has `entrypoint = unknown`, `parent_class = terminal_app` and `has_tty = true`, so rule 4 doesn't apply. Rule 6 then decides, and on this laptop it can pass: the jiggler supplies `input_any`, there is no key counter, and focus sits on the Windows Terminal window that hosts the session. That is SM 6.6 itself, the attack DESIGN 7.12's first row says the headless entrypoint and the parent chain stop. SM's "Still 0" doesn't hold.
- The `--bare` variant on Windows: no hooks fire, and OTel reaches the agent only if the launching environment sets it up (HS 3.5). The scan reports `terminal_app`, `entrypoint = unknown` and `has_tty = true`, so rule 4 doesn't apply, and rule 6 can pass as above. SM's "Still 0" doesn't hold.
- The second variant, the loop typed into the interactive shell: SM relies on `-p` from the arguments, which only that opt-in OTel attribute reports. With it, still 0; without it, rule 6 decides as above on Windows, and on Linux without the opt-in key counter too. On a Mac the key-down idle time stops it either way, as SM says.

Known misses of the process rules, which SM doesn't cover:

- A person who backgrounds a harness from a shell started inside another shell (`bash`, then `claude &`) reads as a script, because the harness is outside the terminal's foreground group and its shell was started by a shell. `claude &` from the terminal's own shell doesn't.
- `(cd x; claude; echo done)` runs claude in a subshell that shares its group, and reads as a script. `cd x && claude` doesn't, and since bash and zsh exec the last command of a subshell, neither does `(cd x && claude)`.
- `setsid claude -p` in a script leaves claude without a terminal, so `has_tty = false` and rule 4 makes the session headless anyway, but `parent_class` reads `terminal_app` rather than `script_runtime`.
- For codex installed through npm on macOS and Windows, a Node script that starts `codex exec` within 3 s of starting reads as codex's own npm launcher (`LauncherWindow`), so the session's `parent_class` is the terminal's. Claude Code has no Node launcher (CP 4.1), so a node right above claude is always `script_runtime`.

## Choice

The scan doesn't read command lines, on any OS. The `parent_class` the golden tables expect on Windows is `terminal_app`, and each such table names this erratum, which `TestGoldenTables` checks.

## Options

1. Keep the choice. A `claude -p` loop on Windows then gets past tier 1 whenever OTel's `app.entrypoint` doesn't reach the agent: always with `--bare`, and with hooks firing too once the repo's settings or the launching environment turn the metrics exporter off. Of the tier-2 flags SM 6.6 lists, the 100% headless share goes, but regular timing, input that resets at the jiggler's period and four hours of agent commits with no hand edit still put the account in front of a reviewer.
2. Read the command line of a harness process and of the shells in its chain only, keep three booleans (`print_mode`, `bare`, `shell_runs_file`) and drop the text in the same function. That closes the gap on all three OSes, for hooked and hookless loops alike, without depending on OTel. The prompt text would pass through the agent's memory, though it is already on the device in the transcript. Windows has `NtQueryInformationProcess` with `ProcessCommandLineInformation` from 8.1 on, Linux `/proc/<pid>/cmdline`, and macOS `KERN_PROCARGS2`, which returns the environment in the same buffer.
3. On Windows, treat a session the scan finds with no hooks and no IDE or desktop host as headless. That closes the `--bare` variant only: a hooked loop with the OTel exporter off fires hooks, so the scan never reports it. And it zeroes every honest hookless session on Windows outside an IDE: Claude Code in a folder whose trust dialog is still open (DESIGN 5.1, HS 3.1), Codex until the teen trusts pairtime's hook in `/hooks` (DESIGN 5.1, HS 6.1), the Cline CLI, which is log reader plus presence only on Windows in v1 (DESIGN 5.1), and OpenCode, which pairtime has no hooks for and reaches only through `pairtime track` (DESIGN 5.1).
