# Erratum: DESIGN 4.4's retention table has no rows for the hook calls the agent holds, nor for the commit records that wait for the uploader

Date 2026-11-21. Raised by the follow-up to #136 (WP-AG7). Status: decided 2026-11-21 by the orchestrator for Karim (D-held-calls-inbox-class, D-commit-records-local). It follows [2026-11-21-agent-local-stores](2026-11-21-agent-local-stores.md), which proposes the two stores' DESIGN 3.5 rows. DESIGN 4.4 gets the two rows below. `docs/retention.json` takes them in the docs PR that applies this erratum, because `TestDesignPeriods` holds retention.json's periods to DESIGN 4.4's rows, so it can't have them first. Applied to DESIGN 4.4 and docs/retention.json on 2026-11-21 by #160. #160's held-calls row said a call is dropped at the first start after 7 days, rather than kept 7 days at most, since expiry then ran only at a start. #159 made the running agent expire held calls too, so the row now says a call is kept 7 days at most, as proposed below, and that no new call is kept once the ledger can't open until `pairtime restore` runs.

## Input

- DESIGN 4.4: "Every field has a finite retention period, published at `https://<host>/privacy`". Its device rows cover prompt text, transcripts, file paths and commands, presence and focus slots, ledger records, minute buckets and `K_dev`.
- Since #136 the agent keeps two more kinds of data on the device. `held.jsonl`, `held.jsonl.pending` and `held.jsonl.replay` hold hook calls it hasn't parsed yet, whole payloads and in plaintext. A start drops a held call taken in more than 7 days before, none is held once the ledger can't open until a person runs `pairtime restore`, and the files go once drained. `commits/YYYY-MM-DD.jsonl` holds DESIGN 4.3's tier-1 commit records until the uploader sends them, and a day's file goes after 180 days.
- `docs/retention.json` is the one source for how long pairtime keeps each kind of data, which the privacy notice renders (AGENTS.md, Retention).

## Expected

Every kind of data the device keeps has a DESIGN 4.4 row with its period, so `/privacy` can publish it.

## Computed

DESIGN 4.4 has a row for neither. The privacy notice said that pairtime encrypts what it keeps of prompts, and nothing of the calls that wait unencrypted to be parsed. The follow-up adds a sentence about them to `internal/server/pages/privacy.md`.

## The rows

DESIGN 4.4's table gains, after the device's minute buckets:

| Where | Data | Kept |
|---|---|---|
| Device | Hook calls the agent hasn't parsed yet, in `held.jsonl`, its pending log and its replay file: whole payloads, prompts included, unencrypted, in a folder only the teen can read | Until parsed, and 7 days at most: a start drops older ones, and none is kept once the ledger can't open until `pairtime restore` runs |
| Device | Commit records waiting for the uploader, in `commits/` | 180 days |
