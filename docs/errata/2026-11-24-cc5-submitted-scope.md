# Erratum: CC5 fails a submission over slots that only its unsubmitted projects competed for, which the freeze's `concurrent_devices` leaves out

Date 2026-11-24. Raised by the spec read of #310 at b47b94fe (#342). Status: open, for the orchestrator to decide for Karim. Until then DESIGN 7.3's CC5 row and D-sb1-submissions Q8 stay as they are (DESIGN 6.6).

## Input

- docs/errata/2026-11-19-check-readings.md, reading 7, "The scope of CC5": the fixtures "take every device and every project of the account in the window", which is what "across projects" and SM 3.4's one human-minute per wall-clock minute say. Its decision of 2026-11-20 let readings 1 to 11 stand as DESIGN 7.3's text, and #315 wrote CC5's row (DESIGN.md:891): "No slot credited twice across the account's devices and projects in W, submitted or not (SM 3.4)". #315 also had DESIGN 6.5's submission preview tell the teen "that CC5's result names each slot two devices or projects both credit, because CC5 compares them (7.3, 7.8)" (DESIGN.md:808). A failed check changes "review priority and the band, never the credited number" (DESIGN 7.3).
- D-sb1-submissions Q8, applied to WP-SB1's Build (PLAN.md:912): the freeze computes `concurrent_devices` over the frozen windows, "counting only the minutes in which two devices credited one slot and at least one of them credited it to one of P's projects (D-no-ingest-flags; DESIGN 8.5)". WP-SB1's CI checks (PLAN.md:913): "two devices crediting one slot to two projects outside P raise no concurrent_devices". Q8's reason is that fraud checks run only on the hours a teen submits: DESIGN 8.5 (DESIGN.md:1093) runs fraud scoring "only on windows a teen submits, never on logged-only hours", and the privacy notice (internal/server/pages/privacy.md:85) says "Checks, risk scores and audits, and only on hours you submit. pairtime doesn't score your other hours for fraud."

## What goes wrong

Inside W, the laptop credits slot S to project X and the desktop credits S to project Y, and neither is in P. The freeze raises no `concurrent_devices`, as Q8 rules. CC5, run on the same submission's reveal, fails it on S. That failure moves the submission's review priority and band, is one of the failed checks that raise its audit probability (7.9), and makes the row a must-view row (7.7 item 2), all worked out from X's and Y's hours, which nobody submitted. Q8 kept such hours out of the flag because the flag would feed the audit probability and show to the reviewer.

The two scopes part only over such slots. A slot where one of the credits is to a project in P counts under both.

The one CC5 fixture, testdata/checks/4.2-cc5-second-device, has no such slot. The Mac credits only `p_b2738424d7c2`, the submission's one project, so each of the 19 slots credited twice has a credit in P, and its verdict, CC5 failing on 19 slots, holds under either scope. No fixture pins the case above.

## Expected

One reading of what a submission's fraud checks may count, for the freeze's flag and for CC5, that keeps DESIGN 8.5 and the notice's promise, or a stated reason why CC5 may count more than the flag.

## Options

1. **CC5 takes Q8's scope.** CC5 fails a slot credited twice across the account's devices in W only when at least one of the credits is to a project in P. DESIGN 7.3's CC5 row changes, and so does 6.5's preview sentence: CC5's result then names only slots with a credit in P, or, if it still names the others as passes, the reviewer sees X's and Y's collisions (7.7 item 7, 7.8's `checks.json`). Reading 7 is amended to match. No fixture verdict changes.
2. **`concurrent_devices` takes CC5's scope.** The freeze counts every minute of W in which two devices credited one slot, whatever the projects, and WP-SB1's Build and CI check change. Q8's own scenario then puts a tier-2 flag on a submission from hours nobody submitted, so DESIGN 8.5 and the notice's "pairtime doesn't score your other hours for fraud" would need new wording.
3. **Both stand, and DESIGN says why.** CC5 checks the submission's own evidence, and a slot of W credited twice is evidence about W's attention whatever project it went to. That reason has to get past Q8's objection ruling, which turned a close argument down for the flag ("it doesn't make B's and C's hours evidence about A"), and a failed CC5 reaches the audit probability and the reviewer as the flag would (What goes wrong). DESIGN 7.3's row already counts unsubmitted projects in W, and 6.5 already tells the teen so at the preview. So this option needs the reason itself, and new wording in 8.5 and the notice, which Q8 read as keeping fraud checks off hours nobody submitted.

## Decision

Open.
