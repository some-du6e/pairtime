# Erratum: while pairtime's agent doesn't take what wakatime-cli v2.28.4 sends it, wakatime-cli holds back what it sends the teen's server too

Date 2026-11-24. Raised by the review of #306 (WP-IN3), from wakatime-cli v2.28.4's source at `9fee1b464bfb` (the release's tag, BSD-3-Clause, read and not copied) and the requests in `testdata/fixtures/wakatime/2.28.4/linux`. Status: open, for the orchestrator to decide before WP-AD17 starts.

## Input

- DESIGN 5.6, "Agent down": "with the agent stopped, the fake Hackatime still receives and wakatime-cli exits without an auth error". PLAN's WP-AD17 builds its gate on the same: "with the agent stopped, the fake Hackatime still receives".
- `internal/server/pages/privacy.md`: "If you also use Hackatime, your editor keeps sending Hackatime what it sent before. pairtime adds nothing to it and removes nothing from it."
- Erratum 2026-11-23 takes the same as given: DESIGN 5.6 covers the agent being down, "when Hackatime still gets every heartbeat", and keeps Hackatime "independent of" pairtime's agent. Its item 4 decided only the other direction, Hackatime out of reach.
- wakatime-cli v2.28.4's source:
  - `pkg/apikey/apikey.go`, `WithReplacing` (lines 58-98): each heartbeat gets one copy per destination, the `api_url`'s with its key (`DefaultAPIURL|api_key`), and one for each `[api_urls]` pattern that matches, with that line's URL and key.
  - `pkg/api/heartbeat.go`, `SendHeartbeats` (lines 25-51): it groups the copies by `APIURL|APIKey` (`groupByAPIURLAndKey`, lines 253-263), sorts the groups' keys with `slices.Sort` (`sortKeys`, lines 265-277), sends the groups in that order, and returns `nil, err` at the first group that fails, so no group after it is sent.
  - `pkg/offline/offline.go`, `WithQueue` (lines 56-81): on that error it queues every heartbeat of the call, the server's copies included, and returns the error.
  - `pkg/backoff/backoff.go`, `WithBackoff` (lines 41-75): a failed call holds back the next calls for every destination (erratum 2026-11-23, item 4).
  - `cmd/heartbeat/heartbeat.go` (lines 143-145) adds `WithQueue` only while the offline queue is on, and `pkg/params/params.go`'s `LoadOfflineParams` (lines 932-937) turns it off for `--disable-offline` or `[settings] offline = false`.
- pairtime's line is `http://127.0.0.1:<port>/api/v1|<key>`. It sorts before every `https://` URL, since `:` (0x3A) comes before `s` (0x73), so before Hackatime's and WakaTime's own, and no `http://` URL sorts after an `https://` one, so no spelling of pairtime's line changes that. Among `http://` servers it sorts before some, such as the capture's stand-in Hackatime at `http://127.0.0.2:7248`, and after others: `http://127.0.0.1` on a port whose digits sort lower, such as a Wakapi on `http://127.0.0.1:3000`, and `http://127.0.0.10`, `http://10.0.0.5` or `http://100.64.0.1`.
- The capture's `replay` run (`testdata/fixtures/wakatime/2.28.4/linux/PROVENANCE.md`): the stand-in Hackatime answered its first request 500 after the stand-in pairtime had taken the same heartbeat; wakatime-cli exited 102 and queued the batch, and a failed call skips the queue sync and starts the backoff. That run has the server's group fail, after pairtime's. The source above gives the same for pairtime's group failing first, and then the server's group isn't sent at all.

## Expected

DESIGN 5.6: with the agent stopped, the teen's server, such as Hackatime, still gets every heartbeat, and wakatime-cli exits without an auth error.

## Computed

While pairtime's agent doesn't take a send, every wakatime-cli call fails at pairtime's group:

- the agent isn't running or has crashed, and the hook binary hasn't started it again yet;
- the agent listens on a port the line no longer names, until it rewrites the line (DESIGN 5.6, "The port"; PLAN, WP-AD16's "Left by WP-IN3");
- something else answers on that port, such as another account's agent, which refuses the key with a 401.

A teen's server whose group sorts after pairtime's, as every `https://` one does, Hackatime's and WakaTime's own included, is then never sent to. One that sorts first, such as a Wakapi on `http://127.0.0.1:3000`, gets that call's heartbeats, but the call fails all the same. Either way the call queues the whole batch, to send again, and backs off every destination, so later calls hold back every server until the agent takes the heartbeats again and the backoff lets a call through. With the offline queue off, nothing is queued: a server that sorts after pairtime's loses the heartbeats of every call that fails at pairtime's group for good, and every server loses those of the calls the backoff stops. The card's "and sends it once the agent takes it again" (below) holds only while the queue is on, so the decision may also want install to refuse, or warn about, `offline = false`, as it refuses a proxy. A line that stays after pairtime is gone, such as a binary deleted or quarantined by antivirus (DESIGN 11, risk 18), holds the server back for good. That is DESIGN 5.6's "pairtime broke my Hackatime" risk.

WP-AD17's gate can't settle it as written. Against a fake Hackatime whose URL sorts after the agent's, as `https://` Hackatime's does, "with the agent stopped, the fake Hackatime still receives" can never pass. Against one that sorts first, such as `http://127.0.0.1:18080`, it passes and proves nothing about Hackatime. Whatever the decision, AD17's fake Hackatime has to sort after the agent's URL, as `https://` Hackatime does.

## What #306 does meanwhile

- The consent card says what happens: "While pairtime's agent doesn't take what wakatime-cli sends it, as while the agent isn't running, wakatime-cli holds back what it sends your server too, and sends it once the agent takes it again."
- `offerWakaTime` stays false, so no release adds pairtime's line.
- PLAN's WP-AD17 says the orchestrator rules on this erratum before that WP starts.
- DESIGN 5.6, PLAN's WP-AD17 gate and `privacy.md` stay as they are until the decision (DESIGN 6.6).
