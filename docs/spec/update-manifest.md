# Update manifest

Status: version 1, from WP-RL1 (docs/PLAN.md). `internal/release` implements it, `tools/release` writes the files, and `.github/workflows/release.yml` publishes them.

Every pairtime release publishes one archive per platform, `checksums.txt`, `manifest.json` and the install scripts. Before pairtime is installed, the install script checks `manifest.json` against the release keys written into it (Install scripts), and before the client updates itself, it checks it against the release keys compiled into it (DESIGN 3.3, CP 4.6). This file specifies those files, the keys and the checks.

## Release files

| File | What it is |
|---|---|
| `pairtime_<version>_windows_<arch>.zip` | The Windows client |
| `pairtime_<version>_<os>_<arch>.tar.gz` | The macOS and Linux clients |
| `checksums.txt` | The SHA-256 of every archive |
| `manifest.json` | The signed manifest |
| `install.sh`, `install.ps1` | The install scripts for macOS and Linux and for Windows, which install this release. The signed manifest lists their SHA-256s (Install scripts) |

`<version>` is the tag without its `v`, as in `pairtime_0.1.0-alpha.1_windows_amd64.zip`. There is one archive for each target in CP section 7: `windows/amd64`, `windows/386`, `windows/arm64`, `darwin/amd64`, `darwin/arm64`, `linux/amd64` and `linux/arm64`.

### Archives

An archive holds four files at its root and no directories: the binary (`pairtime`, or `pairtime.exe` on Windows) with mode 0755, then `LICENSE`, `README.md` and `THIRD_PARTY_NOTICES.txt` with mode 0644. Every file has the release commit's time. The tar.gz archives are USTAR with owner 0 and no gzip timestamp, and the zip archives record Unix modes. The same commit built with the same Go version gives the same bytes.

The binary is built the way `ci.yml` builds it, with the tag as the version:

```sh
CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build -trimpath -buildvcs=false \
  -ldflags "-s -w -X github.com/some-du6e/pairtime/internal/version.Version=<tag> \
    -X github.com/some-du6e/pairtime/internal/version.Commit=<commit> \
    -X github.com/some-du6e/pairtime/internal/version.Date=<commit time>" \
  ./cmd/pairtime
```

### THIRD_PARTY_NOTICES.txt

Every binary statically links Go's runtime and standard library and some third-party modules, and their licenses (BSD-3-Clause, BSD-2-Clause, MIT, Apache-2.0) require a binary to come with their copyright notices and license terms. `tools/release notices` writes the file for one built binary, so each archive lists what its own target links. go-keyring's vendored `internal/shellescape`, for one, is linked only on macOS, so only the macOS archives carry its MIT license.

- The modules come from the binary's build info, the list `go version -m` prints. `go list -deps` then finds the binary's packages, with the GOOS, GOARCH, cgo setting and tags the build info records, and the two have to name the same modules, or the command fails.
- From each module it copies every license and notice file in a linked package's directory and in each directory above it, up to the module root: the directories `tools/ci/licensecheck` reads. A file counts when its name, ignoring case, is `LICENSE`, `LICENCE`, `COPYING`, `UNLICENSE`, `MIT-LICENSE`, `NOTICE` or `PATENTS`, alone or followed by `.`, `-` or `_` and more, as in `LICENSE-APACHE` or `NOTICE.txt`, unless it's a file the go command compiles, like lib/pq's `notice.go`. A module without a license file at its root fails.
- Go's part is `LICENSE` and `PATENTS` from the `GOROOT` of the go command running the tool, which must be the Go version that built the binary.
- `tools/release/extra-notices.txt` lists material that pairtime's own packages embed, or that their code was ported from, and that isn't a module, so no build info points at it. Each line names the package's directory, the license file and what the material is, both paths from the repository root. A binary's notices include the file when the binary links the package. Every line is checked on every run, so a renamed file fails instead of dropping a notice, and so does a directory that holds no Go file a build compiles, such as `pkg/redact/rules`, because no binary links it. The file lists gitleaks' rules, which `pkg/redact` embeds, and Presidio's recognizers, which it ports. gitleaks' line is:

  ```text
  pkg/redact pkg/redact/rules/LICENSE gitleaks v8.30.1's default rules, embedded in pkg/redact
  ```

