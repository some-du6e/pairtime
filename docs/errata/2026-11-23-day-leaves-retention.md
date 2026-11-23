# Erratum: DESIGN 4.4 doesn't list the day's leaf hashes, and a day root's count can show a few people's use

Date 2026-11-23. Raised by the three-lens review of WP-SV17 (#229 and #237, findings in #249). Status: decided 2026-11-23 by the orchestrator for Karim. Applied to DESIGN 4.3 and 4.4 in #229 under `oracle-change`, with docs/retention.json and the privacy notice.

## Input

- WP-SV17 keeps each published day's leaf hashes, `LEAF(leaf)` of every witness row of the day in leaf order, in the table `day_leaves` (migration 0010). A batch's inclusion proof (ledger-v1 5.2, 8) is made from them, so it can still be made after the day's other witness rows are deleted. D-day-leaves first kept them for good, under `no_personal_data`.
- A leaf commits to one device's batch: `u8(len(device_id)) || device_id || u64(b) || H_b || u64(received_at)`. Anyone holding a teen's ledger or a submission's bundle can recompute it and find it among the day's leaves. So the leaves are about a device, which `no_personal_data` excludes (AGENTS.md, Retention), and DESIGN 4.4 gives every kind of kept data a finite period. Keeping them after the day's last witness row has gone also buys nothing, because a proof reads the batch's own witness row first.
- Each day root publishes `count_d` for good. On a server where one person uploads, as on the pilot while sign-ups are open only to its operators, that count is the person's batch count for the day, and an empty root marks a day they didn't use pairtime. DESIGN 4.3 says the day root carries no per-user data, 4.4 "only hashes and a daily count", and the privacy notice "nothing about you".

## Expected

Every kind of data the server keeps has a period in DESIGN 4.4, which the privacy notice shows. A day's leaves go once nothing can use them. What a day root publishes is described as it is on a small server too.

## Decision

Decided 2026-11-23 by the orchestrator for Karim, in #249, correcting D-day-leaves, and applied in #229 under `oracle-change`:

- A day's leaf hashes stay as long as any of that day's witness rows. The witness rows' retention entry, `server.witness`, deletes a day's `day_leaves` row at the first sweep after the day's last witness row has gone, whether through the entry's 180 days, a submission's spot-check window closing or an account's erasure. ledger-v1 8 and its decision 37 say so.
- The limit: the leaf of an erased account's batch stays until the day's last witness row goes, because taking one leaf out would break the proofs of the day's other batches. Only someone holding that batch's device id, number, head and receipt time can match it.
- DESIGN 4.4's witness row names the leaves. docs/retention.json moves `day_leaves` from `no_personal_data` into `server.witness`, whose text in the privacy notice says how long the leaves stay.
- On a server with few uploaders, a day root's count shows how many batches they uploaded that day. DESIGN 4.3 and 4.4, docs/retention.json's `public.day_roots` and the privacy notice say so, and that an instance can keep the day roots off by leaving `PAIRTIME_WITNESS_REPO` unset. The count stays, because the day head and every inclusion proof need it.

## Applied wording

DESIGN 4.3, the last cell of the row for `pairtimed` to HCA, forges, programs, public git and Healthchecks.io:

> Per-user data in pings or the day root, which carries only hashes and a daily count. On a server with few uploaders, that count shows how many batches they uploaded that day; an instance can keep the day roots off by leaving `PAIRTIME_WITNESS_REPO` unset

DESIGN 4.4, two rows:

| Where | Data | Kept |
|---|---|---|
| Server | Witness rows and batch roots, and each day's leaf hashes, which stay as long as any witness row of their day | 180 days; rows a submission uses stay until its spot-check window closes |
| Public repo | Day roots | Permanent; only hashes and a daily count, which on a server with few uploaders shows how many batches they uploaded that day. An instance can keep them off by leaving `PAIRTIME_WITNESS_REPO` unset |
