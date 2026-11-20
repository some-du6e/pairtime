# Harness fixtures

What real AI harnesses send and write, captured on real machines. Each adapter in `internal/adapters/` is tested by replaying these files and comparing the events it produces with expected JSON (DESIGN 5.1, 5.3). They are oracles: an adapter is fixed to match its fixtures, never the other way round (docs/PLAN.md, ground rule 5).

## Layout

```text
testdata/fixtures/<harness>/<version>/<os>/
  PROVENANCE.md         who captured it, when, on what, and how it was scrubbed
  <run>/                one run of the capture script, named after the surface: cli, vscode, ...
    capture.json        how prepare.py set the run up: the harness, the hook form, the settings folder
    hooks/NNNNNN/       one hook call, numbered in arrival order
      stdin             the bytes the harness wrote to the hook's stdin, unchanged
      argv              the hook's arguments, one per line, as the harness passed them
      env               the names of the environment variables the hook saw, one per line, sorted; never their values
      time              when the call arrived: unix_ns, utc and the clock used, one key=value per line
      cwd               the hook process's working directory
      parents           the hook's parent processes, nearest first: pid and executable name, never command lines
      stderr            only when the dumper itself hit an error
    otlp/NNNNNN.json    one OTLP/HTTP JSON request body, as sent
    otlp/NNNNNN.meta    its arrival time, path, content type, encoding and header names
    transcripts/        the harness's own session files for the sessions the hooks saw, under home/ by their path
      INDEX.txt         each copied file with its source path, size and modification time
    STEPS.txt           which SCRIPTS.md step the person was on, with the time they started it
```

- `<harness>` is the id `pkg/event` uses: `claude_code`, `codex`, `cursor`, `copilot`, `gemini`, `devin`, `cline` or `opencode`.
- `<version>` is the version number the harness reports, without its name: `2.1.328`, not `2.1.328 (Claude Code)`.
- `<os>` is `windows`, `macos` or `linux`. The exact build goes in `PROVENANCE.md`.

## Rules

- Every fixture directory has a `PROVENANCE.md` made from [PROVENANCE.template.md](PROVENANCE.template.md). CI checks its front matter (`go run ./tools/ci/provenance`).
- Fixtures come from the capture kit in [tools/capture](../../tools/capture/README.md), following [SCRIPTS.md](../../tools/capture/SCRIPTS.md), so every prompt in them is text the script supplied.
- Everything is scrubbed before it's committed: the home path, account name, hostname, git name and email, other names the person lists, email addresses and anything shaped like a key or token become fixed placeholders. The scrub report, which lists the originals, stays on the capturing machine. Then a person reads every file. The repo is public, so nothing private may remain.
- A fixture written from documentation rather than captured says `synthetic: true`. An adapter can't ship with synthetic fixtures alone (DESIGN 5.1).
- Fixtures aren't edited after they're merged. A new harness version gets a new `<version>` directory. A capture that turns out wrong is replaced in a PR of its own with the `oracle-change` label, never in the PR that changes the adapter it tests.

## Placeholders

The scrub replaces values with these, so tests can rely on them:

| Value | Placeholder |
|---|---|
| Home directory | `C:\Users\capture` on Windows, `/Users/capture` on macOS, `/home/capture` on Linux, in every spelling the files use |
| OS account name | `capture` |
| Hostname | `capture-host` |
| git `user.name` | `Capture User` |
| git `user.email` and other email addresses | `capture@example.invalid` |
| Anything else the person lists with `--also`: a real name, a school | `REDACTED` |
| Account and organization ids in telemetry | `00000000-0000-4000-8000-000000000000` |
| Key- or token-shaped strings | `REDACTED_<KIND>`, such as `REDACTED_GITHUB_TOKEN` |
