# Erratum: Claude Code 2.1.327 transcripts say who answered a permission request, and date a refusal

Date 2026-11-21. Raised by WP-AD3. Status: decided 2026-11-23 by the orchestrator for Karim: amended. In lookback only, a Claude Code transcript's refusal and a person's once-only acceptance of an edit or write become agent.permission, answered at the result entry and asked at the call's tool use or the tool result before it, whichever is later; shapes no capture has shown give none, and a live session gets none from its transcript, so these answers are always level C (Decided, below). WP-FX5's lookback PR applies it to SM, DESIGN, PLAN and the lookback fixtures, after #222. A WP-AD3 follow-up then has the transcript reader write these answers, WP-SC7 adds `pkg/score`'s guard and a tools/capture PR adds the capture steps; until the WP-AD3 follow-up lands, the reader writes no `agent.permission`.

## Input

WP-AD1's Claude Code 2.1.327 capture on Linux, `transcripts/.../617e16a3-ddb4-44ca-af68-72cf58ed1a34.jsonl`, lines counted from 0. Every user entry that holds a tool result carries one of two fields:

| Line | Tool, step | Field | Entry time | Tool use time |
|---|---|---|---|---|
| 56 | Edit, step 4 | `permissionDecision` `{"decision": "accept", "source": "user_temporary", "grantScope": "once"}` | 16:49:50.401 | 16:49:33.699 |
| 58 | Bash `python3 -c ...`, step 4 | `permissionDecision` `{"decision": "accept", "source": "user_temporary"}` | 16:52:01.537 | 16:49:34.892 |
| 85 | Bash `ls ... && git ls-files ...`, step 6 | `permissionDecision` `{"decision": "accept", "source": "config"}` | 16:53:04.497 | 16:53:04.413 |
| 89 | Bash `rm README.md`, step 6 | `toolDenialKind` `user-rejected`, `toolDenialBeforeRun` `true`, `toolUseResult` `User rejected tool use` | 16:53:22.799 | 16:53:07.788 |
| 98 | Write, step 22 | `permissionDecision` `{"decision": "accept", "source": "hook", "reasonType": "hook"}` | 16:53:59.966 | 16:53:57.624 |

The other tool results of the capture have `source` `config` or `user_temporary` too, the subagent's in `617e16a3-.../subagents/agent-a627b317dcd2e5387.jsonl` included. The `source` values are the ones OTel's `claude_code.tool_decision` uses (HS 3.3), which E-6.1-permission maps to `decided_by`.

## Expected

SM 1.2's `agent.permission` row is filled from "`PermissionRequest` paired with the next tool event; Claude `tool.blocked_on_user` span and `code_edit_tool.decision`; Codex `approval.requested`". The transcript isn't listed. WP-AD3 says the transcripts give no `agent.permission` for steps 4 and 6 "unless the test names a transcript entry that dates the answer and shows who gave it, and SM 1.2 then lists it". SM 2.8 keeps a permission answer's wait in lookback, up to `UNKNOWN_PA_MAX_S`, which needs an answer the logs date.

## Computed

- Who answered: every tool result says it, `user_*` for the person, `config` for a rule and `hook` for a PermissionRequest hook, and a refusal says `user-rejected`.
- When the request came: the tool use entry, which Claude Code writes as the model asks for the tool, before the prompt shows.
- When the answer came: only for a call that ran no time after it. A refusal's entry is the answer itself (`toolDenialBeforeRun`, line 89, 15 s after its request). An edit or a write applies as soon as it is accepted, so its result entry dates the answer to within the edit's run, tens of milliseconds (lines 56 and 98). A command's result comes when the command ends: line 58's comes 2.5 minutes after its tool use, and the hook's `duration_ms` says the command ran 280 ms of that, but the transcript alone can't tell the run from the wait.

## Reasoning

The transcript can date a refusal, an edit's acceptance and a write's acceptance, with who answered, and no acceptance of a command. Writing `agent.permission` from it would change SM 1.2's sources, and the WP leaves that to SM. Lookback would gain the permission answers SM 2.8 credits, for refusals and edits, which are the human acts that matter most in an approval-heavy session. A hooked session needs none of it: its `PermissionRequest` hook and OTel's `tool_decision` give both times (E-6.1-permission).

