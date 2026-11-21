---
method: described
captured_on: 2026-11-19
captured_by: WP-PG0
source: https://github.com/hackclub/ari/tree/637233962a0f391c65fa1fcbe666a5b98ea10eae none
source: https://github.com/hackclub/ari-webhooks/tree/01a47d67aa91ee83f5db60bc69642ac53c05d7e1 none
---

# Ari's ship ingest at 637233962a0f

Neither hackclub/ari nor hackclub/ari-webhooks has a license in its repository metadata (HC OQ 7), so nothing here is Ari's text. `ingest.schema.json` is a JSON Schema we wrote for the ship payload programs send to `POST /api/ingest/:program`, with the names, types and limits in our own words.

The keys are the ones Ari documents for programs, in `src/routes/docs/webhooks/inboundFields.ts` at the pinned ari commit. The limits and the 422 field names come from the validator that enforces them, `internal/ingest/validate.go` and `payload.go` in ari-webhooks at `01a47d67aa91`, the commit HC 3.6 read. The two agree, and where the validator is more precise, the schema follows the validator. How journal entries become cuttable rows comes from ari's `src/lib/review/settlement.ts`.

| File | What it is |
|---|---|
| `ingest.schema.json` | The ship payload. `#/$defs/journal_entry` is one `journals[]` entry on its own |
| `ingest.example-pairtime-journals.json` | A ship as pairtime's Ari bridge would send it: journals only, no Hackatime projects, no program time |
| `ingest.example-hackatime.json` | An ordinary Hackatime ship, for contrast |

WP-PG8 validates its bridge's output against `ingest.schema.json`. WP-PG10 maps pairtime rows onto Ari's time sources from the same facts.

## What a pairtime bridge has to send

DESIGN 5.10's bridge sends one entry per row of a checked submission, with `at`, `seconds`, `text` and `markdown`, and Ari's rules shape it. `text` is required, and Ari answers 422 `journals.text` when it is blank; `markdown` only falls back to `text`. `seconds` must be a whole number, so the bridge rounds pairtime's half-second credit down. An entry holds at most 86,400 seconds, so a longer row is cut at each UTC midnight. A ship holds at most 200 entries, so past that the bridge sends one entry per person per UTC day. Ari adds up every entry, so the bridge's entries are the only ones that carry seconds. `#/$defs/journal_entry`'s `x-invalid-examples` pin the 86,400-second cap and the whole seconds. Ari takes a ship under the same `external_id` again only once the open one is decided or withdrawn, and answers 409 `already_queued` until then. `docs/errata/2026-11-20-ari-journal-entries.md` has the reasoning.

## Files read

Fetched with `gh api -H "Accept: application/vnd.github.raw" "repos/hackclub/<repo>/contents/<path>?ref=<commit>"` on 2026-11-19; `process.go` and `store.go` the same way on 2026-11-20, at the same commit. Blob ids are `git hash-object` of what came back.

| Repo | File | Git blob | Used for |
|---|---|---|---|
| hackclub/ari | `src/routes/docs/webhooks/inboundFields.ts` | `22e0f65478b5` | the documented keys, types and requirements |
| hackclub/ari | `src/routes/docs/webhooks/sections/SendingSections.svelte` | `936a971cfda7` | context for the ingest endpoint |
| hackclub/ari | `src/routes/docs/webhooks/sections/FieldsSections.svelte` | `5debc57537e4` | context |
| hackclub/ari | `src/routes/docs/webhooks/examples.ts`, `rows.ts` | `338ae3344f38`, `a3701bd25d81` | context; nothing taken |
| hackclub/ari | `src/lib/review/settlement.ts` | `c2c22dc40a7d` | the four time sources, and one cuttable row per journal entry, all summed into the journals total (lines 3-4, 135-153, 170) |
| hackclub/ari-webhooks | `internal/ingest/validate.go` | `e2b45a55b9f5` | requirements, limits and 422 field names (lines 18-357) |
| hackclub/ari-webhooks | `internal/ingest/payload.go` | `2f73aa644708` | URL and date parsing, program time precedence (lines 54-143) |
| hackclub/ari-webhooks | `internal/ingest/process.go` | `e199ab5b085a` | 409 `already_queued` for a ship sent under the `external_id` of an open one (lines 103-105) |
| hackclub/ari-webhooks | `internal/ingest/store.go` | `61f755805ab4` | which ships count as open (`findOpenShip`, lines 90-102) |
| hackclub/ari-webhooks | `README.md` | `bb6dec6e4589` | context |

## Where the schema is stricter than Ari

So that anything valid here is valid at Ari, the schema takes the spelling Ari tells new integrations to use where Ari accepts several:

- `track` must be exactly `hardware` to waive `demo_url`. Ari also takes other casing and surrounding spaces.
- The evidence floor counts program time only as a solo ship's `maker.program_seconds`. Ari also counts `program_minutes` and `program_hours`, and on a ship that lists collaborators it counts their program time and their own `hackatime_projects` instead, never the maker's program time.
- The floor counts a `hackatime_projects` list only when every name in it has a character that isn't a space. Ari drops the blank names and counts the rest.
- A journal entry's `at` must be a string. Ari falls back to `date` when `at` is null.
- A non-numeric `minutes` fails here. Ari skips it and tries `hours`.

## What the schema can't check

A ship valid against `ingest.schema.json` is valid at Ari when it also keeps these four rules, which no schema in `check.py`'s subset can state:

1. No two collaborators share an email, ignoring case and surrounding spaces, or Ari answers 422 `collaborators` (validate.go lines 171-174).
2. On a ship that lists collaborators, every journal entry's `email` names one of them, again ignoring case and surrounding spaces, or Ari answers 422 `journals.email` (lines 247-257).
3. `at`, `date` and `shipped_at` name a date and time that exist. The `when` pattern checks only their shape, so it takes `2026-02-30` or `25:00`, which Ari's parser refuses with 422 `journals.at` or `shipped_at` (payload.go lines 59-78). `shipped_at` must also fall between 2000-01-01 and a day after Ari receives the ship (payload.go lines 97-102).
4. A string that mustn't be blank has a character that isn't whitespace in Unicode's sense. Ari tests with Go's `strings.TrimSpace`, which also strips characters such as U+00A0, while `check.py` runs `nonblank`'s `\S` in ASCII mode. A title made only of U+00A0 passes here and is blank to Ari, and the same goes for the floor's Hackatime names.

For WP-PG8's bridge, 3 and 4 hold if it formats `at` from a real timestamp and writes its own `text`. 1 and 2 matter only if it sends collaborators.

The evidence floor's `examples` and `x-invalid-examples` pin both sides of it: a solo ship's program time counts and a shared ship's maker time doesn't, and a list of blank names is no Hackatime project.
