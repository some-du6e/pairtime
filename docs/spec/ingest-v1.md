# Ingest wire format, version 1

Status: the contract between `pairtimed`'s ingest (WP-SV4, and the shape half of WP-SV5's firewall) and the client's uploader (WP-AG5). Changing it needs both sides; an uploader that needs a change says so in its PR, and the orchestrator settles it.

This is how a pairtime client sends tier-1 data to `pairtimed`: `POST /api/v1/ingest/batches`, `GET /api/v1/ingest/state` and `GET /api/v1/time`. DESIGN 4.3 says what tier 1 holds and what never crosses the network, DESIGN 4.5 how a request is signed and what the firewall refuses, and DESIGN 3.6 with docs/spec/ledger-v1.md sections 6 and 7 what a batch root and a receipt are. This file fixes the bytes: every header, the signed message, the JSON of the body and of each response, the order of the checks, and a machine-readable code for each refusal. Section 7 lists what it decides that DESIGN leaves open.

## 1. Conventions

- Times are Unix milliseconds, UTC, written as decimal integers, the unit of `pkg/event`'s `t` (ledger-v1 1). Two exceptions keep the forms their sources use: a bucket's `minute` is an RFC 3339 string, as `encoding/json` writes `pkg/event`'s `time.Time`, and a commit's `author_time` and `committer_time` are Unix seconds, git's unit (`internal/adapters/git`).
- Hex is lowercase. A hash is 64 hex digits, a signature 128.
- A *key* is an opaque key: 1 to 128 ASCII characters, each one of `A-Z a-z 0-9 _ -` (ledger-v1 1, `pkg/event`'s `isKey`). `device_id` and project keys are keys.
- JSON is RFC 8259, UTF-8. An integer member is a JSON number with no fraction or exponent.
- `KiB` is 1,024 bytes and `MiB` 1,048,576. DESIGN 4.5's "256 KB" and "4 MB" are 262,144 and 4,194,304 bytes.
- Every response from the three endpoints, refusals included, carries `X-Pairtime-Time: <the server's clock in Unix ms>` and `Cache-Control: no-store`. A JSON response has `Content-Type: application/json`.
- Every request carries `User-Agent: pairtime-<component>/<version>`, such as `pairtime-agent/v0.1.0`, because Cloudflare's bot filter is on in front of the server. The server doesn't check it.

## 2. `POST /api/v1/ingest/batches`

### 2.1 Headers

```text
POST /api/v1/ingest/batches
Authorization: Bearer ptd_<30+ base62><6-char CRC32>
Idempotency-Key: <UUIDv7>
Content-Type: application/json
Content-Encoding: gzip
X-Pairtime-Device: <device_id>
X-Pairtime-Req: <request counter, strictly increasing per device>
X-Pairtime-Time: <unix ms>
X-Pairtime-Body-SHA256: <hex(SHA-256(raw body))>
X-Pairtime-Signature: ed25519=<base64 Ed25519(device_key, M)>
User-Agent: pairtime-agent/<version>
```

| Header | Value |
|---|---|
| `Authorization` | `Bearer ` and the device token `pairtime login` got (WP-SV3; DESIGN 3.10). |
| `Idempotency-Key` | A UUIDv7 (RFC 9562 section 5.7) in lowercase canonical form, `xxxxxxxx-xxxx-7xxx-Nxxx-xxxxxxxxxxxx` with `N` one of `8 9 a b`, 36 characters. A new one for each new body; a retry of the same body keeps it (2.10). |
| `Content-Type` | `application/json`, optionally with `; charset=utf-8`. |
| `Content-Encoding` | `gzip`: the body is one gzip member (RFC 1952) holding the JSON of 2.3, with nothing after it. |
| `X-Pairtime-Device` | The device's `device_id`, the key the token belongs to. |
| `X-Pairtime-Req` | The request counter, a decimal integer from 1 to 9223372036854775807 with no sign and no leading zeros. Each request the server processes must carry a counter greater than every counter it processed for the device before (2.5 step 13). |
| `X-Pairtime-Time` | The client's clock, corrected by the offset it learned from `X-Pairtime-Time` on earlier responses, as a decimal integer of Unix milliseconds with no sign and no leading zeros. |
| `X-Pairtime-Body-SHA256` | The SHA-256 of the raw body as 64 lowercase hex digits: the same 64 characters that end `M`. |
| `X-Pairtime-Signature` | `ed25519=` and the 64-byte signature in base64 with padding (RFC 4648 section 4), 88 characters. |

The *raw body* is the exact bytes of the HTTP request body as sent: the gzip stream, before anything decodes it. The signature and `X-Pairtime-Body-SHA256` cover those bytes, so the server checks them before it inflates anything.

### 2.2 The signed message

DESIGN 4.5's message, byte for byte:

```text
M   = "pairtime-ingest-v1\n" || device_id || "\n" || req || "\n" || time || "\n" || hex(SHA-256(raw body))
sig = Ed25519(device key, M)                    RFC 8032 section 5.1: PureEdDSA, no context, no prehash
X-Pairtime-Signature: ed25519=<base64(sig)>
```

`"\n"` is the one byte `0x0a`. `device_id`, `req` and `time` are the ASCII of the `X-Pairtime-Device`, `X-Pairtime-Req` and `X-Pairtime-Time` values exactly as sent. The hex is lowercase. `M` has no trailing newline. The device key is the Ed25519 key the device registered with its token (WP-SV3); `crypto/ed25519.Sign` computes `sig`.

Worked example. The device key's seed is `SHA-256(ascii("pairtime ingest-v1 example device key"))`:

```text
seed       = 7bf821b60e33d7e00abe88a5e7e8e31715789eacda1a3ac3eea47eca45c210c1
public key = fadacf1c88bce8152ec27995a05aa8d9c53ac5678801cdd2e05b19060398a677
```

The body holds one batch root, a valid batch 1 with `n = 3`, `R_b` = 32 bytes of `0xaa`, `prev` = zero(32) and `H_b = SHA-256(0x03 || prev || R_b || u64(3) || u64(1))` (ledger-v1 6), gzipped by Python's `gzip.compress(json, compresslevel=9, mtime=0)` into 129 bytes. Any gzip writer is fine; the hash is over whatever bytes go on the wire.

```text
json = {"batches":[{"b":1,"n":3,"R_b":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","H_b":"709a14e467695dbaddf729b36d14c2629ba332116c1942c8a21a8e1d21309123","prev":"0000000000000000000000000000000000000000000000000000000000000000"}]}
raw  = 1f8b0800000000000203a5cc310e02310c44d1bb4c9d22b683b3c909a86911424e1c448510209ad5de9d882becaf669ab7a2d9a7dfc71bf5bca2a152c00355024ed7f9603b43c0f10fe5588cd2489ab51cbc99fb2d7369a24ea9b3ce69224ca49d4ae2be18932d839c49622196093d5fe33ba5b8336c97ed07d5b9d682f7000000
hex(SHA-256(raw)) = c8e78a7463f88a78d206683e69e3f847fe3d83715ad7ec2845e997167ddf4e15
```

With `device_id = d_example`, `req = 42` and `time = 1795089660250` (2026-11-19T12:01:00.250Z), `M` is 110 bytes:

```text
M   = pairtime-ingest-v1\nd_example\n42\n1795089660250\nc8e78a7463f88a78d206683e69e3f847fe3d83715ad7ec2845e997167ddf4e15
    = 7061697274696d652d696e676573742d76310a645f6578616d706c650a34320a313739353038393636303235300a63386537386137343633663838613738643230363638336536396533663834376665336438333731356164376563323834356539393731363764646634653135
sig = 1ffce087cf6259d5b28584fd9bc097e4333fb8cdf9818cc7b060dddbd66f2362f508f8768f53c61ebe72e750e6a0fef48870b8cfef6def01f78a3640bcf35500
X-Pairtime-Body-SHA256: c8e78a7463f88a78d206683e69e3f847fe3d83715ad7ec2845e997167ddf4e15
X-Pairtime-Signature: ed25519=H/zgh89iWdWyhYT9m8CX5DM/uM35gYzHsGDd29ZvI2L1CPh2j1PGHr5y51DmoP70iHC4z+9t7wH3ijZAvPNVAA==
```

`internal/server/ingest`'s tests check this example with `crypto/ed25519`, and send it to a server whose clock reads that time, which accepts the batch.

### 2.3 The body

The gzip member holds one JSON object with up to four members, each optional; an absent member and an empty one mean the same:

```json
{
  "batches": [ <batch root>, ... ],
  "buckets": [ <minute bucket>, ... ],
  "commits": [ <commit record>, ... ],
  "labels": <labels>
}
```

`batches`, `buckets` and `commits` are arrays, and `labels` is an object. Any other member, or a member of another JSON type, refuses the whole request with `body_shape` (2.6). An empty object is a valid body: it moves the counter and stores nothing.

Inside an item, every member is in the allowlist below with its JSON type and, for a string, the one form it may take. A member the allowlist doesn't have, a missing required member, a string of any other form and a number out of range each refuse that item, with the item codes of 2.9; the rest of the request goes on. No string anywhere can hold free text: each is a key, an enum value, a hash or HMAC, a version, or a time of fixed form (DESIGN 4.5; SP 8.2). An item must not repeat a member name. The server stores what it parsed, re-encoded, never the bytes it received.

#### 2.3.1 Batch roots

DESIGN 4.3's `{b, n, R_b, H_b, prev}`, exactly these five members:

| Member | Type | Form |
|---|---|---|
| `b` | integer | The batch number, 1 to 9223372036854775807 |
| `n` | integer | `n_b`, the batch's record count, 0 to 9223372036854775807. A 0 is refused as `batch_empty`, not as a bad number, because ledger-v1 6 names that refusal |
| `R_b` | string | The batch's Merkle root, a hash |
| `H_b` | string | The batch's head, a hash |
| `prev` | string | `H_(b-1)`, a hash; 64 zeros for batch 1 |

#### 2.3.2 Minute buckets

A bucket is the JSON that `encoding/json` writes for `pkg/event`'s `MinuteBucket.Shareable()`: DESIGN 4.3's `{v, params, minute, device, slots[4]{p, cr, why, kind, ev, lbl}, credited_s, uncredited{code: s}, agent_running_s, state, witness, rev}`, which `event.Tier1BucketFields` and `event.Tier1SlotFields` pin. It never holds `anchors`, the event ids that stay on the device (DESIGN 4.1 E-5): a bucket with that member is refused as `unknown_field`.

| Member | Type | Form |
|---|---|---|
| `v` | string | The scoring version, `N.N.N` with 1 to 9 digits in each part |
| `params` | string | `sha256:` and 64 hex digits, the parameter table's hash (SM 8) |
| `minute` | string | The minute's start, `YYYY-MM-DDTHH:MM:00Z`, exactly 20 characters, from 2020 to 2099 |
| `device` | string | A key, which must equal `X-Pairtime-Device` |
| `slots` | array | Exactly 4 slot objects, in order |
| `credited_s` | number | Seconds, 0 to 60 |
| `uncredited` | object | Optional. Each member's name is an uncredited reason code of SM 5.2 (`pkg/event`'s `Reason.Uncredited`) and its value seconds, 0 to 60; at most 64 members |
| `agent_running_s` | number | Seconds, 0 to 60 |
| `state` | string | `pending`, `final`, `provisional` or `restored` |
| `witness` | string | `witnessed`, `offline` or `backfilled`. It is the client's own reading, stored as sent; the server's witness label is WP-SV6's |
| `rev` | integer | The revision, 0 to 4294967295 |

