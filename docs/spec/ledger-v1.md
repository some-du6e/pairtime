# Ledger format, version 1

Status: complete. Part 1 (WP-LG1) is sections 1 to 9 and 11 to 13; part 2 (WP-LG3) is section 10, the content: token commitments, the redacted reveal, keyed features and the challenge draw.

This is the byte-level specification of the ledger in DESIGN 3.6 and the day tree in DESIGN 7.4. It covers how a device derives its keys, how it turns each record into a 65-byte entry, how it seals entries into batches and chains the batches, what the server signs in a receipt, and how the server builds and chains the tree it publishes each day. Section 10 adds what a record commits about content and how a submission opens it. `pkg/ledger` implements it in Go. `tools/ledger_ref.py` implements it again in standard-library Python, shares no code with `pkg/ledger`, and checks it against the vectors in `testdata/ledger/` (section 11).

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

`device_id`, `project_key` and `session_key` are `pkg/event` opaque keys: 1 to 128 ASCII characters, each one of `A-Z a-z 0-9 _ -`. Writers refuse any other string in these fields, and so do verifiers wherever they read one, as in a receipt message or a day leaf.

## 2. Domain separation

Every value the ledger hashes into a tree or a chain, and every message it signs, starts with a byte or a string that says what kind of value it is, so no value can pass for another kind:

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
| SHA-256 of a token leaf | `0x00`, because it is a Merkle leaf | 10.2 |
| SHA-256 of a MinHash block | `0x05` | 10.5 |
| HMAC-SHA256 under `K_feat`, by what it keys | `0x01` error signature, `0x02` template, `0x03` command, `0x04` path, `0x05` word shingle, `0x06` character shingle | 10.5, 10.6 |
| HMAC-SHA256 under a draw seed, a score | `u8(len(device_id))`, 1 to 128, for a turn; `0x00` for a commit | 10.9 |

`0x02` is unassigned. No tagged SHA-256 input in version 1 starts with it.

A token leaf shares the leaf prefix with batch and day leaves on purpose, because it is the leaf of a content tree (DESIGN 3.6). No verifier mixes trees: it knows whether it is checking a reveal against `C`, an entry against `R_b` or a head against `T_d`. The token salt under `K_tok` has no tag, as in DESIGN 3.6, because `K_tok` keys nothing else. Neither has the seed of a draw, `SHA-256(r || SHA-256(M))`, which serves only as an HMAC key.

The plain digests carry no tag. The project and session digests (4.3) and a body's hash (4.6) each sit at a fixed offset in a fixed-length header or entry, where nothing else can take their place. So does `SHA-256(M)` inside the draw seed (10.9).

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
| `K_tok` | `pairtime/v1/token` | token salts (10.2) |
| `K_feat` | `pairtime/v1/feat` | keyed scorer features and keyed error signatures (10.5, 10.6) |
| `K_vault` | `pairtime/v1/vault` | the vault (DESIGN 3.5, WP-LG6) |

The salt is RFC 5869's default for a salt that isn't provided, HashLen zero bytes, which for SHA-256 is 32. With `L = 32` the whole derivation is two HMACs:

```text
PRK = HMAC-SHA256(zero(32), K_dev)              shared by all four keys
K_x = HMAC-SHA256(PRK, ascii(label) || 0x01)
```

HMAC pads a key shorter than SHA-256's 64-byte block with zeros, so an empty salt and `zero(32)` give the same `PRK`. RFC 5869's test case 3 runs through that path.

Other parts of pairtime derive keys under the same `pairtime/v1/` prefix: `backup` and `recovery` in DESIGN 7.11, `seal` in DESIGN 7.6, `project` in the git adapter (WP-AD5), `app` in the focus component (WP-OS2) and `session` in the hook path (WP-AG3). Their own specs define them, and the git adapter's and the hook path's are below. A new ledger key takes a new label, and no label is ever used for two keys. `K_dev` and the keys derived from it never leave the device unencrypted (DESIGN 4.3, 7.11).

The git adapter's project secret is `K_project`, derived as above with the label `pairtime/v1/project`. Project keys are HMACs under it, each over a domain string of its own, so a root commit and a folder can never give the same key:

```text
repo key   = "g" || hex(HMAC-SHA256(K_project, ascii("pairtime/v1/project/roots") || 0x00
                                   || root_1 || 0x0a || ... || root_n || 0x0a)[0:16])
folder key = "d" || hex(HMAC-SHA256(K_project, ascii("pairtime/v1/project/folder") || 0x00
                                   || utf8(folder))[0:16])
```

`root_1` to `root_n` are the repo's root commit ids in lowercase hex, sorted and without repeats. `folder` is the folder's canonical path with forward slashes, lowercased on Windows and macOS. `internal/adapters/git/keys.go` has the code, and `TestProjectKeyVectors` pins a vector for each form.

The hook path's session secret is `K_session`, derived as above with the label `pairtime/v1/session`. A harness session's `session_key` is an HMAC under it, over a domain string of its own:

```text
session key = "s" || hex(HMAC-SHA256(K_session, ascii("pairtime/v1/session/id") || 0x00 || utf8(id))[0:16])
```

`id` is the harness's own session id, as its hook payload names it (`session_id`, `sessionId` or Cursor's `conversation_id`). The key depends on the id alone, so the copies of one session's calls that two harnesses make have one key. The events of the session carry it, and `os.focus` lists it in `hosted_sessions`. `internal/agent/hookpath` has the code, and `TestSessionKey` pins a vector.

The harness adapters key a session's turns, subagents and scheduled prompts under `K_session` too, each over a domain string of its own (`internal/adapters`, WP-AD2):

```text
turn key  = "t" || hex(HMAC-SHA256(K_session, ascii("pairtime/v1/session/turn")  || 0x00 || part(session) || part(id))[0:16])
agent key = "a" || hex(HMAC-SHA256(K_session, ascii("pairtime/v1/session/agent") || 0x00 || part(session) || part(id))[0:16])
cron key  = "c" || hex(HMAC-SHA256(K_session, ascii("pairtime/v1/session/cron")  || 0x00 || part(session) || part(prefix))[0:16])
part(x)   = u32be(len(utf8(x))) || utf8(x)
```

