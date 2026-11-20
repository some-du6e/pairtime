# Redaction test data

The oracles `pkg/redact` is checked against (WP-PV1). None of them was written by the code it checks, and none holds real data: every value is either copied from an upstream project's own tests or invented.

| Directory | What it is | Where it comes from |
|---|---|---|
| `gitleaks/` | The planted corpus: 6,396 true positives and 374 false positives for 219 of gitleaks' rules, each with the secrets gitleaks' detector found in it | gitleaks' own rule sources at v8.30.1, run through gitleaks' config generator by `gitleaks/extract.sh`, under gitleaks' MIT License in `gitleaks/LICENSE`. See [gitleaks/README.md](gitleaks/README.md) |
| `presidio/` | 414 PII cases, valid numbers and near misses with a wrong checksum, plus the IBAN country table and three Verhoeff vectors | Presidio's recognizer tests and `iban_patterns.py` at 2.2.366, copied unchanged. See [presidio/README.md](presidio/README.md) |
| `code/` | 16 code-block collapse goldens: `<name>.md` in, `<name>.golden` out | Written by hand from CommonMark 0.31.2's fence rules, section 4.5, and the list-item and block-quote rules of sections 5.1 and 5.2 for a fence that follows a marker |

What the tests check:

- `TestGitleaksCorpus`: for every true positive, each rule finds exactly the secrets gitleaks found, and the whole pipeline leaves none of them in the text. No rule fires on its own false positives.
- `TestPresidioCases`: each recognizer returns exactly the spans Presidio's tests expect, valid numbers redacted and near misses kept. The one difference is deliberate and covers US SSNs only: a shape Presidio scores below 0.4 isn't redacted without a context word, and none of those test texts has one (`pkg/redact/doc.go`).
- `TestIBANFormatsMatchPresidio`: the Go IBAN table is Presidio's, entry for entry.
- `TestCodeBlockGoldens`: rendering each input with its code blocks collapsed gives its golden byte for byte.

Change these files only when the upstream pin moves (pkg/redact/rules/README.md), never to make a test pass. If a case looks wrong, follow the plan's errata rule.
