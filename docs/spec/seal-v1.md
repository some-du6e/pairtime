# Sealed blobs, version 1

Status: the format `pkg/seal` writes and reads (WP-SB2; DESIGN 7.6). A client seals a reveal to the evidence key before it uploads it, and `pairtimed` re-seals a stored blob to a viewer's key on every read, so tier-2 content never crosses Cloudflare in plaintext. `internal/server/keys` holds the evidence keys. WP-SB7's WebCrypto viewer is a second implementation, written from this spec and not from `pkg/seal`'s code, so a change here needs both, and the example in 8 must still open.

## 1. Notation

- `||` joins byte strings. `u8(x)` is `x` as one byte, and `ascii(x)` is the bytes of the ASCII string `x`.
- A P-256 public key is written as its uncompressed point, 65 bytes: `0x04 || X || Y`, each coordinate 32 bytes big-endian (SEC 1 2.3.3). It is what WebCrypto's `exportKey("raw")` gives for an ECDH P-256 key.
- `key_id(K)` is the first 8 bytes of `SHA-256(K)`, over the 65-byte point. In lowercase hex it is the 16 digits the keys document gives as the evidence key's `key_id` (keys-v1 2) and `pairtimed keys show` prints.

## 2. What a blob is sealed under

Each blob belongs to one submission and holds one part of it, such as its metadata reveal or its content reveal. The work packages that upload name the parts. Both names enter the key derivation:

```text
info = ascii("pairtime/v1/seal") || u8(len(submission_id)) || ascii(submission_id) || u8(len(part)) || ascii(part)
```

`submission_id` and `part` are each 1 to 128 bytes of printable ASCII without spaces, 0x21 to 0x7e. A blob opens only under the `submission_id` and `part` it was sealed under, so a server can't take a blob uploaded for one submission or part as another's.

## 3. The blob

| Bytes | Field | Meaning |
|---|---|---|
| 0 | `version` | `0x01`. A reader refuses any other value |
| 1 to 8 | `key_id` | `key_id(R)` of the public key `R` the blob is sealed to |
| 9 to 73 | `E` | The ephemeral public key, an uncompressed P-256 point |
| 74 to 85 | `n` | The AES-GCM nonce, 12 random bytes |
| 86 to the end | `c` | The AES-256-GCM ciphertext with its 16-byte tag at the end |

Bytes 0 to 85 are the header. A blob is 102 bytes longer than its plaintext.

## 4. Sealing

To seal a plaintext `m` to a P-256 public key `R` under a `submission_id` and `part`:

1. Make a new P-256 key pair, `e` and its public key `E`, from a secure random source. Each blob gets its own.
2. `Z` is the ECDH shared secret of `e` and `R`: the 32-byte X coordinate of `e·R` (SEC 1 3.3.1). In WebCrypto it is `deriveBits` with ECDH and a length of 256 bits.
3. `K = HKDF-SHA256(salt = the empty string, IKM = Z, info, L = 32)` (RFC 5869). An empty salt gives the same key as 32 zero bytes, which is the salt RFC 5869 uses when none is given.
4. `n` is 12 random bytes.
5. `header = u8(1) || key_id(R) || E || n`.
6. `c = AES-256-GCM-Encrypt(key = K, nonce = n, plaintext = m, additional data = header)`, with a 128-bit tag at the end of `c`.
7. The blob is `header || c`.

## 5. Opening

To open a blob with the private key `r` of `R`, for an expected `submission_id` and `part`:

1. A blob shorter than 102 bytes, or whose version isn't 1, isn't a sealed blob.
2. Its `key_id` must be `key_id(R)`. A blob that names another key is sealed to that key, and this one doesn't try it.
3. `E` must be a point on P-256 in uncompressed form. A compressed point, the point at infinity and a point off the curve aren't sealed blobs.
4. `Z` is the X coordinate of `r·E`, and `K` comes from `Z` and the expected `submission_id` and `part`, as in 4 step 3.
5. AES-256-GCM decrypts `c` with `K`, the nonce `n` and the header as additional data. A tag that doesn't verify means the blob was changed or isn't sealed under that `submission_id` and `part`. Either way nothing is returned.

## 6. Re-sealing

A re-seal opens a blob and seals its plaintext again under the same `submission_id` and `part`, with a new ephemeral key and a new nonce. The new blob has other bytes and another SHA-256.

- **To a viewer.** The review page makes an ephemeral P-256 key pair with WebCrypto and sends its public key with `GET /api/v1/submissions/:id/reveals`. `pairtimed` opens each stored blob with the evidence key it names and re-seals the plaintext to the viewer's key, in memory, and the browser opens the answer with its private key (DESIGN 7.6). `pkg/seal` reads the viewer's key as the 65-byte point or as a DER SubjectPublicKeyInfo, which is what `exportKey("spki")` gives.
- **To a new evidence key.** After a rotation, WP-SB9's pass re-seals each stored blob sealed to the retired key to the current one (DESIGN 10.2). The new blob names the new key, and the retired key can't open it.

