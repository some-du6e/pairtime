# AGENTS.md

pairtime is an AGPL-3.0 tracker for the human time spent building with AI. README.md has the layout and docs/BOUNTY.md the requirements (R1-R11).

## Build and test in CI

GitHub Actions is the only place pairtime gets built and tested. Locally, gofmt is the only Go tool to run. To check a change, push the branch, open or update its PR, and read the runs:

```sh
gh run list --branch <branch>
gh run view <run-id> --log-failed
```

A change is done when every workflow its PR started is green. A WP marked "Race: yes" also needs one green `gh workflow run nightly.yml --ref <branch>`. The account's Actions capacity is shared with another busy project, so Linux is the default and the rest is path-filtered or manual (DESIGN 3.13):

| Workflow | What it runs | When |
|---|---|---|
| ci.yml | the one Linux job: lint, `go test ./...`, the seven-target cross-compile with the 15 MB client budget, and the license, oracle and provenance guards | every PR, pushes to main |
| platform.yml | `go test` of the platform packages on Windows and macOS, and the desktop tests on Linux | PRs touching its paths, release tags |
| nightly.yml | `go test -race` over internal/agent and internal/server | daily, 03:00 UTC |
| web.yml | npm licenses, svelte-check, vitest, the UI build, and Playwright on Chromium against a `pairtime agent` built with that UI embedded | PRs touching web/, extension/, internal/webui/, docs/api/ or the agent's HTTP code; release tags |
| objstore.yml | the object storage client against a MinIO service container | PRs touching internal/server/objstore/, weekly |
| tidy.yml | `go mod tidy` and npm lockfiles, committed to the branch | by hand |
| fixtures.yml | the fixture provenance check | by hand |

Any workflow also runs by hand: `gh workflow run <file> --ref <branch>`.

- A check CI lacks becomes a step of ci.yml's one job, with no matrix. A new workflow gets `workflow_dispatch`, a `concurrency` group that cancels stale runs, `timeout-minutes`, `permissions: contents: read` unless it pushes, and actions pinned to a full commit SHA with the version in a comment.
- Windows and macOS runners are the scarce ones, so read every failure in a run before pushing again. Tests there use injected fakes. Code needing them outside platform.yml's paths adds its path to that filter in the same PR.
- Tests that need an X server or a session D-Bus go in `*_desktop_test.go` files under `//go:build linux && desktop`. platform.yml's Linux job runs them with `-tags desktop` inside Xvfb, openbox and a private bus (tools/ci/desktop_session.sh); real focus switching with xdotool happens only there.
- go.sum and package-lock.json come from CI: `gh workflow run tidy.yml --ref <branch>`. Its push starts no CI run, so pull, then push again (an empty commit will do).

## Oracles and errata

