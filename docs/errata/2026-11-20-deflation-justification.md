# Erratum: DESIGN 9.3 drafts Deflation Justification, which the handbook leaves to a person

Date 2026-11-20. Raised by the review of #13 (WP-PG0) in issue #44, written in PR #55. Status: waiting for Karim. DESIGN 9.3 stays as it is. `testdata/upstream/airtable/fields.json` now marks the field as the reviewer's, which is what the handbook says.

## Input

The handbook's Override Hours Spent Justification page has a table of the justification fields that says, for each one, when to include it and whether it can be automated. Deflation Justification goes in when the reviewer deflated the tracked hours, and the table asks for a person to write it, as it does for Specific Technical Features. The fields it says can be automated are the Hackatime project names and dates, the Hackatime id and the Lapse links. `testdata/upstream/handbook/fields.json` records the page as retrieved on 2026-11-19, with `written_by: human` for this field. Fetched again on 2026-11-20, the page has the same SHA-256 (`1bff585e...`).

DESIGN 9.3's field table fills Deflation Justification with "a factual draft from the reason codes (...) that the reviewer confirms or rewrites", and the review page shows that draft (DESIGN 7.7 item 11). WP-PG1 puts the draft in the justification endpoint. WP-PG2's Airtable script writes the mapped fields from that endpoint, and its CI check has "every mapped field filled from the golden justification".

Until PR #55, `airtable/fields.json` marked the wrangler column `Justification - Deflation Justification` as `filled_by: pairtime`. Its schema defines that as text from the justification endpoint, written by the Airtable script or pasted by the reviewer.

## Expected

The field holds text a person wrote.

## What DESIGN gives

Text pairtime wrote. A reviewer who confirms the draft sends pairtime's sentence under their own name. Nothing in DESIGN 9.3 or WP-PG2 ties the script to that confirmation either, so a default mapping that includes the field can write the draft into Airtable before anyone has read it, which DESIGN 7.7 doesn't intend.

The draft also describes a different deflation. The handbook's examples give a reviewer's reasons for cutting hours, such as features that don't match the submitter's experience. pairtime's draft says what pairtime's scoring left out of Hackatime's hours, which the Alternate Tracking Method text already says ("For this window Hackatime logged 9.1 h ... pairtime credited 5.9 h").

## What the capture does now

`airtable/fields.json` marks the column `filled_by: reviewer`, like Specific Technical Features. Its note says the handbook asks for a person, that DESIGN 9.3 has pairtime draft the text for the reviewer, and that until this is settled the Airtable script leaves the field out of its default mapping while the reviewer sees the draft on the review page. `airtable/PROVENANCE.md` names it as the one field DESIGN 9.3 fills that isn't marked `pairtime`. WP-PG2 takes DESIGN 9.3's table as its oracle along with this capture, so this erratum is what keeps the field out of its default mapping for now.

## For Karim

1. Does a draft the reviewer confirms unchanged count as written by a person?
   - No, which is how the capture now reads the handbook. DESIGN 9.3's row becomes "written by the reviewer; the review page shows pairtime's factual draft", and WP-PG2 leaves the field out of its default mapping.
   - Yes. Then the reviewer confirms or edits the draft on pairtime's review page before the endpoint returns it, so the script never writes an unconfirmed draft. DESIGN 9.3 and WP-PG1 gain that step, and `airtable/fields.json` goes back to `filled_by: pairtime` in a PR of its own.
2. Either way, the week-one proposal to Quality and Integrity (DESIGN 9.4) can ask them, since the handbook is theirs.
