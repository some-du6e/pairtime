# Ingest wire format, version 1

Status: the contract between `pairtimed`'s ingest (WP-SV4, and the shape half of WP-SV5's firewall) and the client's uploader (WP-AG5). Changing it needs both sides; an uploader that needs a change says so in its PR, and the orchestrator settles it.

This is how a pairtime client sends tier-1 data to `pairtimed`: `POST /api/v1/ingest/batches`, `GET /api/v1/ingest/state` and `GET /api/v1/time`. DESIGN 4.3 says what tier 1 holds and what never crosses the network, DESIGN 4.5 how a request is signed and what the firewall refuses, and DESIGN 3.6 with docs/spec/ledger-v1.md sections 6 and 7 what a batch root and a receipt are. This file fixes the bytes: every header, the signed message, the JSON of the body and of each response, the order of the checks, and a machine-readable code for each refusal. Section 7 lists what it decides that DESIGN leaves open.

## 1. Conventions

- Times are Unix milliseconds, UTC, written as decimal integers, the unit of `pkg/event`'s `t` (ledger-v1 1). The times in a body, a bucket's `minute` and a commit's `observed_at`, are on the device's own clock and never corrected. Only `X-Pairtime-Time` is corrected (2.1). Two exceptions keep the forms their sources use: a bucket's `minute` is an RFC 3339 string, as `encoding/json` writes `pkg/event`'s `time.Time`, and a commit's `author_time` and `committer_time` are Unix seconds, git's unit (`internal/adapters/git`).
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
| `Content-Encoding` | `gzip`: the body is one gzip member (RFC 1952), with its reserved flag bits zero, holding the JSON of 2.3, with nothing after it. |
| `X-Pairtime-Device` | The device's `device_id`: the per-install id the client's ledger made at install and writes into every event, which `pairtime login` registers with the token (WP-SV3). Receipts sign it (2.8), and every bucket names it (2.3.2). |
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

`"\n"` is the one byte `0x0a`. `device_id`, `req` and `time` are the ASCII of the `X-Pairtime-Device`, `X-Pairtime-Req` and `X-Pairtime-Time` values exactly as sent. The hex is lowercase. `M` has no trailing newline. The device key is the Ed25519 key the device grant bound to the token (WP-SV3; D-key-at-grant); `crypto/ed25519.Sign` computes `sig`.

Worked example. The device key's seed is `SHA-256(ascii("pairtime ingest-v1 example device key"))`:

```text
seed       = 7bf821b60e33d7e00abe88a5e7e8e31715789eacda1a3ac3eea47eca45c210c1
public key = fadacf1c88bce8152ec27995a05aa8d9c53ac5678801cdd2e05b19060398a677
```

The body holds one batch root, a valid batch 1 with `n = 3`, `R_b` = 32 bytes of `0xaa`, `prev` = zero(32) and `H_b = SHA-256(0x03 || prev || R_b || u64(3) || u64(1))` (ledger-v1 6), gzipped into 129 bytes: the 10-byte header `1f8b 08 00 00000000 02 03` (no flags, mtime 0, XFL 2, OS 3), zlib's level-9 raw deflate of the JSON, then its CRC-32 and length. That is what Python 3.11 and 3.12's `gzip.compress(json, 9, mtime=0)` give on Linux; other writers give other bytes. Any of them is fine on the wire, because the hash is over whatever bytes go there, so a test of signing starts from `raw` as given.

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

The gzip member holds one JSON object with up to four members, each optional. An absent member, a `null` one and an empty one mean the same:

```json
{
  "batches": [ <batch root>, ... ],
  "buckets": [ <minute bucket>, ... ],
  "commits": [ <commit record>, ... ],
  "labels": <labels>
}
```

`batches`, `buckets` and `commits` are arrays, and `labels` is an object. Any other member, a member of another JSON type, or a member that appears twice refuses the whole request with `body_shape` (2.6). An empty object is a valid body: it moves the counter and stores nothing.

Inside an item, every member is in the allowlist below with its JSON type and, for a string, the one form it may take. A member the allowlist doesn't have, a missing required member, a string of any other form and a number out of range each refuse that item, with the item codes of 2.9; the rest of the request goes on. No string anywhere can hold free text: each is a key, an enum value, a hash or HMAC, a version, or a time of fixed form (DESIGN 4.5; SP 8.2). An item must not repeat a member name in any of its objects, maps included: one that does is refused as `bad_rule`, naming the object (2.9), where `encoding/json` alone would keep the last value. The server stores what it parsed, re-encoded, never the bytes it received.

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

A bucket is the JSON that `encoding/json` writes for `pkg/event`'s `MinuteBucket.Shareable()`: DESIGN 4.3's `{v, params, minute, device, slots[4]{p, cr, why, kind, ev, lbl}, credited_s, uncredited{code: s}, agent_running_s, agent_only{p: s}, state, witness, rev}`, which `event.Tier1BucketFields` and `event.Tier1SlotFields` pin, `agent_only` being the decision D-bucket-agent-only's. It never holds `anchors`, the event ids that stay on the device (DESIGN 4.1 E-5): a bucket with that member is refused as `unknown_field`.

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
| `agent_only` | object | Optional. For each project key, the seconds in the minute a turn of that project ran and weren't credited to that project: SM 2.6's agent-only seconds, which the chess clock shows (D-bucket-agent-only). Each member's name is a project key, as `slots[].p`, and its value seconds, 0 to 60 in steps of 0.5 and no more than `agent_running_s`; at most 64 members. Absent and `{}` both mean none |
| `state` | string | `pending`, `final`, `provisional` or `restored` |
| `witness` | string | `witnessed`, `offline` or `backfilled`. It is the client's own reading, stored as sent; the server's witness label is WP-SV6's |
| `rev` | integer | The revision, 0 to 4294967295 |

