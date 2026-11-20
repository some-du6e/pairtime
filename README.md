# pairtime

A time tracker for building with AI. It's meant to count the hours you spend thinking, steering, reading, testing and fixing, and to skip the hours your agent spends running on its own.

We're building it for Hack Club's AI time tracking bounty. [docs/BOUNTY.md](docs/BOUNTY.md) has the requirements, quoted from the announcement.

## Status

Research and design are in progress, and the code is a skeleton. Both binaries build for every target and answer `version` and `help`, `pairtime agent` runs the background agent's skeleton, and `pkg/event` holds the event model, but nothing tracks time yet. There's nothing to install.

## Layout

One Go module holds the client, the server and the code they share.

| Path | What's in it |
|---|---|
| `cmd/pairtime` | The client. One binary will be the background agent, the hook receiver for AI harnesses and the CLI |
| `cmd/pairtimed` | The server: accounts, projects, rollups and the live dashboard, once they exist |
| `pkg/event` | The event model from the [struggle-metric spec](docs/design/struggle-metric.md): the envelope, the 18 event kinds, the records the scorer derives from them, and validation for all of it |
| `internal/agent` | The background agent: its data directories, the single-instance lock, `config.toml`, `state.json`, the scheduler every timer goes through, and the HTTP server on 127.0.0.1 |
| `internal/agent/presence` | Presence and power: idle-time samples on each 15-second slot boundary and suspend, resume, lock and unlock, recorded only while a project context is live (DESIGN 3.3, D14) |
| `internal/agent/keys` | The device keystore: the device key `K_dev`, made once per install and kept in the OS's secret store or, failing that, a user-only file, and the ledger keys derived from it |
| `internal/agent/ledger` | The ledger writer: each event's record in the day's segment, a batch sealed every minute and at once on lock, suspend, prompt and stop, the batch chain in `batches.seg`, and recovery from a crash at any point (DESIGN 3.5, 3.6) |
| `internal/platform/windows` | Win32 calls without cgo: input idle time, suspend and lock notifications, EcoQoS, and DPAPI, which keeps the device key. No keyboard listener of any kind |
| `internal/platform/darwin`, `internal/platform/linux` | What macOS and Linux report about idle time, focus, sleep and screen lock, and the device keystore's half on each: the Keychain on macOS, the Secret Service on Linux |
| `internal/webui` | The local dashboard's built files, embedded in the client. The repo carries a placeholder, so Go builds never need Node |
| `web` | The web UI in Svelte 5 and TypeScript: the local dashboard the agent serves, the server UI, and the components they share. [web/README.md](web/README.md) says how it builds |
| `docs/api` | OpenAPI specs of pairtime's HTTP APIs. The UI's TypeScript types are generated from them |
| `internal/version` | Build info, set at link time |
| `docs/schema/event.schema.json` | A JSON Schema for events, for adapters written in other languages. A test fails if it drifts from `pkg/event` |
| `docs/` | Requirements, research and the design docs |

## Build from source

You need Go 1.26 or later and nothing else. pairtime never uses cgo, and its few modules are pure Go (DESIGN 3.12).

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
