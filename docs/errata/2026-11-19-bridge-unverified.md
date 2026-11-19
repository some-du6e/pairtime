# Erratum: no `via` for a bridge record whose heartbeat failed the peer-process check

Date 2026-11-19. Raised by WP-EV1. Status: waiting for Karim. WP-EV1's PR implements the choice below until Karim decides.

## Input

DESIGN 6.2 D19 says `logged_s`, `ai_logged_s` and `hackatime_logged_s` "count only records with `via` in `hook`, `log`, `otel` or `editor`, plus bridge records from heartbeats that passed the peer-process check. `track`, `plugin` and `editor_unverified` records never count."

DESIGN 4.1 E-2 gives `via` eleven values: `hook`, `log`, `otel`, `os`, `git`, `shell`, `editor`, `editor_unverified`, `bridge`, `track` and `plugin`. Editor heartbeats get one value for each outcome of the peer-process check (DESIGN 5.7). Bridge records get one value for both.

## Expected

D19 needs the scorer to tell the two kinds of bridge record apart. It counts the ones whose heartbeat passed the check and never counts the ones whose heartbeat failed it.

## What E-2 gives

Both kinds arrive as `via = bridge`, and nothing else in the event says whether the check passed. The scorer can count both or neither.

Take a script on a Windows laptop that posts transcript-shaped heartbeats to the agent's local port, each with `ai_prompt_length > 0` and `ai_line_changes > 0`. The check fails, because the process on the other end of the connection isn't wakatime-cli. DESIGN 5.7 turns each heartbeat into a `prompt.submit` and an `agent.tool` with `via = bridge`. If the scorer counts `bridge`, the script raises `ai_logged_s`, and with it the `ai_logged / 3` ceiling on its own level-C credit. That is the pump D19 closes. If the scorer never counts `bridge`, it drops the honest bridge records from wakatime-cli that D19 counts.

## Choice

A twelfth value, `bridge_unverified`, next to `bridge`, the way E-2 pairs `editor` with `editor_unverified`. The enum lists it right after `bridge`.

| `via` | Heartbeat | Level | Counts toward `logged_s`, `ai_logged_s`, `hackatime_logged_s` |
|---|---|---|---|
| `bridge` | passed the peer-process check | C unless corroborated (DESIGN 5.2, 6.4) | yes |
| `bridge_unverified` | failed it | C unless corroborated | never |

Both carry the `via_bridge` label, so the review page still shows that a minute rests on the bridge. The new value changes what D19 counts and nothing else.

## Reasoning

- Giving failed bridge records `editor_unverified` would call a prompt or tool record an editor heartbeat, and their minutes would lose `via_bridge`, the label E-6 adds so reviewers see the side door.
- `plugin` has the same label problem, and the records didn't come from a plugin.
- A separate flag such as `peer_verified` would be a thirteenth envelope field, unused by every kind except the two the bridge emits.
- Dropping the records at the bridge would treat them differently from failed editor heartbeats, which DESIGN keeps as `editor_unverified`.

## For Karim

1. Accept `bridge_unverified`, or pick another fix. Accepting it adds the value to E-2's list and to D19's sentence.
2. macOS skips the peer-process check (DESIGN 5.7), and DESIGN doesn't say which `via` editor heartbeats get there. This erratum assumes `editor` for them and `bridge` for bridge records, since a check that didn't run didn't fail. If the check comes to macOS through libproc, both pairs of values apply there as on Windows and Linux.