Each slot, `encoding/json`'s form of `event.BucketSlot`:

| Member | Type | Form |
|---|---|---|
| `p` | string | Optional. A project key |
| `cr` | number | Credited seconds, 0 to 15 in steps of 0.5 |
| `why` | string | Optional. A credit reason code of SM 5.2, with `pkg/event`'s `~r` and `+restored` suffixes where they apply, such as `READ~r` (`Reason.Credited`) |
| `kind` | string | Optional. An activity kind of SM 5.1 |
| `ev` | string | Optional. The evidence level, `A`, `B` or `C` |
| `lbl` | array | Optional. Labels, each of `via_track`, `via_plugin`, `via_bridge` (the side doors) and `focus_unknown` (seconds that are level C because the client couldn't tell which window was in front, SM 2.5) at most once and in that order (`pkg/event`'s `SlotLabels`) |

On top of the forms, a bucket must pass `event.MinuteBucket.Validate`: the slots' `cr` sum to `credited_s`, a credited slot has a project, a reason and its kind, credited and uncredited seconds fit in a minute, a backfilled minute's credit is level C, and the rest of that function's rules. A bucket that fails one is refused as `bad_rule`.

#### 2.3.3 Commit records

DESIGN 4.3's `{sha, parents, project, observed_at, author_time, committer_time, files_changed, insertions, deletions, path_hmacs[], credited_seconds_since_parent, rewrites[], actor, agent_trailer}`, in the JSON of `internal/adapters/git`'s `Commit` with the two members it doesn't fill yet:

| Member | Type | Form |
|---|---|---|
| `sha` | string | The commit id, 40 or 64 hex digits |
| `parents` | array or `null` | Distinct commit ids of the same form, at most 1,024; `null` or `[]` for a root commit |
| `project` | string | A project key |
| `observed_at` | integer | When the device first saw the commit, Unix ms from 2020 to 2099, on the device's clock |
| `author_time` | integer | The commit's author date in Unix seconds as git stores it, 0 to 9223372036854775807. The server only compares it with its own clocks |
| `committer_time` | integer | The commit's committer date, in the same form |
| `files_changed` | integer | Optional: absent when the client couldn't read the commit's shortstat. 0 to 2147483647 |
| `insertions` | integer | Optional, as `files_changed`. 0 to 2147483647 |
| `deletions` | integer | Optional, as `files_changed`. 0 to 2147483647 |
| `path_hmacs` | array | Optional. Distinct keys of the changed paths in WP-AD2's keyed form, at most 1,024. A commit that changed more than 1,024 paths leaves the member out, and the record still goes up |
| `credited_seconds_since_parent` | object | Optional. Split by activity kind (DESIGN 5.8; SM 5.3): each member's name is an activity kind of SM 5.1 and its value seconds, 0 to 1e9; at most 8 members |
| `rewrites` | array | Optional. At most 2,000 distinct objects `{"old_sha", "new_sha"}`, each a commit id: the commits this one replaced through an amend, a rebase or a squash. 2,000 is the most a rebase maps (`internal/adapters/git`'s `maxRebase`) |
| `actor` | string | `human`, `agent` or `unknown` |
| `agent_trailer` | string | Optional. One of `aider`, `claude`, `lovable` and `replit`: the agent a co-author trailer, Replit's trailer or a bot author credits, the values `internal/adapters/git` writes. Never the trailer itself, which can hold a person's name, so the list is closed and a new agent reaches the server first |

A list with an entry twice is refused as `bad_rule`, with the list as `field`.

#### 2.3.4 Labels

DESIGN 4.3's `{client_build, scoring_version, params, harnesses[{name, version}], typing_module, vm}`:

| Member | Type | Form |
|---|---|---|
| `client_build` | string | The client's version, a *version*: 1 to 64 characters matching `^v?[0-9]+(\.[0-9]+){0,3}([-+][0-9A-Za-z][0-9A-Za-z.-]{0,40})?$`, such as `2.1.327`, `v0.1.0-alpha.1`, `0.1.0+3dbf6d4` or a Go pseudo-version (D-version-grammar; `pkg/event`'s `IsVersion`). A bare word isn't a version, so no file, folder or host name passes, and a dev build sends `v0.0.0-dev`, not `dev` |
| `scoring_version` | string | `N.N.N`, as a bucket's `v` |
| `params` | string | `sha256:` and 64 hex digits |
| `harnesses` | array | At most 16 objects `{"name", "version"}`: `name` a harness of `pkg/event`'s `Harness` list other than `none`, at most once (`bad_rule` otherwise); `version` optional, a version as `client_build` is. The client sends each harness it saw once, `other` included, with the version it saw last when that is a version, and with no version otherwise, as `pkg/event` keeps a `harness_version` only when it is one |
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

The server reads a body's members in order and refuses an array at its first item past its limit, so it never holds more items than the limits allow, however many the body has. A client splits a larger upload across requests.

### 2.5 Checks, in order

The server checks a request in this order and answers the first failure with the refusal of 2.6. Steps 1 to 12 read but write nothing, but for the device store noting when the device last connected once its token checks out (step 2; WP-SV3), and so do the item forms of 2.3 and `MinuteBucket.Validate`, which the server checks after step 12 and before the writer; step 13 and after run as one transaction in the server's writer.