Each slot, `encoding/json`'s form of `event.BucketSlot`:

| Member | Type | Form |
|---|---|---|
| `p` | string | Optional. A project key |
| `cr` | number | Credited seconds, 0 to 15 in steps of 0.5 |
| `why` | string | Optional. A credit reason code of SM 5.2 (`Reason.Credited`) |
| `kind` | string | Optional. An activity kind of SM 5.1 |
| `ev` | string | Optional. The evidence level, `A`, `B` or `C` |
| `lbl` | array | Optional. Side-door labels, each of `via_track`, `via_plugin`, `via_bridge` at most once and in that order |

On top of the forms, a bucket must pass `event.MinuteBucket.Validate`: the slots' `cr` sum to `credited_s`, a credited slot has a project, a reason and its kind, credited and uncredited seconds fit in a minute, a backfilled minute's credit is level C, and the rest of that function's rules. A bucket that fails one is refused as `bad_rule`.

#### 2.3.3 Commit records

DESIGN 4.3's `{sha, parents, project, observed_at, author_time, committer_time, files_changed, insertions, deletions, path_hmacs[], credited_seconds_since_parent, rewrites[], actor, agent_trailer}`, in the JSON of `internal/adapters/git`'s `Commit` with the two members it doesn't fill yet:

| Member | Type | Form |
|---|---|---|
| `sha` | string | The commit id, 40 or 64 hex digits |
| `parents` | array or `null` | Commit ids of the same form, at most 1,024; `null` or `[]` for a root commit |
| `project` | string | A project key |
| `observed_at` | integer | When the device first saw the commit, Unix ms from 2020 to 2099 |
| `author_time` | integer | The commit's author date, Unix seconds, 0 to 4102444799 |
| `committer_time` | integer | The commit's committer date, Unix seconds, 0 to 4102444799 |
| `files_changed` | integer | 0 to 2147483647 |
| `insertions` | integer | 0 to 2147483647 |
| `deletions` | integer | 0 to 2147483647 |
| `path_hmacs` | array | Optional. Keys of the changed paths in WP-AD2's keyed form, at most 1,024 |
| `credited_seconds_since_parent` | number | Optional. Seconds, 0 to 1e9 |
| `rewrites` | array | Optional. At most 1,024 objects `{"old_sha", "new_sha"}`, each a commit id |
| `actor` | string | `human`, `agent` or `unknown` |
| `agent_trailer` | string | Optional. A key naming the agent a trailer credits, such as `claude`; never the trailer itself |

