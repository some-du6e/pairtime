# Erratum: DESIGN 10.3 has evidence rely on bucket versioning, which R2 doesn't have

Date 2026-11-20. Raised by the review of #33 (WP-SV15, issue #62). Status: decided 2026-11-20 by the orchestrator for Karim: option 1, with an eraser whose dry run deletes no blob, `unavailable` for a blob the store can't return, and `pairtimed doctor` failing on a bucket that keeps versions or ignores `If-None-Match: *`. For item 2, a new WP-SV18 (M5) owns the evidence store's retention delete on WP-SV14's window jobs, and WP-SB4 adds the evidence store to the eraser. A submission's window opens at its latest decision, at a withdrawal before any decision, or at its freeze when neither comes (DESIGN 4.4). The Decision section below says what changed.

## Input

DESIGN 10.3's backup table has this row:

| Layer | Mechanism | RPO and RTO |
|---|---|---|
| Evidence | Sealed blobs written once under content hashes, bucket versioning on | n/a |

The pilot's buckets are on Cloudflare R2 (10.3, 10.6). 10.5 item 4, the list of what Karim sets up, names the two buckets, the snapshot bucket's 30-day lifecycle and keys scoped to both. It says nothing about versioning.

Three things delete evidence blobs:

- The teen's purge, which "deletes the item from the evidence store at once" (7.11). WP-RV5 builds it.
- The evidence store's retention, "until the spot-check window closes; 12 months after the decision until HQ sets a window" (4.4). No WP builds this delete yet. WP-SV14's retention jobs cover the other rows of 4.4.
- `pairtimed keys rotate-evidence`, which re-seals every stored blob when the evidence key is compromised (10.2). WP-SB2 builds it. A re-sealed blob is new ciphertext with a new hash, so the blob sealed to the old key is deleted.

## Expected

The row promises that no evidence blob is lost, so its RPO is "n/a", and that the store keeps a blob's first version even when a write gets past pairtimed's write-once put. The three deletes above expect the blob to be gone afterwards.

## What the stores give

**R2 has no versioning.** R2's S3 API page (developers.cloudflare.com/r2/api/s3/api/, modified 2026-11-10) lists GetBucketVersioning and PutBucketVersioning under "Unimplemented bucket-level operations" and mentions neither ListObjectVersions nor versionId. The same page says R2 evaluates If-None-Match "atomically against the committed bucket state", so write-once holds there. But anyone with the bucket's key, which is in `secrets.env`, can delete a blob, or overwrite it with a plain PUT that leaves the header out, and R2 keeps none of the old bytes. `Evidence.Get` then fails with `ErrNotFound` or `ErrDigestMismatch`, and nothing can restore the blob. Litestream and the nightly `VACUUM INTO` both copy the SQLite database, which holds the index rows (10.1) and not the blobs. So on the pilot the row's "n/a" doesn't hold. A deleted blob is gone.

**A versioned bucket keeps what the deletes must remove.** On a store with versioning, AWS S3 for one, a DeleteObject without a versionId only adds a delete marker, and the sealed blob stays behind as a noncurrent version. objstore's `Bucket.Delete` sends exactly that request. A purge would then leave the purged item in the bucket, retention would leave expired blobs, and rotation would leave every blob sealed to the key it retired because that key may be compromised. Each delete would have to list the key's versions with ListObjectVersions and delete each one by versionId. A lifecycle rule that expires noncurrent versions doesn't meet the purge's "at once".

**Hetzner may not allow both.** Hetzner Object Storage, which 10.6 prices for the move to a VPS, lists "Conditional PUT/DELETE operations on versioned Buckets" as not supported (docs.hetzner.com/storage/object-storage/supported-actions/, last changed 2024-09-23). The row doesn't say which conditional headers it means. If it still holds, a versioned evidence bucket there wouldn't refuse a second put, so "written once" and "versioning on" couldn't both be true. A test against a real Hetzner bucket should settle it before any move.

## Options

1. **Drop versioning from the row.** Evidence rests on write-once puts, the digest check and keys scoped to the two buckets, and the RPO says that a blob deleted with the bucket's key is lost. Purge, retention and rotation stay plain deletes. This is what R2 can do and what pairtimed does today.
2. **Versioning where the store has it, and deletes that remove every version.** The row says "versioning on where the store offers it". WP-RV5, the retention delete and WP-SB2 each delete every version of a key. On R2 nothing changes from option 1. On Hetzner the conditional-put limit above has to be resolved first.
3. **A second copy that pairtimed controls.** pairtimed also writes each new blob to a third bucket under its own key, and every delete removes both copies. This works on every store and survives losing one bucket or one key. It doesn't stop a deliberate delete, because whoever holds pairtimed's `secrets.env` holds both keys. It costs a second write per blob and a second copy that each of the three deletes has to remove.
4. **R2 bucket locks.** A lock rule blocks deletes and overwrites under a prefix for a set period (developers.cloudflare.com/r2/buckets/bucket-locks/, modified 2026-10-28). It would block the purge's "at once" too, so it doesn't fit evidence as DESIGN defines it.

