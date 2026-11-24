# Erratum: a `track` label's class comes from its length alone, and DESIGN 5.4 and ledger-v1 10.7 class it by its content too

Date 2026-11-20. Raised by the fix-up for issue #99, from the spec review of #94 (WP-LG5). Status: decided 2026-11-23 by the orchestrator for Karim: option 1, by content. A track anchor is classed like a prompt from its label after pkg/redact, with text_len as the typed length, and never as answer; cli.track gains a shareable low_effort and a local error_sig (DESIGN 4.1 E-15), and its label keeps its line breaks as LF.

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

## Decision

The orchestrator decided this on 2026-11-23 under Karim's delegation: option 1, with three refinements. A `track` call is never `answer`, because its session has no hook or log record before the call that could show a question. `cli.track` gains a shareable `low_effort` and a local `error_sig`. The ledger writer derives both from the label after `pkg/redact`, as it does a prompt's from its text, and the scorer reads `error_sig` back from the record's `f.es` (DESIGN 4.1 E-15). The label keeps the message's line breaks as LF. On one line, a label had an error signature only when it started with the error line, and the signature then ran to the label's end, so `still broken` before an error was never an error paste. No tag from v0.1.0-alpha.1 to alpha.6, and not main, registers a `track` command. Nothing in `cmd/` or `internal/` builds a `cli.track` event: `KindCLITrack` appears only in the ledger writer's `prepare` and `kept` switches and in `recover.go`'s kind list. A non-hook inbox message gets only a journal line. So no chain a released client wrote holds a `cli.track` record, and `f.v` stays 1. The deadline is WP-AD18's first release; after it, a change to this rule takes `f.v = 2`. One gap remains. A label has no paste boundary, so `text_len` counts a pasted error, as `typed_len_est` does in a prompt whose harness doesn't mark pastes. So `still broken` above an error line is `substantive` once the two reach `ERROR_PASTE_TEXT_MAX` characters, though its `es` is kept. docs/errata/2026-11-23-error-paste-typed-length.md raises that question for prompts and labels alike. No SM worked number and no fixture number moves. The keep-going call's lead drops from 140 s to 20 s, so scoring takes a MINOR (DESIGN 6.6).