#### 2.3.4 Labels

DESIGN 4.3's `{client_build, scoring_version, params, harnesses[{name, version}], typing_module, vm}`:

| Member | Type | Form |
|---|---|---|
| `client_build` | string | The client's version, such as `v0.1.0`: 1 to 64 characters of `A-Z a-z 0-9 . _ + -`, starting with a letter or digit |
| `scoring_version` | string | `N.N.N`, as a bucket's `v` |
| `params` | string | `sha256:` and 64 hex digits |
| `harnesses` | array | At most 16 objects `{"name", "version"}`: `name` a harness of `pkg/event`'s `Harness` list other than `none`, `version` in `client_build`'s form; no name twice |
| `typing_module` | boolean | Whether the opt-in typing module is on |
| `vm` | boolean | Whether the device is labeled as a VM (DESIGN 6.2 D17) |

### 2.4 Limits

Per request (DESIGN 4.5):

| Limit | Value | Refusal |
|---|---|---|
| Raw body | 262,144 bytes (256 KiB). A `Content-Length` over it is refused before the body is read, and a body without one is read to one byte past it, then refused | 413 `body_too_large` |
| Inflated body | 4,194,304 bytes (4 MiB). Inflating stops at one byte past it, so a gzip bomb costs the server at most 4 MiB of output | 413 `decompressed_too_large` |
| Batch roots | 2,000 | 413 `too_many_batches` |
| Minute buckets | 2,000 | 413 `too_many_buckets` |
| Commit records | 500 | 413 `too_many_commits` |

