# pairtime

A time tracker for building with AI. It's meant to count the hours you spend thinking, steering, reading, testing and fixing, and to skip the hours your agent spends running on its own.

We're building it for Hack Club's AI time tracking bounty. [docs/BOUNTY.md](docs/BOUNTY.md) has the requirements, quoted from the announcement.

## Status

v0.1.0-alpha.1, the first signed release, came out on 2026-11-21. It tracks Claude Code. `pairtime install` shows every change it would make before it makes any, then adds Claude Code's hooks and the login item and starts a background agent, all without admin rights. The agent turns each hook call, your git commits, and the computer's idle time and the app in front into events. The scorer splits the time into yours, your agent's and the rest ([the struggle-metric spec](docs/design/struggle-metric.md)), and `pairtime today`, `recap`, `statusline` and `dashboard` show it. Window titles are never read.

Nothing leaves the computer until `pairtime login` links it to a pairtime server through Hack Club Auth. From then on the agent uploads signed batches that hold numbers, reason codes and opaque keys, never prompts, code, file paths or window titles, and `pairtime privacy show` prints the next upload before it goes. `pairtimed` signs people in with Hack Club Auth, links their computers, takes the uploads, rolls them up and shows each person's hours on its dashboard. One runs at https://pairtime.karimeltaib.com, backed up by Litestream and a nightly snapshot. `pairtime uninstall` takes it all back out and deletes the device key with what pairtime recorded, keeping only the daily totals and recaps unless told to delete those too.

The agent keeps the text of your prompts and of the agent's final replies on the computer, encrypted, in its vault, so a spot check of your hours can see them. To keep none, and delete what's kept, run `pairtime privacy keep-text off`. By hand, set `keep_prompt_text = false` under `[privacy]` in pairtime's `config.toml`; if the file has no `[privacy]` line, as one written before this version has, add these two lines at the end:

```toml
[privacy]
keep_prompt_text = false
```

`pairtime doctor` checks the agent, its idle and focus sources, Claude Code's hooks with a timed test call, the adapters, the device key's store, the binary's signature and the hook calls only the spool delivered, one line each, and `doctor --report` prints a footprint report with no names or paths in it to share. `pairtime ledger show --day <date>` prints in plain JSON every record of a day that the ledger holds under this computer's device key; records from an earlier key, as before a restore, aren't in it.

Not built yet: harnesses other than Claude Code, Claude Code's transcripts and OpenTelemetry (WP-AD3, WP-AD4), and updates from inside the client.

## Install

Install pairtime on the computer where you run Claude Code, since pairtime counts your time from your typing and mouse on that computer. These commands install v0.1.0-alpha.3. Its install script checks the release's signature, then shows what `pairtime install` would change before it changes anything.

On macOS or Linux:

```sh
curl -fsSL https://github.com/some-du6e/pairtime/releases/download/v0.1.0-alpha.3/install.sh | sh
```

On Windows, in PowerShell:

```powershell
irm https://github.com/some-du6e/pairtime/releases/download/v0.1.0-alpha.3/install.ps1 | iex
```

Then `pairtime login` links the computer to your account on a pairtime server. The empty dashboard on the server gives the same two commands.

## Layout

One Go module holds the client, the server and the code they share.

