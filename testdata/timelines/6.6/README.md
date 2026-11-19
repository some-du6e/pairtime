# SM 6.6: a scripted `claude -p` loop pretending to be a person

Casey runs `farm.sh` in Git Bash in Windows Terminal from 22:00 to 02:00, with the terminal in front, the lid open and a USB mouse jiggler plugged in. Each iteration has an LLM write a prompt, runs `claude -p "$p" --permission-mode acceptEdits`, then sleeps 60 to 299 s. Every SM row is used, and nothing is credited in four hours.

| | |
|---|---|
| SM rows | 22:00, 22:01, 22:02, 22:03 to 22:04, 22:05, the range 22:06 to 01:59, and the paragraph after the table |
| Date and zone | Saturday 2026-11-21 into Sunday, Asia/Kolkata, UTC+05:30. 22:00 local is 16:30Z; the half-hour offset still maps local minutes onto UTC minutes |
| OS and evidence | Windows 10 without the key counter, so `idle_keys_s` is null and the machine's level is B. Nothing is credited: headless prompts never count where `input_keys` is unavailable (SM 2.2 rule 4). Witnessed |
| Window | 22:00:00 to 02:00:00. Records from 21:59:50 show Casey starting the script and aren't checked |
| Project | Casey's, which SM doesn't name |
| Erratum | [2026-11-19-sm-6.6-uncredited-codes](../../../docs/errata/2026-11-19-sm-6.6-uncredited-codes.md): SM's rules don't decide each minute's uncredited codes, so the rows give SM's codes without seconds |

## Events

- 21:59:50: Casey clicks the terminal (`os.focus`, hosting no harness), types `bash farm.sh` and presses Enter at 21:59:59.6. That is the last key press of the night.
- The jiggler moves the pointer every 12 s, at 21:59:44.9 and every 12 s after, so every 15 s slot has input and presence stays `yes`.
- 49 iterations, as SM counts them: one at 22:00, the next at 22:04:57 and 47 more. Each one has `session.start` from Claude Code's `SessionStart` hook (`entrypoint` `sdk`, `parent_class` `script_runtime`, `permission_mode` `acceptEdits`), an `os.focus` saying the terminal now hosts that session, and `prompt.submit`, all at the moment `claude` starts. Then tool calls every 15 to 45 s (a read, edits, test runs) and some transcript text. Then the final message, `agent.stop`, and 0.3 s later `session.end` and an `os.focus` saying the terminal hosts nothing again.
- The first iteration is SM's to the second: `claude` at 22:00:05, two edits at 22:01:05 and 22:01:38, the stop at 22:02:10, a 167 s sleep, the next iteration at 22:04:57 and its `claude` at 22:05:02.
- Four commits by the agent, one an hour, in the first iteration that starts after 22:50, 23:50, 00:50 and 01:50 (`git.change` with `actor` `agent`).

## Timing choices SM leaves open

- After the first two iterations, `gen_prompt.py` takes 3 to 8 s, `claude` runs 70 to 160 s and the sleep is 60 to 299 s, drawn from a seeded hash, never from a random source. gen.py tries seeds in order and keeps the first schedule with 49 iterations whose prompt spacing has a mean within 10 s of 300 s, a standard deviation within 8 s of 75 s and a burstiness within 0.04 of −0.6, the three numbers SM gives. The schedule it keeps has 297.5 s, 78.0 s and −0.58.
- The 48 prompts after the first are SM's "varied, plausible" LLM prompts. SM quotes none of them, so `prompts.json` has their lengths, drawn from 45 to 140 characters, and no text.
- Presence samples every 15 s, plus one at each hook and focus event. The jiggler means the agent never slows to one sample a minute.
- The agent events replay to 11,073 s under Hackatime's `min(gap, 120 s)`, about 3 h 5 min. SM says about 3 h 10 min, two minutes of agent events per iteration plus the 120 s gap credit before each one; the fixture's sleeps under 115 s earn less than the full 120 s.

## What the rows rely on

- 22:00 to 22:02 and 22:05 have buckets because an agent is running. 22:03 and 22:04 have none: nothing is credited and no agent runs (SM 5.1). In the range 22:06 to 01:59, 135 of the 234 minutes have a bucket.
- `agent_running_s` per row comes from the iteration times. It is 55 s in 22:00, from 22:00:05.
- `agent_only_s` is the agent's whole running time, 5,731 s, since nothing is credited.
