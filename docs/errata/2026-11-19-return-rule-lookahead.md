# Erratum: the return rule can decide a second after its minute is final

Date 2026-11-19. Raised by WP-SC1. Status: the return rule decided 2026-11-20 by the orchestrator for Karim (Decision, below). SM 2.5's lookups, in [the lookup look-ahead erratum](2026-11-20-lookup-lookahead.md), are still waiting for a decision. No fixture depends on either.

## Input

- DESIGN 3.7 and 6.2 D1: a minute bucket stays `pending` for `PENDING_WINDOW_S` = 900 s after its minute ends, "because a lead reaches back up to 420 s before its anchor and reading needs an anchor within 600 s". After that it is `final`.
- SM 2.1's return rule: a silent run of up to `RETURN_GAP_MAX_S` = 600 s is `returned` when "its first following anchor is substantive, in the same project and within 600 s of the run's end". Its reading part "is placed at the start of the run, right after the tail".

## Expected

Every rule that can credit a second has decided it by the time the second's minute is final.

## What the rules give

Take a 600 s silence after a long answer, from 10:00:30 to 10:10:30, and a typed prompt at 10:20:30, 600 s after the silence ends. The prompt qualifies the run, so the reading part of `RETURN_CREDIT` starts right after the 30 s tail, at 10:01:00. The minute 10:01 is final at 10:17:00, 900 s after it ends, and the prompt that decides its credit comes 210 s later. The rule reaches up to `RETURN_GAP_MAX_S` + `READ_HORIZON_S` = 1,200 s from the start of a run to its anchor.

So either the bucket is final without the return credit SM gives it, or a final bucket changes.

## What WP-SC1 does

WP-SC1 derives slots, anchors and the returned runs, not buckets. Its own outputs stay pending for `PENDING_WINDOW_S` + `RETURN_GAP_MAX_S` + `READ_HORIZON_S` = 2,100 s behind the latest record, so they are decided before they are final. `pkg/score`'s `rules.settle` holds that sum.

## A rule to decide it

1. Lengthen the pending window to the rule's reach. D1's 900 s keeps 300 s for late records beyond the 600 s reading horizon; the same margin beyond 1,200 s gives 1,500 s. Witnessing (DESIGN 7.4) is unaffected, since it is about when the batch reached the server, not when the bucket became final.
2. Or cap the return rule so it fits the window: its anchor must come within `PENDING_WINDOW_S` of the run's start.
3. Or let a returned run's credit land in a later revision of a final bucket, with `rev` raised, as the pending window already allows.

## For Karim

1. Pick one of the three, or another rule. The first changes D1's number, the second SM 2.1's rule, the third what `final` means.

## Decision

Decided 2026-11-20 by the orchestrator, under Karim's delegation, with none of the three choices as written. The return rule counts its horizon from the start of the silence. A silent run is returned only if its first following anchor on record within `READ_HORIZON_S` (600 s) of the run's start is substantive and in the same project, with no lock, sleep or focus change in between (SM 2.1). An act is on record at the later of its own time and its event's `t`, so a typed command counts from its end. An act on record later doesn't count for the run, and the first one on record in time decides, so a Step per minute gives what one Step gives. For a teen: a quiet stretch counts only when the act that ends it is recorded within ten minutes of when you went quiet. `PENDING_WINDOW_S` and `REVEAL_POST_S` stay 900 s and no parameter value changes. `pkg/score`'s `settle` becomes `PENDING_WINDOW_S` and its `reach` becomes `READ_HORIZON_S`, the bound DESIGN 3.7 now sets for every rule.

Why the choices lost. 1: a 1,500 s window holds every minute pending for 25 minutes, and `REVEAL_POST_S` would have to grow with it, for a rule that allows 20 minutes from going quiet to acting, twice grit's 10-minute limit (PA 1.14). 2: it can leave late records 30 s. A run that starts 30 s into a minute has its first returned second in that minute, final 960 s after the minute starts, and the anchor may come at 930 s. 3: a final bucket that changes later isn't final.

The arithmetic above holds. Minute 10:01 is final at 10:17:00, and the prompt comes 210 s later. The rule reaches 1,200 s from the run's start, and 1,170 s from the first credited second at 10:01:00; 1,185 s is the general bound, with the shortest tail. The example also needs the answer to arrive after the last focused input at 10:00:30, and SM 2.7 needs a human act within 600 s of each returned reading second. Under the decided rule, the act that ends the run is that act. The worst case leaves 330 s for late records: a run starting 30 s into a minute, final 960 s after the minute starts, with its act on record by 630 s. A sample slowed to one a minute (DESIGN 6.2 D14) can show the input before a non-hook act up to two minutes late, which still leaves 210 s.

What it costs: a reply sent more than ten minutes after going quiet, such as eight silent minutes and then three minutes of typing, keeps its typing but loses the return credit, at most `RETURN_CREDIT`. No fixture moves. The only returned run in any fixture is 6.4's setup silence: its slots start at 23:30:15, the `/goal` comes 585 s later, and it credits 0 s either way.

## Left open

- SM 2.5's lookups: [the lookup look-ahead erratum](2026-11-20-lookup-lookahead.md). CC6 needs the same bound, since `REVEAL_POST_S`'s 900 s doesn't reach a long run's end bracket.
- SM 4.2's discounts, for WP-SC6. A turn's rewrite-loop discount rests on its own edits, which can come long after the lead it withholds. A change event's record, such as a typed command's at its end, can come after a prompt it should free from a loop.
- Acts recorded after the fact. The bound costs a typed command its lead once it runs longer than nine minutes, its watch from ten, and everything but its trail from fifteen, because `shell.command` is written when it ends. It costs a permission answer its wait when its record, paired with the next tool event, comes more than eight minutes later. The v1.5 shell integration can record the command at its start mark (OSC 133 or 633), and the Claude Code adapters can date the answer from a record written at the answer, such as OTel's `claude_code.tool_decision` (HS 3.3).
