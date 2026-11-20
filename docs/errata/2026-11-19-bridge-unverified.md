# Erratum: no `via` for a bridge record whose heartbeat failed the peer-process check

Date 2026-11-19. Raised by WP-EV1; the macOS default revised after the review of #4 (issue #17). Status: waiting for Karim. pkg/event implements the choice and the macOS default below until Karim decides.

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
| `bridge` | passed the peer-process check | C, unless keyboard input in the host window in the 10 s before the anchor makes it A (DESIGN 5.2, 6.4) | yes |
| `bridge_unverified` | failed it, or came from a Mac, where the check doesn't run | the same as `bridge` | never |

Both carry the `via_bridge` label, so the review page still shows that a minute rests on the bridge. The new value changes what D19 counts and nothing else.

## Reasoning

- Giving failed bridge records `editor_unverified` would call a prompt or tool record an editor heartbeat, and their minutes would lose `via_bridge`, the label E-6 adds so reviewers see the side door.
- `plugin` has the same label problem, and the records didn't come from a plugin.
- A separate flag such as `peer_verified` would be a thirteenth envelope field, unused by every kind except the two the bridge emits.
- Dropping the records at the bridge would treat them differently from failed editor heartbeats, which DESIGN keeps as `editor_unverified`.

## macOS, where the check doesn't run

DESIGN 5.7 skips the peer-process check on macOS, and WP-OS13 builds it for Windows and Linux only. DESIGN doesn't say which `via` records get there. D19 counts only bridge records "from heartbeats that passed the peer-process check", and on a Mac no heartbeat passes a check that never runs. Giving them `bridge`, on the view that a check that didn't run didn't fail, would reopen the pump above on every Mac.

Until Karim decides, the default is the one that keeps the pump closed. WP-AD16 sets `via` from the peer check and has no macOS rule of its own, so it takes this default:

| Record on macOS | `via` | Why |
|---|---|---|
| Bridge record | `bridge_unverified` | A script posting transcript-shaped heartbeats can't raise `ai_logged_s`. The cost is that honest bridge records from wakatime-cli on a Mac count toward none of `logged_s`, `ai_logged_s` and `hackatime_logged_s`. Their anchors keep the bridge level rule, and macOS's key-down idle time gives the keyboard evidence that can make them level A |
| Editor heartbeat | `editor` | DESIGN 5.7 skips the check because macOS gives keyboard evidence directly, and editor heartbeats aren't agent events, so they don't feed `ai_logged_s` |

## For Karim

1. Accept `bridge_unverified`, or pick another fix. Accepting it adds the value to E-2's list and to D19's sentence.
2. Which `via` should macOS records get while the peer-process check doesn't run there?
   - (a) The default above: `bridge_unverified` for bridge records, `editor` for editor heartbeats.
   - (b) `bridge` for bridge records too. Honest bridge records then count on a Mac, and so do a script's fakes, which raise the ceiling on the script's own level-C credit.
   - (c) Bring the check to macOS through `libproc` with purego, as DESIGN 5.7 allows. Both pairs of values then apply there as on Windows and Linux, and the question goes away.

   (a) and (b) both leave macOS editor heartbeats unchecked, and that needs a look too. A macOS editor heartbeat counts toward `logged_s` and D15's `hackatime_logged_s`. D15 splits `hackatime_logged_s` by the category each heartbeat carries, and a heartbeat posted straight to the local port can claim `ai coding`. If D15's AI part sets the level-C ceiling, a script on a Mac can raise that ceiling with editor heartbeats.
