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
| footprint.yml | the release-built agent idle on Windows: working set under 25 MB, CPU time under 0.1% of wall time; then 200 warm `pairtime hook` calls and 200 of an empty Go program, timed around each process, with the hook's p95 at most 10 ms over the empty program's and at most 50 ms | PRs touching internal/agent, internal/platform/windows, cmd/pairtime, go.mod or go.sum (3 minutes idle); daily, 03:30 UTC, and release tags (10 minutes) |
| nightly.yml | `go test -race` over internal/agent and internal/server | daily, 03:00 UTC |
| postgres.yml | the server tests on Postgres 18 as well as SQLite | PRs touching its paths, the packages that hold SQL; daily, 03:30 UTC |
| web.yml | npm licenses, svelte-check, vitest, the UI build and the notices of the npm code in it, Playwright on Chromium against a `pairtime agent` built with that UI embedded, and SPDX headers | PRs touching web/, extension/, internal/webui/, docs/api/, tools/release/extra-notices.txt, or the agent code the smoke test runs (internal/agent/, internal/version/, cmd/pairtime/); release tags |
| objstore.yml | the object storage client against a MinIO service container, or with `-f minio=source` against MinIO built from its source | PRs touching internal/server/objstore/, weekly |
| restore-drill.yml | the deploy bundle against MinIO: deploy with compose.yml, back up, destroy, restore from the replica and the key backup, checked with the age and sqlite3 tools and the server's pages; with `-f target=production`, a read-only restore of the real replica | PRs touching deploy/, Dockerfile.pairtimed or internal/server/backup/; weekly |
| probe.yml | a deployed server from GitHub's network: TLS, `/healthz` and `/.well-known/pairtime-keys.json` | by hand, `-f host=<host>` |
| release.yml | the seven targets' archives, checksums.txt and the signed manifest.json | release tags, signed with the release key and published; PRs touching internal/release or tools/release, as a snapshot signed with a throwaway key and never published |
| server-image.yml | the server image for linux/amd64 and linux/arm64, pushed to ghcr.io/some-du6e/pairtimed as `sha-<commit>`, and as `main` from main | pushes to main touching cmd/pairtimed/, internal/server/, deploy/, Dockerfile.pairtimed, go.mod or go.sum; by hand |
| tidy.yml | `go mod tidy` and npm lockfiles, committed to the branch | by hand |
| fixtures.yml | the fixture provenance check | by hand |

Any workflow also runs by hand: `gh workflow run <file> --ref <branch>`.

- A check CI lacks becomes a step of ci.yml's one job, with no matrix. A new workflow gets `workflow_dispatch`, a `concurrency` group that cancels stale runs, `timeout-minutes`, `permissions: contents: read`, and actions pinned to a full commit SHA with the version in a comment. Two exceptions: release.yml's run on a release tag never cancels, and a job that pushes (tidy.yml) or publishes (release.yml) gets the write permissions it needs, on that job alone.
- A check that needs a `services:` container, such as Postgres or MinIO, gets a path-filtered workflow of its own. A short check that starts its container in a step with `docker run`, like the mock OIDC server, stays in ci.yml. A PR that adds a workflow or changes its triggers updates its row in the table.
- Windows and macOS runners are the scarce ones, so read every failure in a run before pushing again. Tests there use injected fakes. platform.yml keeps two lists: its `paths` filter decides whether it runs, and `PLATFORM_DIRS` decides what the Windows and macOS jobs test. Code that needs those runners adds its directory to both lists in the same PR.
- Tests that need an X server or a session D-Bus go in `*_desktop_test.go` files under `//go:build linux && desktop`, and each one's name starts with `TestDesktop`; ci.yml fails any other test function in those files. platform.yml's Linux job runs `go test -tags desktop -run '^TestDesktop'` in each package that has such a file, inside Xvfb, openbox and a private bus (tools/ci/desktop_session.sh); real focus switching with xdotool happens only there. A desktop test belongs in a package platform.yml's paths already cover, such as internal/platform/linux, because a change under any path in that filter starts the Windows and macOS jobs too. A desktop test outside platform.yml's paths adds its directory to the filter.
- go.sum and package-lock.json come from CI: `gh workflow run tidy.yml --ref <branch>`. Its push starts no CI run, so pull, then push again (an empty commit will do).

## Oracles and errata

