# Erratum: the device keeps a record of its past uploads, which DESIGN 3.5 and 4.4 don't list

Date 2026-11-21. Raised by the review of #131 (WP-AG5) in issue #150. Status: decided 2026-11-21 by the orchestrator for Karim (#150 ruling R6): the uploader keeps a bounded local log of each answered request, 90 days, with the body's SHA-256 and never the body. This erratum proposes its rows in DESIGN 3.5 and 4.4. Applied to DESIGN 3.5 and 4.4 on 2026-11-21 by #NNN. The Privacy page that lists the log stays a WP-UI1 follow-up.

## Input

- PLAN's WP-AG5 Build: "`pairtime privacy show` prints the next upload byte for byte, and the dashboard's Privacy page lists every past upload in plain words (DESIGN 8.8)".
- DESIGN 3.5's table lists every file the client keeps, and DESIGN 4.4's gives every kind of data a finite retention period.
- Before R6, nothing on the device recorded a past upload. The request's body went from `outbox.json` once its answer came, and the uploader's `Status` kept only the last success and failure. A page built later couldn't list the uploads sent before it existed.

## Decision

From the first upload on, the uploader appends one JSON line to `uploads.jsonl` in the data directory for each request pairtimed answered, whether it took the request or refused it as a whole:

- when the answer came, and the server the request went to;
- how many batch roots, minute bucket revisions and commit records it carried, and whether it carried the labels;
- the answer's HTTP status, and its result codes with how many items got each, or the refusal's error code;
- whether the answer was the server's replay of a request it had already taken;
- the SHA-256 of the request's JSON body before gzip, which is the body `pairtime privacy show` printed for it. The body itself is never kept.

Every answer that is pairtimed's counts, a 429 or a 5xx included, and a refusal the uploader tries again at once, as after `clock_skew`. A failure that didn't reach pairtimed isn't an answer: the ledger's upload-failure notes (rtype 0x41) record those. Entries older than 90 days go, checked once a day whether answers come or not, and the file keeps at most 200,000 entries, past which the oldest go. A line a crash cut short is passed over and goes at the next pruning. The file is readable by its user only, holds only numbers, codes, times, a URL and hashes (DESIGN 4.3's kind of data), and never leaves the device. `pairtime uninstall` deletes it with the rest of the data directory.

## Proposed rows

DESIGN 3.5, after `state.json`:

| Path | Format | Holds | Kept |
|---|---|---|---|
| `uploads.jsonl` | JSONL, appended | Each upload pairtimed answered: when, to which server, how many batch roots, minute bucket revisions and commit records it carried and whether it carried the labels, the result codes, and the SHA-256 of the body `pairtime privacy show` printed, never the body | 90 days |
| `outbox.json` | JSON, atomic rename | The body of the request that waits for its retry, as `pairtime privacy show` printed it, so the retry sends the same bytes with the same `Idempotency-Key` | Until its answer comes |

`outbox.json` is the uploader's retry file from the first version of #131; DESIGN 3.5 doesn't list it either.

DESIGN 4.4, after the device's minute buckets:

| Where | Data | Kept |
|---|---|---|
| Device | Records of past uploads: when, where, counts of what went, result codes, body hashes | 90 days |

Once DESIGN carries the 4.4 row, `docs/retention.json` gains a `device.uploads` entry, which `/privacy` renders, with its case in `TestDesignPeriods`. A code PR can't add the entry first, because that test checks every period against DESIGN 4.4's own row.

## What the code does

`internal/agent/uploader/uploads.go`: `send` and `answered` append a `PastUpload` to `uploads.jsonl` for each of pairtimed's answers, and `pruneUploads` drops the entries older than 90 days, at the uploader loop's first wake and once a day after. `PastUploads` reads them back for the Privacy page. `TestPastUploads` checks that an answered request is kept with its counts, its codes and its body's SHA-256, that no bucket or root of its body is in the file, that a refusal is kept too, and that entries older than 90 days and torn lines go, with no answer needed. `cmd/pairtime/uninstall.go` lists `uploads.jsonl` and `outbox.json` among pairtime's entries, with `account.json` and login's lock directory, so an uninstall from a data directory that is a link deletes them too.
