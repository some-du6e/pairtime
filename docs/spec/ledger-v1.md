# Ledger format, version 1

Status: part 1 of 2. Part 2 comes with WP-LG3 and fills section 10: token commitments, keyed features and the challenge draw.

This is the byte-level specification of the ledger in DESIGN 3.6 and the day tree in DESIGN 7.4. It covers how a device derives its keys, how it turns each record into a 65-byte entry, how it seals entries into batches and chains the batches, what the server signs in a receipt, and how the server builds and chains the tree it publishes each day. `pkg/ledger` implements it in Go. `tools/ledger_ref.py` implements it again in standard-library Python, shares no code with `pkg/ledger`, and checks it against the vectors in `testdata/ledger/` (section 11).

DESIGN says what goes into each value. This spec says how each value is encoded, byte for byte. Where DESIGN leaves an encoding or a rule open, the spec decides it, and section 13 lists every such decision.

The format can't change once a server has witnessed data in it, because old bundles must keep verifying (DESIGN 3.6). Changing any byte below makes a new version, ledger v2, with new context strings and HKDF labels. Version 1 stays readable forever.

## 1. Conventions

- `a || b` is the bytes of `a` followed by the bytes of `b`.
- `u8(x)` is `x` as one byte. `u64(x)` is `x` as eight bytes, big-endian. Every integer in this spec is unsigned.
- `ascii(s)` is the bytes of the ASCII string `s`, with no terminator and no length.
- `zero(n)` is `n` zero bytes. `x[0:n]` is the first `n` bytes of `x`.
- `SHA-256` is FIPS 180-4's. `HMAC-SHA256(K, m)` is RFC 2104's HMAC over SHA-256, 32 bytes long. `HKDF-SHA256` is RFC 5869's HKDF over SHA-256.
- `Ed25519(K, m)` is RFC 8032 section 5.1: PureEdDSA, no context, no prehash. That is what Go's `crypto/ed25519.Sign` computes. A signature is 64 bytes.
- Times are Unix milliseconds, UTC, the unit of `pkg/event`'s `t`.
- In JSON, a byte string is lowercase hex and an integer is a JSON number.

`device_id`, `project_key` and `session_key` are `pkg/event` opaque keys: 1 to 128 ASCII characters, each one of `A-Z a-z 0-9 _ -`. A writer never builds a header, receipt message or day leaf from any other string, and a reader rejects one built from any other string.

## 2. Domain separation

Every value the ledger hashes or signs starts with a byte or a string that says what kind of value it is, so no value can pass for another kind:

| Input | First bytes | Section |
|---|---|---|
| SHA-256 of a Merkle leaf | `0x00` | 5.1 |
| SHA-256 of a Merkle node | `0x01` | 5.1 |
| SHA-256 of a batch chain link | `0x03` | 6 |
| SHA-256 of a day chain link | `0x04` | 8 |
| HMAC-SHA256 under `K_leaf`, the nonce | `0x01` | 4.4 |
| HKDF-SHA256 info | `pairtime/v1/` and a label | 3 |
| Ed25519 by the server key, a receipt | `pairtime-receipt-v1` | 7 |
| Ed25519 by the server key, a day head | `pairtime-day-v1` | 8 |

`0x02` is unassigned. No SHA-256 input in version 1 starts with it.

The nonce's tag byte leaves room for other HMACs under `K_leaf` with other first bytes. Version 1 has only the nonce.

The server key signs a third kind of message, the bundle manifest (DESIGN 7.8), which WP-SB8 specifies. Its context string must differ from both strings above, so that no signature over one kind of message verifies as another.

## 3. Device keys

`K_dev` is 32 bytes from the operating system's random number generator, made once at install and stored as DESIGN 3.6 says. Four keys come from it, each 32 bytes:

```text
K_x = HKDF-SHA256(IKM = K_dev, salt = zero(32), info = ascii(label), L = 32)
```

| Key | label | Used for |
|---|---|---|
| `K_leaf` | `pairtime/v1/leaf` | record nonces (4.4) |
| `K_tok` | `pairtime/v1/token` | token salts (part 2) |
| `K_feat` | `pairtime/v1/feat` | keyed scorer features (part 2) and keyed error signatures (DESIGN 3.6) |
| `K_vault` | `pairtime/v1/vault` | the vault (DESIGN 3.5, WP-LG6) |

