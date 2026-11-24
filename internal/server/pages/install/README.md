# The install scripts pairtimed serves

pairtimed serves this folder's `install.sh` and `install.ps1` at `/install.sh` and `/install.ps1`, so `curl -fsSL https://<host>/install.sh | sh` and `irm https://<host>/install.ps1 | iex` install pairtime from a deployed server (docs/PLAN.md, WP-RL2). Those commands can't pass flags, and the repository's `install/` scripts name no release and trust no key, so the server serves one release's own scripts.

The folder holds that release's files, for the release `ScriptsRelease` names in `../install.go`, each byte for byte as the release published it:

- `install.sh` and `install.ps1`, which the release stamped with its version and the keys it trusts;
- `manifest.json`, the release's signed manifest.

At start, pairtimed checks that a key in `internal/release/release-keys.txt`, the keys this build trusts, signed the manifest, and that each script's SHA-256 is the one the manifest lists in `install_scripts`. If either check fails, pairtimed doesn't start. CI also fails while a served script trusts a key that `release-keys.txt` doesn't list (TestServedScriptsTrustOnlyThisBuildsKeys).

The copy stays pinned to a release because pairtimed is built from `main`, where `install/` can be newer than any release.

## Moving the pin

After each release, a PR moves the copy to it. The release's manifest must list `install_scripts`:

```sh
v=v0.1.0-alpha.7
gh release download "$v" --repo some-du6e/pairtime --pattern install.sh --pattern install.ps1 --pattern manifest.json --dir internal/server/pages/install --clobber
```

Then the same PR sets `ScriptsRelease` in `../install.go` to the release, and `installRelease` in `../dashboard.go` and README.md's install section with it: its "These commands install vX." sentence and its two install commands. TestDashboardFollowsThePin and TestInstallLines hold them together. The host serves the new copy once an image built from that commit is pinned (deploy/runbook.md, Upgrades).

## Turning the pin off

`ScriptsRelease = ""` turns the pin off. Both paths then answer 404, and pairtimed starts without them. The PR that drops a key from `internal/release/release-keys.txt` turns the pin off, unless the pin already names a release that a remaining key signed and whose scripts trust no dropped key. A later PR pins a release the new key signed (docs/spec/update-manifest.md, Replacing a key).
