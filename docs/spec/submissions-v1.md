# Submissions API, version 1

Status: the contract between `pairtimed`'s submissions API (WP-SB1, package `internal/server/submission`) and the clients that use it: the agent's submit code and the dashboard's submission page (WP-SB3, WP-SB6), `pairtime submit` for calibration (WP-CA0), and the program API, whose create WP-PG6 adds. The orchestrator decided its rules on 2026-11-24 (D-sb1-submissions; docs/errata/2026-11-24-submissions-retention-and-refusal.md). DESIGN 7.5 steps 1 and 2 say what a create and a freeze do, DESIGN 4.3's submissions row what a client sends, and DESIGN 4.4's submissions row what the server keeps. This file fixes the bytes: the routes, the bodies, the signed message, the refusals, the views, the caps and the list's paging. Section 12 lists what it decides that the rulings leave open. Changing it needs both sides.

A teen submits on the web, with a web session that has a fresh login, or from one of the account's computers, with the device token and a request signed by the device key (2). Either way the create freezes at once. A program's credential reads the submissions addressed to it, once WP-PG6 connects it (2.3).

## 1. Answers

Every answer is JSON, `Content-Type: application/json; charset=utf-8`, with `Cache-Control: no-store` and `X-Pairtime-Time: <the server's clock in Unix ms>`, which a computer corrects its clock by, as it does from ingest's answers (ingest-v1 1). Times in bodies are RFC 3339 in UTC, as Go's `encoding/json` writes a `time.Time`, such as `2026-11-24T12:00:00Z` or `2026-11-24T12:00:00.25Z`. The server keeps them to the millisecond.

A refusal is `{"error": "<code>", "detail": "<text>"}`, where `detail` is an English sentence for logs and people. Three refusals add a member: `device_key_id` on `signature_invalid` (2.2), `login_url` on `fresh_login_required` (2.1) and `submissions` on `already_submitted` (3.3). `rate_limited` and `unavailable` carry a `Retry-After` header. A copy of a refused signed create or withdrawal gets `{"error": "<code>"}` alone (2.2).

| Status | `error` | When |
|---|---|---|
| 400 | `body_shape` | The body isn't one JSON object with exactly the members the route takes, each named once, spelled as 3.1 spells it, not null and of its type, or a withdrawal has a body |
| 400 | `json_invalid` | The body isn't one JSON value in valid UTF-8 |
| 400 | `body_unreadable` | The body couldn't be read, as when the connection broke off; send the same request again |
| 413 | `body_too_large` | The body is over 16,384 bytes |
| 400 | `program_unknown` | A create names a program this server doesn't take submissions for (9) |
| 400 | `request_id_invalid` | `request_id` isn't 1 to 128 letters, digits, `_` or `-` |
| 400 | `project_invalid` | A project id or key isn't 1 to 128 letters, digits, `_` or `-` |
| 400 | `project_repeated` | A project id or key is named twice |
| 400 | `too_many_projects` | More than 32 projects |
| 400 | `project_unknown` | A project id isn't the account's, or a key isn't one the calling computer uploaded |
| 400 | `window_invalid` | A window doesn't start before it ends, ends after the freeze, or starts more than 180 days before the freeze (3.2) |
| 400 | `query_invalid` | A query parameter the route doesn't take, one sent twice, or one of the wrong form (7, 8) |
| 401 | `not_signed_in` | The request carries no credential. The answer carries two `WWW-Authenticate` challenges, `Bearer realm="pairtime"` and `Session realm="pairtime"` |
| 401, 403, 400 | ingest's codes | A computer's request whose token or signed headers don't check out (2.2) |
| 401 | `token_invalid` | Also a program credential that doesn't check out (2.3) |
| 403 | `cross_origin` | A web session's create or withdrawal that doesn't come from this server's own pages (2.1) |
| 403 | `fresh_login_required` | A web session's create or withdrawal without a fresh login made at most 10 minutes before (2.1) |
| 403 | `not_verified` | The account isn't verified by Hack Club Auth, so it can't submit hours (DESIGN 8.2) |
| 403 | `program_not_allowed` | A program's credential tries to create, withdraw or read devlog windows (2.3) |
| 404 | `not_found` | An id the caller may not see, or that the server doesn't hold. It is never 403, so nobody can probe ids |
| 409 | `request_conflict` | The account used this `request_id` for another request (3.3) |
| 409 | `already_submitted` | The overlap refusal (3.3) |
| 409 | `limit_reached` | A create past the account's caps (10) |
| 409 | `no_devlog_windows` | A devlog-mode create whose cut list is empty; send one window instead (3.2) |
| 409 | `not_withdrawable` | A withdrawal once step 9's checks have run (6) |
| 429 | `rate_limited` | A computer past its rate, or an account past the rate of its web session's creates and withdrawals, or either with another such request still being answered; wait `Retry-After` seconds and send it again (2.1, 2.2) |
| 503 | `unavailable` | The server couldn't read or write; every 503 carries `Retry-After: 5` and the request goes again after it |