A client splits a larger upload across requests.

### 2.5 Checks, in order

The server checks a request in this order and answers the first failure with the refusal of 2.6. Steps 1 to 12 read but write nothing; step 13 and after run as one transaction in the server's writer.

1. Ingest is set up on this server: a device store and the server's signing key. Otherwise 503 `ingest_off`.
2. `Authorization` holds a `Bearer` token: otherwise `token_missing`. The token passes its CRC32 checksum before any lookup and names a device: otherwise `token_invalid`. The device isn't revoked: otherwise `device_revoked`.
3. `X-Pairtime-Device` equals the token's `device_id`: otherwise `device_mismatch`.
4. The device has registered its Ed25519 key: otherwise `device_key_missing`.
5. `Idempotency-Key`, `X-Pairtime-Req`, `X-Pairtime-Time`, `X-Pairtime-Body-SHA256` and `X-Pairtime-Signature` are each present once and in the form of 2.1: otherwise `idempotency_key_malformed`, `counter_malformed`, `time_malformed`, `body_hash_malformed` or `signature_malformed`, in that order.
6. `Content-Type` is `application/json` and `Content-Encoding` is `gzip`: otherwise `content_type_unsupported` or `encoding_unsupported`.
7. `X-Pairtime-Time` is within 300,000 ms of the server's clock, either way: otherwise `clock_skew`. The response's `X-Pairtime-Time` is what the client corrects by.
8. The raw body fits 2.4's limit: otherwise `body_too_large`.
9. `hex(SHA-256(raw body))` equals `X-Pairtime-Body-SHA256`: otherwise `body_hash_mismatch`.
10. The signature verifies over `M` under the device's registered key: otherwise `signature_invalid`.
11. The raw body is one gzip member with nothing after it, and inflates within 2.4's limit: otherwise `gzip_invalid` or `decompressed_too_large`.
12. The inflated bytes are one JSON object in valid UTF-8 (`json_invalid`), with only 2.3's members in their types (`body_shape`), and within 2.4's counts (`too_many_batches`, `too_many_buckets`, `too_many_commits`).
13. In the writer's transaction: if the device's most recent processed request had this `Idempotency-Key`, the server answers with that request's response, byte for byte, and changes nothing, when the raw body's SHA-256 is the one it had; when it isn't, 422 `idempotency_key_reused`. Otherwise `X-Pairtime-Req` must be greater than the device's stored counter: otherwise 409 `counter_stale`.
14. The items, in order: the batch roots in array order, then the buckets, then the commits, then the labels (2.7). The counter becomes `X-Pairtime-Req`, and the response is stored with the `Idempotency-Key` as the device's most recent.

### 2.6 Request refusals

A request refused as a whole gets one of these statuses and this body, where `detail` is an English sentence for logs and people, never a value the request sent:

```json
{"error": "clock_skew", "detail": "X-Pairtime-Time is 312000 ms from the server's clock; correct the clock from this response's X-Pairtime-Time and sign again."}
```

