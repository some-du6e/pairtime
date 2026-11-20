# Certificate Transparency Merkle vectors

These files are copied from [transparency-dev/merkle](https://github.com/transparency-dev/merkle), the Go library behind several Certificate Transparency and transparency-log implementations, at commit `75a6539d7b48af0d529236f24487d2814fd1dca7` (2026-11-19). They test RFC 6962 and RFC 9162 Merkle trees, which use the same hashes: `SHA-256(0x00 || entry)` for a leaf and `SHA-256(0x01 || left || right)` for a node.

`tools/ledger_ref.py selftest` checks pairtime's Merkle code against them. Nobody in pairtime wrote them, which is why they're an oracle.

| File here | Upstream path | Changes |
|---|---|---|
| `LICENSE` | `LICENSE` | None |
| `constants.go.txt` | `testonly/constants.go` | Renamed, so the Go toolchain and pairtime's SPDX gate skip it. Content unchanged |
| `inclusion/**/*.json` | `testdata/inclusion/**/*.json` | None. All 98 files |

`constants.go.txt` holds the eight leaf inputs, the hash of every complete subtree, and the root of every tree of size 0 to 8 built from them. Each file under `inclusion/` is one inclusion-proof check: a leaf index, a tree size, a root, a leaf hash and a proof, all base64, with `wantErr` saying whether verification must fail. Six must pass; the other 92 are mutations of them (a flipped bit, a wrong index or size, a missing, extra or garbage proof element, an empty or wrong root) and must fail.

`SHA256SUMS` lists the SHA-256 of every copied file, in `sha256sum` format; `selftest` checks it before it uses them. To check the copy against upstream:

```sh
git clone https://github.com/transparency-dev/merkle && cd merkle
git checkout 75a6539d7b48af0d529236f24487d2814fd1dca7
sha256sum LICENSE testonly/constants.go
(cd testdata && find inclusion -name '*.json' | LC_ALL=C sort | xargs sha256sum)
```

The files are under the Apache License 2.0, in `LICENSE`, copyright Google LLC. They're kept here unmodified apart from the one rename above.
