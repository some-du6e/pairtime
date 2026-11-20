# Side door (c): a plugin reporting 50,000-word outputs

A plugin posts events to the agent's local HTTP endpoint for a session it claims: a prompt, then an output of 50,000 words and a stop 40 s later, twice. Its events say `"source": "hook"`; the agent records how they arrived, `via` `plugin`. A jiggler keeps presence on a Windows laptop and the terminal stays in front. Each prompt opens a level-C anchor, and each turn fills the reading bucket by its 20 s base and nothing more. Uncapped, one such output would fill 16,666.667 s, past the bucket's 600 s cap.

| | |
|---|---|
| Plan | WP-FX4 (c) |
| Rules | DESIGN 5.2, 5.3, 6.2 D2 and D19, 6.4 |
| Date and zone | Wednesday 2026-11-25, America/Chicago, UTC-06:00 |
| OS and evidence | Windows 10, `idle_keys_s` null. Every credited second is level C. Witnessed |
| Window | 14:00:00 to 14:12:00 |

## Events

- `os.focus` at 13:58:00: Windows Terminal, with the plugin's session in `hosted_sessions`.
- `os.presence` every 15 s. HTTP events take no sample.
- Turn 1: `prompt.submit` at 14:04:40 (150 characters, none pasted), `agent.output` with 50,000 words and `agent.stop` at 14:05:20. Turn 2 the same at 14:10:40 and 14:11:20. All `via` `plugin`, harness `other`.

## Timing choices

- The plugin's manifest names the terminal's harness process among its hosts, so the agent's process walk puts the plugin's session in `hosted_sessions` (DESIGN 5.3 asks only that the focused window's process match the plugin's hosts).
- The jiggler moves every 12 s, at 14:04:38 and 14:10:38 among other times, and the samples at 14:04:45 and 14:10:45 show those moves. That is the input in the 10 s before each prompt that SM 2.2 rule 6 needs.
- Two invented prompts of 150 characters, so each lead is 120 + 150 / 1.5 = 220 s.
- The turns are 360 s apart, so the 20 s base and the leads leave a gap the cap decides: 100 s from 14:05:20 to 14:07:00.

## How the numbers follow

DESIGN 5.3 opens an anchor for a plugin prompt "only under SM 2.2 rule 6". The jiggler gave input in the 10 s before each prompt, the focused window hosts the session, and 150 typed characters are plausible, so both prompts are `human_typed` and substantive. A plugin anchor without keyboard input is level C and carries `via_plugin` (DESIGN 5.2, 6.4). DESIGN 5.4's restriction on the typing part is for track anchors, so SM 2.3's rule applies: with no keyboard signal on this machine, the whole 220 s lead needs presence only.

| | Credited |
|---|---|
| Lead of turn 1, 14:01:00 to 14:04:40 | 220 s `P` |
| Base of turn 1, 14:04:40 to 14:05:00, the agent claimed to be running | 20 s `SUP` |
| Lead of turn 2, 14:07:00 to 14:10:40 | 220 s `P` |
| Base of turn 2, 14:10:40 to 14:11:00 | 20 s `SUP` |
| In all | 480 s |

The outputs add nothing. DESIGN 5.2 lets plugin output fill a turn's bucket "only up to `R_TURN_BASE_S` = 20 s per turn" when no hook or transcript record backs it, and the base already reaches 20 s. From 14:05:00 to 14:05:20 the claimed agent is running with nothing to read (`X.agent_only`), and from 14:05:20 to 14:07:00 nothing covers the jiggler (`X.no_anchor`), and the same after turn 2.

SM 4.2's autopilot test marks turn 1 unread. The next prompt comes 320 s after the stop, less than a quarter of the time 50,000 words take at 238 wpm, which is 3,151 s. The discount starts only at the third low-effort prompt after an unread turn, and both prompts are substantive, so nothing is discounted.

Every record but the OS ones arrived via `plugin`, so none counts toward `logged_s` or `ai_logged_s` (D19). Both are 0.