| Status | `error` | When | What the client does |
|---|---|---|---|
| 503 | `ingest_off` | Ingest isn't set up on this server (step 1) | Retry later, after `Retry-After` when present |
| 401 | `token_missing` | No `Bearer` token | Sign in again (`pairtime login`) |
| 401 | `token_invalid` | The token fails its checksum or names no device | Sign in again |
| 403 | `device_revoked` | The device was revoked | Stop uploading; the device signs in again as a new device |
| 403 | `device_mismatch` | `X-Pairtime-Device` is missing or isn't the token's device | Fix the client |
| 403 | `device_key_missing` | The device hasn't registered its key | Register it (WP-SV3), then retry |
| 400 | `idempotency_key_malformed` | Missing, repeated, or not a lowercase UUIDv7 | Fix the client |
| 400 | `counter_malformed` | Missing, repeated, or not 1 to 2^63 - 1 in the form of 2.1 | Fix the client |
| 400 | `time_malformed` | Missing, repeated, or not a decimal integer in the form of 2.1 | Fix the client |
| 400 | `body_hash_malformed` | Missing, repeated, or not 64 lowercase hex digits | Fix the client |
| 400 | `signature_malformed` | Missing, repeated, not `ed25519=`, or not 64 bytes in padded base64 | Fix the client |
| 415 | `content_type_unsupported` | `Content-Type` isn't `application/json` | Fix the client |
| 415 | `encoding_unsupported` | `Content-Encoding` isn't `gzip` | Fix the client |
| 401 | `clock_skew` | `X-Pairtime-Time` is more than 300 s from the server's clock | Correct the offset from the response's `X-Pairtime-Time`, then sign again with a new counter |
| 413 | `body_too_large` | The raw body is over 262,144 bytes | Split the upload |
| 400 | `body_hash_mismatch` | The raw body's SHA-256 isn't `X-Pairtime-Body-SHA256` | Retry; the body changed on the way |
| 401 | `signature_invalid` | The signature doesn't verify under the device's registered key | Check the key against the registered one; never retry blindly |
| 400 | `gzip_invalid` | Not one valid gzip member, or bytes after it | Fix the client |
| 413 | `decompressed_too_large` | It inflates past 4,194,304 bytes | Split the upload |
| 400 | `json_invalid` | Not one JSON object, or not valid UTF-8 | Fix the client |
| 422 | `body_shape` | A top-level member that isn't in 2.3, or one of the wrong JSON type | Fix the client |
| 413 | `too_many_batches` | More than 2,000 batch roots | Split the upload |
| 413 | `too_many_buckets` | More than 2,000 minute buckets | Split the upload |
| 413 | `too_many_commits` | More than 500 commit records | Split the upload |
| 422 | `idempotency_key_reused` | The key is the device's most recent request's, with another body | Use a new key for a new body |
| 409 | `counter_stale` | `X-Pairtime-Req` is at or below the stored counter | Read `GET /api/v1/ingest/state`, continue above its `req`, and sign again |
| 503 | `unavailable` | The database didn't take the write | Retry the same body and key after `Retry-After` |

Other methods on the path get 405 from the router. A refused request stores nothing and moves no counter, so a retry after any refusal is checked from step 1 again.

### 2.7 Processing

Steps 13 and 14 run in one transaction of the writer goroutine (DESIGN 3.10), which takes one op at a time, so two requests from one device can't interleave. The transaction stores every accepted item, the counter and the response together, or none of them, and the request then gets 503 `unavailable`.

`received_at` is the server's clock, in Unix ms, when the transaction runs. Every batch root and bucket revision a request stores gets the same `received_at`.

**Batch roots** are checked in array order, so one request can carry a run `b, b + 1, ...`, each linking to the head the one before it set. A batch root `{b, n, R_b, H_b, prev}`:

1. passes 2.3.1's forms (codes of 2.9);
2. has `n` at least 1 (`batch_empty`), and `H_b = SHA-256(0x03 || prev || R_b || u64(n) || u64(b))`, ledger-v1 6 (`head_mismatch`);
3. if the server already stored batch `b` of this device: is `duplicate` when `n`, `R_b`, `H_b` and `prev` all equal the stored ones, and answers with the stored receipt; otherwise `batch_conflict`, and the stored row stays;
4. if the server holds no batch of this device: starts the device's witnessed chain at `b`. Batch 1 must have `prev` = 64 zeros (`prev_mismatch`); a later `b` takes its `prev` as given, ledger-v1 6's one exception, for a device that left local-only mode with batches it never sent;
5. otherwise, with the stored head at batch `h`: `b` above `h + 1` is `chain_gap`; `b` at or below `h` with no stored row, because it is before the chain's start or its row passed its retention period, is `before_chain`; `b = h + 1` needs `prev` equal to the stored `H_h` (`prev_mismatch`).