## 7. The evidence keys

The evidence key is a P-256 key pair. `pairtimed init` makes it, and its private half is `/data/keys/evidence.p256` (DESIGN 7.6, 10.2). The keys document lists the current evidence key alone (keys-v1 2), and a client seals each upload to that key. `pairtimed` records each evidence key's validity in `key_history` under the kind `evidence`, as it does for the server keys (keys-v1 3).

The server holds its evidence keys in a ring: the current key, and any retired key whose private half is still on disk, because blobs stored before a rotation stay sealed to it until WP-SB9's pass has re-sealed them. The ring opens a blob with the key its `key_id` names, and it tells an upload sealed to the current key from one sealed to any other, which WP-SB4 refuses with `409 evidence_key_retired`.

Until WP-SB9 builds `pairtimed keys rotate-evidence`, an evidence key stays current from the start that first recorded it, and `pairtimed serve` refuses to start with any other key in `evidence.p256`. Blobs sealed to the recorded key would otherwise stop opening.

## 8. Worked example

These values come from Python's `cryptography` package, which runs OpenSSL, and from this spec's steps. No pairtime code computed them. `pkg/seal`'s tests seal with this ephemeral key and nonce and must get this blob byte for byte, and must open it. Each scalar is the SHA-256 of an ASCII label, read as a big-endian integer.

| Value | Hex |
|---|---|
| `r`, the SHA-256 of `pairtime seal-v1 example: evidence key` | `ec392461dbe71c4b37df4e0e696785b87eb51e3f72bd3fa38f496adc2f77778a` |
| `R` | `0406293dfee843e0a8069a4cc8dce523770c58ed9ceb615ae828bf31eea6c59fd13f1632ccd717a62fc4398b7ecc2901f894cf11037e9468f956d86cc6a2e41be4` |
| `key_id(R)` | `6edc953f4d170120` |
| `e`, the SHA-256 of `pairtime seal-v1 example: ephemeral key` | `9266083ed88a670c21546706834e5bcade9b9164ae44c597b1fafdad48ae477e` |
| `E` | `041551646e2190c666be618a781a018bfd0f33f90997ed144d031c0f74965dbd9519afaff1031a5d4c20940d04349cbe1289855ff680dec1787d953f69c6a49c87` |
| `n`, the first 12 bytes of the SHA-256 of `pairtime seal-v1 example: nonce` | `743ba9269fc22e5171f02d6e` |
| `submission_id`, `part` | `example-submission-1`, `metadata` |
| `info` | `7061697274696d652f76312f7365616c146578616d706c652d7375626d697373696f6e2d31086d65746164617461` |
| `Z` | `fb7b6a3df656e0c0166bf9e9596703293f7332c9c0fea6cd5bdf3bca2f1ad683` |
| `K` | `b6e3311074450e4273cf1298667d6b0c729e7c3c9cf3915bab24724a9f7af2d4` |
| `m` | the 28 ASCII bytes `{"note":"an example reveal"}` |

The blob, 130 bytes:

```text
016edc953f4d170120041551646e2190c666be618a781a018bfd0f33f90997ed
144d031c0f74965dbd9519afaff1031a5d4c20940d04349cbe1289855ff680de
c1787d953f69c6a49c87743ba9269fc22e5171f02d6e08553f06669807e41082
1df43d32aa5577b23a836a9dc7b12dd6d5149763196e13b25df9f84cbaf4b012
b6dd
```

## 9. Decisions this spec makes

DESIGN 7.6 names the primitives and the info string, and PLAN.md's WP-SB2 names the blob's fields. These are what they leave open, decided here:

1. `submission_id` and `part` enter `info` each after its length in one byte, as ledger-v1 writes `device_id` into a leaf, so no two pairs give the same `info`. DESIGN 7.6 writes the string as `"pairtime/v1/seal" || submission_id || part`, and the plain join would let `("ab", "c")` and `("a", "bc")` share a key.
2. Both are printable ASCII without spaces, so Go and JavaScript encode them to the same bytes with no Unicode normalization in between.
3. The salt is empty. `Z` is already uniform enough for HKDF's extract step, and an empty salt is one less value for a second implementation to get wrong.
4. The whole header is the additional data. `Z` is only the X coordinate of `e·R`, so `E` and its negation give the same `Z`. Without the header in the tag, a blob with `E` negated would open to the same plaintext under another SHA-256. With it, any changed header byte fails to open, the key id and the nonce included.
5. The nonce is random and travels in the blob, as WP-SB2 asks, though each `K` seals one plaintext only, since every blob has its own ephemeral key.
6. The key id is 8 bytes, the id the keys document and `pairtimed keys show` already use for the evidence key, so a blob says which key opens it without the full point.
7. A version byte leads the blob, so a later format can change anything after it.
