# Ledger vectors

Test data for the ledger format in [docs/spec/ledger-v1.md](../../docs/spec/ledger-v1.md). Everything here is an oracle under the plan's ground rule 5. A PR that changes these files along with the code they check needs the `oracle-change` label.

| File | What it is | Where it comes from |
|---|---|---|
| `vectors.json` | Keys, digests, two device chains (54 batches, 1,529 records) and three day trees, in the format of spec section 11.3 | `python3 -I tools/ledger_ref.py vectors`, from fixed seeds |
| `rfc5869.json` | HKDF-SHA256 test cases 1 to 3 | RFC 5869 appendix A, extracted from the RFC text by script |
| `rfc4231.json` | HMAC-SHA256 test cases 1 to 7 | RFC 4231 section 4, extracted from the RFC text by script |
| `ct/` | RFC 6962 and RFC 9162 Merkle roots, subtree hashes and 98 inclusion-proof checks | transparency-dev/merkle at one pinned commit, see [ct/README.md](ct/README.md) |

`python3 -I tools/ledger_ref.py selftest` checks all of it, and `ci.yml` runs that on every PR. It fails when `vectors.json` isn't byte for byte what `vectors` writes, so never edit the file by hand. Change `tools/ledger_ref.py` and regenerate it.

What the vectors cover is in spec section 11.4. Two things to know before using them:

- The keys are test keys. They're published here, so nothing real may ever use them.
- The bodies are stand-ins. Each has the real envelope with its record's real nonce, `{"n":"<nonce>","e":{"stand_in":"<record type>","seq":<s>}}`, but no real event. Hashing treats a body as opaque bytes, so a stand-in exercises the same code a real body does. Tests of the canonical body writer can't use these bodies, because Python wrote them, not `encoding/json`.

`pkg/ledger`'s tests compute every value in `vectors.json` from the inputs next to it and compare. They also write their own random batches in the same format and run `python3 -I tools/ledger_ref.py check <file>` on them (WP-LG2).