1. Ingest is set up on this server: a device store and the server's signing key. Otherwise 503 `ingest_off`.
2. `Authorization` holds a `Bearer` token: otherwise `token_missing`. The token passes its CRC32 checksum before any lookup and names a device: otherwise `token_invalid`. The device isn't revoked: otherwise `device_revoked`.
3. `X-Pairtime-Device` equals the token's `device_id`: otherwise `device_mismatch`.
4. The device's row holds the Ed25519 key its sign-in bound (D-key-at-grant): otherwise `device_key_missing`.
5. `Idempotency-Key`, `X-Pairtime-Req`, `X-Pairtime-Time`, `X-Pairtime-Body-SHA256` and `X-Pairtime-Signature` are each present once and in the form of 2.1: otherwise `idempotency_key_malformed`, `counter_malformed`, `time_malformed`, `body_hash_malformed` or `signature_malformed`, in that order.
6. `Content-Type` is `application/json` and `Content-Encoding` is `gzip`: otherwise `content_type_unsupported` or `encoding_unsupported`.
7. `X-Pairtime-Time` is within 300,000 ms of the server's clock, either way: otherwise `clock_skew`. The response's `X-Pairtime-Time` is what the client corrects by.
8. The raw body fits 2.4's limit: otherwise `body_too_large`, or `body_unreadable` when reading it fails.
9. `hex(SHA-256(raw body))` equals `X-Pairtime-Body-SHA256`: otherwise `body_hash_mismatch`.
10. The signature verifies over `M` under the device's key: otherwise `signature_invalid`. Then the device is within its rate, a bucket of 30 requests that refills one every 10 s (SP 8.6), with no other request of its in flight: otherwise 429 `rate_limited`. Only a signed request counts against the rate, so nobody without the device's key can use it up.
11. The raw body is one gzip member with nothing after it, and inflates within 2.4's limit: otherwise `gzip_invalid` or `decompressed_too_large`.
12. The inflated bytes are one JSON value in valid UTF-8 (`json_invalid`), and an object. The server reads its members in order and refuses the first that fails: a member that isn't in 2.3, is of another JSON type or appears a second time (`body_shape`), or an array with more items than 2.4 allows, refused at its first item past the limit (`too_many_batches`, `too_many_buckets`, `too_many_commits`).
13. In the writer's transaction: every row the server holds for the device belongs to the token's account, as D-device-account-binding has it: otherwise 403 `device_mismatch`, and nothing is read back or stored. The device's row still holds the request's token, so it wasn't removed or linked again since step 2: otherwise 403 `device_revoked`, and nothing the request wrote stays. Then, if the device's most recent processed request had this `Idempotency-Key`, the server answers with that request's response, byte for byte, and changes nothing, when the raw body's SHA-256 is the one it had; when it isn't, 422 `idempotency_key_reused`. Otherwise `X-Pairtime-Req` must be greater than the device's stored counter: otherwise 409 `counter_stale`. The server may give these answers from a read before the writer, and checks again inside it.
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
| 401 | `token_invalid` | The token fails its checksum or names no device, as a removed device's token, or one from before the device signed in again, no longer does | Sign in again (`pairtime login`), which links the same install with the same `device_id` (see `device_revoked`) |
| 403 | `device_revoked` | The device's account can no longer sign in, or the device was removed or linked again with a new token while the request was on its way (step 13). A removed device keeps no token, so a request that arrives after the removal gets `token_invalid`, and WP-SV3 removes the devices of an account that turns ineligible at once, so this answer is rare | Stop uploading. `pairtime login` links the same install again to the account it was linked to, with the same `device_id`, which keeps its chain (5.3). Once the server holds uploads from that `device_id`, another account can link the install only as a new device, with a new `device_id` and `K_dev` (D-device-account-binding; device-auth-v1) |
| 403 | `device_mismatch` | `X-Pairtime-Device` is missing or isn't the token's device; or rows the server holds for this `device_id` belong to another account than the token's (step 13; D-device-account-binding) | Fix the client. A client that sent its own `device_id` hit the second case, and, with the teen's go-ahead, signs in again as a new device, as after a fork (5.3) |
| 403 | `device_key_missing` | The device has no key on record | Sign in again (`pairtime login`). WP-SV3 binds the key in the device grant (D-key-at-grant): `pairtime login` sends its public key with the code request, and the code, the approval and the token all name it. No token can add or swap a key, so a device linked that way always has one, and this answer means a fault |
| 400 | `idempotency_key_malformed` | Missing, repeated, or not a lowercase UUIDv7 | Fix the client |
| 400 | `counter_malformed` | Missing, repeated, or not 1 to 2^63 - 1 in the form of 2.1 | Fix the client |
| 400 | `time_malformed` | Missing, repeated, or not a decimal integer in the form of 2.1 | Fix the client |
| 400 | `body_hash_malformed` | Missing, repeated, or not 64 lowercase hex digits | Fix the client |
| 400 | `signature_malformed` | Missing, repeated, not `ed25519=`, or not 64 bytes in padded base64 | Fix the client |
| 415 | `content_type_unsupported` | `Content-Type` isn't `application/json` | Fix the client |
| 415 | `encoding_unsupported` | `Content-Encoding` isn't `gzip` | Fix the client |
| 401 | `clock_skew` | `X-Pairtime-Time` is more than 300 s from the server's clock | Correct the offset from the response's `X-Pairtime-Time`, then sign again with a new counter |
| 413 | `body_too_large` | The raw body is over 262,144 bytes | Split the upload |
| 400 | `body_unreadable` | The body couldn't be read, as when the connection broke off | Retry the same body and key |
| 400 | `body_hash_mismatch` | The raw body's SHA-256 isn't `X-Pairtime-Body-SHA256` | Retry; the body changed on the way |
| 401 | `signature_invalid` | The signature doesn't verify under the device's key. The body adds `device_key_id`, that key's id in `key_id`'s form (2.8), a number of the server's and nothing the request sent | Compare `device_key_id` with the id of the device's own key. On a mismatch, sign in again; on a match, stop and report it (`pairtime doctor`). Never retry blindly |
| 400 | `gzip_invalid` | Not one valid gzip member, or bytes after it | Fix the client |
| 413 | `decompressed_too_large` | It inflates past 4,194,304 bytes | Split the upload |
| 400 | `json_invalid` | Not valid JSON, more than one JSON value, or not valid UTF-8 | Fix the client |
| 422 | `body_shape` | Not an object, or a top-level member that isn't in 2.3, is of the wrong JSON type or appears twice | Fix the client |
| 413 | `too_many_batches` | More than 2,000 batch roots | Split the upload |
| 413 | `too_many_buckets` | More than 2,000 minute buckets | Split the upload |
| 413 | `too_many_commits` | More than 500 commit records | Split the upload |
| 422 | `idempotency_key_reused` | The key is the device's most recent request's, with another body | Use a new key for a new body |
| 409 | `counter_stale` | `X-Pairtime-Req` is at or below the stored counter | Read `GET /api/v1/ingest/state`, continue above its `req`, and sign again |
| 429 | `rate_limited` | The device is past its rate, or another of its requests is in flight (step 10) | Retry the same body and key after `Retry-After` |
| 503 | `unavailable` | The device store or the database failed | Retry the same body and key after `Retry-After` |

