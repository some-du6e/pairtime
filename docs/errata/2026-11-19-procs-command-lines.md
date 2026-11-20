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

WP-OS6 reads each process's id, parent id, executable name and start time, and on Linux and macOS its process group and whether it has a terminal. It never reads another process's command line or environment, because `claude -p "<prompt>"` carries the prompt on its command line and other programs carry tokens there. The command line is the only place `farm.sh` and `-p` appear, so the scan can't see either.

On Linux and macOS it doesn't need them for `bash farm.sh`. An interactive POSIX shell starts each command in a process group of its own, and a shell running a script doesn't, so `claude` sharing its parent shell's group means the shell runs a script. The scan finds `script_runtime` there, as SM says (`internal/platform/testdata/procs/sm66-loop.json` and `sm66-bare.json`, the darwin tables).

Windows has no process groups. Every shell counts as interactive there, and the nearest non-shell ancestor of Casey's `claude` is Windows Terminal, so the scan gives `parent_class = terminal_app` (the windows tables in the same files).

## What that does to credit

- SM 6.6 as written: hooks fire, and Claude Code's OTel `app.entrypoint` is `sdk-cli`, so `entrypoint = sdk` and rule 4 makes the prompts headless. Still 0. Only the `parent_class` label differs from SM.
- The `--bare` variant on Windows: no hooks fire, and OTel only reaches the agent if the launching environment sets it (HS 3.5). The scan reports `terminal_app`, `entrypoint = unknown` and `has_tty = true`, so rule 4 doesn't apply. Rule 6 then decides, and on this laptop it can pass: the jiggler supplies `input_any`, there is no key counter, and focus sits on the Windows Terminal window that hosts the session. SM's "Still 0" doesn't hold.
- The second variant: SM already relies on `-p` from the arguments, which hooks and OTel report when they run. Same result as SM.

## Choice

The scan doesn't read command lines, on any OS. The `parent_class` the golden tables expect on Windows is `terminal_app`, and each such table names this erratum, which `TestGoldenTables` checks.

## Options

1. Keep the choice. The Windows `--bare` loop then gets past tier 1. Of the tier-2 flags SM 6.6 lists, the 100% headless share goes, but regular timing, input that resets at the jiggler's period and four hours of agent commits with no hand edit still put the account in front of a reviewer.
2. Read the command line of a harness process and of the shells in its chain only, keep three booleans (`print_mode`, `bare`, `shell_runs_file`) and drop the text in the same function. That closes the gap on all three OSes. The prompt text would pass through the agent's memory, though it is already on the device in the transcript. Windows has `NtQueryInformationProcess` with `ProcessCommandLineInformation` from 8.1 on, Linux `/proc/<pid>/cmdline`, and macOS `KERN_PROCARGS2`, which returns the environment in the same buffer.
3. On Windows, treat a session the scan finds with no hooks and no IDE or desktop host as headless. That also zeroes honest sessions in a folder whose trust dialog is still open, because those fire no hooks either (DESIGN 5.1).
