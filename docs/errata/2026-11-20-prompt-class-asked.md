# Erratum: ledger-v1 10.7 doesn't say which records a prompt's `answer` class is taken from

Date 2026-11-20. Raised by the fix-up for issue #99, from the spec review of #94 (WP-LG5). Status: waiting for a decision. Until it is decided, the ledger writer takes `asked` from every record before the prompt, in `s` order, as `pkg/score.Questions` follows them, and a restart no longer changes what it finds.

## Input

- SM 2.2: a human prompt is `answer` when "the previous `agent.output` had `asks_question`, or the harness showed a question dialog."
- DESIGN 5.2: in a session a hook or log reader sees, outputs and stops that came through a side door (`track`, `plugin`, `bridge`) don't count.
- ledger-v1 10.7: "`pc` is SM 2.2's class of the text, as the writer computed it when it wrote the record ... CC8 recomputes it for challenged turns." DESIGN 7.3's CC8 compares the recomputed class with the committed one.
- `pkg/score.Questions`, which the writer uses: it follows each session's last output and stop record by record, in the order the agent records them. It applies DESIGN 5.2's rule as records arrive, so a side-door output counts until the session's first hook or log record and stops counting from then on. The scorer applies the rule knowing every record of the session, so a side-door output in a session whose first hook record comes later counts for the writer and not for the scorer, and `Questions`' comment says so. It forgets a session once 1024 others have been heard from since.

## What #94 did

After a restart, Open rebuilt `Questions` from the records of the last 64 batches only. Batches are sealed every `BATCH_S` and also on every prompt, stop, lock and suspend, so 64 batches can be 20 minutes of work. An agent whose final output asks a question, a restart 70 batches later, and then the user's "yes": the writer recorded `low_effort`, where SM 2.2 gives `answer`. Whether a prompt got `answer` depended on when the agent had last restarted, which no record shows, so no verifier could recompute it.

## What the fix-up does

Every 64th batch's seal frame keeps what `Questions` remembers after the batch's last record. Open restores the newest one and reads only the records after it, and rows say where that frame is. A prompt's `pc` now follows from the device's records before it, in `s` order, wherever the agent restarted. `TestQuestionsAfterRestart` pins it, and the seal frame's layout is the writer's own (ledger-v1 4), so no spec text changes with it.

## What 10.7 still leaves open

The spec doesn't say which records `asked` reads, so a verifier can't know what to recompute:

1. That `asked` comes from the device's records before the prompt in `s` order, per session: the session's last `agent.output` asked a question, or its last `agent.stop` stopped on one.
2. That DESIGN 5.2's side-door rule applies as the records arrive, as `Questions` does, and not with hindsight, as the scorer does. Until both use one rule, the live scorer's class and `f.pc` can differ for one session shape, which CC6 and CC8 would read as a fault.
3. Whether a writer may forget a session, and when. `Questions` forgets after 1024 other sessions, which follows from the chain but which no verifier holding a few revealed batches can count.
4. What CC8 does when the reveal doesn't hold the session's previous output: a challenged turn's batches may not show the record that asked.

## For Karim

A wording for 10.7, after "as the writer computed it when it wrote the record": "Its `answer` is taken from the device's records before the prompt, in `s` order: the session's last `agent.output` asked a question, or its last `agent.stop` stopped on one, where outputs and stops through `track`, `plugin` or `bridge` stop counting once the session has a hook or log record, applied as the records arrive (`pkg/score.Questions` is the reference). CC8 recomputes `answer` only when the reveal holds that output or stop, and otherwise checks the rest of the class." Then `pkg/score` either applies the side-door rule as records arrive too, or the scorer reads `f.pc` for prompts, so CC6 compares like with like. The 1024-session bound can stay as an implementation limit if 10.7 says a verifier never fails a turn for a session the writer forgot. Dropping it means the writer keeps every session the device ever ran, in memory and in every 64th seal frame, about 100 bytes each.
