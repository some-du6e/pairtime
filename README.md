# pairtime

A time tracker for building with AI. It's meant to count the hours you spend thinking, steering, reading, testing and fixing, and to skip the hours your agent spends running on its own.

We're building it for Hack Club's AI time tracking bounty. [docs/BOUNTY.md](docs/BOUNTY.md) has the requirements, quoted from the announcement.

## Status

Research and design are in progress, and the code is a skeleton. Both binaries build for every target and answer `version` and `help`, `pairtime agent` runs the background agent's skeleton, `pairtimed serve` starts the server on SQLite and answers health checks, and `pkg/event` holds the event model, but nothing tracks time yet. There's nothing to install.

## Layout

One Go module holds the client, the server and the code they share.

| Path | What's in it |
|---|---|
| `cmd/pairtime` | The client. One binary will be the background agent, the hook receiver for AI harnesses and the CLI |
| `cmd/pairtimed` | The server. `serve` checks SQLite, applies the migrations and serves until it's stopped, `migrate` applies them alone, and `healthcheck` asks a running server whether it's ready. Accounts, projects, rollups and the live dashboard come later |
| `pkg/event` | The event model from the [struggle-metric spec](docs/design/struggle-metric.md): the envelope, the 18 event kinds, the records the scorer derives from them, and validation for all of it |
| `pkg/params` | The public parameter table: every tunable value in the credit rule (SM 8, DESIGN 6.3), as canonical JSON with its SHA-256 |
| `pkg/ledger` | Part 1 of the [ledger format](docs/spec/ledger-v1.md): device keys, records, Merkle trees and inclusion proofs, the batch chain, the server's receipts, day trees and verification |
| `internal/agent` | The background agent: its data directories, the single-instance lock, `config.toml`, `state.json`, the scheduler every timer goes through, and the HTTP server on 127.0.0.1 |
| `internal/server` | The server's packages: `config` reads `pairtime.toml`, `store` opens the database (SQLite in WAL mode, or Postgres), `migrations` holds the schema, `writer` is the one goroutine every write goes through, and `ops` serves the health and readiness endpoints |
| `internal/release` | The [update manifest](docs/spec/update-manifest.md), the signed list of release archives a client checks its downloads against, and the release keys built into the client |
| `internal/timelines` | Loads `testdata/timelines`, the struggle-metric spec's worked examples written out as events. The scorer's tests compare their output with them |
| `internal/webui` | The local dashboard's built files, embedded in the client. A placeholder stands in until the UI exists, so Go builds never need Node |
| `internal/version` | Build info, set at link time |
| `tools/` | `ledger_ref.py`, a standard-library Python reference for `pkg/ledger`; `release`, which packages and signs releases; `capture`, the kit for capturing harness fixtures; and `ci`, the checks the workflows run |
| `testdata/` | What the tests read: the timelines, ledger vectors, captures of upstream interfaces, harness fixtures, and the license check's seeded modules |
| `docs/schema/event.schema.json` | A JSON Schema for events, for adapters written in other languages. A test fails if it drifts from `pkg/event` |
| `docs/` | Requirements, research, the design docs and the specs |

## Build from source

You need Go 1.26 or later and nothing else. pairtime never uses cgo, so there's no C compiler to install. Besides the standard library, it uses a few modules, all listed in `go.mod`. The client uses `golang.org/x/sys` for Windows and Unix system calls, `purego` on macOS, `godbus` and `go-keyring` on Linux, `fsnotify` to watch files and `BurntSushi/toml` for its config file. The server uses `modernc.org/sqlite`, `lib/pq` for Postgres and the same TOML parser. Each of them, and each module they pull in, is pure Go and MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0 or ISC licensed (DESIGN 3.12). ci.yml checks those licenses on every PR.

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

## License

[AGPL-3.0](LICENSE). Every Go file carries `SPDX-License-Identifier: AGPL-3.0-or-later`.