| Path | What's in it |
|---|---|
| `cmd/pairtime` | The client. One binary is the background agent, the hook receiver for AI harnesses and the CLI. `install` and `uninstall` set it up and take it out, `login` and `logout` link the computer to a pairtime server and unlink it, `privacy show` prints the next upload, `privacy keep-text` turns keeping your prompts' text off or on, `privacy delete` deletes the device key and everything pairtime recorded on the computer, `ledger show` prints a day's records, `today` and `statusline` print your time and your agent's from the days the agent stores, `recap` and `doctor` ask the running agent over its socket, and `dashboard` opens the local dashboard in your browser, signed in |
| `cmd/pairtimed` | The server. `serve` checks SQLite, applies the migrations, signs people in with Hack Club Auth and serves until it's stopped, `migrate` applies them alone, and `healthcheck` asks a running server whether it's ready. `init`, `keys`, `snapshot` and `doctor` set up, back up and check a deployment. `serve` also links the client to accounts, takes its signed uploads, rolls them up and serves each person's dashboard |
| `deploy` | The deploy bundle: `compose.yml` with pairtimed under Litestream, cloudflared and the witness sidecar, `litestream.yml`, the `pairtime.toml` template, and [runbook.md](deploy/runbook.md), the procedure a deploy agent follows. `Dockerfile.pairtimed` at the top builds the image server-image.yml pushes |
| `pkg/event` | The event model from the [struggle-metric spec](docs/design/struggle-metric.md): the envelope, the 18 event kinds, the records the scorer derives from them, and validation for all of it |
| `pkg/params` | The public parameter table: every tunable value in the credit rule (SM 8, DESIGN 6.3), as canonical JSON with its SHA-256 |
| `pkg/score` | The scorer: turns events into credited human seconds, following SM with DESIGN 6.2's deviations |
| `pkg/ledger` | The [ledger format](docs/spec/ledger-v1.md): device keys, records, Merkle trees and inclusion proofs, the batch chain, the server's receipts, day trees and verification, and the content: the commitment of each prompt, final agent message and track label, the redacted reveal that opens one, the keyed features (`features`) and the challenge draw |
| `pkg/redact` | Finds what in a prompt or agent message must stay on the device: the teen's own values, secrets and personal data that passes Presidio's checks, email addresses included (DESIGN 8.7) |
| `internal/agent` | The background agent: its data directories, the single-instance lock, `config.toml`, `state.json`, the scheduler every timer goes through, the HTTP server on 127.0.0.1, the socket hooks hand their events to (`ipc`) and the spool they fall back on (`inbox`), `pairtime hook`'s fast path and what the agent makes of each hook call (`hookpath`), the scan for harness processes no hook has spoken for (`procs`), the check of which process opened a connection to that server, for WakaTime heartbeats and OTLP (`peer`), the one notification per work session (`notifier`), and `pairtime doctor`'s checks and footprint report (`doctor`) |
| `internal/agent/presence` | Presence and power: idle-time samples on each 15-second slot boundary and suspend, resume, lock and unlock, recorded only while a project context is live (DESIGN 3.3, D14) |
| `internal/agent/focus` | Focus: an `os.focus` record each time another window comes to the front, with its app class, the harness sessions it hosts and a keyed hash of the app, recorded only while a project context is live (DESIGN 3.3, SM 3.2). Window titles are never read |
| `internal/agent/keys` | The device keystore: the device key `K_dev`, made once per install and kept in the OS's secret store or, failing that, a user-only file, and the ledger keys derived from it |
| `internal/agent/ledger` | The ledger writer: each event's record in the day's segment, a batch sealed every minute and at once on lock, suspend, prompt and stop, the batch chain in `batches.seg`, and recovery from a crash at any point (DESIGN 3.5, 3.6) |
| `internal/platform/windows` | Win32 calls without cgo: input idle time, suspend and lock notifications, which window is in front and its process, the process table, which process opened a loopback connection, EcoQoS, and DPAPI, which keeps the device key. No keyboard listener of any kind |
| `internal/platform/darwin`, `internal/platform/linux` | What macOS and Linux report about idle time, focus, sleep and screen lock, which process opened a loopback connection, and the device keystore's half on each: the Keychain on macOS, the Secret Service on Linux |
| `internal/platform/localtime` | The time zone the computer is set to now, for clock times such as quiet hours. Go reads it only once per process |
| `internal/platform/clock` | A moment as an event records it: the wall clock, the OS's monotonic clock that keeps counting through sleep, and the id of the boot it counts from. The hook stamps each call with it, and the agent each message it takes in |
| `internal/platform/proctest` | Tests of the three process-table readers against process chains the tests start themselves. No code outside its tests |
| `internal/adapters/claudecode` | Claude Code's adapter: its hooks, which become events (DESIGN 5.1). The transcript reader and OpenTelemetry come later (WP-AD3, WP-AD4) |
| `internal/adapters/git` | The git adapter: which project a folder belongs to, and the commits, amends and rebases made there, read from `.git` (DESIGN 5.8) |
| `internal/install` | What `pairtime install` writes outside pairtime's own directories, the harness hooks and the login item, merged beside what's there, and the uninstall that puts each file back byte for byte; which harnesses are on the machine; and `consent`, the screen that shows each change before install makes it (DESIGN 5.9) |
| `internal/coach/text` | The words pairtime shows a teen about their time, shared by the coach, the CLI and the dashboard: your time, each agent's time named after it, the rest said for what it was, and nothing that scolds (DESIGN 3.8) |
| `internal/server` | The server's packages: `config` reads `pairtime.toml`, `store` opens the database (SQLite in WAL mode, or Postgres), `migrations` holds the schema, `writer` is the one goroutine every write goes through, `ops` serves the health and readiness endpoints, `accounts` keeps accounts and web sessions, `auth` signs people in with Hack Club Auth, `objstore` is the S3 client for evidence and snapshots, `backup` holds the server's keys, their encrypted backup, the nightly snapshot and the replica's lag, `keys` keeps which server key was valid when and publishes them at `/.well-known/pairtime-keys.json` ([keys-v1](docs/spec/keys-v1.md)), `device` links the client to an account with the device grant ([device-auth-v1](docs/spec/device-auth-v1.md)), `ingest` takes its signed uploads ([ingest-v1](docs/spec/ingest-v1.md)), `rollup` keeps the daily totals, `dashboard` and `pages` serve the dashboard, the home page and the privacy notice, and `retention` holds how long each kind of data is kept and the jobs that delete it |
| `internal/release` | The [update manifest](docs/spec/update-manifest.md), the signed list of release archives a client checks its downloads against, and the release keys built into the client |
| `install` | The install scripts every release carries, `install.sh` for macOS and Linux and `install.ps1` for Windows, which check a release's signature and SHA-256s and hand the binary to `pairtime install`, which puts it in place after the consent screen's yes, and their end-to-end tests in `e2e` |
| `internal/timelines` | Loads `testdata/timelines`, the struggle-metric spec's worked examples written out as events. The scorer's tests compare their output with them |
| `internal/jsonschema` | A JSON Schema validator for the keywords the schemas in `docs/schema` use, so tests can check JSON a command prints. The client doesn't link it |
| `internal/rehearsal` | The end-to-end rehearsal of a teen's first hour: the real binaries, Hack Club Auth sign-in (against a mock), `pairtime install`, `pairtime login` approved on `/device`, a Claude Code session typed at the keyboard under Xvfb, the hours on `/dashboard`, and `pairtime uninstall`. rehearsal.yml runs it, on every PR that changes what it builds or reads and on every push to main |
| `internal/webui` | The local dashboard's built files, embedded in the client. The repo carries a placeholder, so Go builds never need Node |
| `web` | The web UI in Svelte 5 and TypeScript: the local dashboard the agent serves, the server UI, and the components they share. [web/README.md](web/README.md) says how it builds |
| `docs/api` | OpenAPI specs of pairtime's HTTP APIs. The UI's TypeScript types are generated from them |
| `internal/version` | Build info, set at link time |
| `tools/` | `ledger_ref.py`, a standard-library Python reference for `pkg/ledger`; `release`, which packages and signs releases; `capture`, the kit for capturing harness fixtures; and `ci`, the checks the workflows run |
| `third_party/` | Patched copies of modules, built in place of the originals through `replace` lines in `go.mod`, each under its own license with a `PROVENANCE.md`: `burntsushi-toml`, the TOML parser, which reads the local time zone on first use rather than as every pairtime process starts |
| `testdata/` | What the tests read: the timelines, ledger vectors, captures of upstream interfaces, the consistency checks' scenarios, harness fixtures, the redaction corpus, and the license check's seeded modules |
| `docs/schema/event.schema.json` | A JSON Schema for events, for adapters written in other languages. A test fails if it drifts from `pkg/event` |
| `docs/schema/cli-today.schema.json` | What `pairtime today --json` prints. A test fails if it drifts from the Go types |
| `docs/` | Requirements, research, the design docs and the specs |

