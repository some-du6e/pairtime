# The planted-secret corpus

`corpus.jsonl.gz` is every true and false positive gitleaks checks its own rules against, at gitleaks v8.30.1, commit `83d9cd684c87d95d656c1458ef04895a7f1cbd8e`: 220 rule cases with 6,396 true positives and 374 false positives. DESIGN 8.7 asks for a CI corpus of planted secrets built from gitleaks' rule examples that must come out fully redacted, and `TestGitleaksCorpus` in `pkg/redact` is that check.

## How it's built

gitleaks writes each rule in Go, in `cmd/generate/config/rules`, with a list of true positives and false positives that its config generator checks before it writes `config/gitleaks.toml`. Many examples are generated at run time from the rule's own regex (`secrets.NewSecret`) and then put through about 30 config-file and source-code shapes (`utils.GenerateSampleSecrets`), so they can't be copied out of the source as text. `extract.sh` runs the generator itself instead:

1. It clones gitleaks at the pinned tag and checks the commit.
2. It copies `dump.go.txt` into the generator's `utils` package and adds one call to it at the top of `Validate` and `ValidateWithPaths`. The hook runs gitleaks' own single-rule detector on each example and writes the example with the secrets it found.
3. It runs the generator, which then checks every example as usual and fails on any that its rule doesn't catch.
4. It checks that the generator wrote gitleaks' `config/gitleaks.toml` byte for byte, and that `pkg/redact/rules/gitleaks.toml.gz` holds the same file, so the corpus and the embedded rules come from one source.
5. It sorts the cases by rule and gzips them.

It needs git, Go and the network. The committed file came from a CI run of `extract.sh` on 2026-11-19. Because examples are partly random, every run writes different secrets; any run's output is a valid corpus.

## Format

One JSON object per line, sorted by rule id:

```json
{"rule": "<gitleaks rule id>",
 "tps": [{"text": "<example>", "path": "<file path, only for path rules>", "secrets": ["<what gitleaks found>"]}],
 "fps": [{"text": "<example>", "secrets": []}]}
```

Read it with `gzip -dc corpus.jsonl.gz`. It's gzipped because thousands of plausible keys in a public repo would trip GitHub's push protection and secret scanning. Every one of them is a fake that gitleaks generated or ships in its own tests.

## What's not covered

- `dropbox-long-lived-api-token` and `dropbox-short-lived-api-token`: gitleaks defines them without examples (`dropbox.go` returns them without calling `Validate`), so there's nothing to extract.
- `pkcs12-file`: its examples are file names. It matches a path, not text, so `pkg/redact` doesn't load it.
- False positives with a path (from `ValidateWithPaths`): gitleaks kept them because of the file they were in, and `pkg/redact` applies every rule to all text, so the test skips them.

## License

`corpus.jsonl.gz` is made from gitleaks' own rule sources and the examples its generator writes, so it's under gitleaks' MIT License, Copyright (c) 2019 Zachary Rice, in `LICENSE` here (the same file as `pkg/redact/rules/LICENSE`). `extract.sh` and `dump.go.txt` are pairtime's, under the AGPL-3.0-or-later like the rest of the repo.
