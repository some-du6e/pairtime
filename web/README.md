<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# web

pairtime's web UI, in Svelte 5 and TypeScript, built by Vite (DESIGN 3.11, 3.12). Each UI is its own Vite build over the same shared components:

| Path | What it is | Built into |
|---|---|---|
| `src/local/` | The local dashboard: Today, Projects, Privacy and Settings. The agent embeds it and serves it on 127.0.0.1 to the teen's own browser, which `pairtime dashboard` signs in (DESIGN 3.9) | `../internal/webui/dist/`, over the placeholder |
| `src/server/` | The server UI, which `pairtimed` will embed (WP-SV8, WP-SV9) | `dist/server/` |
| `src/shared/` | Components, styles and the typed API client the UIs share | |
| `src/gallery/` | Every shared component on one page, for Playwright and for looking at them. Never shipped | `dist/gallery/` |
| `src/gen/` | TypeScript types generated from `../docs/api/*.openapi.yaml` by `npm run gen`. Not checked in | |
| `tests/` | Playwright specs (`*.spec.ts`) and the helpers they share, the fake agent among them. Unit tests sit next to the code as `*.test.ts` | |

## Commands

These run in CI (`.github/workflows/web.yml`); a person with Node 22.12 or later can run them too.

```sh
npm ci                # install exactly what package-lock.json says; .npmrc turns install scripts off
npm run check         # generate the API types, then svelte-check
npm run test:unit     # vitest
npm run build         # all three builds
npm run dev           # the local dashboard on http://127.0.0.1:5173
npm run dev:gallery   # the component gallery
```

Playwright needs the builds and Chromium, and its smoke test needs an agent built with the dashboard embedded:

```sh
npm run build
(cd .. && CGO_ENABLED=0 go build -o "$PWD/web/pairtime" ./cmd/pairtime)
./node_modules/.bin/playwright install chromium
PAIRTIME_BIN="$PWD/pairtime" npm run e2e
```

The smoke test runs that agent in a data directory of its own, on port 17247 (`PAIRTIME_E2E_PORT` changes it). Without `PAIRTIME_BIN` it is skipped locally and fails in CI.

## How the pieces fit

- **Builds are picked by mode.** `vite build --mode agent` builds `src/local/` for the agent; Vite reserves the mode name `local`. `server` and `gallery` build the other two. Only builds get the Content-Security-Policy `<meta>` that `vite.config.ts` adds, because the dev server injects inline styles.
- **The placeholder stays checked in.** `go build` embeds whatever `internal/webui/dist/` holds, so a plain checkout builds without Node. `npm run build` overwrites `index.html` there, and `git checkout -- internal/webui/dist` puts the placeholder back. `.gitignore` keeps the built assets out, so a built `index.html` committed by mistake fails `internal/webui`'s tests in ci.yml, which run on both.
- **Each build records what it bundled.** `vite build` writes `dist/bundle-<mode>.json`: every module in the build's chunks with the length of the code it rendered there, and every file it emitted as an asset. web.yml and release.yml hold the dashboard's record to the license copies in `../internal/webui/notices/`, which every release archive carries (`tools/ci/npm_notices.py`). So an upgrade that changes a bundled package's license file or version, or an import that bundles a new package, fails until the copies and `tools/release/extra-notices.txt` follow. The errors name each file to copy and each line to write.
- **Types come from the spec.** `src/local/api.ts` binds `createClient` from `src/shared/api/client.ts` to the generated `paths` type. Imports from `src/gen/` are `import type`, so `vite build` works before `npm run gen` has run.
- **The dashboard is tested against a fake agent.** Until WP-AG4's stores fill `GET /v1/days`, the real agent serves empty days, so `tests/dashboard.spec.ts` runs the built dashboard under `vite preview --mode agent` and answers its API from `tests/support/fake-days.ts`: a week of SM 6.3's five-project afternoon. `tests/support/fake.test.ts` holds those answers to the spec's schemas and sums, and `tests/smoke.spec.ts` holds the real agent to the same spec, sign-in included.
- **The words live in `src/local/copy.ts`.** Credited time is "your time", agent time is named after the agent, and uncredited time is described for what it was (DESIGN 3.8). `copy.test.ts` holds them to the spec's kinds, reasons and levels, and fails on streak and shaming words.
- **Bars are SVG geometry.** The CSP allows no `style=` attribute, so a part's width is a `<rect>` attribute and its color a class over the tokens in `base.css`.
- **Decisions use the shared components.** `ConsentCard` (DESIGN 8.4) and `ProfileChoice` (DESIGN 8.3) fix the button labels, which one is prominent, and that nothing is preselected. `tests/consent.spec.ts` and `tests/profile-choice.spec.ts` read those rules from the rendered page's computed styles.

AGENTS.md's "Web UI" section has the rules for changing anything here.
