# Erratum: DESIGN 4.4 has no row for the submission record, and 7.5 step 1 refuses less than 9.2 and 9.7

Date 2026-11-24. Raised by the plan for WP-SB1, the submissions API. Status: open.

## Input

- WP-SB1 (PLAN, WP-SB1's Build) stores each submission:
  - the program and the `external_id` that `POST /api/v1/submissions` is idempotent on;
  - the projects `P`, and the project keys they named at the freeze;
  - each frozen window's start and end;
  - the scoring version and the parameter hash;
  - the fresh 32-byte `r` and `SHA-256(r)`;
  - the eligibility note;
  - the state, with the time of each change, the freeze and the withdrawal included, which DESIGN 4.4 needs to open the spot-check window.
- DESIGN 4.4's table (DESIGN.md:462-483) has no row for this record:
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

Open. The orchestrator decides for Karim. The Decision and the applied DESIGN wording follow in this erratum's next push, and a docs PR with the `oracle-change` label applies them to DESIGN 4.4 and 7.5 step 1.