An accepted batch root is stored as the witness row `(device_id, b, n_b, R_b, H_b, received_at)` with `prev` and its receipt, and becomes the device's head.

**Minute buckets.** A bucket passes 2.3.2's forms and `MinuteBucket.Validate`, names this device (`wrong_device`), and has a `minute` that starts no more than 300 s after the server's clock (`future_minute`) and less than 180 days before it (`expired_minute`), the period DESIGN 4.4 keeps per-minute detail. Then, for the same device and minute:

- with nothing stored, it is stored: `accepted`;
- with a lower `rev` stored, it replaces it: `accepted`;
- with the same `rev` stored and the same bucket, nothing changes: `duplicate`;
- with the same `rev` stored and a different bucket, the stored one stays: `rev_conflict`;
- with a higher `rev` stored, nothing changes: `superseded`.

The server keeps every revision it accepted with its `received_at`, beside the current one, for the bundle's `buckets.json` (E-check-readings). Each bucket is stored under the device owner's account, and the account's rollups move from the replaced revision to the new one in the same transaction (WP-SV7).

**Commit records** pass 2.3.3's forms and are stored per device and `sha`: `accepted` the first time, `duplicate` when the stored record is the same, `updated` when it differs, and the new one replaces it.

**Labels** pass 2.3.4's forms and replace the device's stored labels: `accepted`.

### 2.8 The response

A request that reached step 14 gets 200, even when every item was refused, with this body:

```json
{
  "received_at": 1795089660250,
  "req": 42,
  "head": {"b": 1, "H_b": "f3c1…"},
  "batches": [
    {"status": 200, "code": "accepted", "b": 1,
     "receipt": {"received_at": 1795089660250, "key_id": "0123456789abcdef", "sig": "<128 hex digits>"}}
  ],
  "buckets": [{"status": 200, "code": "accepted"}, {"status": 422, "code": "bad_string", "field": "slots[2].p"}],
  "commits": [],
  "labels": null
}
```

- `received_at` is the transaction's time (2.7), and `req` the counter it stored.
- `head` is the device's stored head after the request, `{"b", "H_b"}`, or `null` while it has none.
- `batches`, `buckets` and `commits` are always present. Element `k` answers item `k` of the request's array of the same name, and an array the request didn't send is `[]`. `labels` is the labels' result, or `null` when the request sent none.
- Each result has `status`, an HTTP status for that item alone (200 stored or already there, 409 refused because of what the server holds, 422 refused for what the item is), and `code` from 2.9. A shape code adds `field`, the item's first offending member as a JSON path in DESIGN 4.3's names, such as `slots[2].p`, `uncredited` or `parents[0]`; for `unknown_field` it is the path of the object that holds the member, `""` for the item itself, and never the member's own name. No result repeats a string the request sent.
- A batch result has `b` once `b` passed its form, and a `receipt` when its code is `accepted` or `duplicate`.

A receipt is ledger-v1 7's, in JSON:

| Member | Form |
|---|---|
| `received_at` | The `received_at` the receipt signs, Unix ms. For a `duplicate`, the first one |
| `key_id` | The server key's id: the first 16 hex digits of the SHA-256 of its 32-byte public key, as `pairtimed keys show` prints it |
| `sig` | `Ed25519(server key, M_b)` with `M_b = "pairtime-receipt-v1" \|\| u8(len(device_id)) \|\| device_id \|\| u64(b) \|\| H_b \|\| u64(received_at)`, 128 hex digits |

`pkg/ledger.VerifyReceipt(serverKey, deviceID, b, H_b, ledger.Receipt{ReceivedAt: received_at, Signature: sig})` checks it. The client stores `received_at` and the signature with the batch in `ledger/batches.seg` (ledger-v1 7).

### 2.9 Item codes

Every kind of item can get these shape codes, all with status 422:

| `code` | Meaning |
|---|---|
| `not_object` | The item isn't a JSON object |
| `missing_field` | A required member is absent |
| `unknown_field` | A member the allowlist doesn't have |
| `bad_type` | A member of the wrong JSON type, `null` included where 2.3 doesn't allow it |
| `bad_string` | A string that isn't the one form its member allows: not a key, an enum value, a hash or HMAC of the right length, a version or a time of fixed form |
| `bad_number` | Not an integer where one goes, or out of range, or a `cr` off its 0.5 steps |
| `too_many` | A list or map with more entries than its bound |

And by kind:

| Item | Status | `code` | Meaning |
|---|---|---|---|
| Batch root | 200 | `accepted` | Stored; the receipt is in the result |
| Batch root | 200 | `duplicate` | Already stored with the same values; the stored receipt is in the result |
| Batch root | 422 | `batch_empty` | `n` is 0 |
| Batch root | 422 | `head_mismatch` | `H_b` doesn't follow from `prev`, `R_b`, `n` and `b` |
| Batch root | 409 | `batch_conflict` | Batch `b` is stored with other values. The device's chain forked; the stored row stays |
| Batch root | 409 | `prev_mismatch` | `prev` isn't the stored head, or isn't 64 zeros for batch 1 |
| Batch root | 409 | `chain_gap` | `b` is beyond the stored head plus 1: a batch in between is missing |
| Batch root | 409 | `before_chain` | `b` is at or below the stored head, and the server holds no row for it |
| Bucket | 200 | `accepted` | Stored as the minute's current revision |
| Bucket | 200 | `duplicate` | The same revision is already stored |
| Bucket | 200 | `superseded` | A higher revision is stored; nothing changed |
| Bucket | 409 | `rev_conflict` | The same `rev` is stored with a different bucket; the stored one stays |
| Bucket | 422 | `bad_rule` | It breaks a rule of `event.MinuteBucket.Validate`; `field` names the member that rule names |
| Bucket | 422 | `wrong_device` | `device` isn't `X-Pairtime-Device` |
| Bucket | 422 | `future_minute` | `minute` starts more than 300 s after the server's clock |
| Bucket | 422 | `expired_minute` | `minute` is 180 days or more before the server's clock |
| Commit | 200 | `accepted` | Stored |
| Commit | 200 | `duplicate` | The same record is already stored |
| Commit | 200 | `updated` | It replaced a different record of the same commit from this device |
| Labels | 200 | `accepted` | Stored as the device's labels |

Every code is final for its item: resending the same item gets the same answer, or `duplicate`. Only 2.6's refusals of the whole request are worth a retry.

### 2.10 Retries and idempotency

A device sends one ingest request at a time and waits for its answer.

A retry of the same body keeps its `Idempotency-Key` and body and is signed again with a new counter and a fresh time; re-sending the identical signed bytes also works while its `X-Pairtime-Time` is within 300 s. When the first attempt reached step 14, the retry gets that response back byte for byte, with `X-Pairtime-Replay: true` added, and nothing changes. When it didn't, the retry is processed as a new request.

The server keeps the response of each device's most recent processed request only. A request with an older key is processed as a new one, and that is safe because every item is idempotent on its own: a batch root already stored is a `duplicate` with its first receipt, a bucket revision already stored is a `duplicate` or `superseded`, and a commit record already stored is a `duplicate`. So a client that lost receipts gets them back by sending the same batch roots again.

## 3. `GET /api/v1/ingest/state`

What a client needs to resume: the device's counter and the head of its chain.

```text
GET /api/v1/ingest/state
Authorization: Bearer ptd_...
X-Pairtime-Device: <device_id>
User-Agent: pairtime-agent/<version>
```

No signature: the answer holds nothing the token's holder couldn't learn by uploading. Steps 1 to 3 of 2.5 apply, with the same refusals. The response is 200:

```json
{
  "device": "d_example",
  "req": 42,
  "head": {"b": 1, "H_b": "f3c1…", "received_at": 1795089660250},
  "chain_start": 1,
  "server_key": {"key_id": "0123456789abcdef", "public_key": "<64 hex digits>"}
}
```

| Member | Meaning |
|---|---|
| `device` | The device's `device_id` |
| `req` | The highest counter the server processed for the device, 0 before its first request. The next request uses a higher one |
| `head` | The stored head, `{"b", "H_b", "received_at"}`, or `null` before the first batch. The next batch root is `b + 1` with `prev = H_b` |
| `chain_start` | The first batch of the device's witnessed chain, or `null` before the first batch (2.7 step 4) |
| `server_key` | The key receipts are signed with now: its id and its 32-byte Ed25519 public key in hex. Until `/.well-known/pairtime-keys.json` (WP-SV17) publishes the keys with their validity dates, this is where a client gets the key to check receipts against |

## 4. `GET /api/v1/time`

```text
GET /api/v1/time
User-Agent: pairtime-agent/<version>
```

No authentication. The response is 200 with `{"server_time": 1795089660250}`, the server's clock in Unix ms, which `X-Pairtime-Time` carries too. A client can set its offset from it before its first upload.

## 5. What the uploader does