Other methods on a path get 405 from the router. A refused request stores nothing and draws no `r`.

## 2. Credentials

### 2.1 A web session

The session cookie Hack Club Auth sign-in sets (`__Host-pairtime_session`). It acts for its account. Reads need only the session, and take no rate.

A create or a withdrawal from a web session passes three more checks:

- **The same-origin check** every session write on the server passes, the rule sign-out uses: an `Origin` header equal to `base_url`, or, without one, `Sec-Fetch-Site` of `same-origin` or `none`, or neither header. Otherwise 403 `cross_origin`.
- **A fresh login** made at most 10 minutes before, by the server's clock when the request arrives: a sign-in that went through `/auth/hca/login?fresh=1`, which asks Hack Club Auth for `prompt=login`, and got an `auth_time` from after it started. An ordinary sign-in never counts, however recent its `auth_time`. Otherwise 403 `fresh_login_required`, with `"login_url": "/auth/hca/login?fresh=1"`. The calling page adds `&return_to=` and its own path, which the API can't know, sends the browser there, and sends the request again when it comes back.
- **The account's rate**, the one each computer has: a burst of 30 creates and withdrawals, refilled one every 10 seconds, with no other create or withdrawal of the account's web sessions in flight. Otherwise 429 `rate_limited` with `Retry-After`. It is kept apart from the rates of the account's computers.

A request with no credential at all gets 401 `not_signed_in`.

### 2.2 A computer: the device token and a signed request

Every request from one of the account's computers, reads included, carries the device token and is signed with the device key, like an upload (DESIGN 4.3, 4.5):

```text
Authorization: Bearer ptd_<30+ base62><6-char CRC32>
X-Pairtime-Device: <device_id>
X-Pairtime-Time: <unix ms>
X-Pairtime-Body-SHA256: <hex(SHA-256(raw body))>
X-Pairtime-Signature: ed25519=<base64 Ed25519(device_key, M)>
```

Each header is the one ingest-v1 2.1 describes. There is no `Idempotency-Key` and no request counter (`X-Pairtime-Req`), and the body is plain JSON, with no `Content-Encoding`. The signed message is ingest-v1 2.2's with `pairtime-submissions-v1` as its context and, in place of the request counter, the method, a space and the request target, the path and, when there is one, `?` and the query, exactly as the request line sends them:

```text
M   = "pairtime-submissions-v1\n" || device_id || "\n" || method || " " || target || "\n" || time || "\n" || hex(SHA-256(raw body))
sig = Ed25519(device key, M)                    RFC 8032 section 5.1: PureEdDSA, no context, no prehash
```