- The local dashboard that `internal/webui` embeds is a Vite build with npm packages in it, which no build info lists either. Each license and notice file of those packages is copied into `internal/webui/notices/<package>/` and listed against `internal/webui` with the title `<name> <version> from npm, bundled into the local dashboard in internal/webui`, so every `pairtime` archive carries it. Svelte's `LICENSE.md` was the first.

  `vite build` writes `web/dist/bundle-<mode>.json` beside each build: every module in its chunks, with the length of the code it rendered there, and every file it emitted as an asset. `tools/ci/npm_notices.py` holds the copies to the dashboard's record. A package has code in the build when a module of its renders code or is a stylesheet, or a file of its is an asset. Each license and notice file at its root, or in a directory between the root and that code, needs a copy with the same bytes as in `web/node_modules`, and a line. A copy or line for a package the build doesn't bundle fails, and so does code from a module that is neither `web/src`'s nor a package's, such as one a bundler writes itself.

The file opens with the binary's main package, platform and Go version and a list of what it contains, then copies each license and notice file word for word under a header naming its module and path. It names no directory on the build machine, so it's as reproducible as the archive. When `release.yml` ships the server (WP-OP1), `pairtimed` gets its own the same way; `ci.yml` already writes them for both binaries.

### checksums.txt

One line per archive in `sha256sum`'s format: the SHA-256 in lowercase hex, two spaces, the file name. Lines are sorted by file name. `sha256sum -c checksums.txt`, run where the downloads are, checks them. `manifest.json` isn't listed, because its signature covers it, and neither are the install scripts, whose SHA-256s are in the signed manifest.

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
  ],
  "install_scripts": {
    "install.ps1": "<64 hex digits>",
    "install.sh": "<64 hex digits>"
  }
}
```

| Field | Rule |
|---|---|
| `version` | The release version: `v` and a SemVer 2.0.0 version without build metadata. It's the tag, and what `pairtime version` prints |
| `commit` | The full git commit hash, 40 or 64 lowercase hex digits |
| `date` | The commit time in RFC 3339, UTC, whole seconds |
| `minimum_version` | The oldest client version the release still supports, at most `version`. A client older than this should update before it does anything else. One whose update fails step 8 of Verifying keeps its own binary and keeps working, and `doctor` says it is below the minimum. Raising the minimum past the last release built with Go 1.26 ends macOS 12 support (DESIGN 3.12). Tags take it from `tools/release/minimum-version.txt`, which holds exactly one version; `release.yml` and `tools/release`'s tests check it on every run. A prerelease sorts before its release, so to require 0.3.0 while its prereleases ship, the file says `v0.3.0-alpha.1` |
| `artifacts` | One entry per platform, at least one, no platform twice |
| `os`, `arch` | Go's `GOOS` and `GOARCH`: lowercase letters and digits. A client skips platforms it doesn't know |
| `url` | Where the archive is published: absolute `https`, no credentials, ending in a file name. Plain `http` is allowed only for a loopback host, which test fixtures use |
| `sha256` | The archive's SHA-256 in lowercase hex |
| `size` | The archive's length in bytes, more than 0 |
| `binary_sha256` | The SHA-256 of the binary inside the archive, so a binary that came some other way, such as inside an editor extension, can be checked too |
| `install_scripts` | The SHA-256 in lowercase hex of each install script the release carries, by file name, each a plain name with no directory: `install.sh` and `install.ps1` (Install scripts). A release without the scripts leaves the field out. It was added after version 1 shipped, as a field older readers ignore, so the payload type stayed the same; whatever serves or checks a script compares it with this |

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
8. Before it replaces its own binary, runs the new one's `version` subcommand with a 60 s timeout, and keeps its own binary unless that exits 0. It records the failure for `doctor` and tries that version again at most once a week. So a build that can't run on the machine, such as a Go 1.27 build on macOS 12 (DESIGN 3.12), is never swapped in. The VS Code extension runs the same check before it puts its bundled binary in place of an installed one.

`internal/release.Open` does steps 1 to 5. Step 7 means a replayed old manifest can't downgrade a client. Nothing here detects a frozen feed, where someone keeps serving an old manifest; a client on such a feed just stays on its version.

## Keys

A release key is an Ed25519 key pair, written as one line of text:

| Kind | Form | Where it lives |
|---|---|---|
| Secret key | `pairtime-release-ed25519-secret:` and the 32-byte RFC 8032 private key in padded standard base64 | The `RELEASE_SIGNING_KEY` secret of the `release` environment, plus an offline copy. Never in the repository, and never a repository secret |
| Public key | `pairtime-release-ed25519-public:` and the 32-byte public key the same way | `internal/release/release-keys.txt`, one per line, compiled into the client with `go:embed` |

The two prefixes differ, so a secret key pasted into the key list fails to parse instead of being committed. A key's id is the first 8 bytes of the SHA-256 of its 32 raw public-key bytes, in hex; the public key of RFC 8032's test 1 has the id `21fe31dfa154a261`. `pairtime version` prints the ids its build trusts, or `none`.

### Making the key

Karim does this once, before the first tag that should be signed (PLAN WP-RL1). These are repository settings, so they're his to run, not an agent's. Step 2 encrypts a backup to the offline `age` key pair (DESIGN 10.5, item 5), so that key pair has to exist first.

1. Set up the `release` environment so that only `v*` tags can deploy to it, and only once Karim approves the run. Only a job that deploys to it can read its secrets.

   ```sh
   gh api -X PUT repos/some-du6e/pairtime/environments/release --input - <<EOF
   {
     "deployment_branch_policy": {"protected_branches": false, "custom_branch_policies": true},
     "reviewers": [{"type": "User", "id": $(gh api users/some-du6e --jq .id)}]
   }
   EOF
   gh api -X POST repos/some-du6e/pairtime/environments/release/deployment-branch-policies \
     -f name='v*' -f type=tag
   ```

   Run both even if `release` already exists. The publish job names the environment, so a tag pushed before this step creates it, and GitHub creates it with no deployment policy and no reviewer, which lets a workflow run on any branch deploy to it and read its secrets. `PUT` sets the policy and the reviewer on an environment that exists; send both fields in any later `PUT` too. If the `POST` reports that the `v*` policy exists, the environment already has it. Then check what it admits and who approves:

   ```sh
   gh api repos/some-du6e/pairtime/environments/release/deployment-branch-policies \
     --jq '.branch_policies[] | "\(.id) \(.type) \(.name)"'
   gh api repos/some-du6e/pairtime/environments/release \
     --jq '.protection_rules[] | select(.type == "required_reviewers") | .reviewers[].reviewer.login'
   ```

   The first should print one line, ending in `tag v*`. Delete the policy of any other line with `gh api -X DELETE repos/some-du6e/pairtime/environments/release/deployment-branch-policies/<id>`. The second should print `some-du6e`. Each publish job then waits on its run's page until Karim approves it. The approval is no guard against a tool that has Karim's credentials (What gets signed).

2. Make the key from the repository root, with its secret half going to an offline copy and straight into the environment:

   ```sh
   go run ./tools/release keygen \
     | tee >(age -r <your age recipient> -o "$HOME/release-signing-key.age") \
     | gh secret set RELEASE_SIGNING_KEY --env release --repo some-du6e/pairtime
   ```

   `keygen` appends the public key to `release-keys.txt` and prints the secret key to standard output once. `tee` encrypts one copy to the offline `age` key DESIGN 10.2 uses for the server's keys, and `gh` stores the other. The copy goes to your home directory, outside the checkout: keygen runs from the repository root, and the next step commits from there, where one `git add -A` would put the encrypted key in the public history for good. `.gitignore` ignores `*.age` as a second guard. GitHub never shows a secret again, so the copy is the only way back, and if the key is lost no installed client can verify any update, including one that would trust a new key. Check that the copy decrypts: `age -d -i <your age identity> "$HOME/release-signing-key.age" | cut -c1-32` prints `pairtime-release-ed25519-secret:` and none of the key.

   Store the key in the environment and nowhere else in GitHub. A repository or organization secret with the same name would reach every workflow run on every branch, so `release.yml` fails while one exists.

3. Once that check prints the prefix, commit `release-keys.txt` in a PR. If it prints anything else, `git checkout internal/release/release-keys.txt` drops the new key, and step 2 can run again. The first tag after the PR merges is signed.

### What gets signed

The publish job signs a tag only if `main` contains the tagged commit. Before it reads the key, it runs `git merge-base --is-ancestor "$GITHUB_SHA" origin/main`, so a `v*` tag pushed by mistake on a PR branch or in an agent's worktree fails there. It catches mistakes, not someone who means to sign such a commit. A tag runs the `release.yml` of the commit it names, so the check runs only on commits whose `release.yml` still has it, and a tag on a commit whose copy leaves it out gets signed.

What stops that is who can push a `v*` tag and approve the run. The environment's required reviewer is step 1 of Making the key, and two rulesets do the rest. They're repository settings, so Karim adds them:

- `main` takes changes only through pull requests and can't be force-pushed or deleted, so "on `main`" means merged.
- Only a repository admin, which on this personal repository means Karim, can create, move or delete a `v*` tag. Without this rule, anyone who can push a branch can push a tag.

```sh
gh api -X POST repos/some-du6e/pairtime/rulesets --input - <<'EOF'
{
  "name": "main",
  "target": "branch",
  "enforcement": "active",
  "conditions": {"ref_name": {"include": ["~DEFAULT_BRANCH"], "exclude": []}},
  "rules": [
    {"type": "deletion"},
    {"type": "non_fast_forward"},
    {"type": "pull_request", "parameters": {
      "required_approving_review_count": 0,
      "dismiss_stale_reviews_on_push": false,
      "require_code_owner_review": false,
      "require_last_push_approval": false,
      "required_review_thread_resolution": false
    }}
  ]
}
EOF
gh api -X POST repos/some-du6e/pairtime/rulesets --input - <<'EOF'
{
  "name": "release tags",
  "target": "tag",
  "enforcement": "active",
  "conditions": {"ref_name": {"include": ["refs/tags/v*"], "exclude": []}},
  "rules": [{"type": "creation"}, {"type": "update"}, {"type": "deletion"}],
  "bypass_actors": [{"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always"}]
}
EOF
gh api repos/some-du6e/pairtime/rulesets --jq '.[] | "\(.id) \(.target) \(.name) \(.enforcement)"'
```

The last command should list both rulesets as `active`. Actor 5 is the admin role. The `main` ruleset lets nobody bypass it, so Karim's own changes go through PRs too, and so do the orchestrator's direct docs commits (#22).

None of this stops a tool that holds Karim's credentials, and the agents on his machines hold them today. They push as `some-du6e`, an admin, and the tag ruleset lets admins bypass it. Any token of his with the `repo` scope, like the one `gh` uses, can approve a pending deployment as him (`POST repos/some-du6e/pairtime/actions/runs/<run id>/pending_deployments`). So such a tool can push a `v*` tag on any commit, with a `release.yml` that skips the check, and approve its run.

To shut that out, give the agents a GitHub account of their own and add it as a collaborator, which on a personal repository has write access and not admin:

```sh
gh api -X PUT repos/some-du6e/pairtime/collaborators/<the agents' account>
```

Once it accepts the invitation, sign the agents' `gh` and git credentials in as that account, and keep `some-du6e`'s tokens off the machines they run on, since an agent can use any token it can read. The tag ruleset then refuses their `v*` tags, and they can't approve the environment, because only Karim reviews it. They can still merge PRs, so a tag signs whatever `main` holds when Karim pushes it, and his approval is where he checks that.

Immutable releases is on for the repository, so a published release's files and its tag can't be changed or replaced (D-immutable-releases; the orchestrator turned the setting on for Karim on 2026-11-20). That matters most for the install scripts. `curl -fsSL <release>/install.sh | sh` runs `install.sh` before anything else, and it carries the release keys that the rest of the download is checked against, so whoever could swap it would control every new install without the signing key, and write access, the agents' account's included, would be enough. The publish job uploads into a draft, which stays editable, and publishes that draft by its id once every file is there, so the files freeze complete. It deletes every draft of the tag that a failed run left before it makes its own, and refuses a tag that already has a published release (tools/ci/publish_release.sh). The signed payload lists both scripts' SHA-256s as well (`install_scripts`), so `check.sh`, and `pairtimed` once it serves the scripts (WP-OP2), can check the copy they have. To see the setting, `gh api repos/some-du6e/pairtime/immutable-releases` prints `"enabled":true`. GitHub also attests each immutable release, and `gh release verify-asset <tag> install.sh` checks a downloaded copy against that attestation.

### Replacing a key

An installed client trusts only the keys compiled into it, so the release that brings a new key has to be signed with the old one. Between rotations, `$HOME/release-signing-key.age` holds the key `RELEASE_SIGNING_KEY` holds. During one, `$HOME/release-signing-key-next.age` holds the key waiting to replace it.

1. Make the new key with `keygen`, with its secret half going only to an offline copy, and check that the copy decrypts. `RELEASE_SIGNING_KEY` keeps the old key. `keygen` says so when the list already holds a key. `age -o` writes over an existing file without asking, and step 3 renames the file at the end of every rotation, so if `$HOME/release-signing-key-next.age` already exists, an earlier rotation never finished. Finish its step 3, or move the file aside, first.

   ```sh
   go run ./tools/release keygen | age -r <your age recipient> -o "$HOME/release-signing-key-next.age"
   age -d -i <your age identity> "$HOME/release-signing-key-next.age" | cut -c1-32
   ```

   The second command must print `pairtime-release-ed25519-secret:` and none of the key. Commit `release-keys.txt`, which now lists both keys, in a PR only once it does, because GitHub holds no copy of this key and the backup is the only one. If it prints anything else, `git checkout internal/release/release-keys.txt` drops the new key, and the step can run again.
2. Once that PR merges, tag a release. The old key signs it, so installed clients accept it, and its binaries trust both keys. Had the new key replaced `RELEASE_SIGNING_KEY` already, no installed client could verify this release or any after it, and GitHub would never show the old key again to undo that.
3. Keep signing with the old key until clients run a build that trusts the new one. Raising `minimum-version.txt` to step 2's version in the last release the old key signs tells clients still older than that to update first. Then move the new key into the environment:

   ```sh
   age -d -i <your age identity> "$HOME/release-signing-key-next.age" \
     | gh secret set RELEASE_SIGNING_KEY --env release --repo some-du6e/pairtime
   ```

   Once `gh` has stored it, rename the copies, so `release-signing-key.age` holds the key `RELEASE_SIGNING_KEY` holds again and the retired key sits under its id. Without this, the next rotation's step 1 would write over `release-signing-key-next.age`, the live key's only offline copy.

   ```sh
   mv "$HOME/release-signing-key.age" "$HOME/release-signing-key-<old key id>.age" &&
     mv "$HOME/release-signing-key-next.age" "$HOME/release-signing-key.age"
   ```

   `<old key id>` is the id of the key that signed the latest release, which that release's notes name.

   A client that never got a build with the new key can't verify a release from here on. Signing with both keys for a while would keep such clients updating: `internal/release.Sign` takes several keys, but `tools/release sign` takes one, so it would need a second `-key-env`, such as `RELEASE_SIGNING_KEY_NEXT`, first.
4. Remove the old key from `release-keys.txt` in a PR.

A leaked secret key can sign manifests that every client trusting it accepts, until those clients update to a build without it. Replace it at once, without waiting at step 3: the release after step 2's can already be signed with the new key and drop the leaked one from the list.

## Snapshots and unsigned releases

- `workflow_dispatch`, and a PR that changes the release code, build a snapshot. Its version is `v0.0.0-snapshot.<run number>` and its `minimum_version` is the same. A throwaway key made in the job signs it and is never stored. The files stay as a workflow artifact for 7 days, with the throwaway public key as `snapshot-key.pub`. Their URLs name a release that doesn't exist.
- A tag pushed while no release key exists, with nothing in `release-keys.txt` and no secret, still publishes. The throwaway key signs it, its notes start with "Unsigned", and no client trusts its manifest; check its downloads against `checksums.txt`.
- Once `release-keys.txt` lists a key, a tag fails instead of publishing if the secret is missing or its key isn't in the list.
- A tag on a commit that `main` doesn't contain fails before anything is signed or published, with a key or without one, if that commit's `release.yml` has the check (What gets signed).

## Where clients find it

Every release has `manifest.json` as an asset. The newest stable one is at `https://github.com/some-du6e/pairtime/releases/latest/download/manifest.json`. GitHub's latest-release redirect skips prereleases, so while every release is an alpha, the updater (DESIGN 3.3) has to list releases through the API. Its work package decides how.