Other methods on the path get 405 from the router. A refused request stores nothing and moves no counter, so a retry after any refusal is checked from step 1 again.

### 2.7 Processing

Steps 13 and 14 run in one transaction of the writer goroutine (DESIGN 3.10), which takes one op at a time, so two requests from one device can't interleave. The transaction stores every accepted item, the counter and the response together, or none of them, and the request then gets 503 `unavailable`.

`received_at` is the server's clock, in Unix ms, when the transaction runs. Every batch root and bucket revision a request stores gets the same `received_at`.

**Batch roots** are checked in array order, so one request can carry a run `b, b + 1, ...`, each linking to the head the one before it set. A batch root `{b, n, R_b, H_b, prev}`:

1. passes 2.3.1's forms (codes of 2.9);
2. has `n` at least 1 (`batch_empty`), and `H_b = SHA-256(0x03 || prev || R_b || u64(n) || u64(b))`, ledger-v1 6 (`head_mismatch`);
3. if the server already stored batch `b` of this device: is `duplicate` when `n`, `R_b`, `H_b` and `prev` all equal the stored ones, and answers with the stored receipt; otherwise `batch_conflict`, and the stored row stays;
4. if the device has no stored head, neither in its chain state nor, should that ever be missing, in a witness row the server still holds: starts the device's witnessed chain at `b`. Batch 1 must have `prev` = 64 zeros (`prev_mismatch`); a later `b` takes its `prev` as given, ledger-v1 6's exception for a device whose first witnessed batch isn't its first batch (7 item 6). Without its chain state, the head is the highest witness row the server still holds;
5. otherwise, with the stored head at batch `h`: `b = h + 1` needs `prev` equal to the stored `H_h` (`prev_mismatch`). `b` above `h + 1` is `chain_gap`, unless the device signed in again since the server stored its head and this is the first batch it stores under the new sign-in (D-local-gap): then `b` takes its `prev` as given, the server records batches `h + 1` to `b - 1` as a local-only gap on the device's chain state, and the chain goes on from `b`. `b` at or below `h` with no stored row, because it is before the chain's start, in a local-only gap, or its row passed its retention period, is `before_chain`.

An accepted batch root is stored as the witness row `(device_id, b, n_b, R_b, H_b, received_at)` with `prev` and its receipt, and becomes the device's head, stored with the sign-in it arrived under. A local-only gap is never filled: the server never asks for its batches, and a root in it is `before_chain` for good.

**Minute buckets.** A bucket passes 2.3.2's forms and `MinuteBucket.Validate`, names this device (`wrong_device`), and has a `minute` that starts no more than 300 s after the server's clock (`future_minute`) and less than 180 days before it (`expired_minute`), the period DESIGN 4.4 keeps per-minute detail. Then, for the same device and minute:

- with nothing stored, it is stored: `accepted`;
- with a lower `rev` stored, it replaces it: `accepted`. A minute keeps at most 128 revisions: once it has 128, a higher `rev` is refused as 409 `too_many_revisions`, and the stored one stays. DESIGN 3.7's 900 s pending window, with an upload at most every 10 s, gives about 90;
- with the same `rev` stored and the same bucket, nothing changes: `duplicate`;
- with the same `rev` stored and a different bucket, the stored one stays: `rev_conflict`;
- with a higher `rev` stored, nothing changes: `superseded`.

The server keeps every revision it accepted, each as a row of its own with its `received_at`, for the bundle's `buckets.json`: DESIGN 7.8's row, as the decision E-check-readings words it, carries the buckets "with `received_at` and revisions". Each bucket is stored under the device owner's account, and the account's rollups move from the replaced revision to the new one in the same transaction (WP-SV7). Every row ingest stores names the account it was uploaded under, and none is ever moved to another account (D-device-account-binding; 2.5 step 13).

