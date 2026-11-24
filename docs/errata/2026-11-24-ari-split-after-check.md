# Erratum: DESIGN 5.10's last fallback for a long Ari ship splits a submission that 7.5 no longer lets anyone split

Date 2026-11-24. Raised by the spec read of #310 at b47b94fe (#342). Status: open, for the orchestrator to decide for Karim. Until then DESIGN 5.10 and 7.5 stay as they are (DESIGN 6.6).

## Input

- DESIGN 5.10, the Ari journals bridge (DESIGN.md:707): Ari pays every `journals[]` entry in full unless its reviewer cuts it, an entry holds at most 86,400 whole seconds, and "a ship holds at most 200 entries (`testdata/upstream/ari/`)". The bridge sends "one entry per row of a checked submission (D13; 7.5 step 9)", and devlog entries a program adds at 0 s "count toward the 200".
- DESIGN 5.10's next paragraph (DESIGN.md:709): "A ship that would pass 200 entries gets one entry per UTC day instead", and on a ship with collaborators "the fallback is one entry per person per UTC day". Then: "If that still passes 200, the bridge refuses, and the program splits the window into two submissions." WP-PG8 builds the bridge with "an error past that" (PLAN.md:1106).
- DESIGN 7.5 step 1, as #310 applies D-sb1-submissions (b) (DESIGN.md:919): "The server rejects a submission whose windows overlap those of another submission of the same teen that covers one of the same project keys, was frozen and wasn't withdrawn, and went to any program that takes hours, the same one included. A withdrawal frees those windows, and a decision, whatever its outcome, or a revocation doesn't."
- DESIGN 7.5 step 5, as #310 applies D-sb1-submissions (d) (DESIGN.md:923): "A submission can be withdrawn only until step 9 starts. Once its checks, risk score and audit draw have run, and after a decision, it can't be". The bridge works from a checked submission, so the one it refuses is past step 9.

## What goes wrong

A program that follows the fallback freezes two submissions, each over part of the refused one's window, on the same projects and to the same program. Each overlaps the refused submission's frozen windows on a shared project key, so each gets `409 already_submitted` at its freeze. The refused submission is `checked` or later, so the withdrawal that would free its windows gets `409 not_withdrawable`. Nothing the program or the teen can do then gets those hours to Ari.

Two ships reach the limit:

- A teen's checked submission over the whole 180-day horizon, with credited rows on each of the 181 UTC days it touches, gives 181 day entries. A program that also lists 20 devlogs at 0 s brings the ship to 201.
- A ship of two people with credit on 101 UTC days each gives 202 day entries, one per person per day.

Before #310, step 1 rejected only "a second submission of the same project and window to another program", so two halves to the same program went through, and the split worked.

## Expected

A checked submission whose ship would pass 200 entries still reaches Ari, or is refused before step 9 locks it. Either way, 7.5's two reasons hold: no hour is paid twice (step 1), and no one re-rolls an audit or a reviewer (step 5).

## Options

1. **Split the ship, not the submission.** The program sends the checked submission's entries in two or more ships of at most 200 entries each, and the submission stays whole, so steps 1 and 5 hold as written. Ari would have to take two ships for one project, each under its own `external_id` (ari-webhooks answers `409 already_queued` to a ship sent under an open one's, per `testdata/upstream/ari/PROVENANCE.md`), and its reviewers would have to see the ships as one claim.
2. **An exception for a ship the bridge refused.** Step 1's refusal, or step 5's limit, gives way for a submission whose ship the bridge refused, so the program can freeze the two halves. The bridge runs in the program's code (`integrations/`), so `pairtimed` would have to learn of the refusal, such as through a program API call that records it. The halves would get new audit draws and reviewers, which step 5 exists to prevent, unless they carry the refused submission's draw, audit outcome and reviewer.
3. **Refuse at the freeze a submission whose ship would pass 200.** The freeze counts the UTC days its frozen windows touch, the most entries the per-day fallback can send, and refuses a submission past the limit, which then never freezes, so two smaller submissions can follow. The freeze doesn't know what else the ship will carry, since the program adds its 0 s devlog entries and its collaborators' entries when it builds the ship. So the program would state them at create, or the limit would leave room for them.

## Decision

Open.
