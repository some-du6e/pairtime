# Upstream contract captures

pairtime talks to six things it doesn't control: Hackatime's program API, the client code programs already run against it (Stardance's `HackatimeService`), Robert's review desk, Ari's ship ingest, the YSWS handbook's justification fields, and the Airtable base programs submit from. Each directory here pins one of them at a fixed commit or retrieval date, so the code that imitates or feeds them is tested against what they actually do, not against what we remember.

These files are oracles (PLAN ground rule 5). Once merged, a PR that changes them together with code fails `oracle-guard` unless Karim adds the `oracle-change` label. When a capture and the code disagree, the code is wrong or the capture has an erratum (ground rule 6). Don't edit a capture to make a test pass.

| Directory | What it pins | How | Read by |
|---|---|---|---|
| `hackatime/` | hackclub/hackatime at `43bde10b02db` (MIT): the eight read endpoints programs call, `:uid` resolution, the duration SQL | Verbatim excerpts under the MIT notice, plus an endpoint list and response schemas we wrote from them | WP-PG3, WP-PG4, WP-PG5, WP-PG11, WP-AD16 |
| `stardance/` | hackclub/stardance at `b1d3e0eac1ae` (no license): every request its `HackatimeService` makes | Described in our own words, no code | WP-PG3, WP-PG4, WP-PG5 |
| `robert/` | Robert's API and webhook signature, from four public clients | Reconstructed. Robert's own docs sit behind Hack Club sign-in | WP-PG7, WP-PG8 |
| `ari/` | Ari's documented ship ingest keys at `637233962a0f` (no license) | Described, as a JSON Schema for the ship payload | WP-PG8, WP-PG10 |
| `handbook/` | The YSWS Project Submission Guidelines' justification fields, retrieved 2026-11-19 | Field names and rules, described | WP-PG1, WP-PG2 |
| `airtable/` | The `wrangler` base's submission table and the unified base's hour fields (data-warehouse at `4ff995dddcb7`, no license) | Field names only | WP-PG2, WP-PG12 |

## Rules for a capture

- **Interfaces only.** A capture records what a client sends and what a server answers: paths, parameters, field names, shapes, status codes, and the arithmetic behind a number. It never records how to get around a live Hack Club service. Anyone who notices a weakness in one reports it to Hack Club privately and keeps it out of this repo, its issues and its PRs (ground rule 7).
- **Copy only what a license allows.** Hackatime is MIT, so `hackatime/` quotes its code verbatim with the notice in `hackatime/LICENSE`. Everything else either has no license or wasn't worth quoting, so those directories state facts in our own words: names, types, limits, line numbers. They contain no upstream code or prose beyond a field name or a format string.
- **Cite the line.** Every claim points at a file and line range at the pinned revision, so a reader can open the same lines and check.
- **Say when we guessed.** A capture rebuilt from callers instead of the source, like `robert/`, says so in its PROVENANCE.md and in its JSON.

## PROVENANCE.md

Every directory has one. It opens with front matter that `check.py` reads:

```text
---
method: copied
captured_on: 2026-11-19
captured_by: WP-PG0
source: https://github.com/hackclub/hackatime/tree/43bde10b02dbd2371fb4f802e724a1a875ac0dfc MIT
---
```

- `method` is `copied` (some text is verbatim upstream text), `described` (all of it is ours) or `reconstructed` (ours, and inferred from callers because the source isn't public).
- `source` repeats, one line per upstream. A GitHub source is a tree or blob URL at a full 40-character commit, then its SPDX license or `none`. A web page is its URL, its license or `none`, then `retrieved YYYY-MM-DD`.
- `copied` requires a license on every source and the upstream's `LICENSE` file next to the note.

The prose below the front matter lists what was taken from where, by file, line range and git blob SHA, and what was left out.

## JSON files and their schemas

A file named `foo.json` or `foo.<label>.json` validates against `foo.schema.json` in the same directory. Every schema has at least one file to validate, and every value in a schema's `examples` must validate as well.

Schemas are JSON Schema 2020-12, limited to the keywords `check.py` implements: `type`, `enum`, `const`, `properties`, `required`, `additionalProperties`, `minProperties`, `maxProperties`, `items`, `minItems`, `maxItems`, `uniqueItems`, `minLength`, `maxLength`, `pattern`, `minimum`, `maximum`, `allOf`, `anyOf`, `oneOf`, `not`, local `$ref` into `$defs`, and the annotations `title`, `description`, `examples`, `default`, `deprecated`, `format`, `$comment`, `$id`. Any other keyword fails the check. That keeps a schema from relying on a rule nothing enforces. Keywords starting with `x-` are notes, except `x-file-ref: true`, which requires the string to name a file in the same directory or below it. Patterns run through Python's `re` in ASCII mode, so keep them to what Python and ECMA-262 agree on.

Downstream WPs can load the response and payload schemas in their own tests. A schema here lists what upstream always sends as `required` and leaves `additionalProperties` open, so pairtime may add fields, such as the `pairtime` object on stats, without failing them.

## Running the check

```sh
python3 -I testdata/upstream/check.py
```

CI runs it in `ci.yml` as the "Upstream captures" step. It needs nothing beyond the standard library. Besides provenance and schemas, it recomputes every Robert webhook signature in `robert/webhook.json` and runs each verification case through the scheme written there.
