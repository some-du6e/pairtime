---
harness: claude_code
harness_version: 2.1.328
os: Windows 11 23H2, build 22631.4460
captured_on: 2026-11-21
captured_by: rowan-example
synthetic: false
scrubbed_by: tools/capture/scrub.py at 68726d8ccf48, every replacement and the remaining text read by eye
kit: tools/capture at 68726d8ccf48
script: tools/capture/SCRIPTS.md, Claude Code, steps 1 to 18
otlp: http/json to tools/capture/otlp_sink.py, prompt logging off
runs: cli (Windows Terminal), vscode (extension 2.1.328)
---

# Fixture provenance

Copy this file into the fixture directory as `PROVENANCE.md`, then replace every value in the front matter above. The block between the two `---` lines is what CI reads (`go run ./tools/ci/provenance`), so keep it first in the file, one `key: value` per line, with nothing after a value on its line. The example values describe a made-up Claude Code capture; none of them is real.

## Required fields

| Field | What to write |
|---|---|
| `harness` | The harness id, as in `pkg/event`: `claude_code`, `codex`, `cursor`, `copilot`, `gemini`, `devin`, `cline` or `opencode`. Same as the first directory name |
| `harness_version` | The version number the harness reports, such as `2.1.328` from `claude --version`, without the product name. Same as the second directory name |
| `os` | The OS and its build, as precise as `winver`, `sw_vers` or `/etc/os-release` gives it: `Windows 10 22H2, build 19045.5131`, `macOS 15.1 (24B83)`, `Ubuntu 24.04.1 LTS, kernel 6.8.0-49`. The directory name is only the family: `windows`, `macos` or `linux` |
| `captured_on` | The date of the capture, `YYYY-MM-DD` |
| `captured_by` | A handle you are happy to have in a public repo, such as your GitHub username. Not your legal name, unless you want it there |
| `synthetic` | `false` for a capture from the real harness. `true` for a fixture written from documentation; an adapter can't ship on synthetic fixtures alone (DESIGN 5.1) |
| `scrubbed_by` | What scrubbed the files and at which commit, and that a person read the result: `tools/capture/scrub.py at <commit>, ...`, or `pairtime fixtures scrub <version>, ...` once that exists (WP-PV1) |

## Optional fields

| Field | What to write |
|---|---|
| `kit` | The commit of `tools/capture` you used, from `git rev-parse --short=12 HEAD` |
| `script` | The `SCRIPTS.md` section and steps you followed |
| `otlp` | The OTLP protocol and the sink, or `off` |
| `runs` | The run directories in this fixture and the surface each one used, such as `cli (Windows Terminal), vscode (extension 2.1.328)` |

## Notes

Below the front matter, write anything a reader of the fixture should know: steps you skipped or repeated and why, anything the harness did that the script didn't predict, prompts you typed that aren't in the script, and the replacements in the scrub report you decided to keep.
