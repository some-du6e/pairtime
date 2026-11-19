---
method: described
captured_on: 2026-11-19
captured_by: WP-PG0
source: https://hackclub.gitbook.io/ysws-project-submission-guidelines/BLBRN8LIfoCZhFV6oMNR/override-hours-spent-justification.md none retrieved 2026-11-19
source: https://hackclub.gitbook.io/ysws-project-submission-guidelines/BLBRN8LIfoCZhFV6oMNR/override-hours-spent.md none retrieved 2026-11-19
source: https://hackclub.gitbook.io/ysws-project-submission-guidelines/BLBRN8LIfoCZhFV6oMNR/required-submission-fields.md none retrieved 2026-11-19
source: https://hackclub.gitbook.io/ysws-project-submission-guidelines/BLBRN8LIfoCZhFV6oMNR/spot-checks.md none retrieved 2026-11-19
---

# The YSWS handbook, retrieved 2026-11-19

Quality and Integrity's YSWS Project Submission Guidelines (`https://hack.club/ysws-rules` redirects there) say what a program must put in the Unified YSWS DB and how to justify hours (HC 3.7). The four pages above were fetched as Markdown with `curl` at 2026-11-19T20:41:54Z, by adding `.md` to each page URL as GitBook allows. Each says "Last updated: March 2026". The site states no license, so `fields.json` keeps the handbook's words only for field names and the date-range example, and paraphrases everything else. `pages[].sha256` is the hash of each page as fetched, so a later reader can tell whether the handbook has changed since.

| Section of `fields.json` | From |
|---|---|
| `submission_fields` | Required Submission Fields: the fields every submission needs, and the two needed only sometimes |
| `justification` | Override Hours Spent Justification: how the justification fields combine, the standard they're held to, and the seven fields with when each is included and who writes it |
| `hours_rules` | Override Hours Spent: updates, default to deflation, outright fraud, AI slop, the one-third rule with its worked example, the 30% cap on art and assets |
| `spot_checks` | Spot-Checks: the verdicts, the four grounds that bear on hours and evidence, and resubmission |

WP-PG1 builds the handbook text from these names and rules. Its golden example in DESIGN 9.3 applies `one_third`: 9.1 h logged with 7.4 h of it ai coding allows at most 1.7 h + 7.4 h / 3, about 4.2 h. WP-PG2 maps the justification fields to the wrangler base's columns, whose names differ (`../airtable/PROVENANCE.md`).

The Hackatime date-range format is the handbook's, but the handbook doesn't say which time zone its dates are in. Ari writes them in UTC (HC 3.6), and DESIGN 9.3 does the same.
