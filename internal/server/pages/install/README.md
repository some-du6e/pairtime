# The install scripts pairtimed serves

pairtimed serves this folder's scripts at `/install.sh` and `/install.ps1`, so `curl -fsSL https://<host>/install.sh | sh` and `irm https://<host>/install.ps1 | iex` install pairtime from a deployed server (docs/PLAN.md, WP-RL2). Those commands can't pass flags, and the repository's `install/` scripts name no release and trust no key, so the server serves one release's own scripts.

The folder holds that release's files, for the release `scriptsRelease` names in `../install.go`:

- `install.sh`, `install.ps1` and `release-keys.txt`, as the release's tag has them in `install/` and `internal/release/`, before `tools/release scripts` stamps the scripts;
- `manifest.json`, the release's signed manifest, byte for byte.

At start, pairtimed stamps the scripts for the release with those keys, as `tools/release scripts` stamped them for the release. It then checks that each script's SHA-256 is the one the manifest lists in `install_scripts`, and that a key in `internal/release/release-keys.txt` signed the manifest. If either check fails, pairtimed doesn't start, and TestInstallScripts and TestStampScripts fail in CI first.

The copy stays pinned to a release because pairtimed is built from `main`, where `install/` can be newer than any release. A script whose bytes no release lists would install a release it wasn't made for.

To serve a newer release, one whose manifest lists `install_scripts`, copy its files and set `scriptsRelease` in `../install.go` to its version:

```sh
v=v0.1.0-alpha.7
git show "$v:install/install.sh" > internal/server/pages/install/install.sh
git show "$v:install/install.ps1" > internal/server/pages/install/install.ps1
git show "$v:internal/release/release-keys.txt" > internal/server/pages/install/release-keys.txt
gh release download "$v" --pattern manifest.json --dir internal/server/pages/install --clobber
```

The server serves the new copy once its image is deployed.
