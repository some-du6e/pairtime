# Erratum: DESIGN 10.3 has evidence rely on bucket versioning, which R2 doesn't have

Date 2026-11-20. Raised by the review of #33 (WP-SV15, issue #62). Status: waiting for Karim. Until Karim decides, pairtimed protects evidence with write-once puts and `Evidence.Get`'s digest check alone. Nothing in pairtimed sets, reads or relies on versioning, and DESIGN stays as it is.

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
3. **A second copy that pairtimed controls.** pairtimed copies each new blob to a second bucket whose key the main bucket's key can't use, and every delete removes both copies. This works on every store. It costs a second write per blob and a second copy that each of the three deletes has to remove.
4. **R2 bucket locks.** A lock rule blocks deletes and overwrites under a prefix for a set period (developers.cloudflare.com/r2/buckets/bucket-locks/, modified 2026-10-28). It would block the purge's "at once" too, so it doesn't fit evidence as DESIGN defines it.

## For Karim

1. Pick an option for DESIGN 10.3's evidence row, or another fix. Options 2 and 3 also change 10.5 item 4's list, and option 2 needs the Hetzner limit checked.
2. The evidence store's retention delete in 4.4 has no WP. Whoever builds it needs the same answer.

The PR that adds this erratum changes no DESIGN text. In PLAN.md it adds to WP-RV5 and WP-SB2 that their deletes must remove every version of a blob on a bucket that keeps versions, which holds under every option above.
