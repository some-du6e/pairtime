# Erratum: a track call 10 to 30 s from a hook prompt is both matched and unmatched

Date 2026-11-19. Raised by WP-FX4. Status: waiting for Karim. No side-door case depends on it. Case (f)'s call comes 4 s after its prompt and (g)'s 170 s after, so each gets one outcome under either reading. WP-SC1 implements both outcomes and needs the answer for calls in between.

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

1. Accept the rule, or separate the two windows another way. Either way DESIGN 5.4's paragraph changes, and WP-SC1 follows it.
