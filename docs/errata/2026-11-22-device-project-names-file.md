# Erratum: DESIGN 3.5 and 4.4 don't list the device's record of its project names on the server

Date 2026-11-22. Raised by the PR that builds D-owner-names (docs/errata/2026-11-22-owner-project-names.md). Status: waiting for a decision.

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

## For Karim

This waits for the orchestrator or Karim. The file holds nothing that isn't already on the computer: the labels are in `config.toml`, and the codes are the dashboard's.