The salt is RFC 5869's default for a salt that isn't provided, HashLen zero bytes, which for SHA-256 is 32. With `L = 32` the whole derivation is two HMACs:

```text
PRK = HMAC-SHA256(zero(32), K_dev)              shared by all four keys
K_x = HMAC-SHA256(PRK, ascii(label) || 0x01)
```

HMAC pads a key shorter than SHA-256's 64-byte block with zeros, so an empty salt and `zero(32)` give the same `PRK`. RFC 5869's test case 3 runs through that path.

Other parts of pairtime derive keys under the same `pairtime/v1/` prefix: `backup` and `recovery` in DESIGN 7.11, and `seal` in DESIGN 7.6. Their own specs define them. A new ledger key takes a new label, and no label is ever used for two keys. `K_dev` and the keys derived from it never leave the device unencrypted (DESIGN 4.3, 7.11).

## 4. Records

A record is one entry in a device's ledger: an event, a scorer checkpoint or an upload-failure note. The writer appends records to `ledger/YYYY-MM-DD.seg` (DESIGN 3.5). That file's layout belongs to WP-LG5 and nothing in it is hashed apart from the header and body bytes below.

### 4.1 Sequence numbers

Every record has a sequence number `s`. The device's first record has `s = 1`, and each record it writes after that, of any type, has the next number. There are no gaps and no repeats. An event's `seq` field equals the `s` of the record that holds it.

Checkpoints and upload-failure notes take numbers from the same sequence. So the record sequence has no gaps, while the events' own `seq` values skip the numbers those records hold.

### 4.2 Record types

| `rtype` | Record |
|---|---|
| `0x00` | never used |
| `0x01` | `session.start` |
| `0x02` | `session.end` |
| `0x03` | `prompt.submit` |
| `0x04` | `agent.output` |
| `0x05` | `agent.tool` |
| `0x06` | `agent.permission` |
| `0x07` | `agent.interrupt` |
| `0x08` | `agent.queue` |
| `0x09` | `agent.stop` |
| `0x0a` | `agent.subagent` |
| `0x0b` | `editor.activity` |
| `0x0c` | `shell.command` |
| `0x0d` | `git.change` |
| `0x0e` | `os.presence` |
| `0x0f` | `os.focus` |
| `0x10` | `os.power` |
| `0x11` | `otel.active_time` |
| `0x12` | `cli.track` |
| `0x13` to `0x3f` | reserved for event kinds added later |
| `0x40` | scorer checkpoint (DESIGN 3.6, 6.5) |
| `0x41` | upload-failure note (DESIGN 3.6, 7.4) |
| `0x42` to `0xff` | reserved |

`0x01` to `0x12` are the 18 kinds in the order of PR1's `kindTable`, as DESIGN 3.6 says. That order was used once. From now on the table is fixed: a code never changes meaning, even if `kindTable` is reordered, and a kind added later takes the next free code from `0x13` up. A writer never emits a reserved code. Checking roots and heads never reads `rtype`, so a reader that meets a code it doesn't know reports the record as unknown and can still verify its batch.

### 4.3 Header

```text
hdr     = u64(s) || u8(rtype) || project || session        33 bytes
project = SHA-256(ascii(project_key))[0:16], or zero(16) for no project
session = SHA-256(ascii(session_key))[0:8],  or zero(8) for the session "none"
```

