# Side door (a): a track loop and a jiggler on Windows

The trust judge's attack on adoption's D3, run against this design (JT, Adoption-first, What breaks). A Windows 10 laptop without the typing module, a USB jiggler, Windows Terminal in front with a Goose session in it, and a loop that asks an LLM for a plausible line and passes it to `pairtime track`. No agent works and nothing else of the session reaches pairtime. The loop gets 387 s credited, all level C, and nothing a program could take.

| | |
|---|---|
| Plan | WP-FX4 (a) |
| Rules | DESIGN 5.2, 5.4, 6.2 D3 and D19, 6.4 |
| Date and zone | Wednesday 2026-11-25, America/Chicago, UTC-06:00. 14:00 local is 20:00Z |
| OS and evidence | Windows 10, `idle_keys_s` null in every sample. Every credited second is level C. Witnessed |
| Window | 14:00:00 to 14:09:00 |

## Events

- `os.focus` at 13:58:00: Windows Terminal, hosting the Goose session, `focus_project` the project.
- `os.presence` on every :00, :15, :30 and :45, and at each track call, which takes the hook path (DESIGN 3.2).
- Ten `cli.track` records, `via` `track`, from 14:04:00 to 14:07:09, 21 s apart. Each has `text_len` 90 and `log_match` `no_reader`, harness `other`, and the session and project from the call's process chain (DESIGN 4.1 E-8). The labels are local, so they are in `prompts.json`, not the events.

## Timing choices

- Goose stands in for any agent v1 has no hooks or log reader for; its `sessions.db` reader is v1.5 (DESIGN 5.1). The agent knows its executable, so the focus walk lists the session and each call's chain resolves to it.
- The loop runs an LLM call and `pairtime track` in 1 s, then sleeps 20 s, so calls come 21 s apart. Each is more than `TRACK_MIN_GAP_S` after the previous anchor, so condition 3 never stops one. At exactly 20 s the rule wouldn't say whether the previous anchor falls "in the previous 20 s".
- Every label has 90 characters, so every anchor's typing part is 90 / 1.5 = 60 s and its lead 180 s. None is a go-ahead or holds an error, so all are substantive.
- The jiggler moves at 13:58:00.5 and every 12 s after, so every slot has input.

## How the numbers follow

Each call meets DESIGN 5.4's four conditions. The chain reaches the session the focused terminal hosts, the jiggler gave input in the 30 s before, the previous anchor is 21 s back, and no hook or log reader sees the session. So each call opens an anchor. No key was pressed in the 10 s before it, so the anchor is level C, and it carries `via_track` (DESIGN 5.4, 6.4).

A track anchor's typing part is realized only in slots with `input_keys`, which this machine never reports. Each anchor credits only its 120 s think part, from 180 s to 60 s before its call. With calls 21 s apart the think parts join into one stretch, from 14:01:00, 180 s before the first call, to 14:06:09, 60 s before the last: 309 s of `P`.

Each call also resets the session's reading bucket to the 20 s base and adds nothing (DESIGN 5.4). The bucket drains only where no anchor covers a second, which here means after 14:06:09:

| From | To | Drains | Left |
|---|---|---|---|
| 14:06:09 | 14:06:27 | 18 s of the 14:06:06 call's base | 2 s, dropped by the next reset |
| 14:06:27 | 14:06:47 | 20 s of the 14:06:27 call's | none, so 14:06:47 is `X.no_anchor` |
| 14:06:48 | 14:07:08 | 20 s of the 14:06:48 call's | none, so 14:07:08 is `X.no_anchor` |
| 14:07:09 | 14:07:29 | 20 s of the 14:07:09 call's | none |

That is 78 s of `READ`, and 309 + 78 = 387 s. No record counts toward `logged_s` or `ai_logged_s`: the `cli.track` records never do (D19), and there is nothing else. So `level_c_cap_s` is min(387, 0 / 3) = 0, and the loop has 0 submittable seconds under any program policy, as DESIGN 5.2 requires.

## Errata

[Turns without a stop](../../../../docs/errata/2026-11-19-turns-without-a-stop.md). A track turn has no agent record, so nothing says whether it is running. The fixture takes the proposed rule: it isn't. That makes the 78 s `READ` rather than `SUP`, the idle seconds after 14:06:09 `X.no_anchor` rather than `X.agent_only`, and leaves 14:08 without a bucket. Rows 14:06 to 14:08 name it. The 387 s and their level don't depend on it.
