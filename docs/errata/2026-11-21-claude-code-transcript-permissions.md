# Erratum: Claude Code 2.1.327 transcripts say who answered a permission request, and date a refusal

Date 2026-11-21. Raised by WP-AD3. Status: waiting for a decision. Until one comes, the transcript reader writes no `agent.permission`, as WP-AD3's Build says, and its test names the entries below.

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