## Install scripts

`install/install.sh` (POSIX sh) and `install/install.ps1` (Windows PowerShell 5.1 and PowerShell 7) are the one-line installs, `curl -fsSL <release>/install.sh | sh` and `irm <release>/install.ps1 | iex` (docs/PLAN.md, WP-RL2). `tools/release scripts` writes the copies a release carries: each repository copy with two lines filled in, the release's version and the keys in `release-keys.txt`. So a release's scripts install that release and trust exactly the keys its binaries trust. The repository's copies have neither and need both given. With `-payload`, which `release.yml` passes, `scripts` also lists both copies' SHA-256s in the payload as `install_scripts` before it's signed, and `tools/release verify` and `check.sh` refuse a release whose copies don't match, or that carries a script the payload doesn't list (What gets signed).

Each script checks `manifest.json` itself, before it runs anything it downloaded, because a downloaded binary can't vouch for itself. macOS's `openssl` can't check Ed25519 and Windows PowerShell 5.1 has no Ed25519, so each carries RFC 8032's verification: `install.sh` in POSIX `bc`, `install.ps1` with .NET's big integers. Each does Verifying steps 1 to 4, refusing base64 that isn't canonical as `internal/release` does, and steps 5 and 6 for the fields it uses:

- it refuses a payload whose `version` isn't the one it was asked for, so an older signed manifest can't stand in for it, one that lists its platform twice, and one whose SHA-256s or size for its platform break the rules above. `commit`, `date` and `minimum_version`, which it doesn't use, it doesn't check;
- it downloads the archive from the directory `manifest.json` came from, after checking that the manifest's `url` names the same file, rather than from `url` itself, since the signed SHA-256 decides whether it's good;
- it checks the archive's size and SHA-256 before unpacking it, and the binary's SHA-256 after.

