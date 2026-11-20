# Side door (d): a script posting editor heartbeats

A script posts heartbeats in the WakaTime plugin's shape straight to the agent's local WakaTime endpoint, one every 100 s, each with a few human line changes. VS Code is in front on the project, and a jiggler keeps presence on a Windows laptop. The process on the other end of each connection is the script, not wakatime-cli, so every heartbeat fails the peer-process check and arrives `via` `editor_unverified`. Each opens a typed-edit anchor at level C, and none counts toward `logged_s`.

| | |
|---|---|
| Plan | WP-FX4 (d) |
| Rules | DESIGN 5.2, 5.7, 6.2 D2 and D19, 6.4 |
| Date and zone | Wednesday 2026-11-25, America/Chicago, UTC-06:00 |
| OS and evidence | Windows 10, `idle_keys_s` null. Every credited second is level C. Witnessed |
| Window | 14:00:00 to 14:11:00 |

## Events

- `os.focus` at 13:58:00: VS Code (`app_class` `editor`), `focus_project` the project.
- `os.presence` every 15 s. Heartbeats arrive over HTTP and take no sample.
- Five `editor.activity` records, `act` `edit`, `edit_source` `unknown`, `via` `editor_unverified`, at 14:03:00, 14:04:40, 14:06:20, 14:08:00 and 14:09:40, alternating between two files.

## Timing choices

- `edit_source` is `unknown` because that is what the bridge gives a plugin-shaped heartbeat with `human_line_changes` > 0 (DESIGN 5.7). No agent edit is within 120 s of any of them, so none becomes the agent's.
- 100 s between heartbeats, less than the 120 s lead, so the leads join.

## How the numbers follow

SM 2.3 makes each heartbeat a typed edit: an edit of unknown source, with input in its slot and the editor focused on the project. Its lead is `EDIT_LEAD_S` = 120 s with no typing part, and it has no trail. It failed the peer-process check, so it is level C (DESIGN 5.7, 6.4). It has no label, since DESIGN 6.4's side-door labels are `via_track`, `via_plugin` and `via_bridge`.

The leads cover every second from 14:01:00, 120 s before the first heartbeat, to 14:09:40, the last: 520 s of `E`, all level C. After 14:09:40 nothing covers the jiggler.

`editor_unverified` records never count toward `logged_s` or `ai_logged_s` (D19), and there is nothing else to count, so both are 0.
