# Side door (e): bridge records of 5,000-line agent edits

An agent pairtime has no adapter for runs as a VS Code extension, and wakatime-cli's AI sync reads its transcript. Three times, the transcript holds a 90-character prompt and, 3 s later, an edit of 5,000 changed lines. Whether the edits are real or the transcript forged, the records come from the real wakatime-cli, spawned by the editor, so they pass the peer-process check and arrive `via` `bridge`. A jiggler keeps presence on a Windows laptop. Each prompt opens a level-C anchor with the typing lead only, and each turn fills the bucket by its 20 s base and nothing more. Uncapped, one such edit would fill 37,500 s.

Unlike (a), (c) and (d), these records count toward `logged_s` and `ai_logged_s` (D19), because their heartbeats passed the check.

| | |
|---|---|
| Plan | WP-FX4 (e) |
| Rules | DESIGN 5.2, 5.7, 6.2 D2, D4 and D19, 6.4 |
| Date and zone | Wednesday 2026-11-25, America/Chicago, UTC-06:00 |
| OS and evidence | Windows 10, `idle_keys_s` null. Every credited second is level C. Witnessed |
| Window | 14:01:00 to 14:09:00 |

## Events

- `os.focus` at 13:58:00: VS Code, `focus_project` the project. The agent runs inside VS Code, so SM 2.2 rule 6's focus test is the editor window's.
- `os.presence` every 15 s. Bridge records arrive over HTTP and take no sample.
- At 14:03:03, 14:05:03 and 14:07:03, a `prompt.submit` from the bridge with `len_chars` 90 and no text. At :06 in each of those minutes, an `agent.tool` edit from the bridge with 3,800 lines added and 1,200 removed, each turn in a different file, and the WakaTime plugin's own heartbeat for that file, `via` `editor`, `edit_source` `agent`.

## Timing choices

- The agent's write changes the open file, so the plugin sends a heartbeat and runs wakatime-cli, whose AI sync sends the transcript's prompt and edit first. The bridge keeps each heartbeat's time, which here is the prompt's own (DESIGN 5.7). wakatime-cli can also stamp a prompt with an earlier transcript entry's time, or carry it on the edit's heartbeat, which this case leaves out ([bridge prompt times](../../../../docs/errata/2026-11-23-bridge-prompt-times.md)). The prompt at :03 and the sync at :06 fall between two presence samples, so nothing else is recorded in between and arrival order stays time order. A later sync would deliver the prompt late, which the pending window handles (DESIGN 3.7); that isn't this case.
- The plugin's heartbeat shares the edit's millisecond. Within 120 s of an agent edit of the same file, the bridge marks it the agent's (DESIGN 5.7), and an agent's edit opens no anchor.
- The jiggler moves at 14:02:59, 14:04:59 and 14:06:59 among other times, and the samples at :00 show it: the input in the 10 s before each prompt.
- 90 characters gives a 60 s typing lead. The edit 3 s after the prompt makes `ai_logged_s` 243 s, a multiple of 3.
- Each turn edits a different file, so SM 4.2's rewrite-loop rule finds no line that two turns both changed, whatever it makes of bridge edits, which carry no hunk ranges. The re-roll and fix-it rules need text, which bridge prompts don't have (D4). No dark-flow rule applies.

## How the numbers follow

A bridge prompt carries no text, only `ai_prompt_length`. With 90 characters it is substantive, since every go-ahead on SM 2.2's list is shorter (AD D2). Rule 6 holds: the jiggler gave input in the 10 s before it, and the editor that hosts the extension has focus. D4 gives it the typing lead only, 90 / 1.5 = 60 s, and on this machine that needs presence only (SM 2.3). It is a bridge anchor with no keyboard input, and no pairtime log reader covers this harness, so it is level C and carries `via_bridge` (DESIGN 5.2, 6.4).

Each turn credits its 60 s lead and its 20 s base. The 5,000 lines add nothing, because the bridge's lines fill a turn only up to `R_TURN_BASE_S` (DESIGN 5.2, 5.7) and the base already reaches it. Three turns give 3 × (60 + 20) = 240 s. Between a base and the next lead, 40 s go uncredited.

`logged_s` counts the prompt, the edit and the plugin's heartbeat, and `ai_logged_s` counts the prompt and the edit. In arrival order the counted records add 0, 3, 0, 117, 3, 0, 117, 3 and 0 s, so both totals come to 243 s. `level_c_cap_s` is min(240, 243 / 3) = 81 s. That is the most a program that opts in to level C could take, and only inside a commit interval whose closing commit is forge-verified (DESIGN 5.2, D5). This case has no commit.

## Errata

[Turns without a stop](../../../../docs/errata/2026-11-19-turns-without-a-stop.md). No `agent.stop` says when a bridge turn ends. Under the proposed rule it runs to the latest agent record the scorer has received for it, the edit at :06, which arrives long before any minute it touches is final. So each base drains as 3 s of `SUP` and 17 s of `READ`, `agent_running_s` is 3 in each turn's minute, and the idle seconds after it are `X.no_anchor`. Rows 14:03 to 14:08 name it. The 240 s, their level, the fills and both logged totals don't depend on it.