Then each one:

- stages the new binary beside its final place and runs its `version`, for at most 60 seconds, and goes on only if that exits 0 and names the release (DESIGN 3.12);
- if the binary has `pairtime install`, runs the staged copy's `install`, with the terminal as its input when the script came through a pipe. `pairtime install` puts the binary at the per-user path, `~/.local/bin/pairtime` or `%LOCALAPPDATA%\pairtime\bin\pairtime.exe`, once the teen says yes, after its own checks that the file there is pairtime and that hooks which run it would still work (docs/errata/2026-11-20-install-binary-after-consent.md). The script passes `--made <folder>` for each folder it made for the binary, `~/.local` and `~/.local/bin` or `pairtime` and `pairtime\bin` in `%LOCALAPPDATA%`, so that `pairtime uninstall` removes those too, and every install command it prints for later carries the same flags. After a no, or a failure before the binary is in place, it removes the staged copy and those folders;
- otherwise, with `--no-install` or for a build without that command, puts the binary at the per-user path itself, without admin rights. On Windows a running `pairtime.exe` is renamed to `pairtime.old.exe` first, since it can't be replaced.

The scripts install for the person running them. They refuse to run as root, or with the elevated half of a split Windows token, as from "Run as administrator", before downloading anything. On Windows that is the test `pairtime hook` makes before it starts the agent, so an administrator whose token isn't split, as with UAC off, can still install.

