# Erratum: Claude Code reports a failed tool call, such as a test run that exits non-zero, only to PostToolUseFailure

Date 2026-11-20. Raised by WP-AD2, from the review of #115 (#139). Status: waiting for Karim. The Claude Code adapter already reads `PostToolUseFailure`. Nothing installs it until this is decided.

## Input

- Claude Code's hooks reference (code.claude.com/docs/en/hooks, read 2026-11-20). `PostToolUse` "runs immediately after a tool completes successfully". `PostToolUseFailure` "runs when a tool that started executing fails: the tool threw an error, or an MCP tool returned an error result". It doesn't fire for a call rejected before it ran: an unknown tool, input that fails validation, or a permission denial. Its input is `PostToolUse`'s `tool_name`, `tool_input` and `tool_use_id`, with `error`, `is_interrupt` and `duration_ms` in place of `tool_response`. For Bash, "a command that ran and exited produces a first line `Exit code N`, then any output the command produced as one block with stdout and stderr interleaved", and the reference's own example is a failed `npm test`.
- HS 3.1 lists `PostToolUseFailure` beside `PostToolUse`, and the hooks notes have `PostToolUse` fire when a "Tool call succeeded" and `PostToolUseFailure` when a "Tool call failed" (`docs/research/notes/signals.md`).
- WP-AD1's Claude Code 2.1.327 capture has no `PostToolUseFailure`, because pairtime doesn't install it, and every Bash call in it exited 0. Step 8's tests and the subagent's runs went through `| tail`.

## Expected

DESIGN 5.1's Claude Code row installs `PostToolUse` with matcher `Edit|Write|Bash`, and no `PostToolUseFailure`. SM 4.2 reads error signatures "from agent tool output (test and build runs)" and counts "a test or build command whose outcome changed" as a change event. SM 2.4 fills the reading bucket with the lines a run shows.

## Computed

With DESIGN 5.1's hooks, a failing `pytest` or `go test` that the agent doesn't pipe makes no `agent.tool`: no `tool_class` `test`, no `error_sig`, no `visible_output_lines`, no `t_start` or `t_end`. Failing runs are what SM 4.2's loops are made of, so the hook path misses them every time.

## What the adapter does

`internal/adapters/claudecode` reads `PostToolUseFailure` for Edit, Write and Bash as it reads `PostToolUse`, and lists it in `Events` and `Sources` under the same matcher. A failed command is `agent.tool` with its class and `cmd_hash`. It has `exit_code` N from the error's `Exit code N` line, and the output after that line gives its `visible_output_lines` and `error_sig`, which equals the signature a `PostToolUse` with the same output gets. An error with no exit-code line, as when the shell didn't start, is all output and has no `exit_code`. A failed edit is `agent.tool` for its file, with no lines. `testdata/gen_expected.py` does the same.

## Reasoning

1. DESIGN 5.1's Claude Code row should add `PostToolUseFailure`, matcher `Edit|Write|Bash`, after `PostToolUse`. `internal/install`'s `claudeHooks` and its goldens follow, and `internal/agent/hookpath`'s `native` map gives it `agent.tool`, as it gives `PostToolUse`, for presence.
2. `pkg/event`'s `agent.tool` has no field that marks a failure without an exit code: a failed edit, or a shell that didn't start. `exit_code` covers commands that ran. Until the scorer needs more, those failures are `agent.tool` records with no lines.
3. WP-AD1 should rerun step 8 with a test command that exits non-zero and isn't piped, so that a capture holds a `PostToolUseFailure` and the adapter's expected records cover it.
