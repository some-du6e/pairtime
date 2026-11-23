# Erratum: what wakatime-cli v2.28.4 sends, against DESIGN 5.6 and 5.7

Date 2026-11-23. Raised by WP-AD16, from wakatime-cli v2.28.4's source at `9fee1b464bfb` (the release's tag, BSD-3-Clause, read and not copied) and the requests in `testdata/fixtures/wakatime/2.28.4/linux`. Status: item 2 decided 2026-11-23 by the orchestrator for Karim. Item 1 is waiting for Karim, and the bridge uses the rule below until he decides, which the orchestrator agreed to. Item 3 is a correction for WP-AD17 to confirm, and DESIGN 5.6 stays as it is until then.

## 1. Which heartbeats are transcript-derived

### Input

DESIGN 5.7's table tells an editor heartbeat from a transcript-derived one by the user agent: "Editor heartbeat (plugin user agent, no AI model token)" against "Transcript-derived heartbeat (user agent carries an AI model token)". HC 1.5 says the same of wakatime-cli: "with an AI model token inserted for transcript-derived heartbeats".

### What the capture shows

The `ai-sync` run's requests to pairtime hold 21 heartbeats that wakatime-cli's AI sync made from WP-AD1's Claude Code transcripts. 18 of them carry `ai_prompt_length`, and four of those have no model token: their user agent ends `go1.27.3 claude-code/2.1.327`, where the others end `go1.27.3 opus/5-5 claude-code/2.1.327`. Three are `app` heartbeats on the entity `Claude <session id>` and one is a file heartbeat. wakatime-cli builds the token from the model a transcript line names (its `pkg/ai/claude.go`, `userAgent`, and `pkg/ai/ai.go`, `aiModelUserAgentToken`), and a person's prompt line names none.

Read literally, DESIGN 5.7 makes those four editor heartbeats: an `editor.activity` `navigate` each, and no `prompt.submit`. The bridge would lose the four prompts and give their seconds to an editor nobody used.

### The rule the bridge uses meanwhile

A heartbeat is transcript-derived when it carries `ai_session`.

- Every heartbeat wakatime-cli's AI sync makes carries it. All 21 in the capture do, and every parser in v2.28.4's `pkg/ai` sets it, the Claude parser from the transcript's file name.
- A plugin can't set it through wakatime-cli. It is no command-line flag, and `--extra-heartbeats` reads no such field (`pkg/params/params.go`, `ExtraHeartbeat`).
- The editor heartbeats wakatime-cli relabels `ai coding` keep the plugin's user agent and carry no `ai_session`, three of them in the same run. So they stay editor heartbeats, as DESIGN 5.7 wants, since it takes the category for untrusted input.
- A process that posts straight to the agent's port can put `ai_session` on anything. The rule decides only which records a heartbeat makes. Its `via` comes from the peer-process check, which such a process fails, so its records are `editor_unverified` or `bridge_unverified` either way (DESIGN 5.7; docs/errata/2026-11-19-bridge-unverified.md).

### Proposed DESIGN 5.7 text

The table's first column, in the first and third rows:

> Editor heartbeat (no `ai_session`) with `human_line_changes > 0` or `is_write`

> Transcript-derived heartbeat (wakatime-cli's AI sync made it: it carries `ai_session`, and its user agent names the AI agent, such as `claude-code/2.1.327`, with a model token when the transcript line named a model) with `ai_prompt_length > 0`

and the second and fourth rows read "Other editor heartbeats" and "Transcript-derived heartbeat with `ai_line_changes > 0`" as now. One heartbeat can match both transcript rows: wakatime-cli moves a session's prompts onto the file the session last edited, and three heartbeats in the capture carry `ai_prompt_length` and `ai_line_changes` together. Such a heartbeat becomes both records.

## 2. The status of a heartbeat the local endpoint refuses

### Input

DESIGN 5.6: the local endpoint answers `heartbeats.bulk` with "per-item responses in Hackatime's shape". Hackatime answers a heartbeat it refuses with `[{"error": ..., "type": ...}, 422]` (`testdata/upstream/hackatime/responses/heartbeats_bulk.schema.json`).

### What wakatime-cli does with a refusal

`pkg/offline/offline.go`, `handleResults`: an entry with status 400 drops its heartbeat. Any other status outside 2xx, and a heartbeat the response has no entry for, go back into the offline queue, and the sync stops there, so the heartbeats queued behind them wait for the next one. The queue stores a heartbeat as its JSON, in which the destination's URL and key are left out (`pkg/heartbeat`, `APIURL` and `APIKey` are `json:"-"`), so a queued heartbeat goes to every destination when it is sent again. A heartbeat pairtime refused with 422 would go back to Hackatime too, at every sync, for as long as it waits (item 3).

### Decision

Decided 2026-11-23 by the orchestrator for Karim. The local endpoint answers a heartbeat it refuses with Hackatime's body, `{"error": ..., "type": ...}`, and status 400 instead of 422. This is a deliberate departure from Hackatime's status code, so that nothing pairtime refuses goes back into wakatime-cli's queue, where it would reach Hackatime again (DESIGN 5.6, 11 risk 1). A heartbeat the endpoint takes, a duplicate of one it took included, gets Hackatime's row shape and 201, as Hackatime gives both.

## 3. How long wakatime-cli keeps a heartbeat it couldn't send

### Input

DESIGN 5.6, Agent down: "wakatime-cli requeues a heartbeat at most 3 times (HC 1.5), and what it does with a refusing destination isn't documented." HC 1.5 says "with up to 3 requeue attempts per heartbeat (`maxRequeueAttempts`)".

### What the source says

In `pkg/offline/offline.go`, `maxRequeueAttempts = 3` is how many times `pushHeartbeatsWithRetry` tries to write heartbeats to the queue file, waiting longer between tries, before it gives up on writing them. It counts writes, not sends. Nothing in that file drops a queued heartbeat for its age or for how often it was sent. The capture's `replay` run shows a heartbeat queued after one destination failed and sent again 31 s later, once the backoff ended, to both destinations. So a heartbeat waits in the queue until a send succeeds, and a refusing destination makes it go out again to every destination at each sync.

### For WP-AD17

Confirm it against the real binary in `e2e-wakatime.yml`'s end-to-end job: a heartbeat sent while one destination refuses for longer than three syncs still reaches both once that destination answers. If it does, DESIGN 5.6's sentence becomes:

> A heartbeat wakatime-cli couldn't send waits in its offline queue until a send succeeds, and goes out again to every destination at each sync (wakatime-cli's `pkg/offline/offline.go`).

HC 1.5's sentence is in docs/research, an oracle, and gets the same correction in the same PR.

## For Karim

1. Item 1: accept `ai_session` as what marks a transcript-derived heartbeat, with DESIGN 5.7's rows as proposed, or pick another rule.