`session` is the harness's own session id, `id` a turn's id (Claude Code's `prompt_id`, which its transcripts and OTel share) or a subagent's (`agent_id`), and `prefix` a scheduled prompt as a `Stop` hook lists it in `session_crons`, without the `… [+N chars]` marker Claude Code puts after a prompt it cut and, in a cut prompt, without any U+FFFD left at the end, which is what a cut through a UTF-16 surrogate pair leaves, and at most its first 1,000 code points. Claude Code cuts at 1,000 UTF-16 units, so a cut prompt with characters outside the Basic Multilingual Plane keeps fewer code points. The keys go in `turn_key`, `agent_key` and `pending_crons[].prefix_hash`. None depends on the harness, so, as with the session key, the copies of one turn that two harnesses make have one key. `TestKeys` in `internal/adapters` pins a vector for each, the memory key's too, and `TestPendingCrons` in `internal/adapters/claudecode` pins `prefix` for cut prompts, all computed apart from the code.

The agent saves the adapters' memory of their sessions in `state.json` beside the key of the `K_session` it was made under, so memory from under another device key, as after a restore, is left behind. That key is over a domain of its own, so no session, turn, subagent or scheduled prompt has it, and it never leaves the device (`internal/agent`, WP-AG7):

```text
memory key = "m" || hex(HMAC-SHA256(K_session, ascii("pairtime/v1/session/memory") || 0x00)[0:16])
```

A hook call's records and a log reader's records take their `event_id`s from `K_session` too, each over a domain of its own, so a call the agent parses again while its records keep their times, or a line it reads again, makes records of the same ids, which the agent leaves out where the ledger has them. A call timed by the agent's own stamp rather than the hook's (`hookpath.Times`) is stamped anew by the start that parses it again (`readHeld`), so its records get new times and new ids. Each id is a UUIDv7 (RFC 9562) whose 48-bit timestamp is the record's `t` in Unix milliseconds, and whose other bits are the first 10 bytes of an HMAC, with the version and variant bits set over them (`internal/agent`, WP-LO1):

```text
hook event_id = uuid7(t, HMAC-SHA256(K_session, ascii("pairtime/v1/hook-event") || u64be(len(utf8(call))) || utf8(call) || u64be(place)))
log event_id  = uuid7(t, HMAC-SHA256(K_session, ascii("pairtime/v1/log-event") || u64be(len(utf8(file))) || utf8(file) || utf8(key)))
uuid7(t, b)   = u48be(t) || (b[0] & 0x0f | 0x70) || b[1] || (b[2] & 0x3f | 0x80) || b[3:10]
```

`call` is the hook call's message id (`inbox.Message.ID`), 32 lowercase hex characters, and `place` the record's place in what the adapter's `Parse` made of the call, counted from its end, so the call's last record has place 0. `file` names the log file as the tailers do, by its path on the device, and `key` names the record among those its line makes (`adapters.Record.LogKey`, such as `1234.prompt`, or else the line's offset and the record's index, such as `1234.0`). The ids leave the device only in the records a submission reveals, and neither domain string is another `K_session` value's. `TestEventIDVectors` in `internal/agent` pins a vector for each, computed apart from the code.

One `K_dev` writes one chain. The nonce (4.4) and the token salts (10.2) are keyed by `s` alone, so they stay unique only while no `s` repeats under one `K_dev`. A `K_dev` that `pairtime restore` unwraps onto a new install (DESIGN 7.11) only verifies and opens the old chain's records. The restored install writes its own chain, under its own `device_id` and a `K_dev` generated for it, from `s = 1`, and keeps the old key for reading. A second chain under the old key would repeat every nonce and salt: a body or a salt revealed from the old chain would let a reader confirm guesses of the new chain's hidden bodies, or of its removed tokens, at the same `s`.

A rollback or a copy under the same `K_dev`, such as a home folder restored from an old backup or a reverted VM snapshot, writes such a second chain too. So before its first upload after a start, and whenever the server refuses its request counter or its `prev`, the client compares `GET /api/v1/ingest/state` with `batches.seg`. The chains have forked if the server holds a batch of this `device_id` that the ledger lacks or holds under another head, or if the install has `K_dev` but no ledger. After a fork, nothing more uploads under the old `device_id` and `K_dev`, which stay only to read and open the old chain, and a new device starts, as after a restore, with a new `device_id` and `K_dev` from `s = 1` and `b = 1` (ingest-v1 5; WP-AG8, D-new-device-wp-ag8).

## 4. Records

A record is one entry in a device's ledger: an event, a scorer checkpoint or an upload-failure note. The writer appends records to `ledger/YYYY-MM-DD.seg` (DESIGN 3.5). That file's layout belongs to WP-LG5 and nothing in it is hashed apart from the header and body bytes below.

### 4.1 Sequence numbers

Every record has a sequence number `s`. The device's first record has `s = 1`, and each record it writes after that, of any type, has the next number. There are no gaps and no repeats, and because one `K_dev` writes one chain (3), no `s` repeats under a `K_dev` either. An event's `seq` field equals the `s` of the record that holds it.

Checkpoints and upload-failure notes take numbers from the same sequence. So the record sequence has no gaps, while the events' own `seq` values skip the numbers those records hold.

Every record of a sealed batch keeps its number. A seal makes its records durable no later than its batch row: sync the segment before writing the row, or write the row into the segment so one fsync covers both. The uploader sends only durable rows, and a backup holds only sealed records, so a crash can cut only records that no sealed batch holds. At start the writer drops a torn tail and seals the records that survived. It gives the next record the number one past the larger of the last surviving `s` and the last sealed batch's `last_seq`. A sealed record lost anyway is lost evidence, which CC7 holds, and its number is never reused.

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
| `0x13` | `relay.message` |
| `0x14` to `0x3f` | reserved for event kinds added later |
| `0x40` | scorer checkpoint (DESIGN 3.6, 6.5) |
| `0x41` | upload-failure note (DESIGN 3.6, 7.4) |
| `0x42` to `0xff` | reserved |

`0x01` to `0x12` are the 18 kinds in the order of PR1's `kindTable`, as DESIGN 3.6 says. That order was used once. From now on the table is fixed: a code never changes meaning, even if `kindTable` is reordered, and a kind added later takes the next free code from `0x13` up. The first one, `relay.message`, took `0x13` on 2026-11-22 (docs/errata/2026-11-22-relayed-prompts.md), so the next takes `0x14`. A writer never emits a reserved code. A verifier fails a record whose code is `0x00` or from `0x42` to `0xff`, which version 1 never assigns. A code from `0x13` to `0x3f` that a verifier doesn't know belongs to an event kind added after the verifier was built: it reports the record's kind as unknown and leaves its body unchecked, and it still checks the record's place in its batch, because checking roots and heads never reads `rtype`. A server is never older than the clients that write to it, so for a server such a code is a fault, as an `f.v` newer than its own is (10.7), and it refuses or holds a reveal that shows one.

### 4.3 Header

```text
hdr     = u64(s) || u8(rtype) || project || session        33 bytes
project = SHA-256(ascii(project_key))[0:16], or zero(16) for no project
session = SHA-256(ascii(session_key))[0:8],  or zero(8) for the session "none"
```

| Record | `project_key` | `session_key` |
|---|---|---|
| An event, `0x01` to `0x13` | the event's `project_key`; no project when it is absent | the event's `session_key`, which is `none` for OS and git events (`pkg/event`'s `NoSession`) |
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
| `e` | the record. For `0x01` to `0x13`, the event after `Event.Shareable()`, as `Event.MarshalJSON` writes it. For `0x40` and `0x41`, the objects of 4.7 | always |
| `f` | the record's keyed scorer features, versioned by `f.v` (10.7) | on every `prompt.submit`, `agent.tool`, `shell.command`, `cli.track` and `relay.message`, and on no other record |
| `c` | the record's content commitment, `{"n": token count, "r": C}` (10.2) | on every `prompt.submit`, final `agent.output` and `cli.track` apart from the ones 10.1 lists whose text the device never had, and on no other record |

So every body starts with the six bytes `{"n":"`, then the nonce in hex and a closing `"`. A body holds no text: `e` comes from `Shareable()`, and `f` and `c` hold keyed values, counts and the prompt class (DESIGN 3.6, 4.2). The counts and the class are plain values: `c.n`, the line numbers of `f.h`'s hunks, and `f.pc`. A metadata reveal shows them with each body it carries, so it tells the server every revealed prompt's token count, about twice its word count, which `len_chars` doesn't give.

No ledger was written before `f` and `c` existed, so a body that lacks one where the table puts it fails verification. A writer that left them off would keep its prompts out of the challenge pool, which takes only records that commit their text (DESIGN 7.5 step 4), and out of the server's dark-flow re-score, which reads only `f` (DESIGN 6.2 D18).

A body is a JSON text (RFC 8259) in valid UTF-8 (RFC 3629). Verifiers in different languages must read the same values from it, so every member name in a body is lowercase: one or more of the ASCII characters `a-z`, `0-9` and `_`. No object has two members of the same name, and the top level holds exactly the members of the table above, in the table's order. A body that breaks this is invalid. Without the rule one stored body could hold two records. Go's `encoding/json` matches member names without regard to case, and keeps the last of two names that match. It folds some non-ASCII letters too, such as U+212A, the Kelvin sign, which matches `k`. So a body holding both `locked` and `LOCKED`, or one that spells `locked` with U+212A, reads one way in Go and another way in Python or JavaScript, which match names exactly. An honest writer never emits such a body: every member name `pkg/event` and this spec use is lowercase, and so are a checkpoint's (4.7).

A verifier that parses a revealed body also checks that it agrees with its header and its chain. `e` is a record of type `rtype`. For an event, `e`'s `seq` is `s`, its `device_id` is the device whose chain holds the record, and the digests of its `project_key` and `session_key` are the header's `project` and `session`. For a checkpoint, the digest of `e.project_key` is the header's `project` (4.7). `f` and `c` appear exactly where the table and 10.1 put them, and `f` fits its kind (10.7). A body that disagrees fails verification, as a body whose hash differs would.

The `device_id` check is what binds a chain to one install. Headers, entries, `R_b` and `H_b` name no device, so without it one account could upload another device's batch rows `{b, n, R_b, H_b, prev}` under its own `device_id` as that device produces them, collect receipts and day leaves for them, and later reveal the other device's headers and bodies with every check passing (DESIGN 4.5). With it, the first revealed event body shows the replay. A server may also refuse a batch whose `H_b` it already stored for another device. Honest heads never collide, because every entry hashes a body that carries a device-keyed nonce.

### 4.6 Entry

```text
entry = hdr || SHA-256(body)        65 bytes
```

Entries are what the batch tree is built from. A revealed batch shows the header of every record in it, and each body's hash stands in for the body until the body itself is revealed (DESIGN 4.2).

### 4.7 Checkpoints and upload-failure notes

A checkpoint's `e` is `{"project_key": p, "t": t, "state": S}`, members in that order. `p` is the project whose state it holds, the key whose digest the header carries. `t` is the checkpoint's UTC minute in Unix milliseconds, a multiple of 900,000, because the scorer seals checkpoints at the minutes divisible by 15 (DESIGN 3.6, 6.5). `S` is the serialized `score.ProjectState` (WP-SC5), keyed values only. DESIGN 6.5's range rule needs `t` and the header check needs `p`, and neither should depend on parsing `S`, whose layout belongs to `pkg/score`. `S` follows 4.5's rule like the rest of the body, so its member names are lowercase too: a map keyed by opaque keys, which may hold capitals, goes in as a list of pairs.

An upload-failure note's `e` is `{"t": t, "reason": r}`. `t` is when the upload failed, in Unix milliseconds, and `r` is one of `dns`, `connect`, `tls`, `http_5xx` and `http_429` (DESIGN 3.6). The note is the client's own claim (DESIGN 7.4).

A verifier refuses a checkpoint or note whose `e` has other members, a `t` that isn't an integer (or, for a checkpoint, isn't a multiple of 900,000), a `p` that isn't an opaque key, or an `r` not in the list.

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

The client uploads only durable rows (4.1), each as `{b, n, R_b, H_b, prev}` with `prev = H_(b-1)` (DESIGN 4.3); the upload's JSON is WP-SV4's. The server refuses a batch with `n = 0`, which no writer seals, and a batch whose `H_b` isn't `SHA-256(0x03 || prev || R_b || u64(n) || u64(b))` for the values it received. It also checks DESIGN 3.10's chain link, `prev` equals the stored head: it refuses a batch 1 whose `prev` isn't `zero(32)`, and once it has stored any batch of a device, it refuses that device's batch `b` unless it stored batch `b - 1` with head `prev`, but for the second exception below. So a device that has stored batches 1 to 3 can't upload batch 5 until batch 4 is in, and batch 4 gets a `received_at`, a receipt and a day leaf like any other. The server then stores `(device_id, b, n_b, R_b, H_b, received_at)`, where `received_at` is the server's clock, in Unix milliseconds, when it stored the row.

Two exceptions let a device skip batches it sealed in local-only mode, which it never sends (DESIGN 4.2). A device's first upload can start after batch 1, and the server starts the device's witnessed chain at that batch (ingest-v1 2.7 step 4). And after the device signs in again, the first batch the server stores under the new sign-in may skip ahead of the stored head: the server records the batches it skipped as a local-only gap and never takes their roots (ingest-v1 2.7 step 5; D-local-gap). From either one, every batch links to the one before it. This spec fixes the bytes; ingest-v1 fixes when the server takes them.

## 7. Receipts

For each batch it stores, the server returns a signed receipt:

```text
M_b       = ascii("pairtime-receipt-v1") || u8(len(device_id)) || ascii(device_id)
            || u64(b) || H_b || u64(received_at)
receipt_b = Ed25519(server key, M_b)        64 bytes
```

`device_id` is 1 to 128 bytes, so its length fits in the one byte before it, and every other field has a fixed width, so `M_b` splits back into its fields from the front.

The server returns `received_at` with each `receipt_b`, because the client can't rebuild `M_b`, or check the signature, without it. The client stores `received_at` next to the receipt with the batch in `ledger/batches.seg` (DESIGN 3.5, whose row names only the receipt), as `pkg/ledger`'s `Receipt` holds both, and checks the receipt under the server key that was valid at `received_at` (DESIGN 10.2) before relying on it. The receipt binds the server to `received_at`: the server can't later deny or re-date a head the device holds a receipt for, and the device can't claim the batch arrived earlier (DESIGN 3.6).

## 8. Day trees

Once a day the server publishes a root over every batch it stored that UTC day, chained to the day before (DESIGN 7.4). It lets an auditor check that the server didn't insert or re-date a batch afterwards.

The day number of a time `t` is `d = floor(t / 86,400,000)`, days since 1970-01-01 UTC. Day `d` holds every stored row whose `received_at` falls on day `d`, and each row becomes one leaf:

```text
leaf = u8(len(device_id)) || ascii(device_id) || u64(b) || H_b || u64(received_at)
```

The leaves are ordered by `received_at`, then by `device_id` compared as unsigned bytes, a shorter id sorting before a longer one it begins, then by `b`. No case folding, locale or collation enters the comparison: `Zq` sorts before `aB`, `a-` before `aB` before `a_`, and `w` before `w_`, while `strings.ToLower` or a database's `en_US` collation would order them otherwise and publish a different `T_d` for good. That order has no ties, because the server stores each `(device_id, b)` once. Equal `received_at` values are common: a device that was offline flushes many batches in one request.

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

The server key that signs `D_d` is the one valid at the last millisecond of day `d`, `(d + 1) × 86,400,000 − 1`, by DESIGN 10.2's validity dates, even when the head is signed or published later. WP-SV17 signs a day at 00:10 UTC the next day and publishes a missed day on a later run, so the key valid at signing time could be a newer one. Fixing the key by `d` lets a verifier find it from the day alone, as step 7 of section 9 finds a receipt's from `received_at`. After a rotation, the server keeps the old key until it has signed every day that ended before the rotation.

It commits `d`, `G_(d-1)`, `T_d`, `count_d`, `G_d` and `signature_d` to the public `pairtime-witness` repository, one file a day at `<YYYY>/<YYYY-MM-DD>.json`, named by the day's UTC date, such as `2026/2026-11-19.json` for 12.5's day:

```json
{
  "format": "pairtime-day-root-v1",
  "day": 20776,
  "date": "2026-11-19",
  "prev": "<G_(d-1), 64 hex digits>",
  "count": 2,
  "root": "<T_d>",
  "head": "<G_d>",
  "key_id": "<16 hex digits>",
  "signature": "<signature_d, 128 hex digits>"
}
```

The file has those nine members in that order and no others, indented by two spaces, with a newline at the end. `key_id` names the server key that signed, the one valid at the end of the day, as `/.well-known/pairtime-keys.json` lists it (docs/spec/keys-v1.md). The file holds hashes, a count and a signature, and never a device id or a time within the day.

A day is due 10 minutes after it ends. At each start and at 00:10 UTC, `pairtimed` publishes every due day that has no head yet, oldest first from `d_0`, the day of the first row it stored, so a day missed while it was down goes out on the next run. The server signs a due day only while the database's replica is within 60 s of it (DESIGN 10.3, 10.4), so the replica holds the day's last rows, and a head follows from its rows and the key alone, since Ed25519 signatures are deterministic. So a server restored from the replica signs the same head again. `pairtimed` stores each head in its database before it writes the head's file, and it resumes the chain from the stored heads, never from the files. It writes each file under a name that starts with a dot in the file's own directory, then renames it, and the witness sidecar skips such names, so the repository never receives part of a file. A file, once written, never changes.

Each bundle carries the index of each batch's leaf in its day tree and the leaf's inclusion proof (DESIGN 7.8), checked against `T_d` with `count_d` leaves. So the server keeps each published day's leaves too: the `count_d` hashes `LEAF(leaf)` in leaf order, stored in the transaction that stores the head and kept as long as any of the day's witness rows (docs/errata/2026-11-23-day-leaves-retention.md). It makes each proof from them and from the batch's own row, never from the day's other rows, so a batch's proof outlives the day's other rows, which retention and an account's erasure delete. The leaves are hashes alone, and the server doesn't publish them.

A published day holds every row on it, because the server never stores a row on a day it has published. While its clock reads a time at or before the end of the newest day it has published, as after the clock was set back across a published midnight, ingest refuses every upload with 503 `day_closed` and stores and receipts nothing (ingest-v1 2.5 step 13, 2.6). Otherwise a batch could hold a receipt and be in no day's tree. And the transaction that stores a day's head counts the day's rows again: when a row came or went after the tree was built, it stores nothing, and the next run builds the tree again, so a head is stored over exactly the rows it commits to.

## 9. Verification

A verifier that holds a run of a device's batches, with headers and body hashes for every record in them, checks:

1. Batch numbers rise by 1 with no gap, but across a local-only gap the server recorded (6), after which a run of its own starts. A run that starts at `b = 1` has `prev = zero(32)`. A run that starts later links its first `prev` to the head the server stored for the batch before it. Only a run that starts at a device's first witnessed batch, where WP-SV4 started the chain after batch 1, or at the batch after a local-only gap (6), takes its first `prev` as given.
2. Sequence numbers rise by 1 with no gap, within and across batches, but across a local-only gap. Batch 1 starts at `s = 1`. Checkpoints and upload-failure notes hold numbers like events, so this runs over every record's `s`, never over events' `seq` alone, and nothing else excuses a gap (4.1).
3. Every header follows 4.3: its rtype isn't `0x00` or `0x42` to `0xff`, a code from `0x13` to `0x3f` that the verifier doesn't know is reported as an unknown kind (4.2), an `os.*` header has the zero project, and checkpoints and upload-failure notes have the fields their row in 4.3 gives them.
4. Every entry is its header followed by the SHA-256 of the record's stored body, and every revealed body is valid JSON by 4.5's rule, agrees with its header, names the chain's device in an event's `device_id`, and has `f` and `c` exactly where 4.5, 4.7, 10.1 and 10.7 put them.
5. `n_b` is the number of the batch's records, and `R_b` is the `MTH` of their entries.
6. `H_b` follows from `H_(b-1)`, `R_b`, `n_b` and `b`.
7. Each receipt verifies over `M_b` under the server key that was valid at `received_at` (DESIGN 10.2).
8. Each inclusion proof verifies (5.2), with `n` taken from a verified head.
9. Each day tree has its rows in order and on its own day, its `count_d`, `T_d` and `G_d` follow from them, its signature verifies over `D_d` under the server key that was valid at the end of day `d` (8), and the day chain has no gaps.
10. Each revealed text verifies `ok` against its record's `c` (10.3).
11. The draw follows from `r`, `SHA-256(M)` and the pool (10.9).

`pkg/ledger` reports a gap in `b` or `s`, a root mismatch and a head mismatch as distinct errors (WP-LG2). Each one points at a different fault: a batch that was hidden or dropped, a record that was changed or moved, a chain that was rewritten.

## 10. Content

Part 2 specifies what DESIGN 3.6 commits about content. That covers the token commitment of each prompt, final agent message and `track` label; the redacted reveal that opens one; the keyed features in `f`, which let the server recompute dark-flow discounts without text (DESIGN 6.2 D18); what CC8 can check of them without a key (DESIGN 7.3); and the challenge draw of DESIGN 7.5 step 4. It uses `K_tok` and `K_feat` (3) and defines version 1 of the feature map, `f.v = 1`.

SM describes several of these rules in words, such as "replaces numbers with `<n>`". This section turns each into steps, because the client that commits and the server or auditor that checks are different programs, and they must agree on every byte. A later version can improve a heuristic only as `f.v = 2`, with new keys. Records written under version 1 keep meaning what this section says (DESIGN 3.6, 6.6).

### 10.1 What a record commits

Three kinds of record commit a text, one text each:

| `rtype` | Record | Committed text |
|---|---|---|
| `0x03` | `prompt.submit` | the prompt as the harness delivered it, pasted blocks expanded |
| `0x04` | `agent.output` with `is_final` true | the turn's final agent message, cut to its first 16 KiB as below |
| `0x12` | `cli.track` | the label after `pkg/redact` (DESIGN 5.4) |

No other record has a `c` member. DESIGN 3.6 names prompts and final agent messages. `cli.track` joins them because DESIGN 5.4 stores and commits the label "so a `track`-anchored turn can be drawn by the challenge like any prompt", and DESIGN 7.5 step 4 puts `track` calls in the pool. A `relay.message` doesn't: it opens no anchor unless it pairs, and the prompt it pairs with commits that prompt's text, which the challenge draws instead (DESIGN 5.2, 7.5 step 4).

Some records of these kinds reach the device without their text, and only these may lack `c`:

- a `prompt.submit` from the WakaTime bridge, `via` `bridge` or `bridge_unverified`, which carries only the prompt's length (DESIGN 5.7);
- a `prompt.submit` or final `agent.output` from a plugin, `via` `plugin`, whose event came without its text (DESIGN 5.3);
- a `prompt.submit` marked `guest`, the prompt T3 Code made of a guest's message, whose text the agent drops when it arrives (DESIGN 5.1).

Such a record has no `c`. A prompt's `f` then holds only `v` and `pc` (10.7), so scorers have no sketch, template or error signature for it, and SM 4.2's re-roll and fix-it rules can't count it (DESIGN 6.2 D4). The challenge can't draw it (DESIGN 5.7). Every other `prompt.submit`, every final `agent.output` and every `cli.track` has `c`, and a verifier fails a body that lacks it (4.5): `via` and `guest` are in `e`, so a verifier can tell the cases apart, and a client can't escape the draw by leaving `c` off a hook or log prompt. One that marks a prompt `guest` keeps it out of the draw only because the prompt earns nothing (SM 2.2 rule 4). A client that labels its prompts `bridge` or `plugin` instead does escape it, as DESIGN 5.7 accepts for every text-less prompt: a bridge prompt gets no think lead (DESIGN 6.2 D4), and a bridge or plugin anchor is level C unless keyboard input in the host window corroborates it (DESIGN 6.4).

The committed text is a byte string of valid UTF-8 as RFC 3629 defines it: no surrogates, no overlong forms, nothing above U+10FFFF. A writer holding text that isn't valid UTF-8 makes it valid before committing. How it does that isn't part of the format, because nobody recomputes it. The vault keeps the committed bytes (DESIGN 3.5, WP-LG6), and a reveal opens those. Nothing in part 2 applies Unicode normalization such as NFC.

An agent message longer than 16,384 bytes commits its longest prefix of at most 16,384 bytes that ends on a character boundary. In bytes: start with `k = 16,384`, lower `k` by one while byte `k` of the message is a continuation byte (`0x80` to `0xbf`), then commit bytes `0` to `k - 1`. That prefix is the "first 16 KiB" the vault keeps (DESIGN 3.5). Prompts and labels commit in full.

### 10.2 Tokens and the commitment

Whitespace in part 2 is exactly the 25 code points of Unicode's `White_Space` property, the same in Unicode 15.0.0 and 17.0.0:

```text
U+0009 to U+000D, U+0020, U+0085, U+00A0, U+1680, U+2000 to U+200A,
U+2028, U+2029, U+202F, U+205F, U+3000
```

Implementations test these 25 code points directly, as 10.4 pins its lowercase table, and not through a library's `White_Space` table, which follows the library's Unicode version: U+180E left the set in Unicode 6.3. A server whose table gained a code point would read an honest kept token holding it as mixing kinds, `bad_item` (10.3), and would strip and normalize texts (10.4) unlike the client, so CC8 could fail an honest turn (10.8). Go's `unicode.IsSpace` tests the same set today, which makes it a useful cross-check in tests. Python's `str.isspace` doesn't, because it adds U+001C to U+001F.

The tokens of a text are its maximal runs of whitespace and its maximal runs of other code points, in order. So neighbouring tokens are of different kinds, no token is empty, and the tokens concatenate to the text. The empty text has no tokens. CRLF is a whitespace token, or part of one. Tokens are numbered from `j = 1` to `n`.

```text
salt_j = HMAC-SHA256(K_tok, u64(s) || u64(j))        32 bytes
t_j    = SHA-256(0x00 || salt_j || w_j)               = LEAF(salt_j || w_j), 5.1
C      = ROOT(t_1, ..., t_n)
```

`s` is the record's sequence number, and `w_j` is the UTF-8 bytes of token `j`. `C` is a `ROOT` over leaf hashes the writer computes itself (5.1), so each `t_j` carries the leaf prefix once. With `n = 0`, `C` is the SHA-256 of no bytes, the same for every empty text, so `c` shows that a text is empty. For a prompt that is nothing new, because its `len_chars` is 0, nor for an agent message, whose `words` and `code_lines` are 0. A `track` label that `pkg/redact` emptied is the exception: its `text_len` counts what the model passed, so its empty `c` shows that redaction removed all of it. A record with no `c` at all is one whose text the device never had (10.1), not an empty text.

The body's `c` member holds the token count next to the root, `n` as a JSON number and `C` as 64 hex digits:

```text
"c":{"n":15,"r":"aa9bfca6f19928733e7a62d0bfad12c6251486f7085a01a297e110631d80ad8b"}
```

DESIGN 3.6 writes the commitment as `C_i` alone. The count has to be committed as well, because a reveal shows each removed token as a leaf hash the server can't check (10.3). Without `n`, a client could hand over a subtree's hash as if it were one removed token. The root still matches, and the reveal understates how much was removed (`vectors-content.json` has such a case). With `n` committed, the tree's shape is fixed, so every leaf in a reveal has to be the real one.

Each salt is its own HMAC, and a reveal shows the salts of kept tokens only. So a removed token's leaf can't be used to test a guess, such as a guessed email address, because its salt stays secret. That is why the salts come from `K_tok` one token at a time, rather than from a per-record key that a reveal would have to open. `K_tok` keys nothing else, and its input is always these 16 bytes.

### 10.3 The redacted reveal

The teen reveals a drawn text as a list of items in token order (DESIGN 7.5 step 6). A kept token comes with its salt. A removed span is a run of consecutive tokens, shown as their leaf hashes with a type and a count:

```json
{"salt": "0de98862b0247d799268f403f86206de96c6880d5bac2e907f01e7d392a8a399", "token": "20"}
{"type": "known", "count": 1, "leaves": ["7aa845239d74ce39e31dc06ff6a54e9ad62ebaa4b00e66abf3e2c43bf4aecfd2"]}
{"type": "code", "count": 11, "leaves": ["...eleven hashes..."], "lines": 3}
```

`token` is the token's UTF-8 bytes in hex, so JSON string escaping can't change them. `lines` appears only on a `code` span, where it gives the number of lines of the collapsed code block (DESIGN 7.5). It is the client's claim, and nothing checks it. The types are a closed list, so a type can't carry free text:

| Type | What removed the span |
|---|---|
| `known` | the teen's known values: account name, home directory, hostname, git name and email, GitHub and HCA usernames, private repo and folder names, the deny list |
| `secret` | gitleaks' rules |
| `pii` | the validated PII patterns ported from Presidio |
| `code` | a fenced code block collapsed in an agent message |
| `teen` | the teen, by hand |

`pkg/redact` (WP-PV1) finds spans as character ranges and rounds each out to whole tokens, so removing an email address inside `email:teen@example.com,` removes that whole token. Adding a type means changing this table.

The submission's own message names the record of each reveal by `(device_id, s)` (WP-SB5). The verifier takes `n` and `C` from that record's body, which the metadata reveal has already tied to a witnessed batch (DESIGN 7.5 step 3). It then checks the items in this order and reports the first failure:

1. `bad_item`: an item isn't well formed. For a kept token: the salt isn't 32 bytes, or the token is empty, isn't valid UTF-8, or mixes whitespace with other code points. For a span: the type isn't in the table, the count is below 1, `leaves` isn't `count` values of 32 bytes, or `lines` is present on a span that isn't `code` or is below 1.
2. `count`: the kept tokens and the span counts don't add up to `n`.
3. `alternation`: two kept tokens break strict alternation. Walking the items gives each token a position, and two kept tokens at positions `p < q` must be of the same kind exactly when `q - p` is even. Checking each kept token against the previous kept token covers every pair.
4. `root`: `ROOT` of the leaves isn't `C`. A kept token's leaf is `SHA-256(0x00 || salt || token)`, and a span contributes its given hashes.
5. `ok`: none of the above.

An `ok` reveal proves that each kept token is the committed token at its position, and that each span covers exactly `count` committed tokens. It proves nothing about what a span held or why it was removed. The reviewer sees the kept text, with each span shown as its typed marker (DESIGN 7.7 item 5).

Each kept token costs 32 bytes of salt, and each removed token 32 bytes of leaf, before hex. Fully removing a 16 KiB agent message of about 3,000 tokens costs about 200 KB of hex. WP-SB5 sets the size limits for a reveal; this format sets none.

### 10.4 Normalization

SM 4.2 compares prompts after normalizing them. Normalization takes a committed text (10.1) and returns a list of words. The normalized text is those words joined by single spaces (U+0020). The steps run in this order.

**1. Lines.** Split the text at every LF (U+000A), and remove one CR (U+000D) from the end of each line that ends with one. So `a<CR><LF>b` has the lines `a` and `b`.

**2. Pasted blocks and error text.** Walk the lines from the top. At each line, the first rule below that applies removes that line and the lines it names, and the walk goes on after them. A line no rule applies to stays. "Stripped" means with leading and trailing whitespace (10.2) removed, and "matches" means the whole line matches.

- *Fenced block.* The line matches `F1` below, and for a backtick fence the rest of the line, the second group, holds no backtick (so a one-line `` ```js x = 1``` `` isn't a fence). The block runs to the first later line that matches `F2` with a fence of the same character, at least as long as the opening one, or else to the end of the text. The closing line goes with it. This is CommonMark's fenced code block without its other cases.
- *Pasted block.* The stripped line matches `P1`, the opening line of the wrapper Claude Code puts around pastes in sessions that mark them (SM 1.2). The block runs to the first later line whose stripped form matches `P2`, the closing line, or else to the end of the text, and the closing line goes with it. Claude Code 2.1.327 writes a paste as `<pasted_content id="1a2b">`, a line break, the pasted text, a line break, then `</pasted_content id="1a2b">` and a line break, where the id is four lowercase hex digits that the closer repeats. `P2` accepts any attributes, so a closer without an id, as older sessions wrote, closes a block too. It doesn't compare ids, so a paste that itself holds a closing line ends there, and the rest of it stays as text.
- *Traceback.* The stripped line is `Traceback (most recent call last):`. It goes, with every following line that starts with a space or a tab. The exception line that ends a traceback then meets the next rule.
- *Error line.* The stripped line matches one of `E1` to `E6`. It goes, with every following line that starts with a space or a tab, which takes stack frames and compiler carets with it. After `E2` or `E3`, a following line that matches `R` goes too: a compiler's gutter line with its line number at column 0, where rustc writes the widest number, as in `2 |     let x: i32 = "a";`. After `E6`, a following line that starts with `[signal ` goes too, and then each goroutine block of the panic: the blank lines before it, its first line, whose stripped form matches `G`, and every line after that which starts with a tab or comes just before one that does, so each frame goes with the file line under it. A blank line is one whose stripped form is empty, and a goroutine block ends at the first line that is neither.

```text
F1  " {0,3}(`{3,}|~{3,})(.*)"
F2  " {0,3}(`{3,}|~{3,})[ \t]*"
P1  "<pasted_content(?: [^>]*)?>"
P2  "</pasted_content(?: [^>]*)?>"
E1  "(?:Uncaught (?:\(in promise\) )?|Exception in thread "[^"]*" |Caused by: )?(?:[A-Za-z_$][A-Za-z0-9_$]*\.)*[A-Za-z0-9_$]*(?:Error|Exception)(?::.*)?"
E2  "error\[E[0-9]{4}\](?::.*)?"
E3  "[^ \t]+:[0-9]+:[0-9]+: (?:fatal )?error:.*"
E4  "FAILED .+"
E5  "--- FAIL: .+"
E6  "panic: .+"
R   "[0-9]+ +\|.*"
G   "goroutine [0-9]+ [^\[]*\[[^\]]*\]:"
```

The quotes at the ends aren't part of the patterns. `E1` to `E6` are SM 4.2's first error lines: an exception line in Python, JavaScript or Java, with the JVM's `Exception in thread "main" ` or `Caused by: `, or a browser's `Uncaught ` or `Uncaught (in promise) `, before it (`E1`), Rust's `error[E0xxx]` (`E2`), a compiler's `file:line:col: error:` (`E3`), pytest's `FAILED` (`E4`), `go test`'s `--- FAIL:` (`E5`) and a Go panic (`E6`). `R` is matched against the line itself and `G` against its stripped form. Every pattern uses ASCII character classes only, is case-sensitive, and needs no lookaround or backreference, so it means the same in Go's `regexp` and Python's `re`. `.` matches any code point, and no line holds an LF.

**3. Words.** The words of each line that stays are its tokens that aren't whitespace (10.2), in order. A line break separates words like any other whitespace.

**4. Lowercase.** Replace each code point of each word with its simple lowercase mapping in Unicode 15.0.0, field 13 of `UnicodeData.txt`, or keep it when it has none. The mapping takes one code point to one: `İ` (U+0130) becomes `i`, `Σ` becomes `σ` even at the end of a word, and `ß` stays. The version is pinned because case tables grow with Unicode and a library's tables follow its own version. Unicode 16.0.0 and 17.0.0 add 55 mappings and change none of these, so Go 1.27's `unicode.ToLower`, whose tables are 17.0.0, differs from this mapping at exactly those 55 code points. `vectors-content.json` lists the whole mapping, 1,433 code points, with the 55 later ones beside it. `selftest` checks the table against Python's `unicodedata`: exactly when that is 15.0.0, and allowing only the 55 when it is 15.1.0 to 17.0.0.

**5. Substitutions.** Apply these within each word, in this order, each to the output of the one before.

1. *URL.* Find the leftmost match of `[A-Za-z][A-Za-z0-9+.-]*://` and replace everything from its start to the end of the word with `<url>`.
2. *Identifier.* Replace each match of `` `[^`]+` ``, leftmost first and without overlaps, with `<id>`.
3. *Path.* Take each maximal run of path characters: ASCII letters and digits and ``. _ - ~ $ % + @ : / \``. The run is a path when it holds at least one `/` or `\` and at least one letter or digit, and also one of these: it starts with `/`, `\`, `./`, `.\`, `../`, `..\`, `~/` or `~\`; it starts with a letter, a `:` and a `/` or `\`; it holds two or more `/` and `\` in all; or the part after its last `/` or `\` holds a `.` followed by a letter or digit. Replace each path with `<path>`. So `src/main.go`, `./run.sh` and `a/b/c` are paths, and `and/or` and `src/components` aren't.
4. *Hex.* Take each maximal run of ASCII letters and digits. The run is hex when it is `0x` or `0X` followed by one or more hex digits, or when it is seven or more hex digits with at least one digit and at least one letter among them. Replace each hex run with `<hex>`. So `3f9a2c1d` and `0x7ffd` become `<hex>`, while the words `deadbeef` and `facade` stay.
5. *Number.* Replace each maximal run of ASCII digits with `<n>`.

**6. Bare words.** Strip each word to its bare form. From the left, drop the word's first character while it is punctuation and the word doesn't start with one of step 5's placeholders, `<url>`, `<id>`, `<path>`, `<hex>` and `<n>`; from the right, drop its last character while it is punctuation and the word doesn't end with one. Punctuation here means the 32 printable ASCII characters that are neither letters, digits nor space, and U+2018, U+2019, U+201C and U+201D. Then drop every `'` and U+2019 left in the word, so `doesn't`, `doesn’t` and `doesnt` are one word. A word that is left empty, such as `!!` or `--`, goes.

**7. Retries.** Walk the words from the left, trying SM 4.2's retry phrases at each word, longest first: `still not working`; then `try again`, `fix it`, `still broken` and `doesnt work`; then `again` and `retry`. A phrase of `k` words matches when the next `k` words equal its words. A match becomes the single word `<retry>`, and the walk continues after it. Otherwise the word stays and the walk moves on by one word.

So `still broken, see src/nav.js:12` normalizes to `<retry> see <path>`. Punctuation at the edges of a word goes, and punctuation inside one stays: `mobile`, `mobile,` and `mobile!!` are one word, while `e.g` and `eg` are two. SM 6.5's 20:07 row needs the first half: `still overlapping on mobile, fix it!!` and the 20:02 prompt `still overlapping on mobile. fix it` both normalize to `still overlapping on mobile <retry>`, so they have one `th` and equal sketches, and 20:07 is attempt 3. A person can apply every rule here by hand, and none of them guesses at meaning. So a re-roll in new words goes unnoticed, which SM 4.2's rewrite rule is there to catch, and two prompts that differ only in a substituted value, such as two identifiers, normalize alike.

### 10.5 Shingles and the keyed MinHash

The shingles of a prompt follow SM 8.5. When the prompt has six or more normalized words, they are its word 3-shingles: each run of three consecutive words, joined by single spaces. When it has fewer, they are the character 5-shingles of the normalized text: each run of five consecutive code points. A normalized text of one to four code points is its own single shingle, and the empty text has none. The shingles form a set, so a repeated shingle counts once. The six, three and five are SM 8.5's `SHINGLE_MIN_WORDS`, `SHINGLE_WORDS` and `SHINGLE_CHARS`, and the 64 below is its `MINHASH_PERMS`. Version 1 of the feature map fixes all four, so a new value takes `f.v = 2` (10, 10.7).

Each shingle `x` gets a per-shingle value under `K_feat`, with a tag byte for its kind:

```text
v(x) = HMAC-SHA256(K_feat, 0x05 || x)        a word 3-shingle
v(x) = HMAC-SHA256(K_feat, 0x06 || x)        a character 5-shingle
```

`x` is the shingle's UTF-8 bytes. A shingle's 64 MinHash values come from `v(x)` through SHA-256:

```text
B_k(x)      = SHA-256(0x05 || v(x) || u8(k))     for k = 0 to 7
h_(8k+r)(x) = bytes 4r to 4r+3 of B_k(x), as a big-endian 32-bit integer, for r = 0 to 7
mh[i]       = the smallest h_i(x) over the prompt's shingles, for i = 0 to 63
```

The sketch of the empty set is `0xffffffff` in all 64 places. `mh` is 256 bytes, the 64 values in order, each big-endian, which is SM 4.2's 256 bytes per prompt.

The estimate of the Jaccard similarity of two prompts is `m / 64`, where `m` is the number of places `i` at which their sketches are equal. A comparison with a threshold such as `SIM_JACCARD` uses exact rationals, as the plan's ground rule 9 asks: `m` passes a threshold of `num / den` when `64 × num ≤ m × den`. At 0.8 that is `m ≥ 52`. Two empty sketches are equal, which agrees with SM 4.2: two empty normalized texts are identical.

No one can compute a sketch value without `K_feat`, so a reader who holds a body can't test a guess at its prompt. The 64 values behave as independent hash functions, since each is a separate 32-bit piece of SHA-256 output over a keyed value. That is MinHash as Broder describes it, with an unbiased estimate and a standard error of at most 0.0625 for 64 values. `selftest` measures the error on random sets.

### 10.6 Keyed 16-byte values and edit hunks

Error signatures, templates, commands and file paths enter `f` as 16-byte keyed values, each kind with its own tag:

```text
K16(tag, x) = HMAC-SHA256(K_feat, u8(tag) || x)[0:16]

