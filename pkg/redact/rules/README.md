# gitleaks' rules as data

`pkg/redact` embeds `gitleaks.json.gz`, gitleaks' default rules converted to JSON. Everything it's made from is in this directory.

| File | What it is |
|---|---|
| `gitleaks.toml.gz` | gitleaks' `config/gitleaks.toml` at tag v8.30.1, commit `83d9cd684c87d95d656c1458ef04895a7f1cbd8e`, byte for byte, gzipped. The plain file's SHA-256 is `e163e53b9e7e8a8511e77271e2b323ed057759542a6d988258afe3a1fa329caf` and its git blob is `256f64790ea6d954f0041024be2938089ae1e7a7` |
| `convert.py` | The converter: stdlib-only Python 3.11 or later, using `tomllib` |
| `gitleaks.json.gz` | What `convert.py --write` writes. Never edit it by hand |
| `LICENSE` | gitleaks' MIT license. The rules are gitleaks' work, Copyright (c) 2019 Zachary Rice |

The config holds 222 rules: 221 with a regex, and `pkcs12-file`, which only matches file names and so never applies to text. gitleaks compiles its patterns with Go's `regexp`, so they compile here unchanged. `ci.yml` runs `python3 -I pkg/redact/rules/convert.py --check` on every PR, which fails if `gitleaks.json.gz` isn't exactly what the converter writes from `gitleaks.toml.gz`. The converter refuses a config whose SHA-256 isn't the pinned one, and any key it doesn't know, such as `extend` or `[[rules.required]]`, so a gitleaks feature the Go side doesn't implement can't slip through.

## Why the files are gzipped

The gitleaks config allowlists 16 published Google API keys by value, and the planted corpus in `testdata/redact/gitleaks` is thousands of things that look like keys. As plain text in a public repo they would trip GitHub's push protection and fill the repo's secret-scanning alerts. Scanners don't open gzip files. Read them with `gzip -dc`.

## Checking the copy against upstream

```sh
git clone --depth 1 --branch v8.30.1 https://github.com/gitleaks/gitleaks
gzip -dc pkg/redact/rules/gitleaks.toml.gz | cmp - gitleaks/config/gitleaks.toml
```

`testdata/redact/gitleaks/extract.sh` makes the same check every time it runs.

## Moving to a new gitleaks tag

1. Replace `gitleaks.toml.gz` with the new tag's `config/gitleaks.toml`, gzipped with `gzip -9n`.
2. Update `TAG`, `COMMIT` and `SHA256` in `convert.py`, and the tag in `testdata/redact/gitleaks/extract.sh`.
3. Run `python3 -I pkg/redact/rules/convert.py --write`. If it refuses a new key, implement it in `pkg/redact/gitleaks.go` first.
4. Run `testdata/redact/gitleaks/extract.sh` to rebuild the corpus from the new tag's rule sources. It needs Go and the network, so run it in CI if you can't run it locally.
5. Update the counts the tests pin (`TestGitleaksRulesCompile`, `TestGitleaksCorpus`) and say in the PR which rules came and went.
