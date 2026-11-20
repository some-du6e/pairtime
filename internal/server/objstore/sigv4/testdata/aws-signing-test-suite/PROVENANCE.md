---
method: copied
captured_on: 2026-11-19
captured_by: WP-SV15
source: https://github.com/awslabs/aws-c-auth/tree/cf69fb7edf7c973524348bbcecda1b1684de4d5e/tests/aws-signing-test-suite/v4 Apache-2.0
---

# AWS's SigV4 test suite at aws-c-auth v1.1.0

`v4/` is AWS's Signature Version 4 test suite as AWS maintains it in awslabs/aws-c-auth, the C library under the AWS Common Runtime. The repository is Apache-2.0; `LICENSE` and `NOTICE` here are its files, byte for byte. The commit is `cf69fb7edf7c973524348bbcecda1b1684de4d5e`, tagged `v1.1.0` (tag object `c07360bd3176`) and released on 2026-11-16, the latest release on the day of the copy.

This is the oracle for `../../sigv4.go` (PLAN WP-SV15, "AWS's published SigV4 test-suite cases, copied with their license at a recorded version"). `sigv4_test.go` reads these files and checks the code against them. If the two disagree, the code is wrong. Don't edit a file here to make a test pass; re-pin the suite at a newer commit instead, in a PR of its own, and record the new commit here.

## How it was copied

1. `gh api repos/awslabs/aws-c-auth/releases/latest` named `v1.1.0`; its tag resolves to commit `cf69fb7edf7c`.
2. `gh api repos/awslabs/aws-c-auth/tarball/cf69fb7edf7c973524348bbcecda1b1684de4d5e` fetched the source at that commit, and `tests/aws-signing-test-suite/v4`, `LICENSE` and `NOTICE` were extracted from it unchanged.
3. `gh api "repos/awslabs/aws-c-auth/git/trees/cf69fb7edf7c973524348bbcecda1b1684de4d5e?recursive=1"` listed the git blob id of every file under `v4/`. `git hash-object` of each extracted file matched its blob id, for all 380 files, and for `LICENSE` (`d645695673349e3947e8e5ae42332d0ac3164cd7`) and `NOTICE` (`7b3f69846bfdf9678ddf1c4a011c8eb61aab0e4e`). Anyone can repeat the check against GitHub: `git hash-object <file>` here equals the blob id the tree API reports at that commit.

## What's here

38 cases, one directory each, ten files per case:

| File | What it holds |
|---|---|
| `context.json` | The inputs: credentials (AWS's documented example key pair, not a real one), session token where the case has one, region, service, timestamp, whether to normalize the path, whether to add `x-amz-content-sha256` (`sign_body`), presigned-URL lifetime, and `omit_session_token` (the token is added after signing, outside the signature) |
| `request.txt` | The request before signing |
| `header-canonical-request.txt`, `header-string-to-sign.txt`, `header-signature.txt` | The three intermediate values when the signature goes in the `Authorization` header |
| `header-signed-request.txt` | The request after header signing |
| `query-canonical-request.txt`, `query-string-to-sign.txt`, `query-signature.txt` | The same for a presigned URL, the signature in the query string |
| `query-signed-request.txt` | The request after query signing |

`sigv4_test.go` compares the canonical requests, strings to sign and signatures byte for byte. It compares the signed requests as sets of headers and query parameters, because the order aws-c-auth writes them in is its own choice and not part of SigV4.

## What was left out

`tests/aws-signing-test-suite/v4a/`, the suite for SigV4a, the ECDSA variant AWS uses for multi-region access points. pairtimed signs for one bucket endpoint at a time, and neither R2, MinIO nor Hetzner Object Storage uses SigV4a. Nothing else under `tests/` belongs to the suite.