es = K16(0x01, error signature)      th = K16(0x02, template)
ch = K16(0x03, command)              p  = K16(0x04, path)
```

`x` is UTF-8 bytes. The tags keep kinds apart, so a command and an error signature with the same text get different values. In JSON each value is 32 lowercase hex digits, which also makes it a valid `pkg/event` opaque key.

**Error signature.** The signature of a text is its first line, in the lines of 10.4 step 1, searching from the top and inside fenced and pasted blocks too, whose stripped form matches one of `E1` to `E6`. Split that stripped line into words, pass each word through the substitutions of 10.4 step 5, and join the words with single spaces. The signature isn't lowercased and gets neither step 6 nor step 7, because an error line's punctuation is the program's, the same each time the error recurs. A text with no such line has no signature. A traceback's signature is its exception line, because no frame line matches `E1`. `es` is the one way pairtime keys an error signature. A prompt's goes in `f.es`. An `agent.tool` record takes its signature from the tool's output, and a `shell.command` record from the command's output where pairtime reads it, which SM 4.2 doesn't for human terminals in v1. Either way the keyed value goes in the event's own `error_sig` field as hex (DESIGN 3.6). Using one function for both lets the fix-it rule count a pasted error and the agent's test failure as one error (SM 4.2).

**Template.** A prompt's template is its normalized text (10.4). So two prompts have the same `th` exactly when their normalized texts are identical, which is SM 4.2's second test for a near-duplicate. The empty text has a template too.

**Command.** The command of an `agent.tool` or `shell.command` record is its command line as run, with each run of whitespace collapsed to one space and none left at either end. Case and everything else stay. A record with no command, or whose command is all whitespace, has no `ch`.

**Path.** A file's path is relative to the root of the record's project, with `/` between components and never `\`, no empty, `.` or `..` component, and no trailing `/`. A file outside the project keeps its absolute path, which starts with `/`, or on Windows with an upper-case drive letter, `:` and `/`. A path that starts with a letter and `:` is always such a Windows path. Case is kept, even on file systems that ignore it. Keyed this way, an agent's edit and a person's edit of one file give the same `p`, whichever tool reported them. `pkg/event`'s `files` and `file_hash` keys (WP-EV1) can use the same value.

**Hunk.** An edit hunk is `{"p": p, "os": old_start, "ol": old_lines, "ns": new_start, "nl": new_lines}`, exactly these five members: the path value and the four numbers of a hunk as `diff -U0` writes it, `@@ -old_start,old_lines +new_start,new_lines @@`. Such a hunk holds changed lines only: a run of removed lines and the added lines that replace them, either of which may be empty, with no unchanged line among them. SM 4.2's rewrite rule measures overlap over "lines the agent changed", and context lines would overstate those by each harness's context size: with three lines of context, an agent that appends three lines below the three it added in the turn before would look as if it rewrote them. Each number is from 0 to 2^31 - 1. As in GNU diff, a range of zero lines starts at the line before it, or at 0 at the top of the file, and a range with lines starts at 1 or later. Lines are numbered from 1. A file's lines are its pieces up to and including each LF, and a last piece without one when the file doesn't end with LF, and two lines are the same when their bytes are.

Edit tools report an edit in one of two ways, and each gives its hunks one way:

- *A patch*, a unified diff with context lines: Claude Code's `structuredPatch` and Codex's `patch_apply_end`. Walk each patch hunk's lines from its header's starts, where a start whose count is 0 names the line before, so the walk's first line on that side is the one after it. A line starting with ` ` is unchanged and counts in both files, `-` counts in the old file, `+` in the new one, and a line starting with `\`, such as `\ No newline at end of file`, counts in neither. Each maximal run of `-` and `+` lines is one hunk. Its old range holds its `-` lines and starts at the first of them, or at the old line before the run when it has none; its new range likewise holds its `+` lines. A patch hunk whose ` ` and `-` lines aren't its header's old count, or whose ` ` and `+` lines aren't its new count, is malformed and gives no hunks.
- *The text before and after*: Cursor's `afterFileEdit`, which gives each edit's `old_string` and `new_string` but no line numbers, so its adapter reads the file to place each edit. Take the file's lines before and after the edit, and drop the longest run of leading lines the two share, then the longest run of trailing lines they share among the lines left. What is left of each side is the hunk, starting after the shared leading run. An edit that changes no line has no hunk.

Hunks are listed in the order the patch lists them, or the order the edits came in, each edit's hunk counted in the file as the edits before it left it.

DESIGN 3.6 writes a hunk as `(HMAC(K_feat, path), start line, end line)`. This spec keeps both sides of the header instead, because SM 4.2's rewrite rule shifts the previous turn's ranges "by any diffs applied in between". A shift needs the number of lines each diff removed and added, which a start and an end on one side don't give. The start and end lines of either side follow from the four numbers.

### 10.7 The feature map `f`

`f` is a JSON object with these members, in this order:

| Member | Value | On |
|---|---|---|
| `v` | the version, `1` | every `f` |
| `mh` | the sketch (10.5), 512 hex digits | `prompt.submit`, `cli.track`, when the record has `c` |
| `th` | the template value (10.6) | `prompt.submit`, `cli.track`, when the record has `c` |
| `es` | the error signature value (10.6) | `prompt.submit`, `cli.track`, when the record has `c` and its text has a signature |
| `pc` | the prompt class: `substantive`, `low_effort`, `answer` or `error_paste` | `prompt.submit`, `cli.track`, `relay.message` |
| `h` | the edit hunks (10.6), a non-empty list | `agent.tool`, when the tool edited a file |
| `ch` | the command value (10.6) | `agent.tool`, `shell.command`, when there is a command |

The writer puts `f` in the body of every `prompt.submit`, `agent.tool`, `shell.command`, `cli.track` and `relay.message` record, even when it holds only `v`, and in no other record (4.5). A prompt whose text the device never had, and so has no `c` (10.1), has an `f` of only `v` and `pc`, and so does every `relay.message`. So an `f` fits its kind when it holds `v` and, for `prompt.submit` and `cli.track`, either `mh`, `th` and `pc` and maybe `es`, on a record with `c`, or `pc` alone, on one without; for `relay.message`, `pc` alone; for `agent.tool`, maybe `h` and `ch`; for `shell.command`, maybe `ch`. Each hunk in `h` has exactly the five members of 10.6.

`pc` is SM 2.2's class of the text, as the writer computed it when it wrote the record, whatever origin the prompt later turns out to have (DESIGN 3.6 calls it "prompt_class as computed"). SM 2.2 and `pkg/score` own that rule, and CC8 checks it on challenged turns whose reveal removed nothing (10.8). `cli.track` carries the prompt features because DESIGN 5.4 classes a `track` anchor like a prompt, from its committed label with the event's `text_len` as the typed length, while the server re-scores from bodies, never from text (DESIGN 6.5 and D18). A `cli.track` record's `pc` is that class, never `answer`, and a label `pkg/redact` emptied is `low_effort`, whatever its `text_len`. The event's `low_effort` and the record's `es` carry what the label decides, so a re-score reads the class from the body, and CC8 checks it against the revealed label and `text_len` (10.8). `relay.message` carries `pc` because a relayed anchor takes its class from the app's own text, not from the prompt the hook saw (SM 2.2 rule 9), and the server re-scores without either text.

The writer emits `f` and `c` the way `encoding/json` writes `pkg/ledger`'s types, as for the rest of the body (4.5): members in the order of the table, absent where it says, and no insignificant whitespace. For the prompt in 10.10, `f` is

```text
{"v":1,"mh":"1d5ee309...044f2069","th":"b30c980dcd8b3324712ae687fc0ed740","es":"825d4a3f99907ce6760e3e1107856a15","pc":"error_paste"}
```

with `mh` shortened here. An `agent.tool` record that ran `npm test` and changed lines 10 to 12 of `src/nav.js` into lines 10 to 14, under the same keys, has

```text
{"v":1,"h":[{"p":"c8b26d53782c22f46eb418e7716dcc97","os":10,"ol":3,"ns":10,"nl":5}],"ch":"3235767396764538f6473785ffd467f8"}
```

A later version adds keys and raises `v`, and its rules apply only to records that carry the new keys (DESIGN 3.6, 6.6). A refit of SM's parameters never changes `f`. So `v` is an integer from 1 up, and a version 1 map holds only this table's members. An `f` whose `v` is missing, isn't an integer or is below 1, that holds a member the table doesn't list under `v = 1`, or whose `v`, `mh`, `th`, `es`, `pc`, `h` or `ch` is `null`, is malformed, and its body fails verification. A reader that meets a `v` above its own reports the record's features as unknown and doesn't guess at them. For a server that is still a fault: a server is never older than the clients that write to it, so it refuses or holds a record whose `f.v` is newer than its own, and a modified client can't switch off the re-score of D18 by writing `{"v":2}`.

### 10.8 What CC8 can check

CC8 (DESIGN 7.3, WP-TR2) checks each challenged prompt or `track` call against what its record committed, using only checks that need no key. No per-shingle value or other keyed value leaves the device for it: the server has the text's reveal (10.3) and the record's body from the metadata reveal.

On every reveal, `f` must hold `es` when a line that the reveal shows whole matches one of `E1` to `E6` once stripped (10.4 step 2), because the text then has an error signature (10.6). A line is shown whole when the reveal keeps every token that holds one of its code points or one of the line feeds around it.

When the reveal removed nothing, the server has the whole text and also checks:

1. For a `prompt.submit`, `len_chars` is the number of code points in the text, and `typed_len_est` is at most `len_chars`.
2. For a `prompt.submit` or a `cli.track`, `low_effort` is what `pkg/score`'s `LowEffort`, SM 2.2's go-ahead test, gives the text.
3. `f` holds no `es` when the text has no error signature.
4. `f.pc` is the class `pkg/score` gives the record (SM 2.2; DESIGN 5.4 for a `track` call, from its revealed label and its `text_len`).

CC8 also compares the challenged turns one submission reveals, sticky re-reveals included (DESIGN 7.5 step 5), that are of one chain, and so written under one `K_dev` (3), have the same `f.v`, and whose reveals removed nothing. Two whose normalized texts (10.4) are equal must carry the same `th` and the same `mh`, and two whose error signatures are equal must carry the same `es`. Every turn of a pair that breaks this fails, because the server can't tell which value is honest. Turns of other submissions aren't compared, so DESIGN 4.4's retention and 7.11's purges never leave CC8 holding text they removed, and `pairtime verify --fetch-reveals`, which fetches only the submission's own reveals, has everything a verdict rests on (DESIGN 7.8).

A withheld, expired, purged or unavailable (DESIGN 7.5 step 8) turn has nothing to check. A turn whose reveal removed a span gets only the `es` rule above.

**What these checks can't prove.** The server can't check a keyed value against `K_feat`, which only the device holds. A client that keys a turn's `mh`, `th` or `es` under a key of its own is caught only when another turn the submission reveals has the same normalized text or error signature: for one loop of five identical prompts, three of them keyed that way, in a pool of 100, a draw of 10 holds such a pair with probability under 0.07. A client that keeps honest sketches but writes a fresh made-up `es` with each pasted error keeps its pastes out of SM 4.2's error count and makes each paste a new error signature, which ends re-roll, rewrite and autopilot counts, and CC8 sees it only when two revealed prompts share a signature. Prompts that differ by a word are never compared. Withholding a drawn turn costs its minutes (DESIGN 7.5 step 7). A removed span of any type takes the turn out of every rule but the `es` rule, and out of that rule for each line it touches. A span's type is the client's own claim (10.3), so a modified client can remove one token of each drawn turn as `known` or `pii`, which looks like routine redaction. The figure above, and DESIGN 7.3's 0.65, hold only against a client that removes nothing from the turns it reveals. Nothing checks the `h`, `ch` and `error_sig` of `agent.tool` and `shell.command`, which come from output that is never revealed, and a made-up `error_sig` on an agent's test run is a new error signature, which SM 4.2 counts as a change event. The flags behind SM 2.2's `answer` class, a prompt's `answers_question`, an earlier `agent.output`'s `asks_question` and an `agent.stop` on a question, have no rule a text can be checked against. A `track` call's `text_len` is the client's own count of the characters the model passed, and no record keeps that text: the committed label has had each CRLF turned into LF and `pkg/redact`'s spans cut out, with nothing in their place (WP-AD18, DESIGN 8.7). So a revealed label shows only that `text_len` is at least the label's length, which a larger count is too. No check compares the two, and check 4 holds `f.pc` to the `text_len` as recorded, not capped at the label's length. By recording a larger one, a modified client turns a label that fails the go-ahead test from `low_effort` into `substantive` at `SUBSTANTIVE_CHARS`, or from `error_paste` into `substantive` at `ERROR_PASTE_TEXT_MAX` when the label has an error signature, and raises the anchor's typing part as far as `COMPOSE_MAX_S` (DESIGN 5.4). A label that passes the go-ahead test, an emptied one included, stays `low_effort` whatever its `text_len`. DESIGN 7.12 accepts all of these as T9 residue.

**What the server learns.** CC8 shows the server nothing beyond the text the teen kept. Comparing committed values is part of re-scoring anyway: the server holds the `th` and `mh` of every prompt in the reveal set (DESIGN 6.5), so an equal `th` or a close `mh` already tells it that an unrevealed prompt repeats a revealed one.

### 10.9 The challenge draw

DESIGN 7.5 step 4, to the byte:

```text
seed  = SHA-256(r || SHA-256(M))
score = HMAC-SHA256(seed, u8(len(device_id)) || ascii(device_id) || u64(s))      a turn
score = HMAC-SHA256(seed, 0x00 || commit id)                                      a commit
K     = min(CHALLENGE_K_MAX, max(CHALLENGE_K_MIN, ceil(CHALLENGE_K_FRAC * P)))    CHALLENGE_K, P turns in the pool
```

`r` is the server's 32 random bytes from the freeze. `M` is the metadata reveal as the client sent it, before sealing (DESIGN 7.6). DESIGN 7.8's bundle carries `r` and the seed, and it also has to carry `SHA-256(M)` for an auditor to recompute the seed (WP-SB8). `device_id` carries the one-byte length of part 1 (7, 8).

The draw ranks the pool, every `(device_id, s)` that WP-SB5 admits under DESIGN 7.5 step 4, by score, comparing scores as 32-byte strings, smallest first. It takes the first `min(K, P)`. A pool lists each item once, so two items can tie only through an HMAC collision. The three parameters come from the parameter table frozen with `r` (DESIGN 6.3, 7.5 step 4), and the bundle carries that table's hash with the pool (DESIGN 7.8). `CHALLENGE_K_FRAC × P` is an exact rational and rounds up. Table 0.1.0, which the vectors use, gives `K = min(30, max(10, ceil(P / 10)))`, so `K` is 10 for pools of up to 100, 11 for 101 to 110, and 30 from 291 on.

The two interview commits are the two with the smallest commit scores, or every commit when there are fewer than two. A commit id is the commit's raw hash: 20 bytes for SHA-1 and 32 for SHA-256. Its `0x00` first byte can never start a turn's input, which starts with a length from 1 to 128. Which commits are eligible is WP-SB5's to decide.

### 10.10 Worked example

These values come from `tools/ledger_ref.py`, and `selftest` fails if any of them stops matching it. Record 2 of 12.2 is a `prompt.submit` with `s = 2`, under the keys of 12.1. Say its text is

```text
still broken, see src/nav.js:12<CR><LF>TypeError: x is undefined
```

It has 15 tokens: `still`, a space, `broken,`, a space, `see`, a space, `src/nav.js:12`, CRLF, `TypeError:`, a space, `x`, a space, `is`, a space and `undefined`. Four of their salts and leaves:

```text
salt_1  = d02c7820505e275a54f699ad69ec044071609006e70bf71dbae4f3f875601e19      "still"
t_1     = 2a4d7636edabb51f88ef2c3478bc1f16d4e96fa02e952f433636f984291ed7ae
salt_2  = c91fb9511af649c2e9cbddc0747044e127d1814a4fe31ff95227d487e2dcd3a9      " "
t_2     = cec8328945a0af051c0077ecc37741d14433e1f42b341474d1c5df5f63d85167
salt_7  = ed7631f22cb4e091fa3d2aee3c4f6b45c8acc20de88a03dfd56d024fcbaff1ab      "src/nav.js:12"
t_7     = 7aa845239d74ce39e31dc06ff6a54e9ad62ebaa4b00e66abf3e2c43bf4aecfd2
salt_15 = 076e8750259d56bb41898ce7ce0227904efb2ad3bf108e84a2806d62c8527786      "undefined"
t_15    = 30c6094dd1e912d894facc3d850c1b5263b8c0e9d7714d11051f7d1ec80f9a34
n       = 15
C       = aa9bfca6f19928733e7a62d0bfad12c6251486f7085a01a297e110631d80ad8b
```

A reveal that removes token 7 as `known` keeps tokens 1 to 6 and 8 to 15 with their salts, and puts `{"type": "known", "count": 1, "leaves": ["7aa84523..."]}` in place of token 7. It verifies `ok`.

Normalizing it removes the second line, an error line by `E1`. The first line's words become `still`, `broken`, `see` and `<path>`, because step 6 strips the comma, and `still broken` becomes `<retry>`, which leaves `<retry> see <path>`. That is three words, so the shingles are the 14 character 5-shingles of those 18 code points, in ascending order:

```text
" <pat"  " see "  "<path"  "<retr"  "> see"  "e <pa"  "ee <p"
"etry>"  "path>"  "retry"  "ry> s"  "see <"  "try> "  "y> se"
```

The first shingle's value, and the first eight of its 64 MinHash values:

```text
v(" <pat") = 8bb3f386a2bdedcd87146997843e177a9fd5e405272bdf1005f3eb37b499a41e
h_0 to h_7 = b93458ce 0b69154c 9209f1c2 c69fb1fe 7ec6269b 350fc364 b17f5127 872b7caa
```

The error signature is `TypeError: x is undefined`. With the class `error_paste`, the body's `f` and `c` are

```text
mh = 1d5ee30902d5e7b729e6f3cc125f6c940886e4c31472a57b1054703f05eb147719253e22056919d00632db7c021bd14e1f289cf107f9ba753d7f54a211e444120cbb90a61a1185c40e787ce6027c216c1006520b1149b59616af640309ba329e0042c0ec0e20757518311269149f4710044c15e405a25c7c231a984b336a1cf2032e36dc39a29b9009c82d0407aae42f07aff42619511a6e07763482018d230a203fdc64178f72140552e3571fbd1dcc052e724a1d824f96055f296d0d4170950a3b7e05023bd77b07c0314a01718dce3da3d08d1b196d0f027ce1ea0422ad1f01b0a6b907432c591d5685f80382f553092e38d7350c6ae40d8155c9044f2069
th = b30c980dcd8b3324712ae687fc0ed740
es = 825d4a3f99907ce6760e3e1107856a15

