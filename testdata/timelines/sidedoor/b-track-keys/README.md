# Side door (b): the track loop on macOS, with key presses before each call

Case (a) on a Mac. The loop, the labels and the jiggler are the same, and before each call a key is pressed eight times in the host window, a second apart. macOS reports key-down idle time with no typing module, so `input_keys` shows that typing, and every track anchor is level A. The keyboard is the corroboration DESIGN 5.2 asks for, so these seconds count as credited, and nothing else about the loop changes.

| | |
|---|---|
| Plan | WP-FX4 (b) |
| Rules | DESIGN 5.2, 5.4, 6.2 D3, 6.4 |
| Date and zone | Wednesday 2026-11-25, America/Chicago, UTC-06:00 |
| OS and evidence | macOS, `idle_keys_s` set in every sample. Every credited second is level A. Witnessed |
| Window | 14:00:00 to 14:09:00 |

## Events

- `os.focus` at 13:58:00: Terminal, hosting the Goose session.
- `os.presence` every 15 s and at each call. The sample at each call shows `idle_keys_s` 1.3: the last key press came 1.3 s before.
- The ten `cli.track` records of (a), at the same local times, with `text_len` 90 and `log_match` `no_reader`.

## Timing choices

- Key presses at 8.3, 7.3, 6.3, 5.3, 4.3, 3.3, 2.3 and 1.3 s before each call. The extra 300 ms keeps every press off a slot boundary, as testdata/timelines/README.md requires, so the sample at each slot's end shows whether the slot had a key press. It also keeps them clear of the jiggler, which moves on the half second. Between the last press before one call and the first press before the next there are 14 s, less than a slot, so every slot from 14:03:45 to the last call has a key press.
- Before 14:03:51.7 only the jiggler moves. The last key press before the fixture is at 13:41:07.3.

## How the numbers follow

DESIGN 5.4's four conditions hold for each call as in (a). This time `input_keys` shows typing in the host window in the 10 s before each one, so every anchor is level A (DESIGN 5.4, 6.4). It still carries `via_track`.

The typing part of a track lead is realized only in slots with key input. From 14:03:45 every slot has some, and every second from 14:01:00 to 14:03:45 is inside the think part of some anchor, which needs presence only. The first call's think part starts the stretch at 14:01:00, and nothing breaks it before the last call at 14:07:09: 369 s of `P`. Then the last call's 20 s base drains until 14:07:29. In all, 369 + 20 = 389 s.

`logged_s` and `ai_logged_s` are still 0. D19 drops `cli.track` records whatever their level, so a keyboard-backed loop still can't raise the ceiling on level-C credit. It has no level-C credit anyway.

As in (a), no track turn runs (SM 1.3, DESIGN 5.4): the 20 s after the last call are `READ`, the rest of 14:07 is `X.no_anchor`, `agent_running_s` is 0, and 14:08 has no bucket.
