# Focus unknown (b): a pty driver during a browser game, on GNOME on Wayland

Kai, 15, starts a script that runs Claude Code for `idle-farm` in a detached tmux session and types a new task into it every 5 minutes with `tmux send-keys`, then plays a game in the browser for twenty minutes. On GNOME on Wayland without pairtime's extension the agent can't tell which window is in front, so SM 2.2 rule 6's focus test passes for every session, and the game's input passes its input test. Each scripted prompt is `human_typed`, and 1,140 of the 1,200 s are credited, every one at level C with `focus_unknown`. No second reaches level A or B. The [X11 twin](../b-pty-game-x11/), with the same acts and the browser in front, credits nothing.

SM 2.2 names this case in the paragraph after its origin table: "a script typing into a terminal while the person uses another app passes too; every second it supports is evidence level C". This case works out what that costs.

| | |
|---|---|
| Source | WP-FX8's (b) (docs/PLAN.md). Invented; SM 2.2 and 2.5 describe the case but give no worked example |
| Rules | SM 2.2 rules 4, 6 and 8 and the paragraph after them, SM 2.5's focus-unknown paragraph, with SM 2.3, 2.4, 3.3 and 5.2 and the parameters of SM 8 |
| Date and zone | Wednesday 2026-11-25, Europe/London, UTC+00:00 |
| OS and evidence | Linux, GNOME on Wayland without pairtime's extension. `idle_keys_s` is null in every sample. Every credited second is level C with `focus_unknown`. Witnessed |
| Window | 20:00:00 to 20:20:00. Records from 19:55:04 set up the session, and samples run to 20:20:30 |
| Project | `idle-farm`, one Claude Code session in tmux |

## The events

- **19:54:51 to 19:55:01.6.** Kai types `./drive.sh` in a terminal. The script starts Claude Code in a detached tmux session, so Claude Code runs under the tmux server, and no window's process is one of its parents.
- **19:55:04.** Claude Code's SessionStart hook: `entrypoint` `cli`, `parent_class` `multiplexer`, a TTY, `permission_mode` `acceptEdits`. It makes a project context live, so the agent records its one `os.focus`, `app_class` `unknown`, and starts sampling every 15 s (DESIGN 3.3).
- **19:55:19.9.** Alt-Tab to the browser, which leaves no record. From 19:55:21.3 to 20:20:30 the game: a key or a pointer move every 0.4 to 2.5 s.
- **Every 300 s from 20:03:00.** The script's prompt: 90 characters that `tmux send-keys` writes to Claude Code's pty, then Enter. The pty isn't the OS's input queue, so the idle time never sees that typing (SM 2.2). The UserPromptSubmit hook records the prompt with `pasted_chars` 0, and the agent takes a sample at once, which shows the game's last input less than 2.5 s before. The four prompts are four different tasks.
- **Each turn**, from its prompt T: 45 words at T + 20, an edit of 16 lines at T + 50 with no visible output, and a 60-word summary with the stop at T + 100.

## What the rules make of each prompt

- **Rules 1 to 3.** Nothing declares the prompt scheduled, a continuation or a subagent's.
- **Rule 4.** `entrypoint` `cli`, `parent_class` `multiplexer`, which isn't on the rule's list, and a TTY. Not `headless`.
- **Rule 6.** Input in the 10 s before submit: the game's. Focus at submit: unknown, so the test "can't be applied and passes for every session" (SM 2.2, 2.5). Plausible length: 90 ≤ 15 × 180, the lead window's 12 input slots of 15 s each. So `human_typed`, class `substantive`, and the anchor passed its focus test only because focus was unknown: level C with `focus_unknown`.
- **The lead.** 120 + 90 / 1.5 = 180 s. Where `idle_keys_s` is null the typing part needs only presence (SM 2.3), and the game gives it.

## Credit, minute by minute

T is a cycle's prompt: 20:03:00, 20:08:00, 20:13:00 and 20:18:00. Every slot's focus is unknown, so every focus test passes for `idle-farm`, and every credited second is level C with `focus_unknown` (SM 2.5).