1. Keep the counter on disk and write the next value before sending. At start, and after `counter_stale`, read `GET /api/v1/ingest/state` and continue above `max(local, req)`.
2. Keep a clock offset from the `X-Pairtime-Time` of the latest response, and send the corrected time.
3. Send batch roots in ascending `b`, from the stored head's `b + 1`. After `prev_mismatch`, `chain_gap` or `before_chain`, read the state and resend from its head. After `batch_conflict`, stop sending batch roots and report the fork: the device's ledger and the server's witness rows disagree, which is a bug or tampering.
4. Check every receipt with `pkg/ledger.VerifyReceipt` under the server key and store it.
5. Treat every item code as final, and resend nothing that got one. Retry a whole request only after a refusal in 2.6 that says to, after a 429 or a 5xx, or after a network error, keeping the key and body (2.10), with backoff and `Retry-After` (WP-AG5).
6. Send labels in the first request after the agent starts, whenever they change, and at least once a day: the server deletes a device's labels 90 days after it last received them (DESIGN 4.4's device row).

## 6. Storage

For reviewers and later work packages; the client never sees it. Migration `0004_ingest.sql` creates:

| Table | Key | What it holds | Retention (docs/retention.json) |
|---|---|---|---|
| `ingest_devices` | `device_id` | The account, the counter, the head, the chain start, and the most recent request's key, body hash and response | `server.witness`: 180 days after the device's last processed request |
| `witness_rows` | `(device_id, b)` | `n`, `R_b`, `H_b`, `prev`, `received_at_ms`, the receipt and its key id, and the account | `server.witness`: 180 days after `received_at_ms`, held while a submission uses them (WP-SB4) |
| `minute_buckets` | `(device_id, minute_ms)` | The current revision of each minute, under the device owner's account | `server.minute_buckets`: 180 days after `minute_ms`, held while a submission uses them (WP-SB4) |
| `minute_bucket_revisions` | `(device_id, minute_ms, rev)` | Every accepted revision with its `received_at_ms` (E-check-readings) | `server.minute_buckets`, as `minute_buckets` |
| `commit_records` | `(device_id, sha)` | The account, the project key and the record | `server.commits`: while the account exists |
| `device_labels` | `device_id` | The account and the latest labels | `server.devices`: 90 days after they were last received |

## 7. Decisions this spec makes

DESIGN 4.3 and 4.5 give the fields and the rules. These are what they leave open, decided here:

1. `X-Pairtime-Body-SHA256` carries the body's hash beside the signature. DESIGN 4.5 refuses "a body whose hash differs", and PLAN's WP-SV4 checks want that refusal and a signature by another key to get different codes. With the hash only inside `M`, a changed body and a wrong key fail the same way.
2. The raw body that `M` and the hash cover is the gzip stream as sent, so a request is authenticated before the server inflates it.
3. The signature is padded standard base64; the counter and the time are decimal ASCII with no sign or leading zeros, so each value has one spelling in `M`.
4. The `Idempotency-Key` is a lowercase UUIDv7, and the server keeps the response of each device's most recent request only. Items are idempotent on their own (2.10), so an older key costs nothing, and the server doesn't hold a reply for every upload.
5. Only a request the server processes moves the counter; a refusal or a replay doesn't. A replay is answered before the counter is checked, so re-sending identical bytes works.
6. A device's first batch may be any `b`, and for `b > 1` its `prev` is taken as given; from then on every batch links to the one before it (ledger-v1 6 leaves this to WP-SV4). A device leaving local-only mode can send its old batch roots from batch 1, which then get late `received_at` values, or start its chain at its current batch.
7. 2,000 batch roots per request, beside DESIGN 4.5's limits. It bounds one writer transaction, and 256 KiB of compressed random hashes doesn't hold many more.
8. One `received_at` per request, the writer transaction's time.
9. Item results are arrays parallel to the request's, so a refused item is named by its place and the response never repeats a string from the request (DESIGN 4.5's "names the rejected item").
10. A bucket's minute must start between 180 days before the server's clock and 300 s after it (SP 8.4's 5 minutes; DESIGN 4.4's 180 days).
11. For one device and minute, a higher `rev` replaces a lower one, and the same `rev` with a different bucket keeps the first (`rev_conflict`).
12. The server keeps every bucket revision it accepted, with its `received_at`, beside the current one, because E-check-readings has the bundle's `buckets.json` carry "the buckets as received live ... with `received_at` and revisions".
13. The bucket's `witness` is stored as the client sent it. The server's own label, from `received_at` (DESIGN 7.4), is WP-SV6's.
14. `GET /api/v1/ingest/state` gives the server key until WP-SV17 publishes `/.well-known/pairtime-keys.json`, and each receipt names its key by id.
15. `GET /api/v1/time` needs no authentication, and every response carries the server's time in `X-Pairtime-Time`.