**Commit records** pass 2.3.3's forms and are stored per device and `sha` with `server_first_seen_at`, the `received_at` of the request that first stored the record (DESIGN 5.8): `accepted` the first time, `duplicate` when the stored record is the same, and `updated` when it differs, when the new record replaces the stored one and `server_first_seen_at` stays. The account's first sighting of a commit is the earliest across its devices.

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
- Each result has `status`, an HTTP status for that item alone (200 stored or already there, 409 refused because of what the server holds, 422 refused for what the item is), and `code` from 2.9. A shape code or `bad_rule` adds `field`, the item's first offending member as a JSON path in DESIGN 4.3's names, such as `slots[2].p`, `uncredited` or `parents[0]`; for `unknown_field`, and for `bad_rule` on a repeated name, it is the path of the object that holds the member, `""` for the item itself, and never the member's own name. A map's member is named by the map alone, `agent_only` and never `agent_only[p_one]`. No result repeats a string the request sent.
- A batch result has `b` once `b` passed its form, and a `receipt` when its code is `accepted` or `duplicate`.
- A bucket result adds `rev`, the revision the server holds, when its code is `superseded`, `rev_conflict` or `too_many_revisions`.

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
| `bad_rule` | Besides the rules of each kind below: an object of the item, a map included, repeats a member name. `field` is the path of that object, `""` for the item itself, never the name |

And by kind:

| Item | Status | `code` | Meaning |
|---|---|---|---|
| Batch root | 200 | `accepted` | Stored; the receipt is in the result |
| Batch root | 200 | `duplicate` | Already stored with the same values; the stored receipt is in the result |
| Batch root | 422 | `batch_empty` | `n` is 0 |
| Batch root | 422 | `head_mismatch` | `H_b` doesn't follow from `prev`, `R_b`, `n` and `b` |
| Batch root | 409 | `batch_conflict` | Batch `b` is stored with other values. The device's chain forked; the stored row stays |
| Batch root | 409 | `prev_mismatch` | `prev` isn't the stored head, or isn't 64 zeros for batch 1 |
| Batch root | 409 | `chain_gap` | `b` is beyond the stored head plus 1, and the device hasn't signed in again since the head was stored: a batch in between is missing |
| Batch root | 409 | `before_chain` | `b` is at or below the stored head, and the server holds no row for it: it is before the chain's start, in a local-only gap, or past its retention period |
| Bucket | 200 | `accepted` | Stored as the minute's current revision |
| Bucket | 200 | `duplicate` | The same revision is already stored |
| Bucket | 200 | `superseded` | A higher revision is stored; nothing changed |
| Bucket | 409 | `rev_conflict` | The same `rev` is stored with a different bucket; the stored one stays |
| Bucket | 409 | `too_many_revisions` | The minute has 128 revisions stored; the stored one stays |
| Bucket | 422 | `bad_rule` | It breaks a rule of `event.MinuteBucket.Validate`; `field` names the member that rule names |
| Bucket | 422 | `wrong_device` | `device` isn't `X-Pairtime-Device` |
| Bucket | 422 | `future_minute` | `minute` starts more than 300 s after the server's clock |
| Bucket | 422 | `expired_minute` | `minute` is 180 days or more before the server's clock |
| Commit | 200 | `accepted` | Stored |
| Commit | 200 | `duplicate` | The same record is already stored |
| Commit | 200 | `updated` | It replaced a different record of the same commit from this device |
| Commit | 422 | `bad_rule` | `parents`, `path_hmacs` or `rewrites` has an entry twice; `field` names the list |
| Labels | 200 | `accepted` | Stored as the device's labels |
| Labels | 422 | `bad_rule` | A harness is named twice; `field` is `harnesses` |

Sending a batch root or a bucket revision again that got 200 changes nothing: it comes back `duplicate` or `superseded`. Commit records and labels are last-write-wins: a commit record comes back `duplicate` only when it equals the stored one, and labels always come back `accepted`.

No resend changes a 422, apart from `future_minute` once the server's clock is within 300 s of the minute, and none changes `batch_conflict`, `before_chain`, `rev_conflict` or `too_many_revisions` (a newer bucket goes up with a higher `rev`). A `chain_gap` root goes through once the missing batches are stored, so the client sends from the stored head (5.3). A root in a local-only gap stays `before_chain`. `prev_mismatch` on batch 1, or on the batch after the stored head, means the device's chain and the server's have forked (5.3). Of whole requests, only 2.6's refusals that say so are worth a retry.

### 2.10 Retries and idempotency

A device sends one ingest request at a time and waits for its answer.

A retry of the same body keeps its `Idempotency-Key` and body and is signed again with a new counter and a fresh time; re-sending the identical signed bytes also works while its `X-Pairtime-Time` is within 300 s. When the first attempt reached step 14, the retry gets that response back byte for byte, with `X-Pairtime-Replay: true` added, and nothing changes. When it didn't, the retry is processed as a new request.

The server keeps the response of each device's most recent processed request only, for 180 days (6). A request with an older key, or one whose response is gone, is processed as a new one. That is safe for batch roots and bucket revisions, which are idempotent on their own: a batch root already stored is a `duplicate` with its first receipt, and a bucket revision already stored is a `duplicate` or `superseded`. Commit records and labels are last-write-wins, so the client sends them only in their current form. A client that lost receipts gets them back by sending the same batch roots again, alone.

