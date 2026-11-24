# Erratum: DESIGN 6.2 D10 doesn't say what its 72 hours count from, which uploads count, or how a time zone change moves a day

Date 2026-11-23. Raised by the plan for WP-SV12 part 1, whose risks ask for an erratum where D10 doesn't say what the 72 hours count from, and by that plan's critic. Status: decided 2026-11-23 by the orchestrator for Karim (D-sv12-lag). Applied to DESIGN 3.8 and 6.2 D10, and to the build-days row D-sv12-retention adds to DESIGN 4.4, in a docs PR of its own, with PLAN's WP-SV12. The code follows in WP-SV12.

## Input

- DESIGN 6.2 D10 counts a day's credited seconds at level A or B "that the server received within `BUILD_DAY_LAG_S` = 259,200 s" without saying from when. Its reason says "The 72 hours shut out lookback and after-the-fact fabrication while keeping the day of a teen who syncs at school". PLAN WP-SV12's test: "a day received after 70 hours counts and one after 73 doesn't".
- D10 doesn't say:
  - what "received" means for a day whose minutes arrive in many revisions;
  - what pending seconds and later revisions do;
  - which zone sets a day's bounds;
  - what a zone change does to a day not yet counted.
- `rollups_daily` keeps no receipt time (migration 0005), so a job that sums it after a deadline counts minutes received after that deadline. `minute_bucket_revisions` keeps each revision's `received_at_ms` (0004) and the server's own witness label, `server_witness` (0009).
- Ingest stores that label beside each revision but hands the rollups the bucket as the device sent it. So `rollups_daily` counts each slot at the level the device claimed.
- The server labels a revision `backfilled` when it arrives more than `WITNESS_LAG_S` after its minute outside a catch-up. That takes in every minute from before the device's chain began (`internal/server/witness`). DESIGN 6.4 makes backfilled credit level C, which D11 keeps off build days.
- `rollup.Apply` moves a revised minute to its date in a new zone, while the dashboard's zone form says "days already counted stay as they are". approach-teen D1 evaluates "each day ... in the timezone in force then".
- Read literally, DESIGN 3.8's "gamified surfaces count only witnessed level A or B minutes" drops the offline minutes a school sync rests on.

## Arithmetic

- 259,200 s = 72 h. Friday 2026-11-20 ends at Saturday 00:00 local, so its deadline is Tuesday 2026-11-24 00:00. A sync at school on Monday at 08:00 is 56 h in. A day's first minute gets up to 96 h, or 97 h on a 25-hour day.
- A minute at 00:05 on UTC 2026-11-20, received at 2026-11-23T23:00Z, is received 71 h after the day ended. The minute is 94 h 55 min old by then, and it counts.
- America/New_York's 2026-11-01 runs 25 h, from 04:00Z to 2026-11-02T05:00Z, so its deadline is 2026-11-05T05:00Z.
- If a day counted only once its deadline had passed, a teen who builds every day would see at most 26 of the last 30 days. On day N, days N-3 to N are still open.
- Take a change from UTC to America/Los_Angeles at 2026-11-20T12:00Z:
  - A minute at 05:00Z is on 2026-11-20 in UTC and on 2026-11-19 in Los Angeles.
  - Los Angeles ends 2026-11-20 at 2026-11-21T08:00Z, so that day's deadline becomes 2026-11-24T08:00Z.
  - Tokyo ends 2026-11-20 at 15:00Z, before UTC does, so after a change to Asia/Tokyo the deadline stays 2026-11-24T00:00Z.
- `DAY_CAP_S` 43,200 = 48 x `BUILD_DAY_S`, so no cap comes near a build day's threshold.

## Decision

1. The 72 hours run from the end of the local day in the profile's time zone. A minute counts by the `received_at_ms` of the minute-bucket revision that carries it. That is the time ingest stamps, and the time the witness label reads.
2. Only revisions the server itself labeled witnessed or offline count.
   - For each device and minute, the last such revision received counts once it is final or `PENDING_WINDOW_S` has passed since its minute ended.
   - A revision the server labeled backfilled adds and removes nothing, so a late restore keeps the minute's earlier credit.
   - A revision stored with no label, by a release from before migration 0009, goes by the client's reading in its bucket.
3. The revisions merge one human minute per wall-clock minute (SM 3.4), and the level A and B seconds are summed over every project. Level C never counts. `DAY_CAP_S`, `OFFLINE_CAP_S` and `OFFLINE_PUSH_S` don't enter it.
4. A day counts the moment that sum reaches `BUILD_DAY_S`, and it stays counted whatever arrives later.
   - A day still short at its deadline is decided then not to count, from the revisions received by the deadline. The read runs inside a writer op, so a revision received in time but committed later isn't missed.
   - Nothing reopens a decided day.
   - Weeks, freezes, consistency, boards and the profile count a build day from the moment it counts.
   - A week is missed only once its last day's deadline has passed.
5. The profile's time zone sets every day's bounds. A change, at most once per 30 days, counts from the moment it is made:
   - A minute takes its date in the zone in force at its own time, and keeps it.
   - A day that had begun in the old zone before the change, and whose deadline hadn't passed, ends at the later of its ends in the two zones.
   - Setting the zone the account already has isn't a change.

DESIGN 6.2 D10 and 3.8, and PLAN's WP-SV12, carry the wording.

Amended in review in #278 (2026-11-23): D10 and 3.8 carry decision 2's rule for a revision stored with no label; a day the job hasn't stored by its deadline counts if its credit reached `BUILD_DAY_S` as of any time up to the deadline, so a late or first run decides as runs on time would have; an open day shows its credit from the as-of read, never `rollups_daily`; D10 and D11 say that D6's cap and forge push are submission rules; and the account keeps every zone change, each with its time and the zone it replaced, so decision 5 holds across any number of changes.

Cost: the bucket-level label (`internal/server/witness`) reads two honest cases as backfilled:
- a restore (SM 4.3) that arrives after `WITNESS_LAG_S`;
- a bucket held up by an outage that began after its batch got through.
So a day that reaches 900 s only through one of them doesn't count.

## Not decided here

WP-SV9's public daily totals at level A or B read `rollups_daily`, which counts each slot at the level the device sent. So a revision the server labeled backfilled reaches them at the level the device claimed. Whether those totals take the server's label, as build days do, is for an erratum before SV9 starts.