An oracle decides whether code is right, so the code it checks never changes it: docs/DESIGN.md, docs/BOUNTY.md, docs/design/** (SM included), docs/research/**, tools/ledger_ref.py, and testdata/ under timelines, ledger, fixtures, upstream, checks and redteam.

- New oracle files may arrive beside new code. An existing oracle changes in a PR of its own. ci.yml's oracle guard fails a PR that modifies, deletes or renames an oracle and also touches cmd/, internal/, pkg/, web/ or extension/, unless Karim adds the `oracle-change` label. A scoring-rule change, SM and code together, is such a PR.
- When an SM worked example, a design number or a captured fixture disagrees with what the spec says should happen, write docs/errata/<YYYY-MM-DD>-<topic>.md with the input, the expected value, the computed value and your reasoning, add the `erratum` label, and stop there. Karim decides what is wrong; until then the code, the spec and the fixture stay as they are (DESIGN 6.6).
- Every testdata/fixtures/<harness>/<version>/<os>/ directory has a PROVENANCE.md, which ci.yml checks (tools/ci/provenance says what it holds).

## Go

- Pure Go: every build and test runs with `CGO_ENABLED=0`, and the client cross-compiles from one Linux job to the targets in docs/research/client-platform.md section 7, `windows/386` included. For OS APIs, that research names the cgo-free route (`golang.org/x/sys`, purego, godbus). The one exception is nightly.yml's `go test -race` on Linux, because the race detector needs cgo. Nothing that ships is built that way.
- Standard library first. A third-party module has to be cgo-free and licensed MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0 or ISC, and so do the modules it pulls in; ci.yml's module license step checks all of them. Its PR says why the standard library falls short, and a module missing from DESIGN 3.12's list also needs a reason the list falls short.
- The `go` directive stays at 1.26 until Go 1.26 leaves support in February 2027. Go 1.27 dropped macOS 12, where 2015-2016 Macs top out (client-platform.md 1.5), so write nothing that needs a newer Go.
- gofmt every Go file.
- The first line of every .go file is `// SPDX-License-Identifier: AGPL-3.0-or-later`.
- Tests are table tests: a slice of named cases, each run with `t.Run`, each name stating the rule it checks.

## Web UI

web/ is Svelte 5 and TypeScript built by Vite: the local dashboard (src/local, built into internal/webui/dist for the agent to embed), the server UI (src/server) and the components both use (src/shared). web/README.md has the layout and the commands, which run in web.yml only.

- package.json pins exact versions, each released at least two weeks earlier unless it fixes a security advisory. A runtime dependency ships in the bundle, so it has to be MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0 or ISC; tools/ci/npm_licenses.py checks every package in the lockfile.
- docs/api/*.openapi.yaml is the API contract. `npm run gen` writes the TypeScript types into web/src/gen/, which isn't checked in, so read the YAML and import the types with `import type`. A route or field changes in the YAML and in Go in the same PR; web/tests/smoke.spec.ts holds the running agent to the YAML.
- Built pages carry a Content-Security-Policy that allows only their own origin: no CDN, web font, inline script, inline style or `style=` attribute. Fonts are system fonts, and colors are hex tokens in web/src/shared/styles/base.css.
- Consent and profile questions use ConsentCard and ProfileChoice from web/src/shared. They fix "Not now" as the most prominent button and keep anything from being preselected (DESIGN 8.3, 8.4), and web/tests/consent.spec.ts checks that on the rendered page.
- The first line of every .ts, .svelte, .css and .html file is an SPDX-License-Identifier comment for AGPL-3.0-or-later.

## The struggle-metric spec decides scoring

docs/design/struggle-metric.md (SM) is the source of truth for what pairtime credits and why. pkg/event follows SM sections 1 and 5 field for field, and the scorer will follow sections 2 to 5 and the parameter table in section 8. Change a scoring rule in SM and in code in the same PR, which needs the `oracle-change` label. Where code disagrees with SM, fix the code; where SM looks wrong, file an erratum (Oracles and errata).

## Content stays on the device

Raw prompt text, agent output, code, file paths, command lines and window titles stay on the device (SM 1). Tier-1 uploads carry numbers, reason codes and opaque keys, never event ids (DESIGN 4.3). Event ids leave the device only inside the ledger records a submission reveals (DESIGN 3.6).

- Content and digests of content are *local*: prompt text and its sketches (MinHash, template hashes, error signatures in prompts), command-line hashes, app identity hashes and scheduled-prompt hashes. Tag them `pairtime:"local"` with `omitempty` in Go and `"x-local": true` in the schema. `Event.Shareable` clears local fields, so pass everything bound off the device through it. It only clears top-level payload fields, so tag the whole field when the private part is nested.
- Identifiers of devices, sessions, turns, projects and files are opaque keys and may be shared. They must be keyed per device (an HMAC under a device secret, or a random id), never a plain hash a reader could recompute from a guess. They pass `isKey`, which checks only their shape. It turns away a path or an email address but passes a bare name like `Makefile`, so the keying is what keeps them private.
- `TestLocalFields` pins the local fields. Changing that list changes what pairtime may upload, so the PR description calls it out.

## Changing the event model

pkg/event and docs/schema/event.schema.json describe the same events, written by hand on both sides, and pkg/event/schema_test.go fails when they drift. Change both in the same commit. To add an event kind:

1. Add its `Kind` constant and payload type, and register it in `kindTable` with its rule (source, via, harness, session).
2. Add its `$defs` entry and its `allOf` rule to the schema.
3. Give `sample()` in event_test.go an instance with every field set, and add a validation case for each rule the payload checks.

Enum values go in the same order in Go and in the schema.
