# Presidio's recognizer tests

These files are copied from [Presidio](https://github.com/data-privacy-stack/presidio) (the `microsoft/presidio` URL redirects there) at tag 2.2.366, commit `211afc337241aee8bf495f282e83ef639a5d015f`. `pkg/redact` ports Presidio's pattern recognizers to Go, and these are Presidio's own test cases for them, which is why they're an oracle.

| File here | Upstream path | Changes |
|---|---|---|
| `LICENSE` | `LICENSE` | None |
| `test_credit_card_recognizer.py.txt` | `presidio-analyzer/tests/test_credit_card_recognizer.py` | `.txt` added, so no Python tool collects it |
| `test_iban_recognizer.py.txt` | `presidio-analyzer/tests/test_iban_recognizer.py` | The same |
| `test_us_ssn_recognizer.py.txt` | `presidio-analyzer/tests/test_us_ssn_recognizer.py` | The same |
| `test_uk_nino_recognizer.py.txt` | `presidio-analyzer/tests/test_uk_nino_recognizer.py` | The same |
| `test_in_aadhaar_recognizer.py.txt` | `presidio-analyzer/tests/test_in_aadhaar_recognizer.py` | The same |
| `test_email_recognizer.py.txt` | `presidio-analyzer/tests/test_email_recognizer.py` | The same |
| `iban_patterns.py.txt` | `presidio-analyzer/presidio_analyzer/predefined_recognizers/generic/iban_patterns.py` | The same |

`SHA256SUMS` lists every file, in `sha256sum` format, and `TestPresidioFiles` checks it before the other tests use them. `TestPresidioCases` parses the `@pytest.mark.parametrize` list in each test file (strings, numbers and tuples, nothing else) and pins how many cases it found, so a parsing slip can't drop some. The recognizers they test are at these upstream paths, under `presidio-analyzer/presidio_analyzer/predefined_recognizers/`: `generic/credit_card_recognizer.py`, `generic/iban_recognizer.py`, `country_specific/us/us_ssn_recognizer.py`, `country_specific/uk/uk_nino_recognizer.py`, `country_specific/india/in_aadhaar_recognizer.py` and `generic/email_recognizer.py`.

To check the copy against upstream:

```sh
git clone https://github.com/data-privacy-stack/presidio && cd presidio
git checkout 211afc337241aee8bf495f282e83ef639a5d015f
cd presidio-analyzer
sha256sum ../LICENSE presidio_analyzer/predefined_recognizers/generic/iban_patterns.py \
  tests/test_{credit_card,email,iban,in_aadhaar,uk_nino,us_ssn}_recognizer.py
```

The files are under the MIT License, in `LICENSE`, copyright Presidio Contributors, and kept unmodified apart from the renames above.
