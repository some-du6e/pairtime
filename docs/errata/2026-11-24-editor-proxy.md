# Erratum: an editor's own proxy gets what wakatime-cli sends pairtime's agent, so DESIGN 4.3's "Nothing leaves the machine on this path" doesn't hold for it

Date 2026-11-24. Raised by the orchestrator's verification of #306's fixes for #335 (WP-IN3, the fourth item of #368), from wakatime-cli v2.28.4's source at `9fee1b464bfb` (the release's tag, BSD-3-Clause, read and not copied). Status: open, for the orchestrator to decide with WP-AD17's other gates, before the WakaTime item is offered.

## Input

- DESIGN 4.3's row for wakatime-cli to the local agent: "Each heartbeat | The full heartbeat, over `127.0.0.1` only | Nothing leaves the machine on this path".
- `internal/server/pages/privacy.md`, under "The short version": "Your prompts, your code, your file names and your window titles stay on your computer, unless you submit hours or publish a devlog." Its list under "What stays on your computer" has "your code, your file names and your folder paths". Each heartbeat names the file being edited, by its path, and its project.
- wakatime-cli v2.28.4's source:
  - `pkg/params/params.go`, `loadProxyURL` (lines 533-560): the proxy is the `--proxy` flag's, then `[settings] proxy`, and only when neither is set the environment's, worked out for the main `api_url` (`httpproxy.FromEnvironment`).
  - `pkg/api/option.go`, `WithProxy` (lines 110-154): `http.ProxyURL` sends every request through that one proxy, to every destination, `127.0.0.1` included, with no exception for loopback. A request through an `https://` proxy that fails to connect is sent again through the same proxy as `http://`.
- WakaTime's plugin for JetBrains IDEs passes wakatime-cli `--proxy` with the IDE's own proxy (`getBuiltinProxy` in jetbrains-wakatime's `WakaTime.java`) whenever the IDE's proxy is on for `api.wakatime.com`.
- Neither `pairtime install` nor `pairtime doctor` can see an editor's `--proxy`: it lives in the editor's settings, and the plugin passes it on each run of wakatime-cli.

## Expected

DESIGN 4.3: nothing leaves the machine on the path from wakatime-cli to the local agent.

## Computed

With an editor that passes `--proxy`, wakatime-cli sends pairtime's copy of every heartbeat to that proxy in plain HTTP, since pairtime's URL is `http://127.0.0.1:<port>/api/v1`: the file's path, its project and the rest of the heartbeat, with pairtime's key in its Basic auth header. A proxy on another machine connects to its own `127.0.0.1`, not the teen's, so pairtime's agent gets none of them, and each send that fails holds back what wakatime-cli sends the teen's own server too (docs/errata/2026-11-24-agent-down-holds-back-server.md).

- In a config pairtime made, the agent is the only `api_url`, so these copies are all wakatime-cli sends, and nothing would leave the machine without the proxy.
- In a config of the teen's with pairtime's line, the proxy also gets the copy for the teen's own server, which an `https://` server's TLS keeps from it. pairtime's copy goes to the proxy unencrypted.

The config can't prevent it: the flag comes before `[settings] proxy`, and pairtime's line has no proxy setting of its own.

DESIGN 4.3's last cell could read: "Nothing leaves the machine on this path, unless an editor passes wakatime-cli a proxy of its own (`--proxy`, as WakaTime's plugin for JetBrains IDEs does), which then gets each heartbeat in plain HTTP". `privacy.md` could say the same where it lists what stays on the computer.

## What #306 does meanwhile

- Both WakaTime consent cards carry the note that an editor's proxy would get pairtime's copy of each heartbeat unencrypted and pairtime none of them, so don't set a proxy for your editors while pairtime's line is in. The card for a config that is there already had it, with two clauses only a config of the teen's needs: a proxy the environment sets for the server's address, and the sends to that server a failed send holds back. The card for the config install makes has the rest since #368.
- `offerWakaTime` stays false, so no release makes or edits a WakaTime config, and no teen meets this before the decision.
- PLAN's WP-AD17 lists this erratum beside the agent-down erratum, as one to decide before the offer turns on.
- DESIGN 4.3 and `privacy.md` stay as they are until the decision (DESIGN 6.6).
