---
method: described
captured_on: 2026-11-19
captured_by: WP-PG0
source: https://github.com/hackclub/data-warehouse/blob/4ff995dddcb74d82b4cd9e078e851317397adc6a/orpheus_engine/defs/airtable/generated_ids.py none
---

# Airtable field names at data-warehouse 4ff995dddcb7

Hack Club's data warehouse keeps a generated list of the Airtable bases it reads, `orpheus_engine/defs/airtable/generated_ids.py` (git blob `b7fe2e97f6ac`), with every base, table and field and its Airtable id. hackclub/data-warehouse has no license in its repository metadata, so `fields.json` holds facts only: base, table and field names, the lines they're on, and our notes on who fills each field. Field ids are left out; the base and table ids identify where the names come from and are in HC 3.1 and 3.7 already.

A script read every name from its line in the fetched file, so the names are exact, the en dash in `YSWS–Weighted Project Contribution` included. The file lists no field types, so `fields.json` gives none.

Two tables:

- **`wrangler` / `ysws_project_submission`**, a program's own submission table. It has the handbook's justification fields as separate columns, the `Automation - Submit to Unified YSWS` button, and the automation fields that report what happened. DESIGN 5.10 makes these names the defaults in the Airtable script's field mapping (WP-PG2), and WP-PG12's sandbox base copies them. 27 of the table's 46 fields are here. The other 19, the survey, the submitter's personal and shipping details and the mailing-list fields, are fields pairtime never writes.
- **`unified_ysws_projects_db` / `approved_projects`**, the Unified YSWS DB rows that HQ's automation copies approved projects into. Only the seven fields that hold or explain a row's hours are here, because DESIGN 9.2's counting rule reads `Override Hours Spent Justification`.

`filled_by` says who writes each field: the program, its reviewer, Hack Club's automation, or pairtime. pairtime marks the fields DESIGN 9.3's table fills from the justification endpoint; the program's script or reviewer writes them. `handbook` gives the field's name in the handbook (`../handbook/fields.json`) where the two differ, as they do for every justification field: the table prefixes `Justification - `, writes `+` for `and`, and calls `Lapse Link(s)` `Lapse Links, comma-separated`.
