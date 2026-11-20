# Harness capture kit

Tools for recording what an AI harness really sends and writes, so pairtime's adapters can be tested against it (DESIGN 5.1, JE must-fix 2). A person runs the kit on their own machine, follows a script for one harness, scrubs the result and opens a pull request with it under `testdata/fixtures/`. [SCRIPTS.md](SCRIPTS.md) has the script for each harness, and [testdata/fixtures/README.md](../../testdata/fixtures/README.md) has the layout the kit writes.

## What it does on your machine

- **It sends nothing anywhere.** The hook dumpers write files. The sink listens on 127.0.0.1 only and forwards nothing. No script opens a network connection, and none needs to be installed.
- **It touches only its own projects.** `prepare.py` makes a small made-up project for each run and puts the hook config in that project's own settings file, so only sessions you start in that folder are captured. Your other projects, your user settings and your real code are left alone.
- **It keeps your own config out of the capture.** Instructions, hooks, plugins, skills and MCP servers you've set up would load into the captured sessions, and their text, or what they print, would end up in a public fixture. So the harness gets a settings folder of its own inside the capture folder, and you sign in to it once more there. Where a harness can't be pointed at one, `prepare.py` refuses to start until the files of yours it would load are moved aside, and prints the commands to do that and to undo it. `collect.py` refuses a session that ran outside the settings folder, and the scrub refuses a capture that holds a line of your own instruction files.
- **It records less than the harness gives it.** The dumpers keep the names of environment variables, never their values, and the executable names and terminals of parent processes, never their command lines. The sink keeps request bodies but only the names of request headers, because headers can carry API keys. The collector copies only the session files the captured hooks name, and only the matching lines of a history file.
- **It shows you what it captured.** Every hook call is a folder of small plain files you can open. The sink prints one line per request, the collector lists every file it copied, and the scrub lists every replacement it made.
- **Nothing private goes into the repo.** Raw captures stay in a folder outside the repo; `prepare.py` refuses one inside it. The scrub replaces your home path, account name, hostname, git name and email, any other value you give it, email addresses, account ids and anything shaped like a key, in every spelling the files use, and leaves out files it can't read as text. It then lists every known value it still finds and every prompt that isn't from SCRIPTS.md. Its report, which holds the original values, is saved outside both folders. You read every scrubbed file before committing it.
- **It can't break the harness.** The dumpers print nothing, because some harnesses feed a hook's output to the model, and always exit 0, because some treat a failing hook as a refusal. No shell ever reads a hook's payload as a script: on macOS and Linux the hook command is the dumper itself, so a harness that drops the hook's arguments runs it bare and it exits without reading anything, and on Windows each hook is one `powershell.exe` command line with nothing to drop.

Delete the capture folder when you're done; that removes the scratch project, the raw files and the reports. The kit is stdlib Python 3.8+, POSIX sh and PowerShell 5.1, so there is nothing to install beyond Python.

## Files

| File | What it is |
|---|---|
| `dump-hook.sh RUN_DIR LABEL` | The hook command on macOS and Linux (and on Windows under Git Bash). Each call writes `hooks/NNNNNN/` with `time`, `argv`, `env`, `cwd`, `parents` (pid, controlling terminal and executable name of each parent) and `stdin` |
| `dump-hook.ps1 RUN_DIR LABEL` | The same in PowerShell, for Windows. Same files, same format; Windows has no terminals, so it adds `console`, which says whether the hook had a console to share with the harness |
| `deny-once.sh RUN_DIR LABEL` | Gemini CLI's `AfterAgent` hook: records the call with `dump-hook.sh`, and turns step 19's first answer down once, so the capture holds a deny-retry |
| `otlp_sink.py RUN_DIR` | Saves each OTLP/HTTP JSON request as `otlp/NNNNNN.json` with a `.meta` file, and records step numbers typed into its terminal in `STEPS.txt` |
| `prepare.py HARNESS ROOT RUN` | Makes the scratch project, the run folder and the harness's settings folder, writes the Claude Code hook config for the harnesses that read it, and prints the commands for the rest |
| `collect.py RUN_DIR` | Copies the transcripts the captured hooks name, with `--history` and `--extra` for history files and harness logs |
| `scrub.py RUN_DIR OUT_DIR` | Writes the scrubbed copy into the repo and the report beside the run |
| `SCRIPTS.md` | The capture script for each harness |
| `capture_test.go` | CI's tests of the kit, run by `go test ./...` |

## How a capture goes

```sh
python3 tools/capture/prepare.py claude_code ~/pairtime-capture cli   # once per run
python3 tools/capture/otlp_sink.py ~/pairtime-capture/runs/cli        # terminal 1
# terminal 2: the variables prepare.py printed (the settings folder and the
# telemetry), then the harness, following SCRIPTS.md step by step
python3 tools/capture/collect.py ~/pairtime-capture/runs/cli --history ~/pairtime-capture/claude-home/history.jsonl
python3 tools/capture/scrub.py ~/pairtime-capture/runs/cli testdata/fixtures/claude_code/2.1.328/linux/cli
```

Then read the report and every scrubbed file, fill in `PROVENANCE.md` from [the template](../../testdata/fixtures/PROVENANCE.template.md), and open a pull request that touches only `testdata/fixtures/<harness>/**`. SCRIPTS.md, "After any capture", has each step.

## Limits

- On Windows the PowerShell dumper stamps the arrival time once PowerShell has started, which can be most of a second late on an old laptop. The numbering still gives the arrival order. Under Git Bash, `dump-hook.sh` is faster.
- macOS `date` has no nanoseconds, so `dump-hook.sh` takes the time from `perl`, which macOS ships, and falls back to whole seconds without it. The `clock` line in `time` says which was used.
- The scrub's patterns are a stopgap. `pairtime fixtures scrub` (WP-PV1) replaces them with `pkg/redact`: gitleaks' rules, checksummed PII and a preview. The manual read stays either way.
- `prepare.py` writes the hook config for the harnesses that read Claude Code's format: Claude Code, Cursor and Devin. For the others, SCRIPTS.md gives the hook config to write by hand.

## Tests

`capture_test.go` runs in `ci.yml`'s `go test ./...` step on Linux. It runs `shellcheck` on `dump-hook.sh`, pipes sample payloads through both dumpers (`pwsh` runs the PowerShell one), posts plain and gzipped bodies to the sink, collects transcripts from a fake home folder, scrubs a planted sample and checks that no planted value survives, and checks the provenance template's front matter. Locally, outside CI, a missing `shellcheck`, `pwsh` or `python3` skips that test instead of failing it.
