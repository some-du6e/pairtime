# Erratum: `error_paste` counts a pasted error line as typed text when nothing marks the paste

Date 2026-11-23. Raised by E-track-label-class. Status: waiting for a decision.

## Input

- SM 2.2's `error_paste`: "an error signature (4.2) and fewer than 40 typed characters outside it".
- SM 8.1's `ERROR_PASTE_TEXT_MAX`: "Typed text outside a pasted error".
- `pkg/score`'s `promptClass` compares `typed_len_est` with `ERROR_PASTE_TEXT_MAX`, and its comment assumes the error is in `pasted_chars`.
- DESIGN 5.4 classes a `track` label with `text_len` as the typed length.

## Computed

A prompt whose harness didn't mark the paste, and every `track` label, counts the error line as typed. So `still broken`, a line break and `TypeError: x is not a function` (43 characters) is `substantive`, and SM 4.2's fix-it rule never discounts it.

## For Karim

1. Keep the count, and have SM 2.2 say `typed_len_est`.
2. Have the writer count the characters outside the text's error lines and carry that count, shareable, on `prompt.submit` and `cli.track` for the class to read. That is a new event field and a scoring MINOR.
