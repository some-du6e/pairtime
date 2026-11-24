---
harness: t3_code
harness_version: 0.1.10-nightly.20261112.4606
os: Ubuntu 24.04.4 LTS, in an LXC container on Proxmox, so the kernel, 7.0.14-28-pve, is the host's
captured_on: 2026-11-23
captured_by: an AI agent (pairtime's WP-ADE1 capture agent) driving T3 Code's web client, not a person
synthetic: false
scrubbed_by: tools/capture/t3code/mkfixture.py's first version, which replaced ids, paths, times and text as the Scrub section says; then pairtime fixtures scrub -n v0.1.0-alpha.6 (CI-built at de190ee3151a, release.yml run 45135543535), which found nothing; then the searches listed there; every file read in #222's review, not typed by a person
kit: tools/capture/t3code, added in #222, whose README's last section says how the versions the capture ran differed; the Claude Code hooks registered were the list in tools/capture/prepare.py at e84edf6d8f91 (CLAUDE_CODE_HOOKS), each running the kit's logger.py instead of dump-hook.sh
script: tools/capture/t3code/README.md, steps 1 to 8, with mkprobes.py's PLAN; the probes are listed below
otlp: off
runs: web (T3 Code's web client in Chrome 157, driven by agent-browser)
---

# Fixture provenance

T3 Code is an agent app, not a harness, so this fixture isn't a harness's hook capture. It is the kind the `t3_code` paragraph of `testdata/fixtures/README.md` describes: the T3 Code store that the T3 Code reader (`internal/adapters/t3code`) reads, captured at the end of a live session, together with what Claude Code's hooks reported while T3 Code drove it. It gives the reader the real fixture DESIGN 5.1 asks for (WP-ADE1, issue #225 rulings Q4 and Q14).

## No human timing

No person typed anything. An AI agent drove T3 Code's web client with agent-browser. It put each message's text into the composer at once and pressed Enter (or Ctrl+Enter to queue), and it sent four steers by writing T3's own `orchestration.dispatchCommand` frame to the page's WebSocket at a time picked from the hook log. No interval in this fixture is a person's.

## Versions

- T3 Code `0.1.10-nightly.20261112.4606` (`t3 --version`), T3's own Linux build, run as `t3 serve --port <scratch port> --host 127.0.0.1 --no-browser <repo>` from a hard-linked copy of the installed runtime folder, whose files were the installed runtime's own (see the PR's capture departures). The store's migration head is 85, and migration 55 is `OrchestrationV2`. The tables, columns and indexes the reader reads are exactly those in `internal/adapters/t3code/testdata/schema.sql`, which was written from T3's source at d8f3feff. Nobody checked that this nightly was built from exactly that commit.
- Claude Agent SDK `0.3.321`, from `CLAUDE_AGENT_SDK_VERSION` in the hook's environment.
- Claude Code `2.1.330` (`claude --version`, and the transcripts' `version`), on PATH as `claude`, which is T3's default `binaryPath`. `DISABLE_AUTOUPDATER=1` was set.
- Model `claude-opus-5-5`, T3's default, in full-access runtime mode.

## How the run was set up

- The run used a throwaway Linux account with its own home. T3 Code ran with `T3CODE_HOME` in that home. Its `userdata/settings.json` set only `guestSharingEnabled: true` and a `claudeAgent` provider instance whose `homePath` (T3's "CLAUDE_CONFIG_DIR path") was a scratch Claude home. That home's `settings.json` held only the hooks below.
- The environment was filtered: the scratch `HOME`, a plain `PATH`, `ANTHROPIC_AUTH_TOKEN` and `ANTHROPIC_BASE_URL` (an Anthropic-compatible router), `DISABLE_AUTOUPDATER=1` and `T3CODE_HOME`, with nothing else from the capturing agent's environment. T3 itself sets `CLAUDE_CODE_ENTRYPOINT=t3-code` for the Claude processes it runs, and the hook saw that value.
- The project was a two-file git repository in the scratch home, `/home/capture/repo` after the scrub. Thread 1 ran in the current checkout (`worktreePath` null). Thread 2 was started with "New worktree", so T3 made a worktree under `<T3 home>/worktrees/repo/`.
- Every hook in prepare.py's Claude Code list (SessionStart, UserPromptSubmit, Stop, StopFailure, Notification, PermissionRequest, PostToolUse on `Edit|Write|Bash`, SubagentStart, SubagentStop, SessionEnd) ran one logger, `python3 -I logger.py <event>`. The logger read the payload and wrote one line per call: the event, `session_id`, `prompt_id`, the arrival time, and, for UserPromptSubmit, `guest_prefix`. `guest_prefix` says whether the prompt opens with T3's guest wrapper `Message from guest ` after optional leading whitespace and an `Ultrathink:` line. The logger wrote no prompt and no other payload field.
- Probes 22 to 27 ran with the logger sleeping 1.5 s after it wrote each Stop record. That was the only way found to land a steer after Claude ended its turn but before T3 noticed (probe 23). The other Stop records came from a logger that didn't sleep.
- A real guest message could not be captured. T3 creates a guest link only through a running T3 Connect tunnel (`apps/server/src/guest/GuestShareService.ts:342-345` at d8f3feff; the share dialog says "Guest links need a running T3 Connect tunnel.", `packages/contracts/src/guestShare.ts:69`), and that needs a T3 cloud account and a public tunnel. So the owner typed three stand-ins whose text opens with T3's guest wrapper, written the way `decideGuestMessageSend` writes it (`apps/server/src/orchestration-v2/GuestShareDecider.ts:217`), `Message from guest Probe Guest, sent by the owner:` and a blank line before the probe text. These rows have no `context.guest` mark, and their `createdBy` is `user`. T3 hands a steer's stored text to Claude Code with no mark at all (`apps/server/src/orchestration-v2/ProviderTurnControlService.ts:405-428`), so a stand-in reaches Claude Code exactly as a promoted guest row would. The reader's prefix test treats them as guests' rows.

## The probes

`web/probes.json` lists every UserPromptSubmit in arrival order: the `prompt_id` and `session_id` the hook gave, what the probe was, the message row (rowid and id) that carried it, that row's run and its ordinal, whether that run is the message's own (its `userMessageId` is the message) or the run it steered into, whether the run's attempt names the hook's session as `nativeThreadId`, and `guest_prefix`. The agent wrote the labels from what the store and T3's trace showed:

- `message`: a new message, which starts a run of its own.
- `message_after_turn_end` (probes 3 and 13): sent from the composer while it still showed the turn running. It reached T3 after the turn ended and started a run of its own.
- `t3_notification_continuation` (hook prompt 4): the background-command notice that Claude Code itself started a turn for. T3 then stored it as a `user` row from `provider`, `createdBy` `agent`, with a `notification` mark. The hook fired 875 ms before that run's `startedAt`.
- `queued` (probe 7): queued with Ctrl+Enter while a turn ran. It got its own run at once, which started after the running one ended.
- `steer` and `steer_during_final_reply` (probes 5, 15 and 17): steered into a running turn. The row names the target run.
- `steer_guest_wrapper_stand_in` (probe 9): a stand-in steered into a running turn.
- `queued_then_promoted_to_steer_guest_wrapper_stand_in` (probe 11): a stand-in queued with Ctrl+Enter, then sent with the queued row's "Send now", which T3 runs as `queued-message.promote-to-steer`. Its queued run (ordinal 10) is left `cancelled` with no `startedAt`. The row moved to the target run (ordinal 9), and its `created_at` and `updated_at` both became the moment of promotion, 9.1 s after its queued run's `requested_at`.
- `steer_after_turn_end_ran_as_own_run` (probes 19 and 21): steers that reached T3 just after the turn ended. T3 stored them once the run had completed, as messages of their own runs, stamped about 0.7 s and 0.6 s after they were sent.
- `steer_follow_up` (probe 23): T3 accepted the steer into the running run (`Orchestrator.dispatchSteerIntoRun`). Claude's turn ended before the steer was delivered, so `ProviderTurnControlService.steer` failed and T3 sent it again as a follow-up (`apps/server/src/orchestration-v2/EffectWorker.ts:287-353`). The row kept its rowid (42) and message id. It got its own run (ordinal 20), with `created_at` = `updated_at` = the run's `requested_at`, 3.0 s after T3 first appended it. No row was stored between the steer and its follow-up.
- `steer_during_stop_hook_guest_wrapper_stand_in` (probe 27): a stand-in that T3 delivered into the target turn while that turn's slow Stop hook ran. Claude Code took it in the same turn, and the hook fired 111 ms before the target run's `completed_at`. T3 then stored a background-task notice of its own (rowid 49, ordinal 23).
- `message_new_worktree_thread` (probe 28): the first message of thread 2, a new session, whose run started 17.8 s after it was requested, once the worktree was made.

Probe 25 has no row and no hook: the agent's frame for it was malformed, and T3 never received it.

## Files

- `web/store.sql`: `INSERT` statements for every row of the seven tables `internal/adapters/t3code/testdata/schema.sql` creates (`effect_sql_migrations` 85, `projection_projects` 1, `orchestration_v2_projection_threads` 2, `orchestration_v2_projection_runs` 24, `orchestration_v2_projection_run_attempts` 24, `orchestration_v2_projection_provider_threads` 2, `orchestration_v2_projection_messages` 52), each with its rowid as T3 stored it. Load `schema.sql` first, then this file. It is the store as it stood after the last probe, so a row that T3 later rewrote (probes 11 and 23) shows only its last version. Nothing else of the store is here: no search table, no events or receipts, no other table.
- `.gitattributes`: `* -text`, as the other fixtures have, so git never changes these files' line endings, even on a Windows checkout with `core.autocrlf`.
- `web/hooks.jsonl`: the logger's records, one per hook call in arrival order, with `event`, `session_id`, `prompt_id` (null where the payload had none), `t_ns` (wall-clock arrival in Unix nanoseconds) and, for UserPromptSubmit, `guest_prefix`. It holds 28 UserPromptSubmit records for 28 distinct `prompt_id`s in two sessions. The two sessions are thread 1's and thread 2's provider-thread `nativeThreadRef.nativeId`, and the `nativeThreadId` of every attempt of their runs.
- `web/probes.json`: the probe list above, one entry per hook prompt. It numbers them by arrival (`hook_prompt`), and the probe numbers above count what the agent sent: hook prompts 1 to 3 are probes 1 to 3, hook prompt 4 is the background notice's turn, hook prompts 5 to 25 are probes 4 to 24, probe 25 has none, and hook prompts 26 to 28 are probes 26 to 28.

`TestCapture` in `internal/adapters/t3code` replays the three files through the T3 Code reader: it loads `schema.sql` and `store.sql` with the `sqlite3` tool, writes each message row at its stamp, asks about each hook prompt when the hook saw it, and holds the reader's records, pairings and guest reports to `probes.json`.

## Scrub

- Every UUID, in a column, inside a JSON payload or inside a composite id such as `run:thread:<uuid>:ordinal:N`, became `00000000-0000-4000-8000-NNNNNNNNNNNN`. They are numbered in order of first appearance in `store.sql` and then `hooks.jsonl`, and the numbering is the same across all three files, so a session id in `hooks.jsonl` matches the attempt's `nativeThreadId` in `store.sql`.
- Claude's message ids (`msg_` plus 52 characters, inside assistant rows' `message:provider:claudeAgent:native-item:...` ids) became `msg_fixtureNNNN`.
- The scratch home became `/home/capture`. The worktree folder's two hex runs became `00000001` and `0000000000000002`.
- Every time moved by +1141 days, from 2026-11-23 to 2030-01-07, with the time of day and the milliseconds kept: ISO stamps, `effect_sql_migrations.created_at`, epoch-millisecond numbers in payloads (`opens.openedAt`) and the hooks' `t_ns`.
- Text replaced with `[<what>, removed: N characters]`, where N is the original length: every assistant row's text (`assistant text`), the two T3 notification rows' text (`T3 notification text`), their `notification.summary` (`T3 notification summary`), and both threads' titles, which Claude generated (`thread title`), in the column and in the payload. No text Claude or Anthropic wrote is left.
- Kept as written: the probe texts, which the capturing agent wrote, including the three guest-wrapper stand-ins; the project title `repo`; the branches `main` and `t3/probe-twenty-eight-worktree`, which T3 made from probe 28's words; model and provider ids; T3's migration names.
- Checked by script: no occurrence of the scratch account name, `/root`, the auth token, the router's URL or host, the machine's host name, any original UUID, `msg_` id or hex run, any 2026 date, or any original epoch value. The thread titles' and the notifications' original words are gone too.
- `pairtime fixtures scrub -n` v0.1.0-alpha.6, the CI-built release binary (release.yml run 45135543535 at de190ee3151a, checked against the release's checksums.txt), ran over this folder with this machine's account, home, host name and git identity, and with the throwaway account's name and home and the repository's folder names given by flag. It found 0 spans in the 4 files it reads, `.gitattributes` included (it leaves PROVENANCE.md alone).
- Searched again before commit, in every file of this folder: the machine's host name, `/root`, the throwaway account's name, and `/home` paths other than `/home/capture`, the fixtures' placeholder home: none. T3 settings names and values: none but T3 Code's own words. Every key and value the fixture shares with the installed T3 Code's settings file is in T3 Code's source at d8f3feff too, and the fixture holds no settings file. Install or telemetry ids: none. `owner_node_id` is null throughout, every node id is made from the replaced run and message ids, and every UUID, `msg_` id and hex run is a placeholder.
- `tools/capture/t3code/mkprobes.py` writes this folder's `probes.json` again byte for byte from its `store.sql` and `hooks.jsonl`. `mkfixture.py`, run on a store rebuilt from this folder's `store.sql`, writes its `hooks.jsonl` byte for byte and its `store.sql` but for the placeholders' lengths, which it measures again.
