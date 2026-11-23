# Erratum: a bridge prompt's time and length are wakatime-cli's, not the prompt's

Date 2026-11-23. Raised by the review of #236 (issue #245), from the WakaTime capture checked against the Claude Code transcripts it was made from. Status: decided 2026-11-23 by the orchestrator for Karim, on #245: a known limit of the level-C bridge, which v1 doesn't correct. DESIGN 5.7 says so, and a heartbeat that carries both an edit and a prompt becomes the edit first, then the prompt.

## Input

DESIGN 5.7's table: a transcript-derived heartbeat with `ai_prompt_length > 0` becomes "`prompt.submit` with `len_chars = ai_prompt_length`, no text, ... at the transcript's timestamp", and one with `ai_line_changes > 0` an `agent.tool` edit. The bridge's first version in #236 took the heartbeat's time as the prompt's own, and wrote a heartbeat that carries both as the prompt, then the edit.

## What the capture shows

The `ai-sync` run in `testdata/fixtures/wakatime/2.28.4/linux` holds 18 heartbeats with `ai_prompt_length`, which wakatime-cli v2.28.4 made from the transcripts in `testdata/fixtures/claude_code/2.1.327/linux/cli/transcripts`. Each one's length is the sum of the session's user lines written from some point on: the prompts a person sent, and the lines Claude Code writes as the user's, such as a scheduled task's prompt, the interrupt marker and a slash command's expanded instructions. Matched that way:

- 11 have the time of the first line they carry.
- 6 have the time of a tool result written before the lines they carry, 26.1 to 56.1 s before the first of them. The heartbeat of the read of `wordcount.py` at 1795193316992 carries the 194-character prompt sent 26.1 s later. The one of the `notes.txt` write at 1795193554590 carries the next turn's 38-character prompt, sent 26.5 s later, and the 42-character interrupt marker, written 48.2 s later.
- 1, the heartbeat of the agent's edit of `wordcount.py` at 1795193390401, carries its own turn's 53-character prompt, sent 21.4 s before it.

Three carry more than one line: the 38 and 42 above, the 80- and 72-character prompts sent 41.7 s apart (152), and a scheduled prompt, the person's "Stop the loop." and the `/goal` command's expanded instructions (42 + 14 + 49 = 105). Five count lines nobody typed: three scheduled prompts of the `/loop` (two heartbeats are one each), the interrupt marker, and the `/loop` and `/goal` commands' expanded instructions. Those expansions are placeholders of 50 and 49 characters in this capture, because the fixture's transcripts had Claude Code's own text replaced before they merged. The `/loop`'s held 5,981 characters before that.

Of the three heartbeats that also carry `ai_line_changes > 0`, two carry the next turn's prompt: the `notes.txt` write and the `hook-allowed.txt` write at 1795193639966, whose 41-character prompt came 56.1 s later. Only the `wordcount.py` edit carries its own turn's.

The review's figure of up to 221 s matched the `notes.txt` heartbeat's 80 characters to the 80-character prompt sent at 1795193775814 by length alone. The sum of the next two lines explains it, and that prompt is in the 152.

## Decision

Decided 2026-11-23 by the orchestrator for Karim, on #245:

- A bridge prompt takes the heartbeat's time and its `ai_prompt_length` as wakatime-cli gives them. Both are approximate. wakatime-cli stamps a prompt with the time of a transcript entry it batches the prompt with, often a tool result before the prompt, and one heartbeat can carry the sum of several lines' lengths, lines nobody typed included. That is one more reason bridge records are level C, and v1 doesn't correct either.
- A heartbeat that carries both `ai_line_changes` and `ai_prompt_length` becomes the `agent.tool` edit first, then the `prompt.submit`, since the prompt such a heartbeat carries is most often the next turn's.
- DESIGN 5.7 gets a known-limit paragraph after its table, in #236's DESIGN edit, under its `oracle-change` label:

  > **Bridge prompt times and lengths.** wakatime-cli stamps a prompt's heartbeat with the time of a transcript entry it batches the prompt with, often a tool result written before it, and a heartbeat's `ai_prompt_length` can be the sum of several user lines, lines nobody typed among them, such as a scheduled task's prompt or a slash command's expanded instructions. So a bridge prompt's time and `len_chars` are approximate, one more reason its records are level C, and v1 doesn't correct them. A heartbeat that carries both `ai_line_changes` and `ai_prompt_length` becomes the `agent.tool` edit first, then the `prompt.submit`, because its prompt is most often the next turn's (docs/errata/2026-11-23-bridge-prompt-times.md).

- The side-door case `testdata/timelines/sidedoor/e-bridge-lines` sends its prompts and edits as separate heartbeats, each prompt at its own time, so no number of it moves. Its README and `gen.py` said the bridge keeps "the transcript's timestamps", and now say that the bridge keeps each heartbeat's time, which here is the prompt's own.