An oracle decides whether code is right, so the code it checks never changes it: docs/DESIGN.md, docs/BOUNTY.md, docs/design/** (SM included), docs/research/**, docs/errata/**, tools/ledger_ref.py, and testdata/ under timelines, ledger, fixtures, upstream, checks and redteam.

- New oracle files may arrive beside new code. An existing oracle changes in a PR of its own. ci.yml's oracle guard fails a PR that modifies, deletes or renames an oracle and also touches cmd/, internal/, pkg/, web/, extension/, integrations/, tools/capture/ or tools/loadtest/, unless it has the `oracle-change` label. Only Karim or the orchestrator, the session that hands out and merges WPs, adds that label, and only to apply a decision made outside the PR: a decided erratum, or a scoring-rule change one of them decided. The PR description links that decision, and the PR's oracle edits stay within what it decided.
- When an SM worked example, a design number or a captured fixture disagrees with what the spec says should happen, write docs/errata/<YYYY-MM-DD>-<topic>.md with the input, the expected value, the computed value and your reasoning, add the `erratum` label, and stop there. The orchestrator decides errata for Karim, outside the PR that raised them, and writes each decision into the erratum's Status line. Karim can overrule it. When only someone outside the project can answer, such as Hack Club HQ or Quality and Integrity, the orchestrator sets an interim answer and the question goes to Karim. Until a decision lands, the code, the spec and the fixture stay as they are (DESIGN 6.6). A new finding about a merged erratum goes in a new erratum file that links it.
- Every testdata/fixtures/<harness>/<version>/<os>/ directory has a PROVENANCE.md, and every fixture file sits inside one of those directories; only README.md files and the root's PROVENANCE.template.md go above them. ci.yml checks both (tools/ci/provenance says what PROVENANCE.md holds).

## Go

- Pure Go: every build and test runs with `CGO_ENABLED=0`, and the client cross-compiles from one Linux job to the targets in docs/research/client-platform.md section 7, `windows/386` included. For OS APIs, that research names the cgo-free route (`golang.org/x/sys`, purego, godbus). The one exception is nightly.yml's `go test -race` on Linux, because the race detector needs cgo. Nothing that ships is built that way.
- Standard library first. A third-party module has to be cgo-free and licensed MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0 or ISC, and so do the modules it pulls in; ci.yml's module license step checks all of them. Its PR says why the standard library falls short, and a module missing from DESIGN 3.12's list also needs a reason the list falls short.
- The `go` directive stays at 1.26 until Go 1.26 leaves support in February 2027. Go 1.27 dropped macOS 12, where 2015-2016 Macs top out (client-platform.md 1.5), so write nothing that needs a newer Go.
- gofmt every Go file.
- The first line of every .go file is `// SPDX-License-Identifier: AGPL-3.0-or-later`, except under third_party/. That holds patched copies of modules, which go.mod's `replace` builds instead of the originals. Each keeps its own license file and headers, and has a PROVENANCE.md naming the upstream commit and the patch. ci.yml's license gate skips it.
- Tests are table tests: a slice of named cases, each run with `t.Run`, each name stating the rule it checks.

## Web UI

web/ is Svelte 5 and TypeScript built by Vite: the local dashboard (src/local, built into internal/webui/dist for the agent to embed), the server UI (src/server) and the components both use (src/shared). web/README.md has the layout and the commands, which run in web.yml only.

- package.json pins exact versions. Every package in package-lock.json, transitive ones included, was released at least two weeks before it was locked, unless it fixes a security advisory or is required by a package that does: Vite 8.3.8 fixes GHSA-p4f5-76q4-ffq7 and requires rolldown `~1.2.18`. tidy.yml locks the newest release in each range, so after it runs, hold a younger package to an older release in its range with an entry in package.json's `overrides`, and drop entries that no longer hold anything back. A runtime dependency ships in the bundle, so it has to be MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0 or ISC; tools/ci/npm_licenses.py checks every package in the lockfile. The license and notice files of each package with code in the bundle ship too, copied into internal/webui/notices and listed in tools/release/extra-notices.txt; after web.yml's build, tools/ci/npm_notices.py names each copy and line an upgrade or a new import needs.
- docs/api/*.openapi.yaml is the API contract. `npm run gen` writes the TypeScript types into web/src/gen/, which isn't checked in, so read the YAML and import the types with `import type`. A route or field changes in the YAML and in Go in the same PR; web/tests/smoke.spec.ts holds the running agent to the YAML.
- Built pages carry a Content-Security-Policy that allows only their own origin: no CDN, web font, inline script, inline style or `style=` attribute. Fonts are system fonts, and colors are hex tokens in web/src/shared/styles/base.css.
- Consent and profile questions use ConsentCard and ProfileChoice from web/src/shared. They fix "Not now" as the most prominent button and keep anything from being preselected (DESIGN 8.3, 8.4), and web/tests/consent.spec.ts checks that on the rendered page.
- Every .ts, .js, .svelte, .css and .html file under web/ opens with an SPDX-License-Identifier comment for AGPL-3.0-or-later. An .html file has `<!doctype html>` on line 1, which TestFS needs the built page to start with, and the comment on line 2; every other file has it on line 1. web.yml's SPDX step checks them, and internal/webui's placeholder too.

## The struggle-metric spec decides scoring

docs/design/struggle-metric.md (SM) is the source of truth for what pairtime credits and why. pkg/event follows SM sections 1 and 5 field for field, and the scorer will follow sections 2 to 5 and the parameter table in section 8. Change a scoring rule in SM and in code in the same PR, which needs the `oracle-change` label. Where code disagrees with SM, fix the code; where SM looks wrong, file an erratum (Oracles and errata).

## Content stays on the device

Raw prompt text, agent output, code, file paths, command lines and window titles stay on the device (SM 1). Tier-1 uploads carry numbers, reason codes and opaque keys, never event ids (DESIGN 4.3). Event ids leave the device only inside the ledger records a submission reveals (DESIGN 3.6).

- Content and digests of content are *local*: prompt text and its sketches (MinHash, template hashes, error signatures in prompts), command-line hashes, app identity hashes and scheduled-prompt hashes. Tag them `pairtime:"local"` with `omitempty` in Go and `"x-local": true` in the schema. `Event.Shareable` clears local fields, so pass everything bound off the device through it. It only clears top-level payload fields, so tag the whole field when the private part is nested.
- Identifiers of devices, sessions, turns, projects and files are opaque keys and may be shared. They must be keyed per device (an HMAC under a device secret, or a random id), never a plain hash a reader could recompute from a guess. They pass `isKey`, which checks only their shape. It turns away a path or an email address but passes a bare name like `Makefile`, so the keying is what keeps them private.
- `TestLocalFields` pins the local fields. Changing that list changes what pairtime may upload, so the PR description calls it out.

## Retention

docs/retention.json is the one source for how long pairtime keeps each kind of data (DESIGN 4.4). The privacy notice at /privacy renders it, and pairtimed's retention jobs run it.

- A migration that creates a table gives it a place there in the same PR, under the entry for the data it holds, or in `no_personal_data`. Under an entry with a period (`days`, or `{window}` in `kept`), the table is one of the entry's `jobs`, so its rows are deleted on time, or is in `lifetime_tables` when its rows stay while the account exists, as `kept` says. Only an entry without a period lists plain `tables`. Load refuses anything else, and TestTables in internal/server/retention checks every table.
- `no_personal_data` holds only tables with nothing about a person or a device. A PR that adds a table there names it in its description, because no retention job deletes its rows.
- Evidence-store entries take tables and jobs like server entries, because the store's index rows are in the server's database (DESIGN 10.1). A job with `"period": "window"` deletes rows whose time column holds a decision once its spot-check window has closed, by `Window.Closes`, the rule the evidence store uses too.
- A job whose table doesn't exist yet names the WP that creates it in `awaits`. That WP creates the table with the columns the job names and removes `awaits`, which turns the job on.
- No foreign key points at a table a retention job deletes from, such as `devices`: the delete would fail, or cascade into rows that stay longer, such as the witness rows of a device removed 90 days ago. TestTables checks this.
- Ingest's chain head (the `prev` a new batch must match) and request counter live on the device row, never only in `witness_rows`, whose job deletes a device's newest rows too once they are 180 days old, as after a summer away.
- Whatever decides, re-decides or withdraws a submission (WP-RV4's outcome, the withdraw endpoint) sets `decided_at_ms` on that submission's holds in `submission_batches` and `submission_minutes`, or deletes them. Each hold table's own job deletes a hold once its window has closed, but a hold whose `decided_at_ms` stays NULL keeps itself and the rows it names for ever.

## Changing the event model

pkg/event and docs/schema/event.schema.json describe the same events, written by hand on both sides, and pkg/event/schema_test.go fails when they drift. Change both in the same commit. To add an event kind:

1. Add its `Kind` constant and payload type, and register it in `kindTable` with its rule (source, via, harness, session).
2. Add its `$defs` entry and its `allOf` rule to the schema.
3. Give `sample()` in event_test.go an instance with every field set, and add a validation case for each rule the payload checks.

Enum values go in the same order in Go and in the schema.
