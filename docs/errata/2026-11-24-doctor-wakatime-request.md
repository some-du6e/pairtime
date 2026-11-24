# Erratum: DESIGN 4.3 lists everything that leaves a teen's machine, and has no row for `pairtime doctor`'s request to the WakaTime server

Date 2026-11-24. Raised by the verification of #306's fixes for #335 (WP-IN3). Status: decided 2026-11-24 by the orchestrator for Karim (D-doctor-wakatime-request): the row as proposed, with the api_url's user name and password in its last cell; it lands with doctor's wiring.

## Input

- DESIGN 4.3: "Everything that leaves a teen's machine is in this table. There is no telemetry or analytics channel." Its rows cover wakatime-cli's two destinations, `pairtimed`, the evidence store and GitHub Releases.
- DESIGN 5.9 has `pairtime doctor` show "both WakaTime destinations reachable", and #306 builds that check in `internal/agent/doctor/wakatime.go`. `GatherWakaTime` sends a `GET` of `<server>/users/current/statusbar/today` to the server the config's `api_url` names, or to WakaTime's own, `https://api.wakatime.com/api/v1`, when it names none. The request carries no key, no user name or password from the `api_url`, and no body, and follows no redirect. Doctor also asks the agent's endpoint, with pairtime's key, but over `127.0.0.1`, so that request stays on the machine.
- #335's ruling on its item 10 put a sentence in `internal/server/pages/privacy.md`: "`pairtime doctor` also asks the WakaTime server in your `~/.wakatime.cfg` whether it answers, with no key and none of your data."

## Expected

DESIGN 4.3's table has a row for every request that leaves a teen's machine, so doctor's request to the WakaTime server has one.

## Computed

It has none. Once doctor's WakaTime line is wired, each `pairtime doctor` run sends that request, from the teen's computer to a server the teen's config names. A row for it could read:

| From, to | When | Exactly what | Never in it |
|---|---|---|---|
| Client to the WakaTime server in the config, `pairtime doctor` | Each `pairtime doctor` run, once doctor's WakaTime line is wired | A `GET` of `<api_url>/users/current/statusbar/today`, with no key, no user name or password and no body, following no redirect; `<api_url>` is the config's, or WakaTime's own when it names none | The teen's key, heartbeats, anything else in the config |

## What #306 does meanwhile

- Doctor's WakaTime line isn't wired, so no `pairtime doctor` run sends the request yet. PLAN's WP-IN3 leaves the wiring to its next PR, after #222.
- `privacy.md` names the request already, as #335 ruled.
- DESIGN 4.3 stays as it is until doctor's wiring lands the row, as decided (DESIGN 6.6).
