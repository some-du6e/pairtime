# Erratum: a track call 10 to 30 s from a hook prompt is both matched and unmatched

Date 2026-11-19. Raised by WP-FX4. Status: decided 2026-11-20 by the orchestrator for Karim: the rule below, one window. A track call with a hook or log prompt of its session within `TRACK_DEDUPE_S` (30 s) is `track_matched` and raises no flag, and one with none raises `track_without_prompt` (Decision, below). No side-door case depends on it. Case (f)'s call comes 4 s after its prompt and (g)'s 170 s after, so each keeps its one outcome.

## Input

DESIGN 5.4, on a session pairtime sees through hooks or a log reader:

- "A `track` call from the same session within `TRACK_DEDUPE_S` = 30 s of such a prompt is recorded as `track_matched`, opens nothing, and feeds a compliance counter per harness."
- "A `track` call with no hook or log prompt within `TRACK_LOG_MATCH_S` = 10 s of it ... opens nothing and raises the tier-2 flag `track_without_prompt`, because something outside the agent's turn called the CLI."

Take a Claude Code turn where the model thinks for 20 s, then makes its first tool call and runs `pairtime track` with it, as the AGENTS.md entry asks ("alongside your first other tool call").

## Expected

One outcome. The call is the agent following the entry, so it should be `track_matched` and raise no flag.

## What the rules give

Both. The call is within 30 s of the prompt, so it is `track_matched`. No prompt is within 10 s of it, so it raises `track_without_prompt`. Credit is the same either way, since both open nothing. The flag isn't harmless, though: DESIGN 7.9 counts tier-2 flags in the audit probability, so an agent that follows the entry but thinks for more than 10 s raises the teen's audit chance.

## A rule to decide it

One window for both. A call within `TRACK_DEDUPE_S` of a hook or log prompt of its session is `track_matched` and raises nothing. A call with no such prompt within `TRACK_DEDUPE_S` raises `track_without_prompt`. `TRACK_LOG_MATCH_S` keeps its job in DESIGN 5.2, where a log reader's matching entry takes over from a side-door anchor.

## For Karim

1. Accept the rule, or separate the two windows another way. Either way DESIGN 5.4's paragraph changes, and WP-SC1 follows it. Accepted on 2026-11-20 (Decision, below).

## Decision

The orchestrator decided this on 2026-11-20 under Karim's delegation, and the rule above stands. On a session pairtime sees through hooks or a log reader, `TRACK_DEDUPE_S` = 30 s is the only window. A `track` call with a hook or log prompt of its session within 30 s of it, before or after, is `track_matched` and raises nothing. A call with none raises `track_without_prompt`. `TRACK_LOG_MATCH_S` = 10 s keeps its job in DESIGN 5.2 and in `cli.track`'s `log_match`. A teen can be told it in one sentence: a `track` call in a session pairtime already sees counts as your agent's when it comes within 30 seconds of a message you sent there, and pairtime flags any other for review.

DESIGN 5.4 now says this, gives the flag's reason as an inference, and says when a call's session counts as seen. It does when the call's `log_match` isn't `no_reader`, or when a hook or log record of the session is dated at most `TRACK_DEDUPE_S` after the call. `pkg/score` used to count a hook record from any time, so one that came after a track anchor's minute was final could turn the anchor into a flag. The rule now looks at most 30 s past a call, inside the 600 s that the return-rule decision sets for every rule (DESIGN 3.7).

Neither reading moves credit. Both outcomes open nothing, and `track` records never count toward `logged_s` (D19). The flag does cost the teen, since DESIGN 7.9 counts tier-2 flags in the audit chance and 6.4 pre-approves no row with a flag. A 10 s window would flag an agent that follows the entry but thinks before its first tool call. DESIGN already allows 30 s for that delay in gate condition 2's `TRACK_INPUT_PRE_S`, which EV 4.4 sized for "a slow first token". The 10 s came from EV 4.4's opt-in probe mode.

The 30 s still needs checking. It rests on EV 4.4's estimate that the first tool call usually comes a few seconds after the person presses Enter, and nothing has measured that yet. `TRACK_DEDUPE_S` is a table value. The compliance counter records how long after its session's last prompt each call came, flagged calls included (DESIGN 5.4). If calls from opted-in agents often come later than 30 s, a MINOR table should widen the window. `TRACK_INPUT_PRE_S` rests on the same estimate, and there a call more than 30 s after the person's last input costs a hookless session its anchor, so check the two together.

No parameter value, fixture number or credited second changes. Case (f)'s call is 4 s from its prompt and (g)'s is 170 s, and each session has a hook `session.start` at 13:58:30. Only (g)'s `why` text changes.
