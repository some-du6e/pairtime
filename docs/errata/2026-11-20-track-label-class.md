# Erratum: a `track` label's class comes from its length alone, and DESIGN 5.4 and ledger-v1 10.7 class it by its content too

Date 2026-11-20. Raised by the fix-up for issue #99, from the spec review of #94 (WP-LG5). Status: waiting for a decision. Until it is decided, the ledger writer keeps recording `pkg/score`'s length class as a `cli.track` record's `f.pc`, and the decision has to come before any device's records are witnessed, because ledger-v1 10.7 fixes what a version 1 record's `pc` means for good.

## Input

- DESIGN 5.4, on a fallback `track` anchor: "The anchor is classed like a prompt from its text length and content (SM 2.2)."
- ledger-v1 10.7: "`pc` is SM 2.2's class of the text, as the writer computed it when it wrote the record ... SM 2.2 and `pkg/score` own that rule, and CC8 recomputes it for challenged turns. `cli.track` carries the prompt features because DESIGN 5.4 classes a `track` anchor 'like a prompt from its text length and content', while the server re-scores from bodies, never from text."
- SM 2.2's classes: `low_effort` for a prompt whose normalized text is under 4 characters or on the go-ahead list, which has `keep going`; `error_paste` for an error signature with fewer than 40 typed characters outside it; `answer` after an output that asked a question; `substantive` from `SUBSTANTIVE_CHARS` = 20 typed characters.
- `pkg/score.TrackClass` reads only `text_len`: `substantive` from 20 characters, `low_effort` under that. Its comment gives the reason: "The label is local, so the server's exact re-score never sees it, and the call is classed by its length alone (DESIGN 6.5)." `trackGate` classes the anchor the same way. `pkg/event`'s `CLITrack` keeps `text_len` shareable "so the length the anchor was classed by (DESIGN 5.4) survives Shareable", and has no shareable field from the label's content. The writer records `TrackClass` as `f.pc` (#94).
- `pkg/score/class_test.go`, `TestTrackClass`: "a call is classed by its length, not its label's words".

## Expected

Read as DESIGN 5.4 and 10.7 write it, `pairtime track "keep going!!!!!!!!!!!!!!!!!!!!"` (30 characters) is `low_effort`, because it normalizes to `keepgoing`, on the go-ahead list. `pairtime track "TypeError: x is undefined"` (25 characters, all of them the error line) is `error_paste`.

## Computed

Both are `substantive`, in the scorer's anchor and in the record's `f.pc`. So the go-ahead gets `THINK_LEAD_S` instead of `LOW_LEAD_S`, and SM 4.2's fix-it rule, which discounts `error_paste` and `low_effort` turns, can never count a `track`-anchored error paste once WP-SC6 reads `f`. CC8 recomputes the class from a challenged turn's revealed label (DESIGN 7.3), so it would fail an honest `track` call whose label's content and length disagree.

## Why the writer doesn't change alone

`f.pc` is what the server's re-score and CC8 read. If the writer recorded the content class while the device's scorer kept classing the anchor by length, the live minutes and the re-score from the same records would differ for every such label, which CC6 compares, or the re-score would have to ignore `f.pc` for `track` records, and then 10.7's rule means nothing. The scorer can't class by content from what it sees: DESIGN 3.7 has it step over sealed ledger records, and a `cli.track` body carries `text_len` and nothing from the label. So classing by content takes the event model, the scorer and the writer changing together, and classing by length takes DESIGN 5.4 and 10.7 saying so. Both change an oracle, so neither fits a code fix-up.

## For Karim

Pick one before alpha.

1. **By content, as DESIGN 5.4 says.** `cli.track` gets the shareable `low_effort` and `error_sig` that `prompt.submit` has (SM 1.2, `pkg/event`, the schema), which the writer derives from the redacted label as it does for a prompt. `TrackClass` takes them and applies SM 2.2's rules with `text_len` as the typed length, and `trackGate` and the writer both use it, so `f.pc` and the anchor agree and CC8 recomputes the class from the revealed label. `answer` would almost never apply, since a `track` anchor's session has no hook or log records to have asked a question. This is the change I'd make: a go-ahead sent through `track` shouldn't earn a substantive prompt's lead, and an error paste is the thing SM 4.2's fix-it rule exists to see.
2. **By length.** DESIGN 5.4's sentence becomes "classed from its text length: `substantive` from `SUBSTANTIVE_CHARS` characters and `low_effort` under that", and 10.7 adds that a `cli.track` record's `pc` is that class, which CC8 recomputes from `text_len`, not from the revealed label. Nothing in the code changes, and `TestTrackClass` already pins it.
