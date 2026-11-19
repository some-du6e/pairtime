# SM 6.2: five minutes before school

Maya, 14, on a 2016 Windows 10 laptop without the opt-in key counter, sends one prompt to Cursor's agent, fixes a string by hand and commits, all between opening the lid at 07:40:00 and closing it at 07:44:45. Every SM row is used: 07:40 to 07:44, 265 s credited.

| | |
|---|---|
| SM rows | 07:40, 07:41, 07:42, 07:43, 07:44, and the totals paragraph (265 s credited, 325 s left in the bucket) |
| Date and zone | Tuesday 2026-11-24, America/New_York, UTC-05:00. 07:40 local is 12:40Z |
| OS and evidence | Windows 10, `idle_keys_s` null in every sample, so every credited second is level B. Witnessed: the suspend flushes the spool (SM 6.2) |
| Window | 07:40:00 to 07:45:00. The resume at 07:40:00 is the first record |
| Project | `remind-bot` |

## Events

- `os.power` `resume` at 07:40:00.000 and `suspend` at 07:44:45.000, each with a presence sample taken at once (DESIGN 3.3).
- `os.focus`: Cursor (`app_class` `editor`, `focus_project` `remind-bot`) at the resume, and again at 07:40:00.600 with the new conversation in `hosted_sessions`.
- `os.presence` on every :00, :15, :30 and :45, plus one at each hook and power event.
- Cursor's hooks (`via` `hook`): `sessionStart` at 07:40:00.600, `beforeSubmitPrompt` at 07:41:00 (120 characters, none pasted), `afterFileEdit` at 07:41:50 (28 lines added, 12 removed), `afterAgentResponse` and `stop` at 07:42:30 (120 words).
- WakaTime heartbeats from Cursor (`via` `editor`): the agent's edit at 07:41:50.3, labeled `edit_source` `agent` because `afterFileEdit` reported it, and her edit at 07:43:40, labeled `human` because neither `afterFileEdit` nor `afterTabFileEdit` did.
- `git.change` `commit` at 07:44:30, `actor` `human`.

## Timing choices SM leaves open

- Input. A touch at 07:40:00.4; typing from 07:40:10.3 to the Enter at 07:40:59.8; a touch at 07:41:07.5, then nothing until 07:41:52.0, so the slots 07:41:15 and 07:41:30 have no input and are `bridged` (a 45 s run with input on both sides); scrolling every 8 to 12 s from 07:42:03 while she reads the diff; typing 07:43:22 to 07:43:39.6; clicks and the commit message until the last touch at 07:44:40.0.
- Her last input before the lid closed the evening before is 21:52:31.4, which the resume sample reports as `idle_any_s` 35,248.6.
- The conversation is new, so its `session.start` is in the fixture. SM says only that Cursor hosts the session.
- The agent's 40 diff lines fill the bucket by 300 s and its edit tool shows no other output (`visible_output_lines` 0), which is what makes 325 s left at 07:44:45: 300 + 40 − 15.
- Cursor's review view sends no heartbeat while she reads the diff. A navigation heartbeat there would be a `NAV` anchor whose 120 s lead covers 07:41:20 to 07:41:40, which SM counts as `X.agent_only`.
- One heartbeat for the hand edit, with no separate save heartbeat. A save a few seconds later would be a second `E` anchor and move the `E`/`G` boundary off 07:43:40.

## What the rows rely on

- 07:43:30 to 07:43:40 is inside both the edit's lead and the commit's. SM gives it to `E`: the lead of the next anchor at or after a second wins (SM 3.3 rule 3).
- 07:44:45 to 07:45:00 is `X.absent` because the slot overlaps the suspend. The half-open slot 07:44:30 to 07:44:45 doesn't, so its 15 s are `READ`.
- `agent_running_s` (07:41:00 to 07:42:30) and `agent_only_s` (07:41:20 to 07:41:40) follow from the times SM gives.
