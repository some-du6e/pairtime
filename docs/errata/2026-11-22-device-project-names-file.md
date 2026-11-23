# Erratum: DESIGN 3.5 and 4.4 don't list the device's record of its project names on the server

Date 2026-11-22. Raised by the PR that builds D-owner-names (docs/errata/2026-11-22-owner-project-names.md). Status: decided 2026-11-22 by the orchestrator for Karim. Applied to DESIGN 3.5, 4.3, 4.4 and 8.8 in the same PR as this erratum, which also lists names requests in the privacy views.

## Input

- The client now keeps `project-names.json` in its data directory. The uploader writes it, user-only, after each answer from `POST /api/v1/projects/names` (docs/spec/project-names-v1.md). It holds the labels the server last confirmed for the device's project keys, the short codes the server gave those keys, when the server last answered, the keys the server still lists as hidden on the dashboard until the device's delete acknowledges them, and the names the owner hid there, each with the label it showed and when the computer found it hidden. `pairtime projects` and the local dashboard read it. `pairtime uninstall` deletes it.
- DESIGN 3.5 lists the agent's files, and DESIGN 4.4 what the device keeps and for how long. Neither has this file, so docs/retention.json has no device entry for it, and TestDesignPeriods would refuse one with a period.
- The labels in a names request are text the owner approved, so they are content leaving the computer. DESIGN 3.9 says the local Privacy page shows what left the laptop, and `pairtime privacy show` prints the next upload. Neither, and no line in `uploads.jsonl`, shows a names request yet.

## Expected

Every file the agent keeps has a row in DESIGN 3.5 and a retention period in DESIGN 4.4. The owner can see every label pairtime sent, as they can see an upload.

## Proposed wording

DESIGN 3.5, a new row:

| File | Format | What it holds | |
|---|---|---|---|
| `project-names.json` | JSON with user-only permissions | What the server last confirmed of the device's project names (D-owner-names): the label by project key, the server's short code for each key, when it answered, the keys it still lists as hidden, and the names the owner hid on the dashboard | |

DESIGN 4.4, a new device row:

| Where | Data | Kept |
|---|---|---|
| Device | The server's confirmed project names and short codes, and notes of names the owner hid on the dashboard, in `project-names.json` | Until the next answer from the server replaces them; a hidden name's note until the owner turns the name on again; all of it until uninstall |

A follow-up, after the decision: `pairtime privacy show`, `uploads.jsonl` and the local Privacy page list each names request, with the labels it carried.

## Decision

Decided 2026-11-22 by the orchestrator for Karim, in #196's review (#200), and applied in #196 under `oracle-change`:

- DESIGN 3.5 gets the row for `project-names.json` in its own columns (Path, Format, Holds, Kept), with what the file holds: the sign-in it answers, the labels, the codes, the hidden keys, the names and keys requests carried since the server last answered, until an answer settles them (`sent` and `deleting`), and each note's label, time, reason and whether pairtime could turn the name off. DESIGN 4.4 gets the device row with the file's limits: a note until the server shows the name again after the teen turned it on, 1,000 notes at most, oldest first, and only the notes kept across a sign-in to another server or device. docs/retention.json gets `device.project_names`, with no days.
- A names request's line in `uploads.jsonl` holds what an upload's does, never a label: when, which server, how many names it set and deleted, the result code, and the body's SHA-256, for every request the client sent, answered or not. DESIGN 3.5 and 4.4's rows for `uploads.jsonl` say so. `pairtime privacy show` prints the next names request that carries a name byte for byte, labels included, and DESIGN 8.8 says so. This replaces the follow-up above, whose "with the labels it carried" would put a second, 90-day copy of labels in `uploads.jsonl`.
- DESIGN 4.3's names row says when a request goes: a set only when the owner turns a name on or changes its label, a delete when they turn it off or after the computer turned off a name the server lists as hidden, and a read with no name in it when the agent starts, once a day, and first whenever the computer doesn't know what the server holds.
- DESIGN 4.4's server row for project names is amended: hiding a name deletes the label at once and keeps the project key and the time of the hide until the device turns the name off too, 30 days after the hide at most (`server.project_name_hides`), and the device's purge follows the 90-day rule for a removed device's own row.

The decided erratum docs/errata/2026-11-22-owner-project-names.md keeps its text, but its line 23 cites the wrong rule for the purge: D-project-map-lifetime keeps a device's `rollup_projects` rows for the account's lifetime, so nothing else of the device's projects goes at 90 days. The purge follows the 90-day rule for the removed device's own row (DESIGN 4.4's `device_id` row, `server.devices`), as the spec, migration 0008 and DESIGN 4.4's names row now say.

## For Karim

The orchestrator decided this for Karim; Karim can overrule it. The file holds nothing that isn't already on the computer: the labels are in `config.toml`, and the codes are the dashboard's.
