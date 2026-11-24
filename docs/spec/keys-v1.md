# Keys document, version 1

Status: the format of `/.well-known/pairtime-keys.json`, which `pairtimed` serves (WP-SV17; DESIGN 10.2), and of the server key's validity it publishes. Clients read it to check receipts (docs/spec/ingest-v1.md 5 step 4), and verifiers to check receipts, day heads and bundle manifests (docs/spec/ledger-v1.md 7 to 9; DESIGN 7.8). Its evidence key, which WP-SB2 added, is the key clients seal tier-2 content to before each upload (DESIGN 7.6; docs/spec/seal-v1.md). `internal/server/keys` implements it. Changing a member needs every reader.

## 1. The request

```text
GET /.well-known/pairtime-keys.json
```

No authentication. The answer is 200 with `Content-Type: application/json`, `Cache-Control: no-cache`, so a cache checks with the server before each reuse, and an `ETag`, so a check that finds the document unchanged gets `304 Not Modified` without the body. `pairtimed` writes the document when it starts, and it changes only at a start that records a key the history doesn't hold yet (3), or at a start whose `/data/keys` has no keys, which lists no evidence key (2) until a start with the keys lists it again.

## 2. The document

```json
{
  "format": "pairtime-keys-v1",
  "server_keys": [
    {
      "key_id": "0123456789abcdef",
      "public_key": "<64 hex digits>",
      "valid_from": 1795046400000,
      "valid_to": null
    }
  ],
  "evidence_key": {
    "key_id": "fedcba9876543210",
    "public_key": "04<128 hex digits>",
    "valid_from": 1795600800000,
    "valid_to": null
  }
}
```

| Member | Meaning |
|---|---|
| `format` | `pairtime-keys-v1`. A reader refuses any other value |
| `server_keys` | Every key `/data/keys/server.ed25519` has held, oldest first, `[]` before the server has one. A key stays listed after it is retired, forever, so the receipts and day heads it signed keep verifying (DESIGN 10.2) |
| `key_id` | The key's id: the first 16 hex digits of the SHA-256 of its public key, as `pairtimed keys show` prints it. That is the raw 32 bytes for a server key, as a receipt names it (ingest-v1 2.8), and the 65-byte uncompressed point for the evidence key, as a sealed blob names it (seal-v1 1). A reader computes it from `public_key` and refuses a document whose `key_id` disagrees |
| `public_key` | A server key's 32-byte Ed25519 public key, or the evidence key's uncompressed P-256 point, in lowercase hex |
| `valid_from`, `valid_to` | The first and the last millisecond the key is valid, both included, in Unix ms. `valid_to` is `null` while the key is current |
| `evidence_key` | The current evidence key, which seals tier-2 content (DESIGN 7.6; seal-v1 7), with the four members above, or `null` while the server holds none, as when `/data/keys` has no keys. Its `public_key` is the 65-byte point, 130 hex digits. The document lists only the current evidence key, never a retired one, so its `valid_to` is always `null`. While it is `null`, an upload gets a 503 with a code of its own, not `409 evidence_key_retired`, and a client that reads `null` uploads nothing and tries again later (seal-v1 7) |

## 3. Validity

No two server keys are valid at the same millisecond, so a time names at most one key:

- A receipt counts under the key valid at its `received_at` (ledger-v1 7 and 9 step 7), and a day head under the key valid at the last millisecond of its day, `(d + 1) × 86,400,000 − 1` (ledger-v1 8 and 9 step 9). A verifier that finds no key valid at that time, or one whose id isn't the one the receipt or head names, fails it.
- A server's first key is valid from 00:00 UTC on the day the server first used it: its first receipt, or the start of `pairtimed serve` that first recorded it (#133), whichever is earlier. It is the day and not the millisecond because the document is public, and the time of a server's first receipt says when one person first uploaded. Validity that starts before the first receipt changes no verdict, since the key signed nothing earlier.
- A start of `pairtimed serve` with a key the history doesn't hold, such as one a rotation put in `/data/keys`, makes that key valid from its first receipt or that start, whichever is earlier, and retires the current key the millisecond before. It refuses to start instead when the current key would then lose something it signed: a receipt at or after that millisecond, or the head of a day that hadn't ended by then, as a clock set back would cause.
- `pairtimed serve` refuses to start with a key the document lists as retired, since no client keeps a receipt a key signed after its validity ended (ingest-v1 7 item 24).

The evidence keys have a validity of their own, under the kind `evidence` in the same history (seal-v1 7). No two evidence keys are valid at once, and an evidence key is valid from the start that first records it, at that millisecond, since its validity says nothing about when anyone uploaded. A server key and an evidence key are valid at the same time.

The server keeps the history in its database (`key_history`, migration 0010), not in `/data/keys`, which holds only the current key, and it never deletes a row. After a rotation the server must keep the retired key's private half until it has signed the head of every day that ended before the rotation (ledger-v1 8). `pairtimed` has no rotation command yet; the work package that adds one keeps that key and records the new key's id in `server_keys`.

## 4. Decisions this spec makes

DESIGN 10.2 says the document publishes each key's id with validity dates and keeps old keys forever. These are what it leaves open, decided here:

1. Validity is two Unix-ms times, both included, with `valid_to` `null` while the key is current, the unit of every other time in the ledger and the ingest API (ledger-v1 1).
2. A server's first key is valid from the start of the UTC day of its first use, which is no later than its first receipt. Migration 0007's `server_keys` can't give the date: it holds one row, which a rotation overwrites, and on a server that signed receipts before the row existed, the row's time comes after them.
3. A later key's validity starts at its first use, and the current key's ends the millisecond before, so the history has no gap and no overlap, and a verifier finds a key from a time alone.
4. Keys are named as receipts name them, by the first 16 hex digits of the SHA-256 of the public key, and public keys are lowercase hex, as `GET /api/v1/ingest/state` gives the current one (ingest-v1 3).
5. The document isn't signed. It comes over TLS from the server's own host, and a verifier that doesn't trust the server compares the keys with the ones a bundle or the public witness repository names (DESIGN 7.8).
6. The evidence key's slot holds one key, or `null`, because DESIGN 7.6 lists only the current evidence key. It is `null` whenever the server holds no current evidence key, so a client never seals to a key the server can't open with.