## For Karim

Answered on 2026-11-20 by the decision below.

1. Pick an option for DESIGN 10.3's evidence row, or another fix. Options 2 and 3 also change 10.5 item 4's list, and option 2 needs the Hetzner limit checked. **Option 1.**
2. The evidence store's retention delete in 4.4 has no WP. Whoever builds it needs the same answer. **Answered by D-evidence-retention-wp: WP-SV18 builds it.**

The PR that adds this erratum changes no DESIGN text. In PLAN.md it adds to WP-RV5 and WP-SB2 that their deletes must remove every version of a blob on a bucket that keeps versions, which holds under every option above.

## Decision

Option 1. Evidence keeps one copy, in buckets that never keep versions, and every delete is one plain DeleteObject.

Versioning would protect nothing here. The purge, retention, key rotation and the eraser (DESIGN 8.9, a fourth delete the list above missed) would each have to remove every version, so a bug in one of them would remove every version too, and whoever holds `secrets.env` can delete versions as well. Versioning is also bucket-wide, and this bucket holds Litestream's replica, so it would keep every LTX file Litestream prunes, and the rows 4.4 deletes with them, past the 72 hours DESIGN 10.3 gives the replica. And R2 has no versioning to turn on.

A lost blob costs a spot-check that can't be answered, and it earns nothing. The teen can delete evidence only by purging, which uncredits, or by erasing the account, and a changed blob fails its SHA-256, AES-GCM and `C_i`. Option 3 doubles every write and delete for the same narrow gain, and option 4 blocks the purge.

Changes to DESIGN:

- 10.3. The evidence row says blobs are written once under their SHA-256, with keys scoped to the two buckets and no versions, and that they aren't backed up, so a blob deleted or overwritten with the bucket's key is lost.
- 10.5. Item 4 asks for no versioning on either bucket and no lifecycle rule that deletes objects in the first. Step 4's doctor checks both, and checks that the first bucket refuses a second write-once put.
- 10.2. `rotate-evidence` deletes the blobs sealed to the old key.
- 7.5 step 8, 7.7 and 7.8. An item whose blob the store can't return or open is `unavailable`. It isn't called lost, because 7.5 step 8 already has "witnessed, evidence lost". Before the decision the server asks the client to reveal the item again, since the device keeps prompt text until then (4.4). After the decision it changes no credit. `challenge.json` keeps the status recorded at the time, because the bundle's SHA-256 in the justification text has to keep matching, and `pairtime verify --fetch-reveals` lists an unavailable item without changing its exit code.
- 8.9. The eraser deletes the account's blobs in the evidence store and any backup segments in object storage. The privacy hub sends a dry run first, and the dry run lists the blobs and backup segments it would delete without deleting any, because a blob delete can't be rolled back.

Changes to PLAN: WP-SB2 gets `Evidence.List`, an `Evidence.Delete` that fails on `x-amz-delete-marker: true`, and a rotation that finishes when run again after a crash. WP-SB5 to WP-SB8 and WP-RV2 handle `unavailable`. WP-SV13's dry run deletes nothing outside the database. WP-RV5 takes a purge that removes every copy of an item and uncredits each submission that revealed it. WP-SV18 takes evidence retention, and WP-SB4 the eraser's evidence step, which answers item 2 above. WP-OP1's doctor checks versioning, tested against MinIO, and runs the write-once probe. These replace the "every version" text #81 added to WP-RV5 and WP-SB2.

Item 2, decided as D-evidence-retention-wp. A new WP-SV18 in PLAN's M5 owns the evidence store's retention delete, on the window jobs WP-SV14 gives `evidence.revealed`, and WP-SB4 adds the evidence store to the eraser. A submission's window opens at its latest decision, at a withdrawal that came before any decision, or at its freeze when neither comes (DESIGN 4.4). Five things delete evidence blobs: the purge, retention, key rotation, calibration's 30-day delete and the eraser. Each calls `objstore.Evidence.Delete`: the purge, retention, calibration's delete and the eraser before the index row goes, and key rotation once the row names the re-sealed blob. Retention never deletes a blob that no row names.

Hetzner's page lists conditional writes as unsupported only on versioned buckets and says nothing about unversioned ones. Doctor's write-once probe checks whatever store the buckets use, so WP-OP4's doctor run settles it before any move.

Left as they are:

- Erasing an account inside an approved submission's spot-check window deletes that submission's evidence and uncredits nothing, so a spot-check of it can't be answered. That holds under every option, and the eraser stays as 8.9 has it.
- When a withdrawn or never-decided submission's window starts is its own question, in `docs/errata/2026-11-20-withdrawn-window.md`, because it governs WP-SV14's holds as much as evidence.
- SP 9.2's evidence row stays as SP proposed it. DESIGN 10.3 overrides it.