| Minute | Credited | Reasons | Arithmetic |
|---|---|---|---|
| 20:00 | 60 | P 60 | The first prompt's 180 s lead, from 20:00:00. Its think part runs to 20:02:00 |
| 20:01 | 60 | P 60 | The think part |
| 20:02 | 60 | P 60 | The typing part, 90 / 1.5 = 60 s |
| 20:03 | 45 | SUP 45 | The prompt resets the bucket to 20 s, and 45 words add 45 / 3 = 15 s at 20:03:20: SUP from 20:03:00 to 20:03:35, 20 + 15. Nothing is left to read from 20:03:35 to the edit at 20:03:50 while the turn runs, a turn the rules say a person started: X.agent_only 15. The edit adds 16 × 60 / 8 = 120 s, so SUP again from 20:03:50: 35 + 10 |
| 20:04 | 60 | SUP 40, READ 20 | SUP to the stop at 20:04:40, 10 + 40 = 50 s of the edit's 120, which leaves 70. The 60-word summary adds 20 s, so 90 are left. READ from the stop until the next prompt's lead starts at 20:05:00 |
| 20:05 | 60 | P 60 | The second prompt's lead, which beats the 70 s left in the bucket (SM 3.3 rule 2) |
| 20:06 | 60 | P 60 | The think part |
| 20:07 | 60 | P 60 | The typing part |
| 20:08 | 45 | SUP 45 | As 20:03, a cycle later: X.agent_only 15 |
| 20:09 | 60 | SUP 40, READ 20 | As 20:04 |
| 20:10 | 60 | P 60 | As 20:05 |
| 20:11 | 60 | P 60 | The think part |
| 20:12 | 60 | P 60 | The typing part |
| 20:13 | 45 | SUP 45 | As 20:03: X.agent_only 15 |
| 20:14 | 60 | SUP 40, READ 20 | As 20:04 |
| 20:15 | 60 | P 60 | As 20:05 |
| 20:16 | 60 | P 60 | The think part |
| 20:17 | 60 | P 60 | The typing part |
| 20:18 | 45 | SUP 45 | As 20:03: X.agent_only 15 |
| 20:19 | 60 | SUP 40, READ 20 | As 20:04. The 70 s left at 20:20:00 lie after the window |

## Totals

| Total | Seconds | Arithmetic |
|---|---|---|
| `credited_s` | 1,140 | 4 × (60 + 60 + 60 + 45 + 60) |
| `P` | 720 | 4 × 180 |
| `SUP` | 340 | 4 × (45 + 40) |
| `READ` | 80 | 4 × 20 |
| `X.agent_only` | 60 | 4 × 15 |
| `agent_running_s` | 400 | 4 × (60 + 40) |
| `agent_only_s` | 60 | 400 − 4 × (45 + 40) |
| `level_c_s` | 1,140 | 1,140 |
| `focus_unknown_s` | 1,140 | 1,140 |
| `level_a_s` | 0 | 0 |
| `level_b_s` | 0 | 0 |
| `ai_credited_s` | 1,140 | 720 + 340 + 80 |
| `human_typed_prompts` | 4 | 4 |

What bounds the 1,140 s is their level. Level C never counts toward public totals, build days or boards, and a program takes it only by opting in to level C, inside forge-verified commit intervals, at the handbook third (SM 5.4, DESIGN 6.2 D5 and D11). For that third, the agent events in the window are four prompts, four edits and four stops 50 s apart within a turn and 200 s apart between turns, so `ai_logged_s` is 8 × 50 + 3 × 120 = 760 s, and SM 5.4 recommends at most min(1,140, 760 / 3), 253⅓ s, for them.

## Readings the case relies on

- **The pty's typing is invisible.** `tmux send-keys` writes to the pty, so no sample sees it, and presence comes from the game alone. The prompt passes rule 6's input test on the game's input, which is the weakness SM 2.2 and 2.5 accept for desktops that hide focus, at level C.
- **No window hosts the session.** That would fail rule 6 on a desktop that reports focus, but here focus is unknown, and the test passes for every session whatever window hosts it.
- **No dark-flow rule fires.** The four prompts are four different tasks, so none is a near-duplicate of another (SM 4.2's re-roll). No test runs, so there is no error signature. No prompt is a go-ahead, so there is no autopilot. Each turn edits a different file, so no turn rewrites the last one's lines.
- **No lookup.** A slot of unknown focus is never a browser slot (SM 2.5), so the game holds no lookup and E-lookup-lookahead has nothing to decide.
- **An edit fills the bucket when it lands.** An `agent.tool` record fills the bucket at its `t`, which is its `t_end`, not its `t_start`, as SM 6.1's "lands at 14:05:30" and 6.2's "until the agent's 40-line edit lands at 07:41:50" read it. Each turn's 16-line edit fills at T + 50, which ends the 15 s each cycle has nothing to read. Filled at `t_start`, it would fill at T + 45.5 and that gap would be 10.5 s.
- **The bucket left behind.** At each next lead 70 s are still in the bucket, and the next prompt's reset drops them (SM 2.4). Nothing reaches the 600 s cap, and nothing expires.