"f":{"v":1,"mh":"1d5ee30902d5e7b729e6f3cc125f6c940886e4c31472a57b1054703f05eb147719253e22056919d00632db7c021bd14e1f289cf107f9ba753d7f54a211e444120cbb90a61a1185c40e787ce6027c216c1006520b1149b59616af640309ba329e0042c0ec0e20757518311269149f4710044c15e405a25c7c231a984b336a1cf2032e36dc39a29b9009c82d0407aae42f07aff42619511a6e07763482018d230a203fdc64178f72140552e3571fbd1dcc052e724a1d824f96055f296d0d4170950a3b7e05023bd77b07c0314a01718dce3da3d08d1b196d0f027ce1ea0422ad1f01b0a6b907432c591d5685f80382f553092e38d7350c6ae40d8155c9044f2069","th":"b30c980dcd8b3324712ae687fc0ed740","es":"825d4a3f99907ce6760e3e1107856a15","pc":"error_paste"}
"c":{"n":15,"r":"aa9bfca6f19928733e7a62d0bfad12c6251486f7085a01a297e110631d80ad8b"}
```

The `agent.tool` example of 10.7 uses the same `K_feat`:

```text
{"v":1,"h":[{"p":"c8b26d53782c22f46eb418e7716dcc97","os":10,"ol":3,"ns":10,"nl":5}],"ch":"3235767396764538f6473785ffd467f8"}
```

For the draw, say `r` is the bytes `0x20` to `0x3f` and `M` is the ASCII text `stand-in metadata reveal`. The pool holds 12 turns: `d_example` 2, 9, 14, 23, 31, 40, 52 and 61, and `d_other` 7, 18, 26 and 33. Then `K = 10`, and:

```text
SHA-256(M)                = 2354fe7d5f87c7fa5cbff5be38836fdabd937b9623ccc4c9e3daef33db894f63
seed                      = 2e64adb81e4c1a7352913b0c0e0732706dac00beffcbdfa062e07d8da236b391
score of (d_example, 2)   = 670c31a9fde38cee626f952dda2e3db8697747bf3067bb25f0d924ed53efd2d1
drawn, smallest first     = d_example 9, d_other 26, d_other 7, d_example 14, d_example 52, d_other 33, d_example 2, d_other 18, d_example 23, d_example 40
```

`d_example` 31 and 61 have the two largest scores and aren't drawn.

## 11. Vectors and the reference

### 11.1 Files

| Path | What it is |
|---|---|
| `docs/spec/ledger-v1.md` | this spec, including the rules the reference doesn't encode, such as 4.5's body-header agreement, 4.3's `os.*` writer refusal, 5.2's rule for `n` and the steps of section 9 |
| `tools/ledger_ref.py` | the Python reference |
| `testdata/ledger/vectors.json` | vectors from fixed seeds, written by `ledger_ref.py vectors` |
| `testdata/ledger/vectors-content.json` | part 2 vectors from fixed seeds, written by `ledger_ref.py vectors-content` |
| `testdata/ledger/rfc5869.json` | RFC 5869 test cases 1 to 3 |
| `testdata/ledger/rfc4231.json` | RFC 4231 test cases 1 to 7, HMAC-SHA-256 only |
| `testdata/ledger/ct/` | Certificate Transparency Merkle vectors from transparency-dev/merkle, pinned to one commit |

All of them are oracles under the plan's ground rule 5: a PR that changes them along with the code they check needs the `oracle-change` label. `ci.yml`'s oracle guard doesn't list `docs/spec/` yet, so until it does, review is what holds this file to the rule.

### 11.2 Commands

```sh
python3 -I tools/ledger_ref.py selftest
python3 -I tools/ledger_ref.py vectors [--out PATH]
python3 -I tools/ledger_ref.py vectors-content [--out PATH]
python3 -I tools/ledger_ref.py check FILE
```

`selftest` checks the RFC 5869, RFC 4231 and Certificate Transparency vectors, the rtype table against `pkg/event`'s `kindTable`, and the worked example in section 12 against this file: each of its code blocks, byte for byte, against the same block rendered from the reference's values. Then it checks that `vectors.json` is byte for byte what `vectors` writes, that it passes `check`, and that `check` reports each of 35 kinds of tampering with it under the rule the change breaks. 19 of those changes come with every value derived from them recomputed, so that their rule is the only one that can catch them. For part 2 it checks the whitespace set of 10.2 against Python's `str.isspace`, the lowercase table of 10.4 against Python's `unicodedata`, the tokenizer, the 16 KiB cut, reveals with random removals and every single-item change to them, the MinHash estimate's error and bias on random sets, hunks from patches with context against hunks from the text before and after, and the draw. Then it checks the worked example in 10.10 against this file and that its reveal verifies `ok`, that `vectors-content.json` is byte for byte what `vectors-content` writes, that it passes `check`, that `check` catches 34 kinds of change to it, and that a `note` on a part 2 object changes nothing. `ci.yml` runs it on every PR.

`check FILE` recomputes every value in a file in the format of 11.3 and prints each mismatch with its JSON path. `pkg/ledger`'s tests write such files and run `check` on them (WP-LG2, WP-LG4).

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
| `lowercase` | an object: `unicode` (`"15.0.0"`) and `runs[]`, each `first`, `count`, `delta`, `stride`, meaning that for `i` from 0 to `count - 1`, code point `first + i × stride` lowercases to itself plus `delta` (10.4); optional `later_unicode` and `later[]`, each `code`, `lower`, the mappings Unicode added after `unicode` up to `later_unicode`, which version 1 never applies |
| `commitments[]` | `k_dev`, `seq` (`s`), `rtype`, `text` (the committed bytes), `n`, `root` (`C`), optional `raw` (an agent message before its cut), optional `tokens[]`, each `token`, `salt`, `leaf` (10.1, 10.2) |
| `reveals[]` | `n`, `root`, `items` (10.3), `verdict`: `ok`, `bad_item`, `count`, `alternation` or `root` |
| `normalizations[]` | `text`, `removed_lines` (line indices from 0), `normalized` (the normalized text), `shingle_kind` (`word3` or `char5`), `shingles` (ascending by UTF-8 bytes), `signature` (the error signature, or `null`) (10.4 to 10.6) |
| `features[]` | `k_dev`, `rtype`, `f`, and by kind: for `prompt.submit` and `cli.track`, `prompt_class`, and `text` and `shingle_values` (in the order of the shingles) unless the record is a `prompt.submit` whose text the device never had (10.1), which has neither; for `relay.message`, `prompt_class` alone, since it commits no text; for `agent.tool`, `hunks[]`, each `path`, `old_start`, `old_lines`, `new_start`, `new_lines`, and optional `command`; for `shell.command`, optional `command` (10.5 to 10.7) |
| `diffs[]` | `hunks[]`, each `old_start`, `old_lines`, `new_start`, `new_lines`, and either `patch`, an object of `old_start`, `old_lines`, `new_start`, `new_lines` and `lines[]`, each the hex of one line with its leading ` `, `-`, `+` or `\`, or `before` and `after`, the file's text before and after one edit (10.6) |
| `jaccard[]` | `a`, `b` (sketches), `matches` (`m` of 10.5) |
| `draws[]` | `r`, `metadata` (`M`), `seed`, `pool[]`, each `device_id`, `seq`, `score`, `drawn[]`, each `device_id`, `seq`, smallest score first, and optional `commits[]`, each `commit`, `score`, with `drawn_commits[]`, each `commit` (10.9) |
| `challenge_k[]` | `pool` (`P`), `k` (`CHALLENGE_K`), `drawn` (`min(K, P)`) |

Texts, tokens, commands and shingles are hex of their UTF-8 bytes, like every other byte string here. The texts of commitments, normalizations and features, a commitment's tokens and commands must be valid UTF-8: `check` reports a commitment text that isn't as a mismatch, because no writer can commit it (10.1), and any other such bytes as a format error. A reveal's tokens are the exception. A failing case may hold bytes that aren't UTF-8 there, because a verifier has to give them a verdict, `bad_item` (10.3). Every object of the part 2 sections may carry a `note` string, which `check` ignores. A reveal states the verdict it should get, so a file can hold failing cases as well as passing ones.

Besides recomputing each value from the file's own inputs, `check` applies these rules:

- A chain whose first batch is `b = 1` starts with `prev = zero(32)` and `s = 1`. One that starts later takes its first `prev` and `s` as given. After that, batch numbers, sequence numbers and `prev` links follow sections 4.1 and 6.
- `received_at` and `receipt_msg` come together or not at all.
- Each day after the first has the next day number and chains to the day before. The first day's `prev` is taken as given.
- A batch whose `received_at` falls on a day the file lists appears in that day's rows with the same head and time, and no `(device_id, b)` appears twice.
- A record of rtype `0x00` or `0x42` to `0xff` fails. One from `0x14` to `0x3f` is of a kind added after this reference, which `check` counts as such and doesn't fail (4.2). `0x13` is `relay.message`'s, a kind the reference knows.
- Every body starts with `{"n":"`, its own record's nonce as 64 lowercase hex digits, and `","e":`, as 4.5 has every body begin. A writer that dropped `n`, put it after `e` or wrote it in another form fails here, though the hashes over its own output agree.
- A commitment with `raw` is an agent message, and its `text` is the cut of `raw` (10.1). An agent message without `raw` is at most 16,384 bytes.
- A features object's `f` equals, as JSON, the map 10.7 builds from its inputs.

### 11.4 What the vectors cover

- `keys`: four values of `K_dev`: all zeros, `0x00` to `0x1f`, all `0xff`, and one from a seed.
- `digests`: no project, one-character keys, `none` as a project key, where it is an ordinary key, and 128-character keys.
- Chain A: 50 batches whose sizes include the plan's list, 1, 2, 3, 7, 8, 9, 100 and 1,000, with every rtype the table had when the vectors were first written, `0x01` to `0x12`, `0x40` and `0x41`, uploaded about a minute apart from 23:20 UTC so they span midnight. It has proofs for every leaf of each batch of up to 9 records and for eight leaves of each larger batch.
- Chain B: a 128-character `device_id`, and four batches that share one `received_at`, as an offline flush would. Its records take the same rtypes as chain A's.
- Chain C: one batch of three `relay.message` records, rtype `0x13`, the first kind added after the vectors were first written (4.2), received on 2026-11-22, a day the file has no tree for. The kind has a chain of its own so that no value of chains A and B or of the days changed when it took its code, and the next kind added gets a chain of its own too.
- Days: 2026-11-19 and 2026-11-20, with rows from both chains and from ten devices whose chains aren't in the file. Eight of those rows share a millisecond with a chain A batch: one sorting before it and one after it, and `Zq`, `aB`, `a-`, `a_`, `w` and `w_`, whose byte order differs from their order with case folded or under a locale's collation (8). Chain B's four batches share one millisecond, and two rows fall in the last and the first millisecond of a day. 2026-11-21 has no rows. Every leaf has a proof.

The bodies are stand-ins: the real envelope with the record's real nonce, and `"e":{"stand_in":"<record type>","seq":<s>}` in place of the record. Hashing never parses a body, so a stand-in exercises the same code a real body does. The keys are test keys, published here and used nowhere else.

`vectors-content.json` covers part 2:

- `lowercase`: the whole Unicode 15.0.0 mapping, as 182 runs, and the 55 mappings that 16.0.0 and 17.0.0 add.
- `commitments`: an empty prompt, a short prompt, CRLF, whitespace at both ends, Cyrillic, an emoji, a no-break space, an ideographic space and Chinese in one prompt, a secret and an email address, a whitespace-only text, an agent message with a code block, a `track` label, an agent message over 16 KiB whose cut falls inside a three-byte character, and 12 random texts.
- `reveals`: removals at the start, in the middle and at the end, everything removed, nothing removed, adjacent spans of two types, a code span with its line count, two random removals of each random text, and one or more cases of each failing verdict. Among them is a reveal whose leaves still hash to `C` but cover too few tokens, the case the committed `n` exists for (10.2), and three that fail two checks at once, `bad_item` with `count`, `count` with `alternation` and `alternation` with `root`, so that only 10.3's order gives their verdicts.
- `normalizations`: each rule of 10.4 and each pattern `F1` to `G`, rustc's, `go run`'s and the JVM's output as they print it, the cases where a rule doesn't apply, pastes in the wrapper Claude Code 2.1.327 writes with typed text after them, SM 6.5's 20:02 and 20:07 prompts, which normalize to one text, edge punctuation around placeholders, and 10 random texts.
- `features`: prompts with and without an error signature, three versions of one re-roll, an empty prompt, a prompt of nothing but a paste, a paste in Claude Code's wrapper followed by a question, a bridge prompt with no text, SM 6.5's 20:02 and 20:07 prompts, a `track` label, `agent.tool` records with hunks across two files, an insertion, a deletion, a file outside the project and a Windows path, a command, and a read with neither, and `shell.command` records with and without a command.
- `diffs`: patch hunks with three lines of context, among them the two appends of 10.6, a run with removed and added lines, two runs in one patch hunk, a new file, a deleted file, a line without its LF, and a patch whose counts don't add up; and edits given as text before and after, among them one inside a line, an insertion between two equal lines, and an edit that changes nothing.
- `jaccard` and `draws`: the pairs and draws built from those, including a pool smaller than `CHALLENGE_K`, a pool of 300 where the cap applies, and an empty pool.

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
4. One sequence numbers every record, checkpoints and upload-failure notes included, and an event's `seq` equals its record's `s` (4.1). PR1 documented `seq` as gapless among events, which stops being true once checkpoints exist. `pkg/event`'s comment on `Envelope.Seq` and the event schema now say so. SM 1.1 now says the same (erratum 2026-11-20-sm-1.1-seq-gaps, decided).
5. The rtype table is fixed as listed, with `0x00` and every unlisted code reserved (4.2).
6. A header for an `os.*` event always has the zero project, and checkpoints and upload-failure notes get the header fields in 4.3's table. `pkg/event` doesn't yet refuse a `project_key` on an `os.*` event, so until a `kindTable` rule does, the ledger writer must (4.3).
7. The body's members, their order, and the nonce as hex (4.5).
8. Inclusion proofs fail on any value that isn't 32 bytes, and a verifier takes `n` from a verified head (5.2).
9. The server recomputes `H_b` from the uploaded `prev`, `R_b`, `n` and `b` (6).
10. `device_id` carries a one-byte length in `M_b` and in day leaves. DESIGN writes the receipt as `"pairtime-receipt-v1" || device_id || b || H_b || received_at`, which doesn't say how a variable-length id is encoded (7, 8).
11. The day tree: day numbers, the leaf encoding and order, the `0x04` chain byte, a head for every day including empty ones, the zero genesis, and the signed message `D_d` (8).

Part 2 decides these, for DESIGN 3.6, 5.4, 7.3 and 7.5 and SM 4.2 and 8.5:

12. `cli.track` labels commit like prompts and carry the prompt features. DESIGN 3.6 names only prompts and final agent messages, but DESIGN 5.4 commits the label so the challenge can draw it, classes a `track` anchor like a prompt, and DESIGN 6.5 re-scores without text (10.1, 10.7).
13. An agent message commits its first 16,384 bytes, cut back to a character boundary, which is what the vault keeps. Committed text is valid UTF-8 and never Unicode-normalized (10.1).
14. Whitespace is the 25 `White_Space` code points, listed. Tokens alternate, are numbered from 1, and enter the salt as `u64(s) || u64(j)` (10.2).
15. `c` holds the token count with the root, `{"n": n, "r": C}`. DESIGN 3.6 gives `C_i` alone, but without the count a reveal can pass a subtree's hash off as one removed token (10.2).
16. A reveal shows tokens and leaves in hex; span types are a closed list of five; `lines` goes only on `code` spans and is unchecked; the verdicts come in a fixed order (10.3).
17. Normalization's rules: blocks by line, the fence, paste and error patterns `F1` to `E6`, with a compiler's column-0 gutter lines (`R`) after `E2` and `E3` and a Go panic's signal line and goroutine blocks (`G`) after `E6`, a paste closed by any closing line `P2` matches, Unicode 15.0.0's simple lowercase mapping, the substitutions in a fixed order within each word with exact path and hex rules, every word stripped of its edge punctuation, apart from placeholders, and of its apostrophes, as SM 6.5's 20:07 row needs, and retry phrases matched on those bare words. SM 4.2 names these steps without defining them (10.4).
18. Shingles form a set; character shingles count code points; a normalized text of one to four code points is its own shingle; the empty set's sketch is `0xffffffff` throughout (10.5).
19. The MinHash family is one keyed HMAC per shingle, tagged by shingle kind, expanded with SHA-256 into 64 values. The Jaccard estimate is `m / 64`, compared in exact rationals (10.5).
20. Each 16-byte keyed value has a tag for its kind. The template is the normalized text. The error signature is the first line matching `E1` to `E6`, substituted but not lowercased, and the same function fills `agent.tool` and `shell.command` `error_sig`. Commands collapse whitespace; paths are relative to the project root with `/` (10.6).
21. A hunk carries the four numbers of a `diff -U0` hunk, changed lines only, taken from a patch's lines without its context or from the file's lines before and after an edit. DESIGN 3.6 gives a start and an end line, but SM 4.2's rewrite rule shifts ranges by later diffs, which needs both extents (10.6).
22. `f`'s members, their order, their encodings, and `f` on every record of the four kinds from version 1 on, even when it holds only `v` (10.7).
23. CC8 reveals no keyed value. Without the key it checks `es` against each line a reveal shows whole; on a reveal that removed nothing, the lengths, `low_effort`, `es` and `pc` against the text; and equal `th`, `mh` and `es` between a submission's revealed turns of one chain with equal normalized texts or error signatures. 10.8 states what that can't prove, which is less than DESIGN 7.3 first claimed (issue #15).
24. The draw: the seed hashes the metadata reveal as sent, before sealing; `device_id` carries part 1's length byte; commits are scored with a `0x00` first byte; `CHALLENGE_K` rounds 10% up (10.9).

The review of parts 1 and 2 (issue #19) decided these, before any device witnessed data:

25. Every `prompt.submit`, final `agent.output` and `cli.track` has `c`, and every record of the four feature kinds has `f`. Only the records 10.1 lists, whose text the device never had, lack `c`, and a prompt among them has an `f` of `v` and `pc` alone. Earlier drafts let a body without `f` and `c` pass as written before features existed, which let a modified client keep any prompt out of the draw and the re-score (4.5, 10.1, 10.7).
26. A body is valid UTF-8, every member name in it is lowercase ASCII (`a-z`, `0-9`, `_`), no object repeats a name, and its top level is exactly `n`, `e`, `f`, `c`, in that order, where present (4.5).
27. Body-header agreement includes the event's `device_id`, which must name the device whose chain holds the record (4.5, 9).
28. A checkpoint's `e` is `{"project_key", "t", "state"}` and an upload-failure note's `{"t", "reason"}`, so a verifier can check a checkpoint's project without parsing `score.ProjectState` (4.7).
29. An `f` with a `v` below 1 or not an integer, a `null` member, or a member version 1 doesn't list under `v = 1` is malformed. A `v` above a reader's own is unknown, and a server refuses or holds such a record (10.7).
30. The server refuses an empty batch, and once it holds a batch of a device it refuses that device's batch `b` unless it stored batch `b - 1` with head `prev`, DESIGN 3.10's chain link. Only a device's first upload may start after batch 1. An earlier draft refused only a `prev` that differed from a stored head, which let a device skip a batch (6, 9).
31. The server returns `received_at` with each receipt, and the client stores it next to the receipt. DESIGN 3.5's `batches.seg` row and DESIGN 3.6 name only the receipt, which can't be checked without the time it signs (7).
32. One `K_dev` writes one chain, so no `s` repeats under it. An install restored with `pairtime restore` writes a new chain under a new `K_dev` and keeps the old key only to verify and open the old chain. DESIGN 7.11 unwraps the old key onto the new install, and a second chain from `s = 1` under it would repeat every nonce and token salt (3, 4.1). A rollback or a copy under the same `K_dev` would write such a second chain, so it counts as a fork: a client that finds the server holding a batch of its `device_id` that its ledger lacks or holds under another head, or that has `K_dev` but no ledger, uploads nothing more under that `device_id` and `K_dev`, and a new device starts from `s = 1` and `b = 1` under a new `K_dev`, as after a restore (3; ingest-v1 5).
33. A day head is signed with the server key that was valid at the end of its day, whenever it is signed or published, so a verifier knows the key from `d` alone. Section 9 gave a receipt's key and not a day head's (8, 9).
34. A verifier fails a record of rtype `0x00` or `0x42` to `0xff`. One from `0x14` to `0x3f` that it doesn't know is an event kind added later, as `0x13` is to a verifier built before `relay.message` took it: it reports the kind as unknown, leaves the body unchecked and still verifies the batch, and a server, never older than its clients, refuses or holds it. 4.2 said such a batch still verifies, while section 9 required every rtype to be in the table (4.2, 9).

WP-SV17 decided these, as section 8 said it would:

35. The day file: its path by UTC date, its nine members in their order, and nothing in it from a device but hashes and a count (8).
36. A day is due 10 minutes after it ends, once its last rows are in the replica. The server stores each head before it writes the head's file, and resumes the chain from its stored heads (8).
37. The server keeps each published day's leaf hashes as long as any of the day's witness rows, so a batch's inclusion proof outlives the day's other rows (8; docs/errata/2026-11-23-day-leaves-retention.md).
38. While the server's clock reads a time at or before the end of the newest day it has published, ingest refuses every upload with 503 `day_closed`, and the transaction that stores a day's head recounts the day's rows, so no batch is ever stored on a published day and every receipted batch is in its day's tree (8; ingest-v1 2.5 step 13).

The decision on [the seq-gaps erratum](../errata/2026-11-20-sm-1.1-seq-gaps.md) added:

39. Every record of a sealed batch keeps its number. A seal makes its records durable no later than its batch row: sync the segment before writing the row, or write the row into the segment so one fsync covers both. The uploader sends only durable rows, and a backup holds only sealed records, so a crash can cut only records that no sealed batch holds. At start the writer drops a torn tail and seals the records that survived. It gives the next record the number one past the larger of the last surviving `s` and the last sealed batch's `last_seq`. A sealed record lost anyway is lost evidence, which CC7 holds, and its number is never reused (4.1).