## Proposal

SM 1.2 adds the transcript to `agent.permission`'s sources for lookback and for sessions no hook reported: a tool result with `toolDenialKind` `user-rejected` and `toolDenialBeforeRun` is `reject` by `user`, requested at its tool use entry and answered at its own entry; a result of an Edit, MultiEdit, NotebookEdit or Write with `permissionDecision.source` `user_temporary` or `user_permanent` is `accept` by `user`, answered at its entry; and no command's acceptance, which the transcript doesn't date. `config` and `hook` decisions stay out, since no person answered them. WP-SC7's "log-dated permission answer" test then has its input.

## Decided

- Scope. The proposal stands for lookback and goes for live sessions. A transcript line is any process's word, and a PA anchor checks neither origin nor host. So a live session no hook reports (claude --bare, or one started before install) gets nothing from its transcript, and the scorer opens no via-log agent.permission outside lookback. In lookback an answer keeps the last 60 s of its wait and its 10 s trail at level C (SM 2.8), and opens nothing in a session rule 4 or 5 takes.
- Only the captured shapes. Each of the capture's nine user_temporary decisions carries grantScope once, line 58's included (the table above leaves it out). Every edit and write result has userModified false, and none shows user_permanent. So an edit or write is accept by user only with decision accept, source user_temporary, grantScope once and userModified false. user_permanent, any other grantScope, userModified true (which may be SM 1.2's edit), toolDenialBeforeRun false and a toolDenialKind no capture has shown give none and count a parse failure. Otherwise a grant for the session or for good could make every later allowed edit read as an answer. Only the first refusal among a message's calls is an answer, so a call cancelled with it can't read as a script's sub-300 ms answer and raise permission_too_fast.
- Corrections. The tool use entry is the request only for a message's first call. Line 58's prompt opened at 16:49:50.429, 28 ms after line 56's result and 15.5 s after its own tool use, and line 255's opened 23 ms after line 253's result. So requested_at is the later of the tool use and the tool result before it. An edit's entry dates the answer to within half a second, not tens of milliseconds: the result entries came 109, 83, 10, 156 and 444 ms after OTel's tool_decision (lines 56, 74, 89, 98 and 253; line 253's edit ran 317 ms). Each edit's and write's entry came after its PostToolUse hook began, so the person's own PreToolUse and PostToolUse hooks lengthen the wait by their run. The rest checks out.
- What lookback reads from 617e16a3: four answers, lines 56, 74 and 253 accepted and line 89 refused. Their waits are 16.7, 3.5, 7.4 and 15.0 s, against 16.6, 3.4, 6.8 and 15.0 s from the hooks and OTel. Line 90, 1 ms after the refusal, is the interrupt entry the reader already writes as agent.interrupt, so the refusal was already a change event; its PA adds the wait.
- SM 4.2. A permission the person rejected or edited counts from its answer, its record's t. It no longer counts from the record that pairs it with the next tool event: that is the discount-horizon decision's wording, from before E-6.1-permission dropped the pairing, and a refusal has no next tool event.
- Fixtures, as WP-FX5's PR builds them: 6.1-transcript-only 5,130 while 6.1's first edit lands at C+5:30, and 5,055 once E-6.1-permission moves it to C+5:25. two-sessions 860, with Noor answering A1 with a once-only Yes and then switching to accept-edits mode. two-sessions-lead 640. 6.4-transcript-only 91.
- Teen sentence: before you installed pairtime, a permission prompt you turned down, or an edit you allowed once, counts for the wait before your answer, at level C; after that, only Claude Code's own report of your answer counts.
- Left open, each for its own capture or erratum:
  - What 2.1.327 writes for a grant for the session or for good and the calls after it, for Esc at a prompt, for a call cancelled after a refusal, and for an edit changed in VS Code's diff. tools/capture/SCRIPTS.md gains steps for these; until a capture shows them, they give nothing.
  - Transcript interrupt entries, which are via log and can reach level A or B in a live session no hook reports (DESIGN 6.4).
  - Whether E-6.1-permission's live path should refuse a user_* tool_decision in a session rule 4 takes.