## 3. `GET /api/v1/ingest/state`

What a client needs to resume: the device's counter and the head of its chain.

```text
GET /api/v1/ingest/state
Authorization: Bearer ptd_...
X-Pairtime-Device: <device_id>
X-Pairtime-Time: <unix ms>
X-Pairtime-Signature: ed25519=<base64 Ed25519(device_key, M_state)>
User-Agent: pairtime-agent/<version>

M_state = "pairtime-ingest-state-v1\n" || device_id || "\n" || time
```

The device signs the request, as it signs an upload, because the counter and the head's time change with every upload, and anyone holding a leaked token could otherwise read when the device's owner works (DESIGN 4.5: the device key stops a stolen bearer token). `M_state` has no counter and no body, so it still works after `counter_stale`, and its context string differs from `M`'s, so neither kind of signature passes for the other. Steps 1 to 4 of 2.5 apply, then the forms of `X-Pairtime-Time` and `X-Pairtime-Signature` (step 5), the clock (step 7), and the signature over `M_state` and the device's rate (step 10), with the same refusals. A state request counts against the same rate as an upload, because the server reads the state in its writer transaction. Step 13's account check applies too: rows of the `device_id` under another account refuse it with `device_mismatch`. The response is 200:

```json
{
  "device": "d_example",
  "req": 42,
  "head": {"b": 1, "H_b": "f3c1…", "received_at": 1795089660250},
  "chain_start": 1,
  "relinked": false,
  "gaps": [],
  "server_key": {"key_id": "0123456789abcdef", "public_key": "<64 hex digits>"}
}
```

| Member | Meaning |
|---|---|
| `device` | The device's `device_id` |
| `req` | The highest counter the server processed for the device, 0 before its first request. The next request uses a higher one |
| `head` | The stored head, `{"b", "H_b", "received_at"}`, or `null` before the first batch. The server takes batch `b + 1` next, and only with `prev` equal to this `H_b`; a device whose own head at `b` differs has forked (5.3) |
| `chain_start` | The first batch of the device's witnessed chain, or `null` before the first batch (2.7 step 4) |
| `relinked` | `true` when the device signed in again, a new token for the same `device_id` after `pairtime logout` or a removal on the web, after the server stored its head, and hasn't stored a batch since; `false` otherwise, and while `head` is `null`. While it is `true`, the server takes the device's next batch at any `b` above `head.b`: `head.b + 1` links to the head as always, and a higher `b` takes its `prev` as given, the batches between becoming a local-only gap (2.7 step 5; D-local-gap) |
| `gaps` | The local-only gaps the server recorded, oldest first, each `{"first", "last"}`: the first and the last batch number it skipped. `[]` when there are none. The server never asks for their batches and never takes their roots |
| `server_key` | The key receipts are signed with now: its id and its 32-byte Ed25519 public key in hex. Until `/.well-known/pairtime-keys.json` (WP-SV17) publishes the keys with their validity dates, this is where a client gets the key to check receipts against, and the only key a receipt counts under (5.4) |

## 4. `GET /api/v1/time`

```text
GET /api/v1/time
User-Agent: pairtime-agent/<version>
```

No authentication. The response is 200 with `{"server_time": 1795089660250}`, the server's clock in Unix ms, which `X-Pairtime-Time` carries too. A client can set its offset from it before its first upload.

## 5. What the uploader does

1. Keep the counter on disk and write the next value before sending. At start, and after `counter_stale`, read `GET /api/v1/ingest/state` and continue above `max(local, req)`.
2. Keep a clock offset from the `X-Pairtime-Time` of the latest response, and send the corrected time in `X-Pairtime-Time`. Times in the body stay on the device's clock (section 1).
3. Batch roots come from the device's own ledger, in ascending `b`, and `prev` is never taken from the state. Batches sealed in local-only mode are never sent, nor the buckets of their minutes, because DESIGN 4.2 says those hours can never be submitted. So the uploader keeps, on disk, the first batch sealed online after the link: of the batches sealed after `pairtime login` linked the device, the first one not marked as sealed in local-only mode. A batch sealed in the second the login finished counts as sealed before it (D-relink-boundary). The uploader sends from there or from `head.b + 1`, whichever is higher, and after a re-link it starts again at the first batch sealed online after the new link (D-local-gap):
   - While the state's `head` is `null`, it starts at that batch, the first sealed online after `pairtime login`, whatever its `b`, as a device's first witnessed batch may (2.7 step 4).
   - While `relinked` is `true`, the device signed in again after the server stored its head, and it starts at the first batch sealed online after the new link. When that batch is above `head.b + 1`, the server takes it with its `prev` as given and records the batches between as a local-only gap, which it never asks for. When it is `head.b + 1`, it links to the head as always. Either way it sends that first root in a request of its own, and the rest once that one is stored: in one request, a fork refused at `head.b + 1` followed by a higher root would be taken as a skip, and the fork stored as a local-only gap.
   - Otherwise it sends from `head.b + 1`. Only a sign-in resets what the chain expects next: without one, a skipped batch is `chain_gap`. So a device switched to local-only mode in `config.toml` and back, without signing out, can't skip the batches it sealed in between and never sends their roots: the uploader stops sending batch roots and asks the teen to run `pairtime logout`, then `pairtime login`, after which `relinked` is `true`. `pairtime login` refuses while the computer is still linked, so the logout comes first. The batches sealed while signed out are local-only too, and since one sign-in allows one skip, the batches sealed online between the switch back and the new sign-in fall into the same gap, and their hours can never be submitted.

   After reading the state, compare `head.H_b` with the device's own head at `head.b`. If they differ, or the device has no batch `head.b`, the chains have forked. After `chain_gap` or `before_chain`, read the state and send as above. `batch_conflict`, `prev_mismatch` on batch 1 or on `head.b + 1`, and a 422 on the root at `head.b + 1` each end the chain, because no later root can link past them. Forks happen without tampering, as with a home folder restored from an old backup or a reverted VM snapshot. After a fork, as after a `device_mismatch` (2.6), nothing more uploads under the old pair, the `device_id` and its key: no batch roots, buckets, commit records or labels. Minutes keep recording locally, and the uploader tells the teen (`pairtime doctor`). WP-AG8 then, with the teen's go-ahead, signs in again as a new device: a new token, device key and `device_id`, whose ledger starts a new chain from `s` = 1 and `b` = 1 under a new `K_dev`, keeping the old ledger and key read-only, as ledger-v1 3 does for `pairtime restore` (D-new-device-wp-ag8). The uploader runs the fork check at every start, so it finds a fork before sealing batches under numbers the server already holds.
