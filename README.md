# pairtime

A time tracker for building with AI. It's meant to count the hours you spend thinking, steering, reading, testing and fixing, and to skip the hours your agent spends running on its own.

We're building it for Hack Club's AI time tracking bounty. [docs/BOUNTY.md](docs/BOUNTY.md) has the requirements, quoted from the announcement.

## Status

Research and design are in progress, and the code is a skeleton. Both binaries build for every target and answer `version` and `help`, `pairtime agent` runs the background agent's skeleton, `pairtimed serve` starts the server on SQLite, signs people in with Hack Club Auth and answers health checks, and `pkg/event` holds the event model, but nothing tracks time yet. There's nothing to install.

## Layout

One Go module holds the client, the server and the code they share.

| Path | What's in it |
|---|---|
| `cmd/pairtime` | The client. One binary will be the background agent, the hook receiver for AI harnesses and the CLI |
| `cmd/pairtimed` | The server. `serve` checks SQLite, applies the migrations, signs people in with Hack Club Auth and serves until it's stopped, `migrate` applies them alone, and `healthcheck` asks a running server whether it's ready. Projects, rollups and the live dashboard come later |
| `pkg/event` | The event model from the [struggle-metric spec](docs/design/struggle-metric.md): the envelope, the 18 event kinds, the records the scorer derives from them, and validation for all of it |
| `pkg/params` | The public parameter table: every tunable value in the credit rule (SM 8, DESIGN 6.3), as canonical JSON with its SHA-256 |
| `pkg/score` | The scorer: turns events into credited human seconds, following SM with DESIGN 6.2's deviations |
| `pkg/ledger` | The [ledger format](docs/spec/ledger-v1.md): device keys, records, Merkle trees and inclusion proofs, the batch chain, the server's receipts, day trees and verification, and the content: the commitment of each prompt, final agent message and track label, the redacted reveal that opens one, the keyed features (`features`) and the challenge draw |
| `pkg/redact` | Finds what in a prompt or agent message must stay on the device: the teen's own values, secrets and personal data that passes a checksum (DESIGN 8.7) |
| `internal/agent` | The background agent: its data directories, the single-instance lock, `config.toml`, `state.json`, the scheduler every timer goes through, the HTTP server on 127.0.0.1, the socket hooks hand their events to (`ipc`) and the spool they fall back on (`inbox`), the scan for harness processes no hook has spoken for (`procs`), and the one notification per work session (`notifier`) |
| `internal/agent/presence` | Presence and power: idle-time samples on each 15-second slot boundary and suspend, resume, lock and unlock, recorded only while a project context is live (DESIGN 3.3, D14) |
| `internal/agent/focus` | Focus: an `os.focus` record each time another window comes to the front, with its app class, the harness sessions it hosts and a keyed hash of the app, recorded only while a project context is live (DESIGN 3.3, SM 3.2). Window titles are never read |
| `internal/agent/keys` | The device keystore: the device key `K_dev`, made once per install and kept in the OS's secret store or, failing that, a user-only file, and the ledger keys derived from it |
| `internal/agent/ledger` | The ledger writer: each event's record in the day's segment, a batch sealed every minute and at once on lock, suspend, prompt and stop, the batch chain in `batches.seg`, and recovery from a crash at any point (DESIGN 3.5, 3.6) |
| `internal/platform/windows` | Win32 calls without cgo: input idle time, suspend and lock notifications, which window is in front and its process, the process table, EcoQoS, and DPAPI, which keeps the device key. No keyboard listener of any kind |
| `internal/platform/darwin`, `internal/platform/linux` | What macOS and Linux report about idle time, focus, sleep and screen lock, and the device keystore's half on each: the Keychain on macOS, the Secret Service on Linux |
| `internal/platform/localtime` | The time zone the computer is set to now, for clock times such as quiet hours. Go reads it only once per process |
| `internal/platform/proctest` | Tests of the three process-table readers against process chains the tests start themselves. No code outside its tests |
| `internal/adapters/git` | The git adapter: which project a folder belongs to, and the commits, amends and rebases made there, read from `.git` (DESIGN 5.8) |
| `internal/install` | What `pairtime install` writes outside pairtime's own directories, the harness hooks and the login item, merged beside what's there, and the uninstall that puts each file back byte for byte (DESIGN 5.9) |
| `internal/server` | The server's packages: `config` reads `pairtime.toml`, `store` opens the database (SQLite in WAL mode, or Postgres), `migrations` holds the schema, `writer` is the one goroutine every write goes through, `ops` serves the health and readiness endpoints, `accounts` keeps accounts and web sessions, `auth` signs people in with Hack Club Auth, and `objstore` is the S3 client for evidence and snapshots |
| `internal/release` | The [update manifest](docs/spec/update-manifest.md), the signed list of release archives a client checks its downloads against, and the release keys built into the client |
| `internal/timelines` | Loads `testdata/timelines`, the struggle-metric spec's worked examples written out as events. The scorer's tests compare their output with them |
| `internal/webui` | The local dashboard's built files, embedded in the client. The repo carries a placeholder, so Go builds never need Node |
| `web` | The web UI in Svelte 5 and TypeScript: the local dashboard the agent serves, the server UI, and the components they share. [web/README.md](web/README.md) says how it builds |
| `docs/api` | OpenAPI specs of pairtime's HTTP APIs. The UI's TypeScript types are generated from them |
| `internal/version` | Build info, set at link time |
| `tools/` | `ledger_ref.py`, a standard-library Python reference for `pkg/ledger`; `release`, which packages and signs releases; `capture`, the kit for capturing harness fixtures; and `ci`, the checks the workflows run |
| `testdata/` | What the tests read: the timelines, ledger vectors, captures of upstream interfaces, the consistency checks' scenarios, harness fixtures, the redaction corpus, and the license check's seeded modules |
| `docs/schema/event.schema.json` | A JSON Schema for events, for adapters written in other languages. A test fails if it drifts from `pkg/event` |
| `docs/` | Requirements, research, the design docs and the specs |

## Build from source

You need Go 1.26 or later and nothing else. pairtime never uses cgo, so there's no C compiler to install. Besides the standard library, it uses a few modules, all listed in `go.mod`. The client uses `golang.org/x/sys` for Windows and Unix system calls, `purego` on macOS, `godbus` on Linux, `go-keyring` on macOS and Linux (Keychain and Secret Service), `fsnotify` to watch files and `BurntSushi/toml` for its config file. The server uses `modernc.org/sqlite`, `lib/pq` for Postgres and the same TOML parser. Each of them, and each module they pull in, is pure Go and MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0 or ISC licensed (DESIGN 3.12). ci.yml checks those licenses on every PR.

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
