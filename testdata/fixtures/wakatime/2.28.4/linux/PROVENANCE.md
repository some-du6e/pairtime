---
harness: wakatime
harness_version: 2.28.4
os: Ubuntu 26.04.1 LTS, kernel 7.0.0-1020-azure, GitHub Actions' ubuntu-latest runner
captured_on: 2026-11-23
captured_by: captured in CI, by e2e-wakatime.yml's capture job (run 45191513063 at c644e10e9a1e, the head of PR #228, which merged as d109ba5), not by a person
synthetic: false
scrubbed_by: nothing to replace, because the capture ran under the fixtures' own placeholders (home /home/capture, hostname capture-host) with two public test keys; every file read by an agent with the searches listed below, not read by a person
kit: tools/capture/wakatime/capture.py at c644e10e9a1e (git blob 4121d434dda6), run by .github/workflows/e2e-wakatime.yml at the same commit (git blob fd15e7f752f6)
script: the four runs in tools/capture/wakatime/README.md
otlp: off
runs: dual-send, replay, ai-sync, local-only, each wakatime-cli v2.28.4 from the release's wakatime-cli-linux-amd64.zip, SHA-256 e1dfa9fc4960cb0ebed90ee978c921b8ded9c61ffa63e750ddabd2fedb570345
---

# Fixture provenance

`wakatime` isn't a harness: it is wakatime-cli, the program every WakaTime editor plugin runs, and these are the requests it sent to the servers it reports to. The WakaTime bridge reads them (DESIGN 5.6, 5.7; docs/PLAN.md, WP-AD16).

## No person, no editor

Nobody typed anything. `capture.py` called the real wakatime-cli binary the way an editor plugin does, with the made-up plugin string `vscode/1.106.3 vscode-wakatime/25.5.1`. So wakatime-cli's own work is real: the request bodies, their order and batching, the headers, the offline queue, the rate limit, the backoff and the AI sync. What a plugin decides is not: when a heartbeat is sent, its line and cursor numbers, `human_line_changes` and `--write`. No interval between two heartbeats is a person's, and none of the editor heartbeats came from an editor. A test may use these files for what wakatime-cli sends, never as evidence of how a person works.

## The layout

Each run folder holds `capture.json`, with the `~/.wakatime.cfg` and `wakatime-internal.cfg` wakatime-cli read and every call (its arguments, stdin, exit code and the numbers of the requests it made), and `requests/`, with one `NNNNNN.json` per request body, byte for byte, and one `NNNNNN.meta` per request. The `.meta` keeps the values of six headers that carry no secret and only the names of the rest, and it says how the request authenticated and with which key. `tools/capture/wakatime/README.md` describes both files. `.gitattributes` keeps git from changing a byte of them.

Two recording servers took the requests: `server=pairtime` at `http://127.0.0.1:7247/api/v1`, a stand-in for the agent's local endpoint behind the `[api_urls]` line, and `server=hackatime` at `http://127.0.0.2:7248/api/hackatime/v1`, a stand-in for Hackatime as `api_url`. Both answered in Hackatime's shapes. Their keys, `00000000-0000-4000-8000-00000000a9e7` for pairtime and `00000000-0000-4000-8000-0000000048c1` for Hackatime, are test values of WakaTime's shape, written in `capture.py`. Every request carried its destination's key as HTTP Basic, the key alone.

## What each run holds

