# wakatime-cli capture

`capture.py` records what the real wakatime-cli sends, for the WakaTime bridge's fixtures in `testdata/fixtures/wakatime/<version>/linux/` (docs/PLAN.md, WP-AD16; DESIGN 5.6, 5.7). It runs in CI, in `e2e-wakatime.yml`'s capture job, never on a person's machine: it needs `HOME` to be `/home/capture`, and it writes there.

## What it does

Two recording servers listen on loopback in the roles DESIGN 5.6 gives them: a stand-in for Hackatime at `http://127.0.0.2:7248/api/hackatime/v1`, which wakatime-cli has as its `api_url`, and a stand-in for the pairtime agent at `http://127.0.0.1:7247/api/v1`, which it reaches through the `[api_urls]` line pairtime's installer writes. wakatime-cli sends to each destination in the order of its `url|key` string, and these addresses keep the order a person's machine has, where `http://127.0.0.1` comes before `https://hackatime.hackclub.com`. Both answer in Hackatime's shapes: `201` with one `[row, 201]` per heartbeat for `heartbeats.bulk`, and `grand_total` for `statusbar/today`.

The capture then calls wakatime-cli the way an editor plugin does, with a made-up VS Code plugin string, in four runs:

| Run | What it records |
|---|---|
| `dual-send` | The Hackatime installer's settings plus pairtime's line: navigation, typed edits, a save, a test file, a doc, the `debugging` category, a non-file entity and a plugin's batch of extra heartbeats, with `heartbeat_rate_limit_seconds = 30`, so wakatime-cli queues and batches as it does for a person |
| `replay` | The stand-in Hackatime fails one batch after the stand-in pairtime took it. wakatime-cli queues the whole batch, and the next call after its 30 s backoff sends it to both again, which is what pairtime's deduplication is for |
| `ai-sync` | WP-AD1's Claude Code 2.1.327 transcripts in `CLAUDE_CONFIG_DIR`, with no `ai_logs_last_parsed_at`, and one plugin call with extra heartbeats around the agent's edits. The requests hold the transcript-derived heartbeats, editor heartbeats wakatime-cli relabels `ai coding`, and the one it drops as a duplicate of the agent's |
| `local-only` | No other server: `api_url` is pairtime's, as the installer writes it when there is no `~/.wakatime.cfg`, with a heartbeat and the status bar's `GET` of today |

The keys are fixed values of WakaTime's shape, written in `capture.py`, so nothing in the requests is a secret.

## What it writes

One folder per run:

```text
<run>/
  capture.json          the config files wakatime-cli read, and every call: its arguments, its stdin,
                        its exit code, and the numbers of the requests it made
  requests/NNNNNN.json  a request body, byte for byte as it arrived
  requests/NNNNNN.meta  the request: arrival time, which server took it, method, path and query
                        parameter names, the values of Content-Type, Accept, Accept-Encoding,
                        User-Agent, Timezone and X-Machine-Name, the names of every header, how it
                        authenticated and with which key, and the status it got back
  logs/wakatime.log     wakatime-cli's own log, for reading a failed run
SYSTEM.txt              the runner's OS, kernel and the wakatime-cli version
```

Header values other than those six are never written, as `otlp_sink.py` writes none, because headers can carry keys. `Authorization` is described by its scheme and by which of the two test keys it carries.

## From the artifact to a fixture

1. Download the run's `wakatime-capture` artifact: `gh run download <run-id> -n wakatime-capture -D <folder>`.
2. Copy each run folder into `testdata/fixtures/wakatime/<version>/linux/` without its `logs/`.
3. Read every file. The paths are under `/home/capture`, the hostname is `capture-host` and the keys are the two test values, so nothing should need a scrub, but the read is still what says so.
4. Write `PROVENANCE.md` from the template, with the run's id and commit, and open a pull request that adds only the fixture.