While no key is listed, every release is unsigned, and the scripts refuse it unless told to allow it: `--allow-unsigned`, which under a pipe is `sh -s -- --allow-unsigned`, or for `install.ps1` `-AllowUnsigned` or `$env:PAIRTIME_ALLOW_UNSIGNED = '1'`. `install.ps1` removes that variable from the session once it has read it, and `install.sh` reads no variable for it, so it lasts for one install. It applies only to a script that trusts no key: once a script trusts one, pinned or given with `--release-keys`, a manifest that no trusted key signed is always refused (D-allow-unsigned-scope).

A failure stops the script with a message and removes whatever it downloaded and the folders it made. Once the binary is in place, it stays, and a failure says where it is. The scripts make no request but the downloads, which carry a `User-Agent` naming the script and pairtime's repository. The commands they print for you to run quote any path that needs it. `e2e-install.yml` runs both against a snapshot release built the way `release.yml` builds one, and against broken copies of it. It also runs the script with `--yes` in an empty home, then `pairtime uninstall --yes --delete-data`, and checks that the home is empty again and no agent is left.

## How it's checked

- `internal/release`'s tests run RFC 8032 section 7.1's five vectors through the key parser and signer, use DSSE's own example of the encoding, use semver.org's regular expression and its precedence examples, refuse a signed manifest with a byte changed at any offset, and check that a secret key never prints.
- `tools/release`'s tests run dist, sign and verify in a temporary directory, rebuild to check the archives are reproducible, check that no tar.gz header holds a time or an owner, and check what verify refuses. They build the notices for a fake binary over a fake module cache, refuse one whose modules go list doesn't confirm, and check the checked-in `minimum-version.txt` and `extra-notices.txt`.
- `ci.yml` writes the notices for all fourteen binaries it cross-compiles, `pairtimed` included, so a new module whose license can't be found fails in the PR that adds it.
- `web.yml` runs `tools/ci/npm_notices.py` on the dashboard it builds, and `release.yml`'s dashboard job on the one a release embeds, so a new package with code in the bundle, or an upgrade that changes a license or version, fails until its copies and lines are committed. `release.yml` then checks that every archive's `THIRD_PARTY_NOTICES.txt` holds each of the `internal/webui` files.
- `release.yml` checks every build twice: with `tools/release verify`, then with `tools/release/check.sh`, which uses only `sha256sum`, jq, tar, unzip and OpenSSL. Both check the install scripts against `install_scripts` too, and the tampering step makes sure both refuse a changed archive, payload, signature and script. On a tag it also checks that the shipped linux/amd64 binary names the signing key.
