# Erratum: DESIGN 4.4 has no row for the submission record, and 7.5 step 1 refuses less than 9.2 and 9.7

Date 2026-11-24. Raised by the plan for WP-SB1, the submissions API. Status: decided 2026-11-24 by the orchestrator for Karim (D-sb1-submissions), and amended by the orchestrator the same day on #342. Applied to DESIGN 4.3, 4.4, 6.2 D13, 7.5 steps 1, 2, 5, 8 and 9, 7.9 and 10.2, and to PLAN, in a docs PR of its own; docs/retention.json, the retention tests, the privacy notice, the config and docs/spec/submissions-v1.md follow in WP-SB1.

## Input

DESIGN.md line numbers here are main's at 55152597, where this erratum was raised.

- WP-SB1 (PLAN, WP-SB1's Build) stores each submission:
  - the program and the `external_id` that `POST /api/v1/submissions` is idempotent on;
  - the projects `P`, and the project keys they named at the freeze;
  - each frozen window's start and end;
  - the scoring version and the parameter hash;
  - the fresh 32-byte `r` and `SHA-256(r)`;
  - the eligibility note;
  - the state, with the time of each change, the freeze and the withdrawal included, which DESIGN 4.4 needs to open the spot-check window.
- DESIGN 4.4's table (DESIGN.md:450-483) has no row for this record:
  - The evidence store's row (DESIGN.md:478) holds revealed metadata and content and the risk flags, until the spot-check window closes.
  - The decisions' row (DESIGN.md:479) holds decisions "written without personal data".
  - The minute-bucket and witness rows keep what a submission uses, as holds.
- docs/retention.json's entries are DESIGN 4.4's rows (internal/server/retention/policy.go: "Entries are DESIGN 4.4's rows, in its order"), `TestTables` needs every table placed, and `TestDesignPeriods` ties each period to a 4.4 row. So migration 0013's tables can't be placed. The record can't go under the evidence store's row either: AGENTS.md's retention rules keep a submission's totals and decisions out of `evidence.revealed`, because they "must outlive the evidence".
- DESIGN 7.5 step 1 (DESIGN.md:915) says "the server rejects a second submission of the same project and window to another program". DESIGN 9.2 (DESIGN.md:1143) says "the server rejects overlapping submissions (EV 8.3)", and 9.7's row 3.1 (DESIGN.md:1313) lists "overlap rejection". docs/errata/2026-11-23-build-days-devlogs-retention.md left this to WP-SB1: "This matters for explicit windows whatever devlogs do, so WP-SB1 raises it if the gap still stands." It still stands.
- D13's latest-end bound already keeps a list of devlog windows off time frozen by any submission on the project that wasn't withdrawn, whatever the program. So explicit windows are the only way left to submit the same time twice.

## Expected

- Every kind of data the server keeps has a 4.4 row and a finite period.
- A second payout for the same hours is refused the same way, whichever section a reader follows.

## Options

### Q1: the submission record's 4.4 row

- **(a), proposed:** a Server row before the evidence store's row, kept for the account's lifetime. Its suggested text:

  | Server | Submissions: the program and external_id; the projects P, the project keys they named at the freeze, and each frozen window's start and end; the scoring version and parameter hash; r and SHA-256(r); the eligibility note; the state and the time of each change | Account lifetime; a decision's text stays as its own row says |

  docs/retention.json then gets an entry `server.submissions`, with no period, listing plain tables. The flags the freeze raises, such as `concurrent_devices`, are risk flags, so they go under the evidence store's row as a window job of `evidence.revealed` on `decided_at_ms`, with a `submission_id` column. The account lifetime is finite, because the eraser deletes the rows (DESIGN 8.9), as for build days and commit records.
- **(b):** keep the record only as long as its decision, under the decisions' row. That row is written without personal data, and the record names its account, so (b) needs the decisions' row reworded too.

### Q2: what the refusal refuses

- **(a), proposed:** refuse, with `409 already_submitted`, any submission whose windows overlap in time a window of a frozen submission of the same account that wasn't withdrawn, on any (device, project key) pair the two share, whatever the program. Decided and revoked submissions count. A withdrawal frees its time, as D13's bound does. 7.5 step 1's sentence changes to match 9.2 and 9.7.
- **(b):** the literal reading of 7.5 step 1: refuse only a second submission of the same project and the same window to another program, and change 9.2 and 9.7 to match. Overlapping windows, and the same window to the same program, would then go through.

## Decision

(a) The submission record. A submission is server data kept for the account's lifetime, in a 4.4 row of its own after the row for wrapped `K_dev` and encrypted backups. The row lists what the record holds: the account and the program, whether that program takes hours, the program's `external_id` or the random id the teen's client made with a hash of the request, which credential and which computer made it, the projects `P` and each device id and project key they mapped to at the freeze, whether `W` is one window or devlog windows and each frozen window's start and end, the scoring version and parameter hash, `r` and `SHA-256(r)`, the eligibility note (8.2), and the state with the time of each change. No existing row fits. The evidence-store row ends when the spot-check window closes, while a submission's totals and decisions must outlive the evidence (AGENTS.md, Retention), and the decisions row is written without personal data. The refusal (b) and D13's bound read the record for 180 days past each window's end, WP-SV18 for a spot-check window after the decision, and 7.7's cross-checks and 7.9's earlier outcomes for as long as the account has submissions. The eraser deletes it (8.9), and a withdrawal deletes nothing. The freeze's flags, `concurrent_devices` and `repo_overlap`, are risk flags under the evidence-store row, in a table with a `submission_id` column that is a window job of `evidence.revealed` on `decided_at_ms`, off until WP-SV18. Because the record lasts as long as the account, the server caps how many submissions and frozen windows one account can make, as it caps devlogs (#283), and docs/spec/submissions-v1.md states the caps.

(b) The overlap refusal. At the freeze the server refuses a submission when any of its windows overlaps a window of another submission of the same account that was frozen and not withdrawn, in any state from frozen through decided or revoked, that went to a program that takes hours, the new submission's own program included, and whose frozen keys hold a (device id, project key) pair that the new submission's projects map to at this freeze. Two windows overlap when each starts before the other ends. A withdrawal frees the withdrawn submission's windows. A decision, whatever its outcome, and a revocation don't, so a rejected claim can't move to a program whose reviewers never see the rejection (7.7 item 10). A submission to a program that takes no hours, today only calibration's (6.7), is never refused and never counts against another. The check runs in the freeze's writer op after the idempotency check, so a retry gets the stored submission, and a refused create stores nothing and draws no `r`. The answer is `409 already_submitted`, which names each overlapped submission's id and program to the account and none to a program's credential (WP-PG6). Keys are the right unit, not the whole account, because a submission's credited seconds are only the slots its frozen pairs hold after SM 3.4's merge over every device of the account, with the devices outside the submission read from the per-device minutes the server keeps. A slot another device won counts in none of the submission's totals, so no wall-clock second is credited in two submissions or twice in one, even for a repo on two computers whose keys WP-SV10 hasn't linked yet. A calibration submission's draw, which takes every prompt and goes only to labelers, binds no other submission. Its drawn items are kept with it alone, and its withdrawal marks none of them withheld elsewhere, so volunteering costs a teen neither the hours they submit later nor a larger reveal to a program's reviewers. This answers the 'Not covered here' line of docs/errata/2026-11-23-build-days-devlogs-retention.md, which stays as merged.

(c) Signed device requests. A device token creates and freezes in one request with no fresh login, but only when the request is signed with the device key. The client's submission requests get a 4.3 row of their own, after the publishing row. Every device-token request to a `/api/v1/submissions` route, reads included, carries ingest's `X-Pairtime-Device`, `X-Pairtime-Time`, `X-Pairtime-Body-SHA256` and `X-Pairtime-Signature`. The signature is the device key's Ed25519 signature over ingest-v1 2.2's message `M`, built with `pairtime-submissions-v1` in place of `pairtime-ingest-v1` and, in place of the request counter, the method, a space and the request target, path and query. A GET or a withdraw signs the SHA-256 of an empty body. The server checks these headers as ingest-v1 2.5 does and answers with its codes: `device_mismatch`, `device_key_missing`, `clock_skew` with the server's time, `body_hash_mismatch`, `signature_invalid` and the `_malformed` ones. No request counter is sent or moved, because the create and the withdraw are idempotent, so a replay inside the 300 s gets the first request's answer and changes nothing. A web session creates only with a fresh login (e) and the same-origin check every session write passes. For either credential the freeze reads `users.verification_status` inside its writer op and answers anything but `verified` with `403 not_verified` (8.2). No program credential creates in WP-SB1; WP-PG6 builds program-made submissions.

(d) The limit on withdrawals. Only the account withdraws: by web session with a fresh login (e) and the same-origin check, or by a signed device request (c). A withdrawal is allowed while the submission is `created`, `frozen`, `metadata_revealed`, `challenged` or `content_revealed`. From `checked` on, which begins once 7.5 step 9's checks, risk score and audit draw have run, and in `in_review`, `decided` and `revoked`, it gets `409 not_withdrawable`. A withdraw of a submission already `withdrawn` answers `200` with it unchanged. `withdrawn` and `revoked` are terminal. A withdrawal records `withdrawn_at_ms`, deletes nothing (4.4) and frees the submission's windows (b). Another account's submission gets `404`.

(e) The freeze rules. A web session's create, and its withdraw (d), needs `Session.FreshLoginWithin(10*time.Minute, now)` by the server's clock when the request arrives, the same 10 minutes as loginTTL. An ordinary sign-in never counts, however recent its auth_time (0002_accounts.sql). A missing or older fresh login gets `403 fresh_login_required` with `login_url: "/auth/hca/login?fresh=1"`, and the calling page adds `&return_to=` with its own path, because the API can't know which page called it. Every frozen window starts before it ends, ends no later than the freeze, and starts no earlier than the freeze minus 180 days, the submission horizon. WP-SB1 reads the 180 from server.minute_buckets' days in docs/retention.json, so the two can't drift. An explicit window that breaks any of these gets `400 window_invalid`. No window in a devlog list starts before the horizon, and one starts at the horizon only when the devlog window it comes from crosses it. Explicit windows aren't bounded by notBefore, only by the refusal (b), as PLAN's case of a later window ending before the latest frozen end requires. A freeze before W's minutes are final is allowed, since the re-score range already runs to the freeze, at most 86,400 s past W's end (6.5). At the freeze, `submission_keys` stores the (device id, project key) pairs that P's projects map to through rollup_projects. The refusal (b), D13's bound and the overlap flag, and WP-SB3's reveal rule (a) read that snapshot, while `submission_projects` keeps the ids the teen picked. Nothing changes a submission's keys after its freeze. Devlog windows for several projects: notBefore is the latest end of any window frozen by the account's submissions that weren't withdrawn, went to a program that takes hours and share one of P's (device id, project key) pairs, or 0 when there is none. The list is `devlog.Windows` over the `devlog.Times` of every project in P, merged and sorted, from that notBefore, as WP-SV12 built it. So a list with no earlier frozen window starts at its first devlog, and one after a frozen window starts at that window's end. The submission horizon, the freeze minus 180 days, then cuts the list. A window that ends at or before the horizon is left out, and one that starts before it starts at the horizon instead. A devlog-mode create freezes the whole cut list. When the cut list is empty, the create gets `409 no_devlog_windows` and the client sends one explicit window, which is D13's 'one window if the teen has none'.

(f) The programs list. Programs are a `[[programs]]` list in pairtime.toml, one table per program, with three keys. The list gets a 10.2 row of its own, after `reviewers`. `name` is unique and matches `^[a-z0-9][a-z0-9_-]{0,63}$`, the rule `program_reviewer:<program>` already uses (config.go's programName). `display_name` is the name teens see, 1 to 64 characters with no control characters. `takes_hours` defaults to true, and only a program whose submissions claim no hours sets it false, which today means calibration's (WP-CA0); the freeze copies it into the submission (a, b). There is no `key_sha256`, since this decision rules out an interim key, no programs table, and no CLI that writes the database. `submissions.program` holds the name as TEXT with no foreign key. A create naming a program the list doesn't hold gets `400 program_unknown`. Taking a program out of the list stops new submissions to it; its submissions keep its name, which reads show where no display name is configured. A `program_reviewer:<p>` role whose program isn't listed logs a warning at start and doesn't stop it, so reviewers can finish a removed program's submissions. The new key raises config_version, with its line in keyVersions.

## Applied wording

DESIGN 4.3, a row after the publishing row:

| From, to | When | Exactly what | Never in it |
|---|---|---|---|
| Client to `pairtimed`, submissions | Only when the teen submits hours or withdraws a submission, and when the client reads the teen's own submissions | The program's name, a random id the client made for the request, the projects the teen picked as this computer's project keys or the account's project ids, and one window's start and end or the choice of devlog windows; a withdrawal names only the submission. Every request carries the device token and is signed with the device key over its method, path, time and body hash, like ingest (4.5) | Project names, paths, devlog text or any other content |

DESIGN 4.4, a server row after the row for wrapped `K_dev` and encrypted backups:

| Where | Data | Kept |
|---|---|---|
| Server | Submissions: the account and the program each one is for, and whether that program takes hours (6.7); the program's `external_id` when the program made it, or else the random id the teen's client made for it, with a hash of the request so a retry gets the same submission; whether the teen made it on the web or from a computer, and which computer; the projects `P`, and each device id and project key they mapped to at the freeze; whether `W` is one window or devlog windows, and each frozen window's start and end; the scoring version and parameter hash; `r` and `SHA-256(r)`; the eligibility note (8.2); the state, and when each change of state happened | Account lifetime |

DESIGN 6.2 D13, the start of the 'This design' cell, up to 'Rows inside each window':

> A submission is a list of devlog windows, each the credited time between two consecutive devlogs on any of its projects (one window if the teen has none). A devlog's time is when the server received it, and windows start no earlier than the latest end of any window frozen by a submission on one of those projects that wasn't withdrawn and went to a program that takes hours, so deleting or republishing a devlog never puts submitted time in a new window (7.5 step 2, 4.4). A window that would start more than 180 days before the freeze, the submission horizon, starts then, and one that ends by then is left out.

DESIGN 7.5 step 1, whole:

> 1. **Create.** Through the program API (`POST /api/v1/submissions`, idempotent on `(program, external_id)`) or by the teen, whose client gives each create a random id and sends the same one on a retry. The server rejects a submission whose windows overlap those of another submission of the same teen that covers one of the same project keys, was frozen and wasn't withdrawn, and went to any program that takes hours, the same one included. A withdrawal frees those windows, and a decision, whatever its outcome, or a revocation doesn't. Calibration's submissions (6.7) take no hours, so they are never rejected and cause no rejection. The server also flags an overlap with another user's submission on the same repo fingerprint (SP 10.2).

DESIGN 7.5 step 2, whole:

> 2. **Freeze.** The server records `P`, with the key each of its projects has on each of the teen's computers at that moment, `W`, the scoring version, the parameter hash and a fresh 32-byte random value `r`, and sends the client `SHA-256(r)`. A later linking of projects (SP 4.2) changes no frozen key. `W` ends no later than the freeze and starts no earlier than 180 days before it, the submission horizon (4.4). For a list of devlog windows, `W` is each window's start and end, so a devlog deleted or published later moves no frozen window (D13, 4.4). The web flow asks for a fresh HCA login (`prompt=login`; SP 3.1), made at most 10 minutes before, and the client signs its requests with the device key (4.3).

DESIGN 7.5 step 5, whole:

> 5. **The draw is sticky** (JT must-fix 3). The server records every drawn item, keyed `(device_id, seq)`, for as long as its ledger record could be submitted. A calibration submission's draw (6.7), which takes every prompt and goes only to labelers, is kept only with that submission and binds no other. Any later submission whose pool contains a drawn item must reveal it, on top of its own new draw, or the item counts as withheld. Withdrawing a submission after its draw (`POST .../withdraw`) marks every drawn, unrevealed item withheld for every future submission that covers it. So a teen who sees a fabricated prompt drawn can't walk away and re-freeze a slightly different window: the set of drawn items only grows. A submission can be withdrawn only until step 9 starts. Once its checks, risk score and audit draw have run, and after a decision, it can't be, so no one can re-roll an audit or a reviewer by submitting the same time again.

DESIGN 7.5 step 8, its last sentence:

> Any other submission whose pool holds the item still asks for it, as step 5 says, unless only a calibration submission drew it.

DESIGN 7.5 step 9, a sentence at its end, the orchestrator's amendment to D-sb1-submissions on #342, so DESIGN says what WP-TR3's Build line already does:

> A submission to a program that takes no hours (6.7) stops after step 8, with no risk score, audit draw or review, and stays withdrawable.

DESIGN 7.9, its first sentence, the orchestrator's amendment to D-sb1-submissions on #342: "Each submission gets a deep-audit probability" becomes "Each submission to a program that takes hours gets a deep-audit probability".

DESIGN 10.2, a row after `reviewers`:

| Key | Value at launch |
|---|---|
| `programs` | The programs this instance takes submissions for: each one's name, as `program_reviewer:<program>` and its submissions name it, the name teens see, and whether its submissions claim hours, which only the calibration program's (6.7) don't; the template lists none |
