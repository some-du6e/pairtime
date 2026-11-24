# Erratum: SM 6.6 reads command lines that the procs scan doesn't

Date 2026-11-19. Raised by WP-OS6. Status: decided 2026-11-20 by the orchestrator for Karim: option 2, amended. The procs scan and the hook path read the command line of each harness process and of the shells their walk skips, keep only print mode, Claude Code's stream-json mode, --bare, whether a shell runs a script file and whether the read failed, and drop the text in the function that read it. A print flag makes the entrypoint sdk (exec for codex) under every host, stream-json mode does unless an IDE or agent desktop app decides parent_class, a shell running a script file is a script shell on every OS, and a command line the agent can't read counts as print mode. Until the follow-up to WP-OS6 lands, the scan reads no command lines. Karim can overrule it. The decision's PLAN text is on main from #111, and the capture kit's flags file and its line in testdata/fixtures/README.md from #116; this file's rewrite came with this Status, and so did the PLAN edit that moves WP-AD1's flags record from M1's exit check, where #111 put it, to the first release that ships the follow-up or the hook's `CLAUDE_CODE_ENTRYPOINT` route (Decision below). WP-ERR1 applies the SM 1.2 and 2.2 and DESIGN 3.3, 5.1 and 7.12 wording in a later docs PR, and the follow-up to WP-OS6 applies the code, with AGENTS.md's bullet and the privacy notice.

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
- The `--bare` variant on Windows: no hooks fire, and OTel reaches the agent only if the launching environment sets it up (HS 3.5). The scan reports `terminal_app`, `entrypoint = unknown` and `has_tty = true`, so rule 4 doesn't apply, and rule 6 can pass as above. SM's "Still 0" doesn't hold. Claude Code documents `--bare` as becoming the default for `-p` (HS 3.1), so in time every `claude -p` loop is a hookless one.
- The second variant, the loop typed into the interactive shell: SM relies on `-p` from the arguments, which only that opt-in OTel attribute reports. With it, still 0; without it, rule 6 decides as above on Windows, on Linux without the opt-in key counter, and on Macs without the key counter, where a program that posts key-downs resets key-down idle (WP-OS3's finding).

Known misses of the process rules, which SM doesn't cover:

- A person who backgrounds a harness from a shell started inside another shell (`bash`, then `claude &`) reads as a script, because the harness is outside the terminal's foreground group and its shell was started by a shell. `claude &` from the terminal's own shell doesn't.
- `(cd x; claude; echo done)` runs claude in a subshell that shares its group, and reads as a script. `cd x && claude` doesn't, and since bash and zsh exec the last command of a subshell, neither does `(cd x && claude)`.
- `setsid claude -p` in a script leaves claude without a terminal, so `has_tty = false` and rule 4 makes the session headless anyway, but `parent_class` reads `terminal_app` rather than `script_runtime`.
- For codex installed through npm on macOS and Windows, a Node script that starts `codex exec` within 3 s of starting reads as codex's own npm launcher (`LauncherWindow`), so the session's `parent_class` is the terminal's. Under option 2, `exec` on codex's command line still makes the entrypoint `exec`, so only an interactive codex that such a script drives is missed. Claude Code has no Node launcher (CP 4.1), so a node right above claude is always `script_runtime`.

## Choice

Until the follow-up to WP-OS6 lands, the scan reads no command lines, and the Windows golden tables expect `terminal_app` and name this erratum. As decided on 2026-11-20 (Status above), the follow-up then reads them as option 2 says, amended. For each harness process the scan is about to report or a hook runs under, and for each shell its walk skips, the agent reads the command line in memory and keeps five booleans:

- `print_mode`: a print flag the harness documents: `claude -p` or `--print`, `codex exec`, `gemini -p` or `--prompt`, `cursor-agent -p` or `--print`, `copilot -p` or `--prompt`. It makes the entrypoint `sdk`, or `exec` for codex, under any host, through the script-shell rule and under an IDE or a desktop app.
- `sdk_stream`: Claude Code's `--input-format` or `--output-format stream-json`. It does the same unless an IDE or an agent desktop app decides `parent_class`, because the Agent SDK that IDE integrations are built on drives Claude Code that way.
- `bare`: `claude --bare`, which goes into `automation_flags`.
- `runs_file`: a shell whose arguments name a script file for it to run, other than the harness's own launcher shim. It makes the shell a script shell, so the session is `script_runtime` on Windows, as process groups already make it on Linux and macOS.
- `cmdline_unread`: a process still running whose command line the agent couldn't read. It counts as `print_mode`, goes into `automation_flags`, and `doctor` names the session and the reason, which on Windows is usually an elevated terminal.

The text goes no further than the function that read it, and no environment is parsed. The golden tables then expect SM's `parent_class` and `entrypoint` on every OS and name no erratum.

## What option 2 still misses

Each of these pays only at level B, where `input_keys` is unavailable or, on Macs without the key counter, also counts keys a program posts, and only when nothing else names the mode: for Claude Code, the entrypoint token its hooks' environment holds, or its transcript's in a session no hook reported (SM 1.2).

- Claude Code in stream-json mode under an IDE or the desktop app: a loop typed into an IDE's terminal, or an extension the teen writes, which reads like Claude Code's own extension.
- A harness that runs headless with no mode flag on its command line, as Gemini does when its prompt comes on a pipe (HS 7.5).
- A harness that rewrites its own argument area, as Node's `process.title` can on Linux. WP-AD1 checks that `-p` shows on real installs; for Claude Code, the `sdk-cli` its hooks get still says it.

## What option 2 costs honest sessions

- A script a person runs to start an interactive harness makes the session `script_runtime` on Windows too, as SM 1.2 says and as process groups already make it on Linux and macOS.
- An editor plugin that sends each typed message as a new `claude -p`, and a `claude -p` typed by hand into an IDE's terminal, are headless and never count: rule 4 credits a hand-typed print-mode run only from an interactive shell, which `pkg/score` reads as a `terminal_app`, `multiplexer` or `remote` parent.
- A harness run elevated on Windows, or as another user, counts as print mode, and `doctor` says to start it normally.

## Options

1. Keep the choice. A `claude -p` loop on Windows then gets past tier 1 whenever OTel's `app.entrypoint` doesn't reach the agent: always with `--bare`, and with hooks firing too once the repo's settings or the launching environment turn the metrics exporter off. Of the tier-2 flags SM 6.6 lists, the 100% headless share goes, but regular timing, input that resets at the jiggler's period and four hours of agent commits with no hand edit still put the account in front of a reviewer.
2. Read the command line of a harness process and of the shells in its chain only, keep three booleans (`print_mode`, `bare`, `shell_runs_file`) and drop the text in the same function. That closes the gap on all three OSes, for hooked and hookless loops alike, without depending on OTel. The prompt text would pass through the agent's memory, though it is already on the device in the transcript. Windows has `NtQueryInformationProcess` with `ProcessCommandLineInformation` from 8.1 on, Linux `/proc/<pid>/cmdline`, and macOS `KERN_PROCARGS2`, which returns the environment in the same buffer.
3. On Windows, treat a session the scan finds with no hooks and no IDE or desktop host as headless. That closes the `--bare` variant only: a hooked loop with the OTel exporter off fires hooks, so the scan never reports it. And it zeroes every honest hookless session on Windows outside an IDE: Claude Code in a folder whose trust dialog is still open (DESIGN 5.1, HS 3.1), Codex until the teen trusts pairtime's hook in `/hooks` (DESIGN 5.1, HS 6.1), the Cline CLI, which is log reader plus presence only on Windows in v1 (DESIGN 5.1), and OpenCode, which pairtime has no hooks for and reaches only through `pairtime track` (DESIGN 5.1).

## Decision

Take option 2, amended. For each harness process the procs scan reports or a hook runs under, and each shell its parent-chain walk skips, the agent reads the command line in memory and keeps only five booleans (print_mode, sdk_stream, bare, runs_file, cmdline_unread), dropping the text in the function that read it and never parsing an environment. A documented print flag (claude -p or --print, codex exec, gemini -p, cursor-agent -p, copilot -p) makes the entrypoint sdk, or exec for codex, under every host. Claude Code's stream-json mode does the same unless an IDE or agent desktop app decides parent_class, a shell running a script file is a script shell on every OS, and a command line the agent can't read while the process runs counts as print mode, with doctor saying why. Because #48 has merged, the reading lands as a follow-up code PR, the erratum, SM and DESIGN wording go in an oracle-only PR, and the first release that ships that reading waits until WP-AD1 shows that the VS Code extension and the desktop app start claude without a print flag, using a capture-kit flags file that keeps only yes-or-no answers; no credited-seconds figure changes. The orchestrator's ruling of 2026-11-24 moved that gate from v0.1.0-alpha.1, because no print flag can mark an IDE session headless before the reading ships.
