---
method: copied
captured_on: 2026-11-20
captured_by: D-minio-guard
source: https://github.com/C2SP/wycheproof/tree/65ca494727a52944c204441c860b039e6786b99b/testvectors_v1 Apache-2.0
---

# Wycheproof's AES-GCM and P-256 ECDH vectors at 65ca494727a5

These are three test-vector files from Project Wycheproof, C2SP's repository of vectors that test crypto libraries against known attacks and implementation bugs. The repository is Apache-2.0, and `LICENSE` here is its file, byte for byte. It has no NOTICE file. The commit is `65ca494727a52944c204441c860b039e6786b99b` ("vectorgen: document Go version requirements", 2026-11-11), the head of `main` on the day of the copy. Wycheproof has no GitHub releases. Its newest tag, `wycheproof-v0-vectors`, points at a commit from 2025-09-01, older than the last change to each of these files, so the pin is a commit.

These files are the oracle for two work packages in docs/PLAN.md. WP-LG6's AES-GCM wrapper for the vault must pass `aes_gcm_test.json`. WP-SB2's `pkg/seal`, which seals with ECDH on P-256 and AES-256-GCM, must pass all three. Both WPs read the files here and copy nothing. The files came first, in a PR that changed no code, as the orchestrator's decision D-minio-guard set out. If code and a file disagree, the code is wrong. Don't edit a file here to make a test pass. To move to a newer Wycheproof commit, copy the three files and `LICENSE` again at that commit, record the new commit and blob ids here, and treat the re-pin as an oracle change (docs/PLAN.md, ground rule 5).

## How it was copied

1. `gh api repos/C2SP/wycheproof/commits/HEAD` named commit `65ca494727a5`, with tree `e0c85ba7848f`, on 2026-11-20.
2. `gh api "repos/C2SP/wycheproof/git/trees/65ca494727a52944c204441c860b039e6786b99b?recursive=1"` listed the whole tree, not truncated, with every file's git blob id and size.
3. `gh api -H "Accept: application/vnd.github.raw" repos/C2SP/wycheproof/git/blobs/<blob id>` fetched the four blobs into an empty directory, and `cp` copied them here.
4. `git hash-object` of each file matched its blob id, and each size matched the tree's. The same four files fetched again from `raw.githubusercontent.com` at the commit were identical, byte for byte, and `git ls-tree` of the commit that added them here shows the same four blob ids. Anyone can repeat the check against GitHub: `git hash-object <file>` here equals the blob id the tree API reports at that commit.

## What's here

| File | Upstream path | Git blob id | Bytes | Read by |
|---|---|---|---:|---|
| `aes_gcm_test.json` | `testvectors_v1/aes_gcm_test.json` | `223f53cad33d78013ed635d94889836c036e1d58` | 213177 | WP-LG6, WP-SB2 |
| `ecdh_secp256r1_test.json` | `testvectors_v1/ecdh_secp256r1_test.json` | `e616409b99af423e572f915c0ef0aa444d47e7ac` | 418368 | WP-SB2 |
| `ecdh_secp256r1_ecpoint_test.json` | `testvectors_v1/ecdh_secp256r1_ecpoint_test.json` | `e4a2458f3e1825a7d0dd3e5c838c1bc1535e9227` | 200088 | WP-SB2 |

`LICENSE` is upstream's `LICENSE`, blob `7a4a3ea2424c09fbe48d455aed1eaa94d9124835`, 11357 bytes. Like the original, it has no newline at the end, so an editor that adds one changes its blob id.

Each vector file is one JSON object. `schema` names its JSON schema in upstream's `schemas/`, `notes` says what each flag means, and `testGroups` holds the tests, each with a `tcId`, a `comment`, `flags` and a `result`. Every byte string is lowercase hex. The counts below come from the files. Upstream's `doc/files.md` also has a table of counts, but at this commit it doesn't match the files. It gives `aes_gcm_test.json` 256 tests.

| File | Schema | Groups | Tests | Valid | Acceptable | Invalid |
|---|---|---:|---:|---:|---:|---:|
| `aes_gcm_test.json` | `aead_test_schema_v1.json` | 45 | 316 | 229 | 0 | 87 |
| `ecdh_secp256r1_test.json` | `ecdh_test_schema_v1.json` | 1 | 612 | 330 | 230 | 52 |
| `ecdh_secp256r1_ecpoint_test.json` | `ecdh_ecpoint_test_schema_v1.json` | 1 | 355 | 330 | 1 | 24 |

- `aes_gcm_test.json`: each test gives `key`, the nonce as `iv`, `aad`, `msg`, the ciphertext without nonce or tag as `ct`, and `tag`. The groups cover keys of 128, 192 and 256 bits and nonces of 0 to 2056 bits, all with 128-bit tags. The invalid tests are 81 modified tags and 6 empty nonces. The group with 256-bit keys and 96-bit nonces, the vault's parameters in WP-LG6, holds 66 tests: 39 valid and 27 invalid.
- `ecdh_secp256r1_test.json`: `public` is an X.509 SubjectPublicKeyInfo in DER, broken on purpose in some tests. `private` is the private key, and `shared` is the expected secret. All 222 tests flagged `InvalidAsn` are acceptable ones, with a public key whose ASN.1 encoding is invalid.
- `ecdh_secp256r1_ecpoint_test.json`: `public` is a bare X9.62 point. The 330 valid tests use the same points, private keys and secrets as the other file's. Tests that need the SubjectPublicKeyInfo, such as broken DER or explicit curve parameters, aren't here. Every valid test uses an uncompressed 65-byte point, and the one acceptable test is a compressed point.

In both ECDH files `private` is a big-endian integer that gets a leading zero byte when its top bit is set. So most keys are 33 bytes long and the rest 32, apart from two valid tests with shorter ones. A `valid` test must succeed with the expected output, and an `invalid` one must fail. An `acceptable` test may go either way, but code that accepts one must still get `shared` right, as the `InvalidAsn` and `UnusedParam` notes put it.

## What was left out

The rest of the repository. D-minio-guard named these three files, and the other 356 files in `testvectors_v1/` stay upstream. Among them are `ecdh_secp256r1_pem_test.json` and `ecdh_secp256r1_webcrypto_test.json`, the same curve's ECDH tests with keys in PEM and in JWK. The three schemas the files name stay upstream too; the field descriptions above follow them.