4. Verify each receipt under the server key that was valid at its `received_at`, as ledger-v1 7 and 9 step 7 do (DESIGN 10.2): the key its `key_id` names, which `/.well-known/pairtime-keys.json` (WP-SV17) must also list with validity dates that cover `received_at`. Until WP-SV17 publishes those dates, the state's `server_key` is the only key that counts, for any `received_at`: keep a receipt under another `key_id` unverified, and check it once `pairtime-keys.json` can. A receipt that doesn't verify, or whose key wasn't valid at its `received_at`, is reported (`pairtime doctor`) and not stored. Keep the first valid receipt for each `b` and never replace it: a later receipt for the same `b` and `H_b` with a different `received_at` is a re-dating, which the uploader reports and doesn't store.
5. Resend nothing that 2.9 says no resend changes. A minute's `rev` only rises: the uploader writes each minute's `rev` to disk before sending it, and after `rev_conflict` or `superseded` it sends its current bucket again with a `rev` above the result's `rev`. Hold a `future_minute` bucket, and send its latest revision once its minute is no more than 300 s ahead of the corrected clock. Retry a whole request only after a refusal in 2.6 that says to, a 429, a 5xx or a network error, keeping the key and body (2.10), with backoff and `Retry-After` (WP-AG5). Act on 2.6's error only in a response that carries `X-Pairtime-Time` and pairtimed's JSON body; any other answer, such as a Cloudflare block page, counts as a network error.
6. Send labels in the first request after the agent starts, and whenever they change.

## 6. Storage

For reviewers and later work packages; the client never sees it. A device's counter and chain state are on WP-SV3's `devices` row (migration `0003_devices.sql`), and migration `0004_ingest.sql` creates the rest:

| Table | Key | What it holds | Retention (docs/retention.json) |
|---|---|---|---|
| `devices` (WP-SV3's) | `device_id` | In its `ingest_` columns: the counter, the head's `b`, `H_b` and `received_at`, the link generation the head was stored under, the chain start and the local-only gaps. Ingest reads and writes them only through `device.Store`'s `LoadState` and `SaveState`, inside its writer transaction (D-device-row-chain-state) | `server.devices`: with the device's row, until 90 days after it is removed, or while ingest keeps any witness row, bucket, revision, commit record, label or reply of the device, if that's longer (D-device-account-binding). A device removed before ingest took anything from it goes at once. Should the row ever lose its state while witness rows stay, the highest of them is its head (2.7 step 4) |
| `ingest_replies` | `device_id` | The account, and the most recent processed request's `Idempotency-Key`, body hash and response, with its `received_at_ms` (2.10). The one per-device row of ingest's own, because a retry without it is only processed as a new request | `server.witness`: 180 days after `received_at_ms` |
| `witness_rows` | `(device_id, b)` | `n`, `R_b`, `H_b`, `prev`, `received_at_ms`, the receipt and its key id, and the account | `server.witness`: 180 days after `received_at_ms`, held while a submission uses them (WP-SB4) |
| `minute_buckets` | `(device_id, minute_ms)` | The current revision of each minute, with `agent_only`, under the device owner's account, with its `received_at_ms` and how many revisions the minute has | `server.minute_buckets`: 180 days after `minute_ms`, held while a submission uses them (WP-SB4) |
| `minute_bucket_revisions` | `(device_id, minute_ms, rev)` | Every revision stored, the current one included, each with its `received_at_ms` | `server.minute_buckets`, as `minute_buckets`; the job deletes a minute's revisions together |
| `commit_records` | `(device_id, sha)` | The account, the project key, the record, and `server_first_seen_at` as `first_seen_at_ms` | `server.commits`: while the account exists |
| `device_labels` | `device_id` | The account and the latest labels | `server.devices`: 90 days after the server last received them, so never longer than DESIGN 4.4's device lifetime plus 90 days |
| `request_origins` | `id` | For each processed upload, the device, the account, the IP from `CF-Connecting-IP` (the connection's address when the request didn't come through Cloudflare) and the `CF-JA4` fingerprint when the plan sends one, as an address and a JA4 value only, and `seen_at_ms` | `server.ip_ja4`: deleted 30 days after `seen_at_ms` (DESIGN 4.4) |

## 7. Decisions this spec makes

DESIGN 4.3 and 4.5 give the fields and the rules. These are what they leave open, decided here:

1. `X-Pairtime-Body-SHA256` carries the body's hash beside the signature. DESIGN 4.5 refuses "a body whose hash differs", and PLAN's WP-SV4 checks want that refusal and a signature by another key to get different codes. With the hash only inside `M`, a changed body and a wrong key fail the same way.
2. The raw body that `M` and the hash cover is the gzip stream as sent, so a request is authenticated before the server inflates it.
3. The signature is padded standard base64; the counter and the time are decimal ASCII with no sign or leading zeros, so each value has one spelling in `M`.
4. The `Idempotency-Key` is a lowercase UUIDv7, and the server keeps the response of each device's most recent request only. Batch roots and bucket revisions are idempotent on their own (2.10), so an older key costs nothing for them, and the server doesn't hold a reply for every upload.
5. Only a request the server processes moves the counter; a refusal or a replay doesn't. A replay is answered before the counter is checked, so re-sending identical bytes works.
6. A device's first witnessed batch may be any `b`, and for `b > 1` its `prev` is taken as given; from then on every batch links to the one before it (ledger-v1 6 leaves this to WP-SV4). Only a sign-in resets what the chain expects next (D-local-gap): after `pairtime login` gives the same `device_id` a new token, the first batch the server stores under it may be any `b` above the stored head, and above `head.b + 1` it takes its `prev` as given. The server records the batches it skipped as a local-only gap on the device's chain state, beside the sign-in the head was stored under, never asks for them and never takes their roots. Roots and minutes sealed in local-only mode are never sent, because DESIGN 4.2 says local-only mode uploads nothing, roots included, and nothing in a root says which mode sealed it. A routine logout and login then keeps the device's `device_id` and `K_dev`, and with them every project key, and the gap is honest: its minutes are never uploaded, so they are never credited. Without a sign-in, a skipped batch is `chain_gap`. Should the chain state ever be missing while the server holds witness rows of the device, the highest of them is its head, so the first-batch exception never applies to a device the server still holds batches of.
7. 2,000 batch roots per request, beside DESIGN 4.5's limits. It bounds one writer transaction, and 256 KiB of compressed random hashes doesn't hold many more.
8. One `received_at` per request, the writer transaction's time.
9. Item results are arrays parallel to the request's, so a refused item is named by its place and the response never repeats a string from the request (DESIGN 4.5's "names the rejected item").
10. A bucket's minute must start between 180 days before the server's clock and 300 s after it (SP 8.4's 5 minutes; DESIGN 4.4's 180 days). Unlike SP 8.4's corrected times, the minute is on the device's clock, which the server can't correct, so a device whose clock runs ahead holds a bucket until its minute is within 300 s of the corrected clock (5.5).
11. For one device and minute, a higher `rev` replaces a lower one, and the same `rev` with a different bucket keeps the first (`rev_conflict`).
12. The server keeps every bucket revision it accepted, each as a row with its `received_at`, because the decision E-check-readings has DESIGN 7.8's `buckets.json` carry "the buckets as received live ... with `received_at` and revisions". A minute keeps at most 128 revisions, so neither one request nor many can make its history grow without end.
13. The bucket's `witness` is stored as the client sent it. The server's own label, from `received_at` (DESIGN 7.4), is WP-SV6's.
14. `GET /api/v1/ingest/state` gives the server key until WP-SV17 publishes `/.well-known/pairtime-keys.json`, and each receipt names its key by id.
15. `GET /api/v1/time` needs no authentication, and every response carries the server's time in `X-Pairtime-Time`.
16. `GET /api/v1/ingest/state` is signed with the device key over `M_state`, whose context string differs from `M`'s.
17. Each device's rate is SP 8.6's: a burst of 30 requests, refilled at 6 a minute, one in flight at a time. SP 8.6's separate backlog allowance isn't needed, because a request carries up to 2,000 batch roots and 2,000 buckets, so a week offline catches up within the burst. The rate counts only signed requests.
18. Each processed upload stores its origin: the IP from `CF-Connecting-IP`, which Cloudflare sets and the tunnel-only origin can trust, and `CF-JA4` where the plan sends it, each kept only in its own form (SP 8.7; DESIGN 4.4's 30 days).
19. `agent_trailer` is a closed list, because its value isn't keyed and a trailer can hold a person's name. `harnesses[].version` is optional, because hook payloads often carry none.
20. A device's labels are deleted 90 days after the server last received them. The client sends them at start and on change, so a device in use keeps them, and none outlives DESIGN 4.4's device lifetime plus 90 days.
21. An object that repeats a member name refuses its item as `bad_rule`, naming the object, and a repeated top-level member refuses the request as `body_shape`. `encoding/json` alone keeps the last value, so the server would act on a value other than the one `pairtime privacy show` printed first.
22. The server reads a body's members in order and answers the first that fails, so it can refuse an array at its first item past the limit without reading the rest. A body that breaks two rules gets the first in its own order.
23. `client_build` and `harnesses[].version` are versions in D-version-grammar's form. Unlike keys they aren't keyed, so their form is all that keeps a file, folder or host name out of tier 1 (DESIGN 4.3's Never-in-it column).
24. A receipt counts only under the server key that was valid at its `received_at` (5.4; ledger-v1 7 and 9 step 7). Once `/.well-known/pairtime-keys.json` lists validity dates (WP-SV17), a receipt that a retired key signed after its validity ended, as a server restored with an old keys backup would, is never stored as a batch's only receipt. Until then the state's `server_key` is all a client can check against, and such a receipt passes.