## Build from source

You need Go 1.26 or later and nothing else. pairtime never uses cgo, so there's no C compiler to install. Besides the standard library, it uses a few modules, all listed in `go.mod`. The client uses `golang.org/x/sys` for Windows and Unix system calls, `purego` on macOS, `godbus` on Linux, `go-keyring` on macOS and Linux (Keychain and Secret Service), `fsnotify` to watch files and `BurntSushi/toml`, in a patched copy (`third_party/`), for its config file. The server uses `modernc.org/sqlite`, `lib/pq` for Postgres and the same TOML parser. Each of them, and each module they pull in, is pure Go and MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0 or ISC licensed (DESIGN 3.12). ci.yml checks those licenses on every PR.

```sh
CGO_ENABLED=0 go build -o bin/ ./cmd/...
./bin/pairtime version
CGO_ENABLED=0 go test ./...
```

Cross-compiling works from any OS. CI builds these seven targets: `windows/amd64`, `windows/386`, `windows/arm64`, `darwin/amd64`, `darwin/arm64`, `linux/amd64` and `linux/arm64`.

```sh
GOOS=windows GOARCH=386 CGO_ENABLED=0 go build -o bin/ ./cmd/pairtime
```

To stamp a version the way CI and releases do:

```sh
pkg=github.com/some-du6e/pairtime/internal/version
CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X $pkg.Version=v0.1.0 -X $pkg.Commit=$(git rev-parse HEAD)" \
  -o bin/ ./cmd/...
```

Without those flags, `pairtime version` reports the commit the go command stamped into the binary.

A plain `go build` embeds the placeholder dashboard. To embed the real one, build the UI first, which needs Node 22.12 or later:

```sh
(cd web && npm ci && npm run build)
CGO_ENABLED=0 go build -o bin/ ./cmd/pairtime
```

## License

[AGPL-3.0](LICENSE). Every Go file carries `SPDX-License-Identifier: AGPL-3.0-or-later`.
