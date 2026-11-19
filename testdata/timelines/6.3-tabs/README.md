# SM 6.3 with A and B in two tabs of one iTerm window

The variant in SM 6.3's second-to-last paragraph: the same afternoon as [6.3](../6.3/), but A's Claude Code session and B's Codex session run in two tabs of one iTerm window, so the OS can't see Sam switch between them. SM says the look-ahead rule (SM 3.2) gives 14:01:30-14:02:00 to B, 30 s move from A to B, and the total doesn't change: A 480, B 480, E 540, D 300.

| | |
|---|---|
| SM rows | The paragraph on two tabs, applied to every row of 6.3 |
| Date, zone, OS, evidence | As 6.3: Saturday 2026-11-28, America/Los_Angeles, UTC-08:00, macOS, level A, witnessed |
| Window | 14:00:00 to 14:30:00, as 6.3 |
| Files | `gen.py` calls `../6.3/afternoon.py` with `tabs=True` |
| Erratum | [2026-11-19-sm-6.3-two-tabs](../../../docs/errata/2026-11-19-sm-6.3-two-tabs.md): under SM 3.2's own rule, two more stretches change, so the rules give A 390, B 390 and 180 s `X.ambiguous`. The rows are SM's until Karim decides |

## What differs from 6.3

Only `os.focus`. The input, the harness events, the editor heartbeats and the commit are the same events at the same times, with different `event_id`s and `seq` numbers because two focus events are missing.

- No focus event at 14:02:00.2 or 14:20:30.5. A click on a tab and Cmd-` between tabs are the same input as the switches between windows in 6.3, but the window stays the same.
- iTerm's `os.focus` lists every session it hosts. From 13:59:05, when A's session starts in tab 1, that is both A's and B's, and the event has no `focus_project`, because a window hosting several sessions resolves to none (SM 3.2).
- A's agent-only time is 600 s instead of 570, because SM's paragraph gives 14:01:30-14:02:00 to B while A's agent runs.

## What the rows rely on

- The rows are 6.3's with 14:01 changed to A `P` 30 and B `P` 30, which is what SM's paragraph says. Rows 14:14, 14:15, 14:16, 14:20 and 14:21 name the erratum.
- gen.py applies SM 3.2 rule 1 to the tab window slot by slot and checks what it gets: 14:00:00-14:01:30 A, 14:01:30-14:05:00 B, 14:14:00-14:17:00 ambiguous, 14:17:00-14:22:00 A and 14:22:00-14:25:00 B. The 14:01:30 move is SM's. The other two are the erratum.
