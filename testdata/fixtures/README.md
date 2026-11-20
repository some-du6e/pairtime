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
      env               the names of the environment variables the hook saw, one per line, sorted; never their values (entrypoint keeps one)
      time              when the call arrived: unix_ns, utc and the clock used, one key=value per line
      cwd               the hook process's working directory
      parents           the hook's parent processes, nearest first: pid, controlling terminal (? or ?? when none) and executable name, never command lines
      flags             the harness the hook runs under: its executable name, then yes or no for a print flag (-p, --print, --prompt, codex exec), stream-json input or output, and --bare among its arguments, or ? where the dumper can't tell; never the arguments
      entrypoint        Claude Code only: the value of CLAUDE_CODE_ENTRYPOINT when it is 1 to 32 characters of a-z, 0-9, - and _, other when it isn't, and no file when it is unset; the only value of a variable the dumpers keep
      console           Windows only: attached or none, whether the hook had a console, which it shares with the harness that started it
      stderr            only when the dumper itself hit an error
    otlp/NNNNNN.json    one OTLP/HTTP JSON request body, as sent
    otlp/NNNNNN.meta    its arrival time, path, content type, encoding and header names
    transcripts/        the harness's own session files for the sessions the hooks saw, under home/ by their path
      INDEX.txt         each copied file with its source path, size and modification time
    STEPS.txt           which SCRIPTS.md step the person was on, with the time they started it
    deny-once           Gemini CLI only: the decision tools/capture/deny-once.sh sent back at step 19, which turned that answer down once
    allow-once          Claude Code only: the decision tools/capture/allow-once.sh sent back at step 22, which allowed that permission prompt once
    connections         Claude Code only: the pid and executable name of each process holding one of the sink's connections at step 23, as lsof's p and c lines, or on Windows as Get-Process's Id and ProcessName
```

- `<harness>` is the id `pkg/event` uses: `claude_code`, `codex`, `cursor`, `copilot`, `gemini`, `devin`, `cline` or `opencode`.
- `<version>` is the version number the harness reports, without its name: `2.1.328`, not `2.1.328 (Claude Code)`.
- `<os>` is `windows`, `macos` or `linux`. The exact build goes in `PROVENANCE.md`.

## Rules

- Every fixture directory has a `PROVENANCE.md` made from [PROVENANCE.template.md](PROVENANCE.template.md). CI checks its front matter (`go run ./tools/ci/provenance`).
- Fixtures come from the capture kit in [tools/capture](../../tools/capture/README.md), following [SCRIPTS.md](../../tools/capture/SCRIPTS.md), so every prompt in them is text the script supplied.
- Everything is scrubbed before it's committed: the home path, account name, hostname, git name and email, other names the person lists, email addresses other than the few listed under Placeholders, and anything shaped like a key or token become fixed placeholders. The scrub report, which lists the originals, stays on the capturing machine. Then a person reads every file. The repo is public, so nothing private may remain.
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
| git `user.email` and every other email address except those listed below | `capture@example.invalid` |
| Anything else the person lists with `--also`: a real name, a school | `REDACTED` |
| Account and organization ids in telemetry | `00000000-0000-4000-8000-000000000000` |
| Key- or token-shaped strings | `REDACTED_<KIND>`, such as `REDACTED_GITHUB_TOKEN` |

These addresses name nobody, so the scrub leaves them as the harness wrote them, and tests can rely on them too:

- The public addresses tools write into commits and remotes: `noreply@anthropic.com` (Claude Code's `Co-Authored-By` trailer), `aider@aider.chat` (Aider's), `noreply@github.com`, `noreply@openai.com`, `git@github.com`, `git@gitlab.com`, `git@bitbucket.org` and `git@ssh.dev.azure.com`.
- Addresses at a domain reserved for examples and tests: `example.com`, `example.org`, `example.net` and any name under them, and a name of one label under `.example`, `.invalid`, `.test` or `.localhost`, such as `capture@example.invalid` or `user@myapp.test`. A longer name under those four, such as `jane.doe@westfield.edu.invalid`, is a real address with a reserved label appended, so it's replaced.

A known value or a key inside one of them is still replaced: with the account `rowan`, `rowan@example.com` becomes `capture@example.com`.