| Record | `project_key` | `session_key` |
|---|---|---|
| An event, `0x01` to `0x12` | the event's `project_key`; no project when it is absent | the event's `session_key`, which is `none` for OS and git events (`pkg/event`'s `NoSession`) |
| `os.presence`, `os.focus`, `os.power` | always no project | `none` |
| Checkpoint, `0x40` | the project whose state it holds, always present | `none` |
| Upload-failure note, `0x41` | no project | `none` |

The `os.*` rule comes from DESIGN 3.6, which gives every `os.*` event the zero project. A writer refuses an `os.*` event that has a `project_key`, and a reader rejects an `os.*` header whose project isn't `zero(16)`. That rule lets the server demand the body of every presence, power and focus record in a revealed batch, so a modified client can't hide a lock event by labeling it with another project (EV 6.2). The project an `os.focus` event points at lives in its payload's `focus_project`, inside the body.

### 4.4 Nonce

```text
nonce = HMAC-SHA256(K_leaf, 0x01 || u64(s))        32 bytes
```

The nonce goes inside the body (4.5). Without it, a reader who sees only a body's hash could confirm a guess of a short body, such as a presence sample (DESIGN 3.6). Only the device can compute a nonce, so verifiers never check one.

### 4.5 Body

The body is a byte string that the writer stores as it wrote it, and every hash covers those stored bytes. Nobody ever re-encodes a body to verify it. A reader in another language parses bodies but never has to reproduce one byte for byte.

The writer emits each body the way Go's `encoding/json` writes `pkg/ledger`'s body type: one JSON object, no insignificant whitespace, members in this order:

| Member | Holds | Present |
|---|---|---|
| `n` | the nonce, as 64 lowercase hex digits | always, first |
| `e` | the record. For `0x01` to `0x12`, the event after `Event.Shareable()`, as `Event.MarshalJSON` writes it. For `0x40`, the serialized `score.ProjectState` (WP-SC5). For `0x41`, the note (WP-AG5) | always |
| `f` | the record's keyed scorer features, versioned by `f.v` (part 2) | only when the record has features |
| `c` | the record's content commitment `C` (part 2) | only for a prompt or a turn's final agent message |

So every body starts with the six bytes `{"n":"` and then the nonce in hex. A body holds no text: `e` comes from `Shareable()`, and `f` and `c` hold keyed values only (DESIGN 3.6, 4.2). Part 1 writers emit `n` and `e`; `f` and `c` start with WP-LG4.

A verifier that parses a revealed body also checks that the body agrees with its header: `e` is a record of type `rtype`; for an event, `e`'s `seq` is `s`, and the digests of `e`'s `project_key` and `session_key` are the header's `project` and `session`; for a checkpoint, the digest of the project it names is the header's `project`. A body that disagrees with its header fails verification, as a body whose hash differs would.

### 4.6 Entry

```text
entry = hdr || SHA-256(body)        65 bytes
```

Entries are what the batch tree is built from. A revealed batch shows the header of every record in it, and each body's hash stands in for the body until the body itself is revealed (DESIGN 4.2).

## 5. Merkle trees

Every tree in the ledger is RFC 9162's Merkle tree with SHA-256.

### 5.1 Tree hash

```text
LEAF(d)    = SHA-256(0x00 || d)
NODE(l, r) = SHA-256(0x01 || l || r)
```

`ROOT` takes a list of leaf hashes `h[0], ..., h[n-1]`:

```text
ROOT()                  = SHA-256 of no bytes
                        = e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
ROOT(h[0])              = h[0]
ROOT(h[0], ..., h[n-1]) = NODE(ROOT(h[0], ..., h[k-1]), ROOT(h[k], ..., h[n-1]))
                          for n > 1, where k is the largest power of two smaller than n
```

`MTH` is RFC 9162 section 2.1.1's Merkle Tree Hash. It takes data entries and applies the leaf prefix itself:

```text
MTH(d[0], ..., d[n-1]) = ROOT(LEAF(d[0]), ..., LEAF(d[n-1]))
```

Every tree in part 1 is an `MTH` over data: batch entries (6) and day leaves (8). Part 2's content commitment `C` is a `ROOT` over leaf hashes it computes itself, "used as precomputed leaf hashes" in DESIGN 3.6's words, so its leaves get no second `0x00`. TP B6 asked for this to be pinned down, because a client and a verifier that differ here never agree on a root.

### 5.2 Inclusion proofs

The inclusion proof of leaf `m` in a tree of `n` leaves is RFC 9162 section 2.1.3.1's `PATH(m, D_n)`: a list of 32-byte node hashes, from the leaf's sibling up to a child of the root, empty when `n = 1`. Leaves are numbered from 0.

A verifier runs RFC 9162 section 2.1.3.2's algorithm with the leaf hash `LEAF(d[m])`, `m`, `n` and the root. It also fails the proof when the leaf hash, the root or any path element is not exactly 32 bytes. The algorithm itself fails a proof with too many or too few elements and an `m` that isn't below `n`.

A proof doesn't commit to `n`: some proofs also verify against the same root with a larger `n`. The verifier must take `n` from a head it has already verified, `n_b` for a batch tree and `count_d` for a day tree, never from whoever sent the proof.

Inclusion proofs show that a batch head is in a published day tree (DESIGN 7.4, 7.8), and that one entry is in its batch without revealing the batch's other headers.

## 6. Batches and the chain

The writer seals a batch every `BATCH_S` = 60 s while there are new records, and at once on lock, suspend, prompt and stop (DESIGN 3.6). The first batch is `b = 1`, and each later batch has the next number, with no gaps. Batch `b` holds records `first_b` to `last_b` in sequence order: `first_1 = 1`, `first_(b+1) = last_b + 1`, and `n_b = last_b - first_b + 1`. Every batch holds at least one record, because an empty batch is never sealed.

```text
R_b = MTH(entry_(first_b), ..., entry_(last_b))
H_b = SHA-256(0x03 || H_(b-1) || R_b || u64(n_b) || u64(b))        81 bytes hashed
H_0 = zero(32)
```

Each head depends on every batch before it, so no record can be added, dropped, reordered or changed after its batch is uploaded without changing every later head (DESIGN 3.6, CC7 in DESIGN 7.3).

The client uploads `{b, n, R_b, H_b, prev}` with `prev = H_(b-1)` (DESIGN 4.3); the upload's JSON is WP-SV4's. The server refuses the batch unless `prev` equals the head it stored for batch `b - 1`, or `zero(32)` when `b = 1`, and unless `H_b` is `SHA-256(0x03 || prev || R_b || u64(n) || u64(b))` for the values it received. It then stores `(device_id, b, n_b, R_b, H_b, received_at)`, where `received_at` is the server's clock, in Unix milliseconds, when it stored the row.

## 7. Receipts

For each batch it stores, the server returns a signed receipt:

```text
M_b       = ascii("pairtime-receipt-v1") || u8(len(device_id)) || ascii(device_id)
            || u64(b) || H_b || u64(received_at)
receipt_b = Ed25519(server key, M_b)        64 bytes
```

`device_id` is 1 to 128 bytes, so its length fits in the one byte before it, and every other field has a fixed width, so `M_b` splits back into its fields from the front. The client stores the receipt with the batch in `ledger/batches.seg` (DESIGN 3.5) and checks it against the server's published key (DESIGN 10.2) before relying on it. The receipt binds the server to `received_at`: the server can't later deny or re-date a head the device holds a receipt for, and the device can't claim the batch arrived earlier (DESIGN 3.6).

## 8. Day trees

Once a day the server publishes a root over every batch it stored that UTC day, chained to the day before (DESIGN 7.4). It lets an auditor check that the server didn't insert or re-date a batch afterwards.

The day number of a time `t` is `d = floor(t / 86,400,000)`, days since 1970-01-01 UTC. Day `d` holds every stored row whose `received_at` falls on day `d`, and each row becomes one leaf:

```text
leaf = u8(len(device_id)) || ascii(device_id) || u64(b) || H_b || u64(received_at)
```

The leaves are ordered by `received_at`, then by `device_id` compared byte by byte, then by `b`. That order has no ties, because the server stores each `(device_id, b)` once. Equal `received_at` values are common: a device that was offline flushes many batches in one request.

```text
T_d     = MTH(day d's leaves, in that order)      T_d = SHA-256 of no bytes for a day with no rows
count_d = the number of day d's leaves
G_d     = SHA-256(0x04 || G_(d-1) || T_d || u64(count_d) || u64(d))        81 bytes hashed
```

Every day from the first day the server publishes, `d_0`, has a head, including days with no rows, so the day chain has no gaps. `G_(d_0 - 1) = zero(32)`.

The server signs each day head:

```text
D_d         = ascii("pairtime-day-v1") || u64(d) || G_d
signature_d = Ed25519(server key, D_d)
```

It commits `d`, `G_(d-1)`, `T_d`, `count_d`, `G_d` and `signature_d` to the public `pairtime-witness` repository. WP-SV17 specifies that file. Each bundle carries the index of each batch's leaf in its day tree and the leaf's inclusion proof (DESIGN 7.8), checked against `T_d` with `count_d` leaves.

## 9. Verification

A verifier that holds a run of a device's batches, with headers and body hashes for every record in them, checks:

1. Batch numbers rise by 1 with no gap. A run that starts at `b = 1` has `prev = zero(32)`; one that starts later has, as its first `prev`, the head the server stored for the batch before.
2. Sequence numbers rise by 1 with no gap, within and across batches. Batch 1 starts at `s = 1`.
3. Every header follows 4.3: its rtype is in the table, an `os.*` header has the zero project, and checkpoints and upload-failure notes have the fields their row in 4.3 gives them.
4. Every entry is its header followed by the SHA-256 of the record's stored body, and every revealed body agrees with its header (4.5).
5. `n_b` is the number of the batch's records, and `R_b` is the `MTH` of their entries.
6. `H_b` follows from `H_(b-1)`, `R_b`, `n_b` and `b`.
7. Each receipt verifies over `M_b` under the server key that was valid at `received_at` (DESIGN 10.2).
8. Each inclusion proof verifies (5.2), with `n` taken from a verified head.
9. Each day tree has its rows in order and on its own day, its `count_d`, `T_d` and `G_d` follow from them, its signature verifies over `D_d`, and the day chain has no gaps.

`pkg/ledger` reports a gap in `b` or `s`, a root mismatch and a head mismatch as distinct errors (WP-LG2). Each one points at a different fault: a batch that was hidden or dropped, a record that was changed or moved, a chain that was rewritten.

## 10. Part 2, reserved for WP-LG3

WP-LG3 specifies these in this file, in a section of its own:

- the tokens of a prompt or a turn's final agent message, the token leaves `t_ij`, and the content commitment `C`, a `ROOT` over them (5.1);
- the redacted reveal and how it is verified;
- the keyed features in `f` and their version `f.v`;
- the 16-byte HMACs for `error_sig`, `template_hash` and `cmd_hash`;
- the challenge draw of DESIGN 7.5 step 4.

Part 2 adds its prefixes, tags, labels and context strings to section 2 and reuses none of part 1's.

## 11. Vectors and the reference

### 11.1 Files

| Path | What it is |
|---|---|
| `tools/ledger_ref.py` | the Python reference |
| `testdata/ledger/vectors.json` | vectors from fixed seeds, written by `ledger_ref.py vectors` |
| `testdata/ledger/rfc5869.json` | RFC 5869 test cases 1 to 3 |
| `testdata/ledger/rfc4231.json` | RFC 4231 test cases 1 to 7, HMAC-SHA-256 only |
| `testdata/ledger/ct/` | Certificate Transparency Merkle vectors from transparency-dev/merkle, pinned to one commit |

All of them are oracles under the plan's ground rule 5: a PR that changes them along with the code they check needs the `oracle-change` label.

### 11.2 Commands

```sh
python3 -I tools/ledger_ref.py selftest
python3 -I tools/ledger_ref.py vectors [--out PATH]
python3 -I tools/ledger_ref.py check FILE
```

`selftest` checks the RFC 5869, RFC 4231 and Certificate Transparency vectors, the rtype table against `pkg/event`'s `kindTable`, and the worked example in section 12 against this file, then checks that `vectors.json` is byte for byte what `vectors` writes and that it passes `check`. `ci.yml` runs it on every PR.

`check FILE` recomputes every value in a file in the format of 11.3 and prints each mismatch with its JSON path. `pkg/ledger`'s tests write such files and run `check` on them (WP-LG2).

Each command exits 0 when everything matches, 1 when a value doesn't, and 2 for a usage error or a file that doesn't follow 11.3. Python's standard library has no Ed25519, so the reference builds `M_b` and `D_d` and `pkg/ledger` signs and verifies them against RFC 8032's vectors.

### 11.3 The vectors format

`vectors.json` and the files `check` reads share one JSON format. The top level is an object with `"format": "pairtime-ledger-vectors-v1"`, any of the sections below, and optional `spec`, `generator` and `note` strings. A member the format doesn't define is an error, so a misspelled section can't pass by checking nothing, and a file with nothing to check is an error too. A list may be `null`, which means an empty list, as Go writes a nil slice.

| Object | Members |
|---|---|
| `keys[]` | `k_dev`, optional `prk`, `k_leaf`, `k_tok`, `k_feat`, `k_vault` (3) |
| `digests[]` | either `project_key` and `project`, or `session_key` and `session` (4.3) |
| `chains[]` | `device_id`, `k_dev`, `batches` |
| `batches[]` | `b`, `n`, optional `received_at`, `prev` (`H_(b-1)`), `root` (`R_b`), `head` (`H_b`), optional `receipt_msg` (`M_b`), `records`, optional `proofs` |
| `records[]` | `seq` (`s`), `rtype`, `project_key` (`""` for no project), `session_key`, `body` (the stored bytes), `nonce`, `entry` |
| `proofs[]` | `index` (`m`, from 0), `path` (5.2) |
| `days[]` | `day` (`d`), optional `date` (`YYYY-MM-DD`), `prev` (`G_(d-1)`), `count`, `root` (`T_d`), `head` (`G_d`), `signed_msg` (`D_d`), `rows`, optional `proofs` |
| `rows[]` | `device_id`, `b`, `head` (`H_b`), `received_at`, `leaf` (the leaf bytes, not their hash) |

Besides recomputing each value from the file's own inputs, `check` applies these rules:

- A chain whose first batch is `b = 1` starts with `prev = zero(32)` and `s = 1`. One that starts later takes its first `prev` and `s` as given. After that, batch numbers, sequence numbers and `prev` links follow sections 4.1 and 6.
- `received_at` and `receipt_msg` come together or not at all.
- Each day after the first has the next day number and chains to the day before. The first day's `prev` is taken as given.
- A batch whose `received_at` falls on a day the file lists appears in that day's rows with the same head and time, and no `(device_id, b)` appears twice.
- A body that starts with `{"n":"` carries its own record's nonce there.

### 11.4 What the vectors cover

- `keys`: four values of `K_dev`: all zeros, `0x00` to `0x1f`, all `0xff`, and one from a seed.
- `digests`: no project, one-character keys, `none` as a project key, where it is an ordinary key, and 128-character keys.
- Chain A: 50 batches whose sizes include the plan's list, 1, 2, 3, 7, 8, 9, 100 and 1,000, with every rtype, uploaded about a minute apart from 23:20 UTC so they span midnight. It has proofs for every leaf of each batch of up to 9 records and for eight leaves of each larger batch.
- Chain B: a 128-character `device_id`, and four batches that share one `received_at`, as an offline flush would.
- Days: 2026-11-19 and 2026-11-20, with rows from both chains and from four devices whose chains aren't in the file. Two of those rows share a millisecond with a chain A batch, one sorting before it and one after it; chain B's four batches share one millisecond; and two rows fall in the last and the first millisecond of a day. 2026-11-21 has no rows. Every leaf has a proof.

The bodies are stand-ins: the real envelope with the record's real nonce, and `"e":{"stand_in":"<record type>","seq":<s>}` in place of the record. Hashing never parses a body, so a stand-in exercises the same code a real body does. The keys are test keys, published here and used nowhere else.

## 12. Worked example

These values come from `tools/ledger_ref.py`, and `selftest` fails if any of them stops matching it. All values are hex unless they're quoted text or plain numbers.

### 12.1 Keys

```text
K_dev   = 000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f
PRK     = 46bd320605c5a6b6163ab70bc6345b92a5f908e79fe58979c23ebb47d1a5e307
K_leaf  = 03ece076e53f8e25304839d8440440c015f90b22c6e1fdb8a6df0b250523f6d8
K_tok   = 6f55cd167c1531f33afaa9e9b486ae9c441cd03d69ccafc29189111aa6bc76f4
K_feat  = c9ad26aafffd194d9a0ea333a5fc444203aec6be575eb7bb253f942a67e53e70
K_vault = e04e0f65a92a86bef6551c50829f02191e37092290e74232e1a5acd722f7dd12
```

### 12.2 Three records

Device `d_example` writes three records:

| `s` | `rtype` | `project_key` | `session_key` |
|---|---|---|---|
| 1 | `0x0e` `os.presence` | none | `none` |
| 2 | `0x03` `prompt.submit` | `p_example` | `s_example` |
| 3 | `0x40` checkpoint | `p_example` | `none` |

```text
project = SHA-256(ascii("p_example"))[0:16] = 44f24edfdf081d9d54bf2363932f2238
session = SHA-256(ascii("s_example"))[0:8]  = 8cf787fc8e65ef99
```

Record 1:

```text
nonce     = 606cd8ffa6f47d889c246f68a533884bcb7f06f50856b7a5b7b652661d5b421f
body      = {"n":"606cd8ffa6f47d889c246f68a533884bcb7f06f50856b7a5b7b652661d5b421f","e":{"stand_in":"os.presence","seq":1}}
body hash = 420440de3ab02c01e44bc1f996b96e804743e85e755f8fe28e423258023bf0cf
hdr       = 0000000000000001 0e 00000000000000000000000000000000 0000000000000000
          = 00000000000000010e000000000000000000000000000000000000000000000000
entry     = 00000000000000010e000000000000000000000000000000000000000000000000420440de3ab02c01e44bc1f996b96e804743e85e755f8fe28e423258023bf0cf
```

Record 2:

```text
nonce     = 307601f3934aaac02e04d46a7fc34f067003619812e6928f60858d439075f569
body      = {"n":"307601f3934aaac02e04d46a7fc34f067003619812e6928f60858d439075f569","e":{"stand_in":"prompt.submit","seq":2}}
body hash = 34a9fe8fca3ad97eb963b156da7fbf5f1631dc700b27f31450c489ab5826917d
hdr       = 0000000000000002 03 44f24edfdf081d9d54bf2363932f2238 8cf787fc8e65ef99
          = 00000000000000020344f24edfdf081d9d54bf2363932f22388cf787fc8e65ef99
entry     = 00000000000000020344f24edfdf081d9d54bf2363932f22388cf787fc8e65ef9934a9fe8fca3ad97eb963b156da7fbf5f1631dc700b27f31450c489ab5826917d
```

Record 3:

```text
nonce     = f118141d8b500a8cb7e37bb3fc530542b9427f84196a3f10b76cfa12360142b4
body      = {"n":"f118141d8b500a8cb7e37bb3fc530542b9427f84196a3f10b76cfa12360142b4","e":{"stand_in":"checkpoint","seq":3}}
body hash = 9e85465282ae9812cd845c6b1511668fed3ebf04440a1f9f638aa7cc9fe305d2
hdr       = 0000000000000003 40 44f24edfdf081d9d54bf2363932f2238 0000000000000000
          = 00000000000000034044f24edfdf081d9d54bf2363932f22380000000000000000
entry     = 00000000000000034044f24edfdf081d9d54bf2363932f223800000000000000009e85465282ae9812cd845c6b1511668fed3ebf04440a1f9f638aa7cc9fe305d2
```

The bodies are stand-ins (11.4). A real record 1 would hold the `os.presence` event in `e`.

### 12.3 Batch 1

The three records are batch 1. With three leaves, `k = 2`, so the tree is `NODE(NODE(L1, L2), L3)`:

```text
L1 = LEAF(entry 1) = 7032e9622881fd3312c71e8d40655522fa52f3f8c0081e6ce011e08d95e4bb38
L2 = LEAF(entry 2) = df456675d8772a06b52b8b9e3ef2a349f504162e1b8682deb0e4d5cd769044a1
L3 = LEAF(entry 3) = 6ae6ba7550b1a30f4e1ed0f9af3404c263f3f1c4fd078e70352f7390a193fc44
NODE(L1, L2)       = 7196b4294b0d7ff5dcade1e5d0b908874cc7ca864d6939cc0cc6c70d0e033f65
R_1                = cee3ad5e6675750f997684c411a4ff6cd1fdbeabfd296b3a68679795850a8e0c
H_1 = SHA-256(0x03 || zero(32) || R_1 || u64(3) || u64(1))
    = d0277390f5841d01711ecb7493662d98d18fd1a318f14ce25fefc94edc1b00a7
```

The inclusion proof of record 2, leaf 1 of 3, is `[L1, L3]`:

```text
7032e9622881fd3312c71e8d40655522fa52f3f8c0081e6ce011e08d95e4bb38
6ae6ba7550b1a30f4e1ed0f9af3404c263f3f1c4fd078e70352f7390a193fc44
```

### 12.4 Receipt

The server stores batch 1 at 2026-11-19T12:01:00.250Z, so `received_at` = 1795089660250, which is `000001a1f3ae795a`:

```text
M_1 = 7061697274696d652d726563656970742d7631    "pairtime-receipt-v1"
      09                                        len("d_example")
      645f6578616d706c65                        "d_example"
      0000000000000001                          b
      d0277390f5841d01711ecb7493662d98d18fd1a318f14ce25fefc94edc1b00a7    H_1
      000001a1f3ae795a                          received_at
    = 7061697274696d652d726563656970742d763109645f6578616d706c650000000000000001d0277390f5841d01711ecb7493662d98d18fd1a318f14ce25fefc94edc1b00a7000001a1f3ae795a
```

### 12.5 Day tree

That day is `d` = 20776, which is 2026-11-19. Say it is the first day this server publishes, and the only other batch it stored that day is batch 7 of `d_other`, received 30 s earlier, with a stand-in head `SHA-256(ascii("stand-in head of batch 7 of d_other"))` = `0f8e07775a3efbc8ca16d900af808e95567b6092c72e22a0a3d18872e06cc242`. The earlier `received_at` sorts first:

```text
leaf 1 = 07 645f6f74686572 0000000000000007 0f8e07775a3efbc8ca16d900af808e95567b6092c72e22a0a3d18872e06cc242 000001a1f3ae042a
       = 07645f6f7468657200000000000000070f8e07775a3efbc8ca16d900af808e95567b6092c72e22a0a3d18872e06cc242000001a1f3ae042a
leaf 2 = 09645f6578616d706c650000000000000001d0277390f5841d01711ecb7493662d98d18fd1a318f14ce25fefc94edc1b00a7000001a1f3ae795a
LEAF(leaf 1) = dbb9530abac5f5e52ac7188e663999f5510b184196d22795ae6df086de8ae3de
LEAF(leaf 2) = bf08bde260d74c54b160bf8345364601ae6afe57cfac850ecc3be3f054c6c850
T_d     = NODE(LEAF(leaf 1), LEAF(leaf 2))
        = 10d0de031bcf121f8db0423641f73dc5105696cece9d56f7df58feeb02b372a2
count_d = 2
G_d     = SHA-256(0x04 || zero(32) || T_d || u64(2) || u64(20776))
        = b222eaa5b42b58700786a473f68ca59f03f77e4971d49b77e049bf5f9e6650c5
D_d     = 7061697274696d652d6461792d7631 0000000000005128 b222eaa5b42b58700786a473f68ca59f03f77e4971d49b77e049bf5f9e6650c5
        = 7061697274696d652d6461792d76310000000000005128b222eaa5b42b58700786a473f68ca59f03f77e4971d49b77e049bf5f9e6650c5
```

The bundle's inclusion proof for `d_example`'s batch 1, leaf 1 of 2 in that day's tree, is `[LEAF(leaf 1)]`, which is `dbb9530abac5f5e52ac7188e663999f5510b184196d22795ae6df086de8ae3de`.

## 13. Decisions this spec makes

DESIGN 3.6 and 7.4 give the fields of each value. These are the encodings and rules they leave open, decided here:

1. HKDF uses RFC 5869's default salt, 32 zero bytes, and `L = 32` (3).
2. Every integer is `u64` big-endian: `s`, `n_b`, `b`, `received_at`, `count_d` and `d`. Times are Unix milliseconds (1).
3. Batch numbers and sequence numbers start at 1, and `H_0 = zero(32)` (4.1, 6).
4. One sequence numbers every record, checkpoints and upload-failure notes included, and an event's `seq` equals its record's `s` (4.1). PR1 documents `seq` as gapless among events, which stops being true once checkpoints exist. The comment on `Envelope.Seq` in `pkg/event` needs to change, in WP-LG2 or WP-EV1.
5. The rtype table is fixed as listed, with `0x00` and every unlisted code reserved (4.2).
6. A header for an `os.*` event always has the zero project, and checkpoints and upload-failure notes get the header fields in 4.3's table. `pkg/event` doesn't yet refuse a `project_key` on an `os.*` event, so until a `kindTable` rule does, the ledger writer must (4.3).
7. The body's members, their order, and the nonce as hex (4.5).
8. Inclusion proofs fail on any value that isn't 32 bytes, and a verifier takes `n` from a verified head (5.2).
9. The server recomputes `H_b` from the uploaded `prev`, `R_b`, `n` and `b` (6).
10. `device_id` carries a one-byte length in `M_b` and in day leaves. DESIGN writes the receipt as `"pairtime-receipt-v1" || device_id || b || H_b || received_at`, which doesn't say how a variable-length id is encoded (7, 8).
11. The day tree: day numbers, the leaf encoding and order, the `0x04` chain byte, a head for every day including empty ones, the zero genesis, and the signed message `D_d` (8).
