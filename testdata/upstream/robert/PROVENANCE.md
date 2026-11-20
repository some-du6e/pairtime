---
method: reconstructed
captured_on: 2026-11-19
captured_by: WP-PG0
source: https://github.com/hackclub/the-carnival/tree/de82efcca094c9726ce773dd54a463f929219cbc MIT
source: https://github.com/hackclub/pixl/tree/f7e9c2d241a1fd9eceacb2ae609d3b8962daf343 MIT
source: https://github.com/hackclub/trmnt/tree/73a99ee0c8dae3794403c2463854eaa2d160e4c8 none
source: https://github.com/hackclub/Alchemize/tree/eafb919fe5ac0eaf9858f5d811812ff32cca4f60 none
---

# Robert, reconstructed from four clients

Robert is Telescreen's review desk for YSWS submissions (HC 3.5). Its documentation at `https://telescreen.hackclub.com/robert/docs` needs a Hack Club sign-in, and its source isn't public. Everything here is therefore reconstructed from public code that calls it. Two of the four clients are MIT and two have no license, so the whole directory is in our own words, with each fact cited to the client file and lines that show it. Where the clients disagree, `api.json` lists both readings under `disagreements` instead of picking one.

| File | What it is |
|---|---|
| `api.json` | The six endpoints, the project object, auth, errors, states and score bands |
| `webhook.json` | The signature scheme, the two webhook payloads, delivery and retry behaviour, and test vectors |

WP-PG7 writes a verifier from `webhook.json`'s `scheme` and runs `signing_vectors` and `verify_cases` through it. pairtime's own webhooks sign the same way with `X-Pairtime-Signature` and `X-Pairtime-Timestamp` (SP 10.2), so program authors can reuse their Robert code. WP-PG8's Robert note and the `kind: "pairtime"` proposal start from `api.json`.

## Files read

Fetched with `gh api -H "Accept: application/vnd.github.raw" "repos/hackclub/<repo>/contents/<path>?ref=<commit>"` on 2026-11-19, at the commits in the front matter, which are the ones HC 0 re-read. Blob ids are `git hash-object` of what came back.

| Repo | File | Git blob |
|---|---|---|
| hackclub/the-carnival | `src/lib/telescreen/client.ts` | `1e47b1fb5547` |
| hackclub/the-carnival | `src/lib/telescreen/shared.ts` | `26a3b457ef93` |
| hackclub/the-carnival | `src/lib/telescreen/webhook-signature.ts` | `35fc23af0f2d` |
| hackclub/the-carnival | `src/app/api/webhooks/telescreen/route.ts` | `887f8120778b` |
| hackclub/pixl | `apps/internal-dashboard/lib/robert.ts` | `8bf1d00d497a` |
| hackclub/pixl | `apps/internal-dashboard/app/api/robert/outcome/route.ts` | `5ec56dda6035` |
| hackclub/trmnt | `src/src/lib/server/robert.ts` | `ba7d1d80d920` |
| hackclub/Alchemize | `src/lib/server/telescreenClient.ts` | `c7522f20a34f` |
| hackclub/Alchemize | `src/routes/api/telescreen/webhook/+server.ts` | `6c04c22de21b` |

## The test vectors

The vectors aren't captured traffic. They were computed on 2026-11-19 from the scheme as `webhook.json` writes it down, with Python's `hmac`, and the first was checked against `openssl dgst -sha256 -hmac`. The secret is a made-up string without Robert's `whsec_` prefix, and the bodies use made-up UUIDs. `check.py` recomputes every signature and replays every case on each CI run. Because the vectors and the scheme come from the same reading of the clients, they share any misreading of them: they test that a verifier follows the scheme as reconstructed, not that Robert sends exactly this.

The cases pin the edges where verifiers usually go wrong: the 300 s window in both directions with a clock that has fractions, uppercase hex, a missing `sha256=` prefix, a timestamp with a fraction or in milliseconds, one changed byte, another secret, missing headers and an empty secret.
