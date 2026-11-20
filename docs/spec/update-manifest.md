# Update manifest

Status: version 1, from WP-RL1 (docs/PLAN.md). `internal/release` implements it, `tools/release` writes the files, and `.github/workflows/release.yml` publishes them.

Every pairtime release publishes one archive per platform, `checksums.txt` and `manifest.json`. Before the client installs or updates itself, it checks `manifest.json` against the release keys compiled into it (DESIGN 3.3 and 5.9, CP 4.6). This file specifies those files, the keys and the checks.

## Release files

| File | What it is |
|---|---|
| `pairtime_<version>_windows_<arch>.zip` | The Windows client |
| `pairtime_<version>_<os>_<arch>.tar.gz` | The macOS and Linux clients |
| `checksums.txt` | The SHA-256 of every archive |
| `manifest.json` | The signed manifest |

`<version>` is the tag without its `v`, as in `pairtime_0.1.0-alpha.1_windows_amd64.zip`. There is one archive for each target in CP section 7: `windows/amd64`, `windows/386`, `windows/arm64`, `darwin/amd64`, `darwin/arm64`, `linux/amd64` and `linux/arm64`.

### Archives

An archive holds three files at its root and no directories: the binary (`pairtime`, or `pairtime.exe` on Windows) with mode 0755, then `LICENSE` and `README.md` with mode 0644. Every file has the release commit's time. The tar.gz archives are USTAR with owner 0 and no gzip timestamp, and the zip archives record Unix modes. The same commit built with the same Go version gives the same bytes.

The binary is built the way `ci.yml` builds it, with the tag as the version:

```sh
CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build -trimpath -buildvcs=false \
  -ldflags "-s -w -X github.com/some-du6e/pairtime/internal/version.Version=<tag> \
    -X github.com/some-du6e/pairtime/internal/version.Commit=<commit> \
    -X github.com/some-du6e/pairtime/internal/version.Date=<commit time>" \
  ./cmd/pairtime
```

### checksums.txt

One line per archive in `sha256sum`'s format: the SHA-256 in lowercase hex, two spaces, the file name. Lines are sorted by file name. `sha256sum -c checksums.txt`, run where the downloads are, checks them. `manifest.json` isn't listed, because its signature covers it.

## manifest.json