`"\n"` is the byte `0x0a`, the hex is lowercase, and `M` has no trailing newline. A GET or a withdrawal signs the SHA-256 of an empty body, `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`. The context differs from ingest's, so no ingest signature passes here and no signature from here passes at ingest. No counter is needed. A copy of a signed create or withdrawal that went through is a retry: the create's `request_id` and the withdrawal's idempotency (3.3, 6) answer it with the stored submission, and it changes nothing. While it runs, the server keeps the status and error code of the refusal each signed create or withdrawal got once the device's rate admitted it, by its device and signature, until the last millisecond in which a copy can pass the clock check: the request's `X-Pairtime-Time` plus 300 s. A copy gets that status and `{"error": "<code>"}` again, looked up at the time of its clock check and before the rate check of step 9, rather than being checked anew. So a copy of a refused create can't freeze `W` once the refusal no longer holds, as after a withdrawal. A copy is a replay, not the client's retry, which signs a new `X-Pairtime-Time` and never meets a kept refusal, so it gets no detail. The store has three limits. A request the rate refused isn't kept, so its copy is checked anew once the rate admits it. A restart forgets every kept refusal. And the store holds at most 10,000, dropping the oldest early when more than that are kept at once, which takes about a hundred devices refused at their full rate.

Worked example, with ingest-v1 2.2's example device key, whose seed is `SHA-256(ascii("pairtime ingest-v1 example device key"))` and public key `fadacf1c88bce8152ec27995a05aa8d9c53ac5678801cdd2e05b19060398a677`. A list request from `d_example` at `1795089660250`:

```text
GET /api/v1/submissions?limit=10

M   = pairtime-submissions-v1\nd_example\nGET /api/v1/submissions?limit=10\n1795089660250\ne3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
    = 7061697274696d652d7375626d697373696f6e732d76310a645f6578616d706c650a474554202f6170692f76312f7375626d697373696f6e733f6c696d69743d31300a313739353038393636303235300a65336230633434323938666331633134396166626634633839393666623932343237616534316534363439623933346361343935393931623738353262383535
sig = daa48605db07ded80b6b13d619a23f0f05eb7e0345447251cdebb3687d109044242c12359c20eae8d94660bce159f08e2a9cc445effdebe2b27153a54c0c8c00
X-Pairtime-Signature: ed25519=2qSGBdsH3tgLaxPWGaI/DwXrfgNFRHJRzeuzaH0QkEQkLBI1nCDq6NlGYLzhWfCOKpzERe/96+KycVOlTAyMAA==
```

`M` is 145 bytes. The package's tests check this example with `crypto/ed25519`.

The server checks a computer's request in ingest-v1 2.5's order, answers the first failure with ingest's code and status, and only then runs the route's own checks:

1. `Authorization` holds a `Bearer` token: otherwise 401 `token_missing`. The token names a device: otherwise 401 `token_invalid`. The device isn't removed and its account can still sign in: otherwise 403 `device_revoked`. A device store that fails gets 503 `unavailable`.
2. `X-Pairtime-Device` is there once and is the token's device: otherwise 403 `device_mismatch`.
3. The device's row holds the key its sign-in bound: otherwise 403 `device_key_missing`.
4. `X-Pairtime-Time`, `X-Pairtime-Body-SHA256` and `X-Pairtime-Signature` are each there once and in ingest-v1 2.1's form: otherwise 400 `time_malformed`, `body_hash_malformed` or `signature_malformed`, in that order.
5. `X-Pairtime-Time` is within 300,000 ms of the server's clock, either way: otherwise 401 `clock_skew`. The answer's `X-Pairtime-Time` is the clock to correct by.
6. The body is at most 16,384 bytes and arrives within 60 s: otherwise 413 `body_too_large`, or 400 `body_unreadable`.
7. `hex(SHA-256(raw body))` is `X-Pairtime-Body-SHA256`: otherwise 400 `body_hash_mismatch`.
8. The signature verifies over `M` under the device's key: otherwise 401 `signature_invalid`, whose body adds `device_key_id`, the key's id in ingest-v1 2.8's `key_id` form.
9. The device is within its rate, a burst of 30 requests refilled one every 10 seconds, with no other request of its in flight: otherwise 429 `rate_limited` with `Retry-After`. The rate is kept apart from the device's uploads and its devlog requests, and only a request whose signature verifies counts against it, so nobody without the device's key can use it up.

### 2.3 A program

