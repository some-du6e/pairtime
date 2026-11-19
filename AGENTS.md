# AGENTS.md

pairtime is an AGPL-3.0 tracker for the human time spent building with AI. README.md has the layout and docs/BOUNTY.md the requirements (R1-R11).

## Build and test in CI

GitHub Actions is the only place pairtime gets built and tested. Locally, gofmt is the only Go tool to run. To check a change, push the branch, open or update its PR, and read the run:

```sh
gh run list --branch <branch>
gh run view <run-id> --log-failed
```

A change is done when CI is green. The one job in .github/workflows/ci.yml runs the license gate, gofmt, go vet, go test, and a cross-compile of both binaries for seven targets with a 15 MB budget on the client. A check CI lacks goes into ci.yml in the same PR, still as one job with no matrix, because the account's Actions capacity is shared with another busy project.

## Go

- Pure Go: every build and test runs with `CGO_ENABLED=0`, and the client cross-compiles from one Linux job to the targets in docs/research/client-platform.md section 7, `windows/386` included. For OS APIs, that research names the cgo-free route (`golang.org/x/sys`, purego, godbus).
- Standard library first. A third-party module has to be cgo-free and permissively licensed, and its PR says why the standard library falls short. The first PR that adds one also adds a dependency license check to CI.
- The `go` directive stays at 1.26 until Go 1.26 leaves support in February 2027. Go 1.27 dropped macOS 12, where 2015-2016 Macs top out (client-platform.md 1.5), so write nothing that needs a newer Go.
- gofmt every Go file.
- The first line of every .go file is `// SPDX-License-Identifier: AGPL-3.0-or-later`.
- Tests are table tests: a slice of named cases, each run with `t.Run`, each name stating the rule it checks.

## The struggle-metric spec decides scoring

docs/design/struggle-metric.md (SM) is the source of truth for what pairtime credits and why. pkg/event follows SM sections 1 and 5 field for field, and the scorer will follow sections 2 to 5 and the parameter table in section 8. Change a scoring rule in SM and in code in the same PR. Where they disagree, fix whichever is wrong and say which in the PR.

## Content stays on the device

Raw prompt text, agent output, code, file paths, command lines and window titles stay on the device. Uploads carry numbers, reason codes, opaque keys and event ids (SM 1, SM 5.1).

- A field holding prompt text, or a hash or sketch of anything private, is *local*. Tag it `pairtime:"local"` with `omitempty` in Go and `"x-local": true` in the schema. `Event.Shareable` clears local fields, so pass everything bound off the device through it.
- Identifiers for private things (devices, sessions, projects, files, commands) are opaque keys: random ids or salted hashes that pass `isKey`, which rejects paths and email addresses.
- `TestLocalFields` pins the local fields. Changing that list changes what pairtime may upload, so the PR description calls it out.

## Changing the event model

pkg/event and docs/schema/event.schema.json describe the same events, written by hand on both sides, and pkg/event/schema_test.go fails when they drift. Change both in the same commit. To add an event kind:

1. Add its `Kind` constant and payload type, and register it in `kindTable` with its rule (source, harness, session).
2. Add its `$defs` entry and its `allOf` rule to the schema.
3. Give `sample()` in event_test.go an instance with every field set, and add a validation case for each rule the payload checks.

Enum values go in the same order in Go and in the schema.