`manifest.json` is a [DSSE](https://github.com/secure-systems-lab/dsse) envelope with the manifest inside. Keeping both in one file means a client makes one request for it (DESIGN 4.3) and never pairs a manifest with a signature fetched a moment later.

### The manifest

The envelope's payload is this JSON object, indented by two spaces, with a final newline:

```json
{
  "version": "v0.1.0-alpha.1",
  "commit": "68726d8ccf480210ce26078e4d3006120e6b49a1",
  "date": "2026-11-19T10:49:00Z",
  "minimum_version": "v0.0.0",
  "artifacts": [
    {
      "os": "windows",
      "arch": "amd64",
      "url": "https://github.com/some-du6e/pairtime/releases/download/v0.1.0-alpha.1/pairtime_0.1.0-alpha.1_windows_amd64.zip",
      "sha256": "<64 hex digits>",
      "size": 3145728,
      "binary_sha256": "<64 hex digits>"
    }
  ]
}
```

| Field | Rule |
|---|---|
| `version` | The release version: `v` and a SemVer 2.0.0 version without build metadata. It's the tag, and what `pairtime version` prints |
| `commit` | The full git commit hash, 40 or 64 lowercase hex digits |
| `date` | The commit time in RFC 3339, UTC, whole seconds |
| `minimum_version` | The oldest client version the release still supports, at most `version`. A client older than this should update before it does anything else. Tags take it from `tools/release/minimum-version.txt`, which holds exactly one version; `release.yml` and `tools/release`'s tests check it on every run. A prerelease sorts before its release, so to require 0.3.0 while its prereleases ship, the file says `v0.3.0-alpha.1` |
| `artifacts` | One entry per platform, at least one, no platform twice |
| `os`, `arch` | Go's `GOOS` and `GOARCH`: lowercase letters and digits. A client skips platforms it doesn't know |
| `url` | Where the archive is published: absolute `https`, no credentials, ending in a file name. Plain `http` is allowed only for a loopback host, which test fixtures use |
| `sha256` | The archive's SHA-256 in lowercase hex |
| `size` | The archive's length in bytes, more than 0 |
| `binary_sha256` | The SHA-256 of the binary inside the archive, so a binary that came some other way, such as inside an editor extension, can be checked too |

Versions compare by SemVer precedence, so `v0.0.0-snapshot.9 < v0.1.0-alpha.1 < v0.1.0-beta.1 < v0.1.0`. Readers ignore fields they don't know, so a release can add fields without breaking older clients. A change older clients can't read needs a new payload type.

### The envelope

The envelope has DSSE v1.0.2's three fields:

| Field | Value |
|---|---|
| `payload` | The manifest's bytes in standard base64 with padding |
| `payloadType` | `application/vnd.pairtime.update-manifest.v1+json` |
| `signatures` | A list of `{"keyid", "sig"}`, one per signing key |

Each `sig` is the base64 Ed25519 signature (RFC 8032, plain Ed25519) of DSSE's pre-authentication encoding of the payload, where the lengths are byte counts in decimal:

```text
DSSEv1 <length of payloadType> <payloadType> <length of payload> <payload>
```

`keyid` is the signing key's id (see Keys). `tools/release` writes the envelope as compact JSON with the fields in that order and one final newline:

```text
{"payload":"ewogICJ2ZXJzaW9uIjog...","payloadType":"application/vnd.pairtime.update-manifest.v1+json","signatures":[{"keyid":"21fe31dfa154a261","sig":"..."}]}
```

The verifier accepts that form byte for byte and nothing else. That is stricter than DSSE, which asks verifiers to accept URL-safe base64 as well. pairtime only reads its own manifests, so it gives up nothing, and every byte of a manifest with one signature counts: change any one and it no longer verifies.

### Verifying

A client, or anything else that checks a manifest:

1. Refuses a file over 1 MiB.
2. Refuses it unless it is exactly the form above.
3. Refuses a `payloadType` other than pairtime's.
4. For each signature whose `keyid` is the id of a trusted key, verifies the signature over the pre-authentication encoding. Signatures that fail, or whose `keyid` names no trusted key, are skipped. It refuses the file unless one verifies. As DSSE says, the key id only picks which key to try.
5. Parses the payload it verified, not a second decoding of the envelope, and refuses it if a field breaks a rule above.
6. Takes the entry for its own `os` and `arch`, downloads `url`, checks `size` and `sha256` before unpacking, and checks the binary against `binary_sha256` after.
7. Never installs a version that isn't newer than its own.

`internal/release.Open` does steps 1 to 5. Step 7 means a replayed old manifest can't downgrade a client. Nothing here detects a frozen feed, where someone keeps serving an old manifest; a client on such a feed just stays on its version.

## Keys

A release key is an Ed25519 key pair, written as one line of text:

| Kind | Form | Where it lives |
|---|---|---|
| Secret key | `pairtime-release-ed25519-secret:` and the 32-byte RFC 8032 private key in padded standard base64 | The `RELEASE_SIGNING_KEY` secret of the `release` environment, plus an offline copy. Never in the repository, and never a repository secret |
| Public key | `pairtime-release-ed25519-public:` and the 32-byte public key the same way | `internal/release/release-keys.txt`, one per line, compiled into the client with `go:embed` |

The two prefixes differ, so a secret key pasted into the key list fails to parse instead of being committed. A key's id is the first 8 bytes of the SHA-256 of its 32 raw public-key bytes, in hex; the public key of RFC 8032's test 1 has the id `21fe31dfa154a261`. `pairtime version` prints the ids its build trusts, or `none`.

### Making the key

Karim does this once, before the first tag that should be signed (PLAN WP-RL1). These are repository settings, so they're his to run, not an agent's.

1. Set up the `release` environment so that only `v*` tags can deploy to it. Only a job that deploys to it can read its secrets.

   ```sh
   gh api -X PUT repos/some-du6e/pairtime/environments/release \
     -F 'deployment_branch_policy[protected_branches]=false' \
     -F 'deployment_branch_policy[custom_branch_policies]=true'
   gh api -X POST repos/some-du6e/pairtime/environments/release/deployment-branch-policies \
     -f name='v*' -f type=tag
   ```

   Run both even if `release` already exists. The publish job names the environment, so a tag pushed before this step creates it, and GitHub creates it with no deployment policy, which lets a workflow run on any branch deploy to it and read its secrets. `PUT` adds the policy to an environment that exists. Then check what it admits:

   ```sh
   gh api repos/some-du6e/pairtime/environments/release/deployment-branch-policies \
     --jq '.branch_policies[] | "\(.id) \(.type) \(.name)"'
   ```

   It should print one line, ending in `tag v*`. Delete the policy of any other line with `gh api -X DELETE repos/some-du6e/pairtime/environments/release/deployment-branch-policies/<id>`.

2. Make the key from the repository root, with its secret half going straight into the environment:

   ```sh
   go run ./tools/release keygen | gh secret set RELEASE_SIGNING_KEY --env release --repo some-du6e/pairtime
   ```

   `keygen` appends the public key to `release-keys.txt` and prints the secret key to standard output once. Keep an offline copy too, for example by putting `tee >(age -r <recipient> -o release-signing-key.age)` between the two commands, with the offline `age` key DESIGN 10.2 uses for the server's keys. GitHub never shows a secret again. If the key is lost, installed clients can't verify any update, including one that would trust a new key.

   Store the key in the environment and nowhere else in GitHub. A repository or organization secret with the same name would reach every workflow run on every branch, so `release.yml` fails while one exists.

3. Commit `release-keys.txt` in a PR. The first tag after it merges is signed.

### Replacing a key

1. Make a new key with `keygen` and ship a release whose `release-keys.txt` lists both keys.
2. Sign the following manifests with both. A client that trusts either key accepts them. `internal/release.Sign` takes several keys; `tools/release sign` takes one today and needs a second `-key-env` when this happens.
3. Once clients have updated, remove the old key from the list and stop signing with it.

A leaked secret key can sign manifests that every client trusting it accepts, until those clients update to a build without it. Replace it at once.

## Snapshots and unsigned releases

- `workflow_dispatch`, and a PR that changes the release code, build a snapshot. Its version is `v0.0.0-snapshot.<run number>` and its `minimum_version` is the same. A throwaway key made in the job signs it and is never stored. The files stay as a workflow artifact for 7 days, with the throwaway public key as `snapshot-key.pub`. Their URLs name a release that doesn't exist.
- A tag pushed while no release key exists, with nothing in `release-keys.txt` and no secret, still publishes. The throwaway key signs it, its notes start with "Unsigned", and no client trusts its manifest; check its downloads against `checksums.txt`.
- Once `release-keys.txt` lists a key, a tag fails instead of publishing if the secret is missing or its key isn't in the list.

## Where clients find it

Every release has `manifest.json` as an asset. The newest stable one is at `https://github.com/some-du6e/pairtime/releases/latest/download/manifest.json`. GitHub's latest-release redirect skips prereleases, so while every release is an alpha, the updater (DESIGN 3.3) has to list releases through the API. Its work package decides how.

## How it's checked

- `internal/release`'s tests run RFC 8032 section 7.1's five vectors through the key parser and signer, use DSSE's own example of the encoding, use semver.org's regular expression and its precedence examples, refuse a signed manifest with a byte changed at any offset, and check that a secret key never prints.
- `tools/release`'s tests run dist, sign and verify in a temporary directory, rebuild to check the archives are reproducible, and check what verify refuses.
- `release.yml` checks every build twice: with `tools/release verify`, then with `tools/release/check.sh`, which uses only `sha256sum`, jq, tar, unzip and OpenSSL. On a tag it also checks that the shipped linux/amd64 binary names the signing key.