- `dual-send`: eight plugin calls under the Hackatime installer's `heartbeat_rate_limit_seconds = 30`, with AI sync started at the call's own time, as the installers seed it (HC 1.5). The first call sent at once. The next six fell inside the rate limit and waited in the offline queue. The last, 31 s later, sent its heartbeat with three extra heartbeats from stdin, then sent the six from the queue. So each server got 3 requests, with 1, 4 and 6 heartbeats. The heartbeats are navigation, typed edits, a save, a test file (`writing tests`), a doc (`writing docs`), `debugging`, and a non-file `app` heartbeat in `running tests`, whose `project` and `branch` are empty strings.
- `replay`: the stand-in Hackatime answered its first request 500 after the stand-in pairtime had taken the same heartbeat. wakatime-cli exited 102 and queued it; `--offline-count` then said 1. A failed call skips the queue sync wakatime-cli runs after each call, and the failure starts a 30 s backoff, so the capture waited 31 s. The next call sent its own heartbeat to both servers, then synced the queue to both, so pairtime's `000005.json` is byte for byte its `000001.json`: the same heartbeat a second time. That is the replay the endpoint's deduplication is for.
- `ai-sync`: WP-AD1's Claude Code 2.1.327 transcripts (`testdata/fixtures/claude_code/2.1.327/linux/cli`) in `CLAUDE_CONFIG_DIR`, the project folder they edit made again by `tools/capture/prepare.py` with the files the agent created, and no `ai_logs_last_parsed_at`, so the sync read every transcript line. One plugin call, whose own heartbeat is at the capture's time, added five extra heartbeats placed around the agent's first edit of `wordcount.py` at 2026-11-20T16:49:50.401Z: at +2 s, +30 s, +45 s (on `test_wordcount.py`), +200 s, and 31 minutes after the agent's last edit. wakatime-cli sent 26 heartbeats to pairtime in 3 requests (10, 10 and 6), the first 10 at once and the rest through its queue:
  - 21 transcript-derived ones, all with `ai_session` and category `ai coding`. 18 carry `ai_prompt_length`, 3 an `ai_line_changes` above 0 (all three also a prompt length), and 3 only token counts. Four of the 18 have no model token in their user agent (`go1.27.3 claude-code/2.1.327`): wakatime-cli made them from a prompt's own line, and the model is known only from the agent's lines after it. The rest carry one (`go1.27.3 opus/5-5 claude-code/2.1.327`). wakatime-cli moved the prompts of the main session onto the file that session last edited, which is how file heartbeats come to carry `ai_prompt_length`, and the other sessions' prompts stay `app` heartbeats on the entity `Claude <session id>`.
  - 5 editor heartbeats with the plugin's user agent and no `ai_session`. wakatime-cli dropped the one at +2 s as a duplicate of the agent's heartbeat on the same file within 5 s, and relabelled the ones at +30 s, +45 s and +200 s `ai coding`, though two of them carry `human_line_changes`. The one 31 minutes after and the call's own heartbeat kept their empty category.
- `local-only`: no other server. `api_url` is pairtime's, as DESIGN 5.6's installer writes it when there is no `~/.wakatime.cfg`. One heartbeat, then `--today` twice, which sent `GET /users/current/statusbar/today` with the same Basic auth and printed `1 min` and `{"text":"1 min","has_team_features":false}`.

## What wakatime-cli did that DESIGN doesn't say

- It posts every batch to `heartbeats.bulk`, at most 10 heartbeats a request, and never to `heartbeats`.
- It sends `user_agent` in each heartbeat, and the request's `User-Agent` header is the plugin's even when the batch holds transcript-derived heartbeats. Only the body tells them apart.
- DESIGN 5.7 marks a transcript-derived heartbeat by an AI model token in its user agent. Prompts have none here, which [the erratum](../../../../../docs/errata/2026-11-23-wakatime-cli-2.28.4.md) takes up.
- It sends `X-Machine-Name` from the config's `hostname` and `Timezone` from the runner's zone (`Etc/UTC`). Neither is in the body.

## The read

Every file here was read. The searches: for `runner`, any home folder but `/home/capture`, the runner's host name forms (`fv-az`, `runnervm`), `github`, GitHub token prefixes, `token=`, `password`, `secret`, email addresses and IPv4 addresses other than the two loopback servers. The only hits were the `ai_*_tokens` field names. The entities are the four projects' files under `/home/capture/pairtime-capture/projects/`, the `terminal` app and the three `Claude <session id>` apps, whose ids are AD1's, already public in its fixture. The user agents name wakatime-cli, the runner's kernel, Go 1.27.3, the made-up plugin and Claude Code 2.1.327. The project names are the four run folders, the branch is `master`, and `X-Machine-Name` is `capture-host` throughout. wakatime-cli's own log, which the artifact also holds, stayed out of the fixture.