A program's credential arrives with WP-PG6, which connects its client-credentials check to the API's program hook. Until then no request in production is a program's. A program's request reads the program view of the submissions addressed to it (4.2, 5, 7). In this version it can't create, withdraw or read devlog windows: 403 `program_not_allowed`. A credential that doesn't check out gets 401 `token_invalid`. WP-PG6 adds program-made creates, which stay `created` until the teen freezes them on the web with a fresh login.

## 3. `POST /api/v1/submissions`

### 3.1 The body

```json
{
  "program": "hard-mode",
  "request_id": "<key>",
  "project_keys": ["<key>"],
  "window": {"start": "2026-11-17T00:00:00Z", "end": "2026-11-18T00:00:00Z"}
}
```

| Member | Meaning |
|---|---|
| `program` | A program `GET /api/v1/programs` lists (9), or calibration's, which takes no hours (WP-CA0) |
| `request_id` | A random key the client made for this create: 1 to 128 letters, digits, `_` or `-`, as devlog ids are. A retry sends the same one. A client keeps it until it gets an answer |
| `projects` | The account's project ids, as the dashboard lists them |
| `project_keys` | The calling computer's own project keys, as its uploads name them. Only a computer sends them; from a web session they get 400 `body_shape` |
| `window` | One window, `{"start", "end"}` |
| `devlog_windows` | `true`: the list of devlog windows the server works out (3.2) |

The body has exactly one of `projects` and `project_keys`, with 1 to 32 entries and none twice, and exactly one of `window` and `devlog_windows`. It has no other member: this version takes no `program_project`, code URL, demo URL or claimed hours.

Either form names projects. A key stands for the project its `rollup_projects` row names when the create runs, so either form freezes every (device id, project key) pair of the projects it names, on every computer of the account, never the calling computer's alone (D-sb1-submissions, Q5 and Q17).

### 3.2 Windows

The freeze's time is the server's clock inside the writer op that stores the submission.

**One window.** It starts before it ends, ends no later than the freeze, and starts no earlier than the freeze minus 180 days, the submission horizon, which is `server.minute_buckets`' days in docs/retention.json. Otherwise 400 `window_invalid`. A window isn't bounded by earlier submissions' windows, only by the overlap refusal (3.3). A freeze before the window's minutes are final is fine, since the re-score runs to the freeze (DESIGN 6.5).

**Devlog windows** (DESIGN 6.2 D13). `notBefore` is the latest end of any window frozen by one of the account's submissions that holds one of P's pairs, wasn't withdrawn, and went to a program that takes hours, or 0 when there is none. A decided or revoked submission still counts. The list is the time between each two consecutive devlogs of any of P's projects, merged and in order of when the server received each, from `notBefore`. The first window starts at `notBefore` when it is above 0, and at the first devlog after it when it isn't. So deleting or publishing a devlog after a freeze never puts submitted time in a new window. The horizon then cuts the list: a window that ends at or before the freeze minus 180 days is left out, and one that starts before it starts at it. A devlog-mode create freezes the whole cut list. When the cut list is empty it gets 409 `no_devlog_windows`, and the client sends one window instead, which is D13's "one window if the teen has none". `GET /api/v1/submissions/devlog-windows` shows the list a create would freeze (8).

Only each window's start and end are frozen, never its devlogs, so a devlog deleted or published later moves no frozen window.

### 3.3 The checks, the freeze and the answer

The server checks the body's form (3.1) before anything else. Then one writer op runs these steps in order, so two creates never race:

1. **Idempotency on the account and `request_id`.** When the account made a submission under this `request_id` before, the same canonical request gets 200 with that submission in whatever state it is in now, withdrawn included, and nothing changes, even when `[[programs]]` no longer lists its program, since a retry isn't a new submission. Another request under the same id gets 409 `request_conflict`. The canonical request is the program, the sorted project ids, or the sorted keys with the calling device's id (keys are a device's own), and the window's start and end in Unix ms or the choice of devlog windows. Request ids are the account's own, so no answer says anything about another account.
2. **The program.** `[[programs]]` lists it: otherwise 400 `program_unknown`.
3. **Verification.** The account's `users.verification_status` is `verified`: otherwise 403 `not_verified`.
4. **The submission cap.** The account holds fewer than 1,000 submissions made with its own credentials: otherwise 409 `limit_reached` (10).
5. **P.** The projects or keys resolve to P's project ids and their pairs: otherwise 400 `project_unknown`.
6. **The windows** (3.2): 400 `window_invalid` or 409 `no_devlog_windows`.
7. **The window cap.** The account's frozen windows and this create's together are at most 10,000: otherwise 409 `limit_reached` (10).
8. **The overlap refusal**, for a program that takes hours (D-sb1-submissions, Q2). The create is refused when any of its windows overlaps a window of another submission of the account that holds one of the same pairs, was frozen and wasn't withdrawn, in any state from `frozen` through `decided`, or `revoked`, and went to a program that takes hours, whichever program, the create's own included. Two windows overlap when each starts before the other ends, so windows that only touch at an end don't. A withdrawal frees a submission's windows, and a decision, whatever its outcome, or a revocation doesn't, so a rejected claim can't move to another program. A submission to a program that takes no hours is never refused and never counts against another. The answer is 409 `already_submitted`, naming every submission it overlaps:

   ```json
   {"error": "already_submitted", "detail": "...", "submissions": [{"id": "<id>", "program": "<name>"}]}
   ```

   A program's credential, once WP-PG6 lets one create, gets the same status, error and detail with no `submissions` member.

9. **The freeze.** The server stores the submission with P, each pair it maps to now, and the windows; the scoring version and parameter hash (11.3); a fresh 32-byte `r` and its SHA-256; and the eligibility note, true when Hack Club Auth verified the identity without saying it is eligible (DESIGN 8.2). It moves the submission from `created` to `frozen` and, for a program that takes hours, raises the freeze's risk flags (11.2).

A new submission gets 201 with its teen view (4.1) and `Location: /api/v1/submissions/<id>`. It carries `r_sha256` and never `r`. When the writer's answer is lost, as when the connection drops while it commits, the client sends the same request with the same `request_id`, which gets the stored submission.

## 4. The views

### 4.1 The teen view

What the account sees of its own submission, on the web and on each of its computers:

| Member | Meaning |
|---|---|
| `id` | The submission's id, a UUIDv7 |
| `program` | `{"name", "display_name"}`: the program and the name teens see, from `[[programs]]`, or its name again for a program the list no longer holds |
| `takes_hours` | Whether the program took hours when the submission was made |
| `state` | One of `created`, `frozen`, `metadata_revealed`, `challenged`, `content_revealed`, `checked`, `in_review`, `decided`, `withdrawn`, `revoked` (DESIGN 7.5) |
| `request_id` | The client's id of a teen-made submission, or `null` |
| `external_id` | The program's id of a program-made one (WP-PG6), or `null` |
| `created_by` | `"session"` or `"device"` |
| `device_id` | The computer that made it, or `null` for a web session |
| `projects` | P: `[{"id", "keys": [{"device_id", "project_key"}]}]`, each project with the pairs it mapped to at the freeze, which WP-SB3's reveal set reads |
| `mode` | `"window"` or `"devlog_windows"` |
| `windows` | The frozen windows, in order, each `{"start", "end"}` |
| `scoring_version`, `params` | The scoring version and parameter hash the freeze froze, or `null` before it |
| `r_sha256` | The lowercase hex of `SHA-256(r)`, or `null` before the freeze |
| `r` | `r` in lowercase hex, only once `challenged_at` is set (4.3). Before then the member isn't there |
| `eligibility_to_confirm` | The eligibility note, as the freeze found the account |
| `created_at`, `updated_at` | When it was made and last changed (7) |
| `frozen_at`, `metadata_revealed_at`, `challenged_at`, `decided_at`, `withdrawn_at`, `revoked_at` | When it last reached each of those states, or `null` |
| `review_url` | `<base_url>/review/submissions/<id>`, which tier 0 pastes into a program's form |
| `totals` | `null` until WP-SB4's re-score fills it |

No teen view holds a risk flag.

### 4.2 The program view

What a program sees of a submission addressed to it (D-sb1-submissions, Q13): `id`, `external_id`, `state`, `user`, which is `{"pairtime_id", "hca_sub", "slack_id"}` with `slack_id` `null` when Hack Club Auth sent none, `windows`, `scoring_version`, `params`, `eligibility_to_confirm`, the eight times, `review_url` and `totals`. It never holds flags, projects or keys, the credential or computer that made the submission, `request_id`, `r` or `r_sha256`, or anything about another submission. A submission to a program that takes no hours has no program view.

### 4.3 `r`

`r` is 32 bytes from `crypto/rand`, drawn at the freeze (DESIGN 7.5 step 2). Nothing shows it until the draw is stored (D-sb1-submissions, Q9). WP-SB5 stores the draw and moves the submission from `metadata_revealed` to `challenged` in one writer transaction, which sets `challenged_at`, and it reads `r` for the seed inside that op only. From then on the teen view carries `r`, WP-SB5's challenge endpoint may return it, and WP-SB8's bundle holds it. A submission withdrawn before its draw never shows `r`, and the program view never does. A client that could read `r` earlier could work out the draw and withdraw before it is recorded.

## 5. `GET /api/v1/submissions/{id}`

The account's own submission in the teen view, from any of its credentials, or, for a program's credential, a submission addressed to that program in the program view. Any other id gets 404 `not_found`.

## 6. `POST /api/v1/submissions/{id}/withdraw`

A withdrawal has no body; any body gets 400 `body_shape`. A computer signs it with the SHA-256 of an empty body. A web session needs the same-origin check and a fresh login (2.1), and a program's credential can't withdraw (2.3).

Only the account withdraws (D-sb1-submissions, Q7). From `created`, `frozen`, `metadata_revealed`, `challenged` or `content_revealed`, the server moves the submission to `withdrawn`, records `withdrawn_at` and answers 200 with the teen view. The withdrawal deletes nothing (DESIGN 4.4) and frees the submission's windows (3.3). From `checked` on, once step 9's checks, risk score and audit draw have run, and in `in_review`, `decided` and `revoked`, it gets 409 `not_withdrawable`, so nobody can re-roll an audit or a reviewer by submitting the same time again. A submission already `withdrawn` gets 200 with it unchanged. Another account's submission gets 404.

## 7. `GET /api/v1/submissions`

```text
GET /api/v1/submissions?updated_since=<RFC 3339>&limit=<1 to 100>&cursor=<next_cursor>
```

Every parameter is optional and comes at most once, and the route takes no other: otherwise 400 `query_invalid`. `updated_since` defaults to the start of time and `limit` to 100.

The account's own credential lists the account's submissions in the teen view. A program's credential lists the submissions addressed to that program in the program view, none of them to a program that takes no hours. Either way the list holds the submissions whose `updated_at` is at or after `updated_since`, ordered by `updated_at` and then `id`, at most `limit` of them:

```json
{"submissions": [ ... ], "next_cursor": "<opaque>"}
```

`next_cursor` is `null` on the last page. Sending it back with the same query gives the next page.

`updated_at` moves at every change of state, and at every later change a program can see, such as WP-SB4's totals. The server's one writer sets it from its clock inside the op that makes the change, so commits come in the order of their times. A client that keeps up asks again from the last `updated_at` it saw, inclusive, and drops what it already has by `id` and `updated_at`.

## 8. `GET /api/v1/submissions/devlog-windows`

```text
GET /api/v1/submissions/devlog-windows?project=<id>&project=<id>
GET /api/v1/submissions/devlog-windows?project_key=<key>
```

The list of devlog windows a devlog-mode create of those projects would freeze now, and the bound it starts from, worked out exactly as a create works them out (3.2), for WP-SB6's preview. The projects come as `project`, the account's ids, or, from a computer only, as `project_key`, its own keys, never both, with the limits of 3.1. The answer:

```json
{"not_before": "2026-11-18T12:00:00Z", "horizon": "2026-05-28T12:00:00Z", "windows": [{"start": "...", "end": "..."}]}
```

`not_before` is `null` when no earlier frozen window bounds the list, and `horizon` is the freeze's horizon at the server's clock now. Refusals are a create's: 400 `query_invalid` for anything but those parameters or for keys from a web session, the project codes of 1, and 403 `program_not_allowed` for a program's credential.

## 9. `GET /api/v1/programs`

```json
{"programs": [{"name": "hard-mode", "display_name": "Hard Mode"}]}
```

Each program this server takes submissions for whose submissions claim hours, in the order pairtime.toml's `[[programs]]` lists them (DESIGN 10.2), for a program picker. It needs no credential. Calibration's program takes no hours, so it isn't listed, and `pairtime submit --program calibration` names it.

## 10. Caps and limits

The record of a submission stays as long as the account (DESIGN 4.4), so the server caps what one account can hold, as it caps devlogs (#283), so that one device token can't fill the server's disk:

| Limit | Value | Refusal |
|---|---|---|
| Submissions made with the account's own credentials, a web session's or its computers', withdrawn ones included | 1,000 | 409 `limit_reached`. A retry of a create the server holds still gets 200 |
| Frozen windows across all of the account's submissions | 10,000 | 409 `limit_reached` |
| Projects in one create or preview | 32 | 400 `too_many_projects` |
| A body | 16,384 bytes | 413 `body_too_large` |
| A page of the list | 100 | 400 `query_invalid` for a larger `limit` |
| A computer's requests | a burst of 30, one more every 10 seconds, one at a time | 429 `rate_limited` |
| An account's creates and withdrawals from a web session | a burst of 30, one more every 10 seconds, one at a time | 429 `rate_limited` |

Submissions a program makes for the account (WP-PG6) don't count toward the 1,000. WP-PG6 sets a cap of its own on them.

## 11. On the server

### 11.1 What it keeps

Migration 0013 creates `submissions`, `submission_projects`, `submission_keys`, `submission_windows` and `submission_events`, which docs/retention.json's `server.submissions` keeps while the account exists, and `submission_flags`, a window job of `evidence.revealed` on `decided_at_ms` that stays off until WP-SV18. A withdrawal deletes nothing. The eraser deletes every row of the account's submissions through `submission.DeleteAccount`, and the privacy hub's export counts these tables (WP-SV13). Each flag carries `user_id`, the account its submission is of, and the eraser deletes and counts the account's flags by it. `DeleteAccount` also takes the account's submission ids out of other accounts' `repo_overlap` flags and deletes a flag left naming none. An erasure that would leave a flag of any account on one of the account's submissions, or naming one, fails and changes nothing (D-sb1-flags-erasure). No report gives `submission_flags` a line of its own, whether the delete's, a dry run's or the export's. A flag is a signal for reviewers alone (4.1, 11.2), and a count of them that moved would tell a teen that another account's freeze flagged their submission, which a cheater could use to probe the detection. The `submissions` line stands for the flags: it counts the account's submissions, held or deleted with their review data, and the export says so in its reason. The other tables keep their counts.

### 11.2 The freeze's flags

For a program that takes hours, the freeze raises two risk flags, which reviewers see and no teen or program view shows:

- `concurrent_devices`, `{"minutes": n}`: the minutes the frozen windows touch in which two of the account's devices credited one slot and at least one of them credited it to one of P's projects, read from the per-device minutes `rollup_minutes` keeps (SM 3.4; D-sb1-submissions, Q8). A minute two windows touch counts once, and a slot that only other projects competed for never counts. The server stores the flag only when `n` is above 0. It counts on its read pool just before the freeze's writer op, over the windows the op is expected to freeze, and the op takes that count when its own windows touch the same minutes for the same projects, and counts again inside itself when they don't. So the writer, which takes every account's writes in turn, doesn't read up to 180 days of every device's minutes.
- `repo_overlap`, `{"submissions": [ids]}`: the submissions of other accounts, frozen, not withdrawn and to a program that takes hours, that hold a pair whose repo fingerprint, `HMAC(pepper, root_sha)`, one of P's projects shares, and whose windows overlap the new one's (SP 10.2; Q11). Each of those submissions that is still undecided gets the flag too, naming the new one. Fingerprints come through the `Fingerprints` interface, which finds none until WP-SV10 stores them.

### 11.3 The scoring version

A freeze records the scoring version the server runs, pairtime.toml's `scoring_version` or the built-in scorer's when that is empty, and the `Hash()` of the newest table `pkg/params` embeds whose version is at or below it (D-sb1-submissions, Q10). Both are written once, at the freeze. `pairtimed` refuses to start on a `scoring_version` newer than its built-in scorer's, or, while the built-in is 0.x, on one with another MINOR, since a 0.x MINOR can change a rule (DESIGN 6.6) and the server couldn't re-score at it.

### 11.4 What later work packages call

Every change of state goes through `submission.Move`, one step at a time along DESIGN 7.5's chain. `submission.Touch` moves `updated_at` for a change a program sees that isn't a move. The freeze calls `OnFreeze` in its own writer op, through which WP-SB4 writes its holds, and a withdrawal calls `OnWithdraw` in its op, through which WP-SB5 marks drawn items withheld. WP-SB5 reads `r` through `submission.DrawR` inside the op that stores the draw.

## 12. Decisions this spec makes

The rulings of D-sb1-submissions leave these open, and this spec settles them:

1. A body is at most 16,384 bytes and names at most 32 projects. A create needs no more.
2. The codes `request_id_invalid`, `project_invalid`, `project_repeated`, `too_many_projects`, `query_invalid` and `program_not_allowed`, and a 400 `body_shape` for keys from a web session.
3. `created_by` is `"session"` or `"device"`. WP-PG6 adds its own value.
4. The canonical request of 3.3 step 1 includes the calling device's id when it names keys, since keys are a device's own, so two computers' keys with the same text are two requests.
5. Every answer carries `X-Pairtime-Time`, as ingest's do, so a computer can correct its clock from any of them.
6. A request with no credential gets 401 `not_signed_in` with both challenges, since the routes take a web session or a device token.
7. The list's cursor is opaque, and `updated_since` is inclusive.
8. The preview's path, `/api/v1/submissions/devlog-windows`, and its answer.
9. `GET /api/v1/programs` needs no credential: program names aren't secret, and the picker needs them before anything else.
10. A web session's creates and withdrawals take a rate per account, the one each computer has, so one signed-in account can't fill the writer every account shares. Reads take none.
11. A body names each member once and spells it exactly, as ingest's bodies do, and sends none as null, which ingest's body reads as left out (ingest-v1 2.3) but a create refuses, since it sends exactly one of each pair. encoding/json alone takes a name in any letter case, keeps the last of a repeated one and reads a null as a member left out.
12. The teen view's members (4.1). The rulings fix the program view's (Q13), and the teen view shows the account its own submission, with `r` once challenged (Q9).
13. A new submission gets 201 with `Location`, where the rulings fix only a retry's 200 (Q5).
14. The order of 3.3 steps 2 to 5. The program, the verification and the submission cap come after the idempotency check, so a retry of a create to a program `[[programs]]` no longer lists, or from an account no longer verified, still gets its stored submission. The cap comes before P resolves, so a create past it reads nothing more.
15. `review_url` is `<base_url>/review/submissions/<id>`, the review page's path in DESIGN 3.10 and 7.7.
16. A copy of a refused signed create or withdrawal gets the first refusal's status and error code again while the copy passes the clock check (2.2), because a copy of a refused create could otherwise go through once its refusal no longer holds. Only refusals of requests the device's rate admitted are kept, and only their status and code: a copy of a request that went through is a retry, which idempotency answers. And should a write between the read that sizes a create and its writer op lift a refusal the read found (an unlisted program, an unverified account, an unknown project or an empty devlog list), the create gets 503 `unavailable` and goes again, rather than write past the one row it declared. A window the read finds wrong declares its full rows, since whether it has ended depends on the clock, and the op decides it with its own.
