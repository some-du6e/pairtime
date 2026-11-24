# Erratum: no `via` for a bridge record whose heartbeat failed the peer-process check

Date 2026-11-19. Raised by WP-EV1; the macOS default revised after the review of #4 (issue #17). Status: decided 2026-11-20 by the orchestrator for Karim: `bridge_unverified` is E-2's twelfth `via` value, and item 2 is (c). The peer-process check runs on macOS too, through `libproc` with purego, and on every OS a heartbeat that fails the check, or that the agent can't check, is `editor_unverified` or `bridge_unverified`. PLAN's WP-OS3, WP-OS13 and WP-AD16 took their wording in #111, and WP-OS13 built the macOS check in #202, which also dropped DESIGN 5.7 from internal/platform/darwin's Input Monitoring citations. A later WP-ERR1 docs PR applies the decision's DESIGN text outside 6.4. WP-AD16's agent wiring, which #236 lists, applies DESIGN 6.4 with the evidence page's lines and the `via` descriptions in pkg/event and the schema, unless D-macos-hid-posted's `pkg/score` PR lands first and carries them. The PRs that regenerate testdata/checks and testdata/timelines/6.1 fix the two 6.3-cc10 stories and 6.1's gen.py comment.

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
| `bridge_unverified` | failed it, or the agent couldn't run it | the same as `bridge` | never |

Both carry the `via_bridge` label, so the review page still shows that a minute rests on the bridge. The new value changes what D19 counts and nothing else.

## Reasoning

- Giving failed bridge records `editor_unverified` would call a prompt or tool record an editor heartbeat, and their minutes would lose `via_bridge`, the label E-6 adds so reviewers see the side door.
- `plugin` has the same label problem, and the records didn't come from a plugin.
- A separate flag such as `peer_verified` would be a thirteenth envelope field, unused by every kind except the two the bridge emits.
- Dropping the records at the bridge would treat them differently from failed editor heartbeats, which DESIGN keeps as `editor_unverified`.

## macOS, and heartbeats the agent can't check

WP-OS13 built the peer-process check for macOS in #202, through `libproc` with purego, as it does for Windows and Linux (DESIGN 5.7). On every OS a heartbeat is `editor`, and its bridge records `bridge`, only when the check ran and passed. A heartbeat that failed it, or that the agent couldn't check, is `editor_unverified`, and its bridge records `bridge_unverified`.

## For Karim

1. Accept `bridge_unverified`, or pick another fix. Accepting it adds the value to E-2's list and to D19's sentence.
2. Which `via` should macOS records get while the peer-process check doesn't run there?
   - (a) The interim default this erratum set until the decision: `bridge_unverified` for bridge records, `editor` for editor heartbeats.
   - (b) `bridge` for bridge records too. Honest bridge records then count on a Mac, and so do a script's fakes, which raise the ceiling on the script's own level-C credit.
   - (c) Bring the check to macOS through `libproc` with purego, as DESIGN 5.7 allows. Both pairs of values then apply there as on Windows and Linux, and the question goes away.

   (a) and (b) both leave macOS editor heartbeats unchecked, and that needs a look too. A macOS editor heartbeat counts toward `logged_s` and D15's `hackatime_logged_s`. D15 splits `hackatime_logged_s` by the category each heartbeat carries, and a heartbeat posted straight to the local port can claim `ai coding`. If D15's AI part sets the level-C ceiling, a script on a Mac can raise that ceiling with editor heartbeats.

   Under (a) and (b), unchecked macOS editor heartbeats also meet DESIGN 6.4's editor-plus-commit pair by `via` alone, so the corroborated band, the only band a program preset may pre-approve, can rest on them. Accepting (a) or (b) therefore needs two DESIGN edits as well: 5.2's editor sentence gets a macOS exception, and 6.4's "an editor heartbeat that passed the peer-process check" pair says what stands in for the check on macOS (for example, key-down input in the editor's window). Under (c), neither edit is needed.

## Decision

Item 1: `bridge_unverified`, as above. Item 2: (c).

(a) is dropped because macOS's key-down idle time backs typed edits only. SM 2.3's editor navigation needs only input in its slot, and `pkg/score` gives a keyless `editor` anchor level B. So on a Mac a jiggler and a script posting `navigate` heartbeats would earn level B, where DESIGN 7.12 gives a script posting to the port 0 submittable seconds. A key-down that another process posts also resets key-down idle (#75, internal/platform/darwin/doc.go), so key-down idle can't stand in for the check on typed edits either. Unchecked heartbeats would also count toward D19's totals, feed D15's `ai coding` split and meet 6.4's editor-plus-commit pair. (b) adds the bridge pump on top.

(c) leaves no OS exception, so DESIGN 5.2's editor sentence and 6.4's pair hold as written. The cost falls on a wakatime-cli that no listed editor started. Its heartbeats get `editor_unverified` or `bridge_unverified` even when they are honest.
