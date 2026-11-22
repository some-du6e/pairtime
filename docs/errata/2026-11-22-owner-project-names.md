# Erratum: DESIGN 4.3 and 4.4 have no row for a project name the owner shows on their own dashboard

Date 2026-11-22. Raised on 2026-11-22, when the live server's owner shared a screenshot of their own dashboard, whose nine projects were named by keys cut short. Status: decided 2026-11-22 by the orchestrator for Karim (D-owner-names). Applied to DESIGN 4.3 and 4.4 in the same PR as this erratum.

## Input

- The server never learns a project's name. The owner's dashboard calls each project by its key, and from #180 by a short code such as "Project 7c41e9". Both are opaque by design: keys are HMACs under the device's secret (DESIGN 4.3, AGENTS.md).
- On their own dashboard the owner wants their own names: they know which project "chip8-emulator" is, and nobody knows which one "Project 7c41e9" is.
- DESIGN 4.3 has a publishing row, "a project's display name and repo URL when made public", sent "only when the teen clicks Publish". Nothing covers a name shown to its owner alone.
- DESIGN 3.5 puts settings in `config.toml`, and 3.9 lists "project names and visibility" among the local app's settings.

## Expected

The owner can choose to see a project's name on their own dashboard, project by project, from their computer. Nobody else sees it, and nothing else about the project leaves the computer with it.

## What changes

- A project's name can show on its owner's own dashboard. It's off by default, and the owner turns it on per project, on the computer. It is the private case of DESIGN 4.3's publishing row: the same kind of text, shown to one person.
- What leaves the device is a label the owner saw and approved, and the project key it names, nothing else. The label is cleaned the way the display name is (D-display-name): runs of spaces and control characters become one space, formatting characters such as the bidirectional overrides are dropped, and at most 64 characters stay. The default label is the name pairtime shows for the project locally. For a git repo with worktrees, that's the main checkout's folder name, never a worktree folder's. It is never a path, a remote URL, a branch name, or anything else.
- Where it shows: the owner's signed-in dashboard and `GET /api/v1/users/me/dashboard`, and nowhere else. Never on a public page, a program read, an export for anyone else, or /demo.
- How it travels: a request signed with the device key, like ingest (DESIGN 4.5). Turning a name off sends a delete, and the server drops the row at once. The computer sends a name when the owner turns it on or changes its label, and doesn't send it again on its own, so a name hidden from the dashboard stays hidden until the owner turns it on again on the computer.
- The dashboard has a "Hide this name" button that deletes the row. The website can hide a name, but it can never set or change one.
- The server keeps the row until the owner turns the name off, on the computer or the dashboard. The device's purge, 90 days after the device is removed, deletes it with the device's other project rows (D-project-map-lifetime), and so does account deletion.
- The name isn't an event field, isn't in the ledger and isn't in tier-1 uploads, so `TestLocalFields` and `TestTier1Shape` don't change.

## Proposed wording

DESIGN 4.3, a new row after the publishing row:

| From, to | When | Exactly what | Never in it |
|---|---|---|---|
| Client to `pairtimed`, the owner's project names | Only when the teen turns a project's name on, changes its label, or turns it off, one project at a time on the computer; off by default | The label the teen saw and approved and the project key it names, in a request signed with the device key like ingest (4.5), or a delete when the teen turns the name off. The label is at most 64 characters, cleaned as the sign-in's display name is (4.4), and by default it's the name pairtime shows for the project locally: for a git repo with worktrees, the main checkout's folder name. It's the private case of the publishing row: shown only on the teen's own signed-in dashboard and `GET /api/v1/users/me/dashboard`. The dashboard can hide a name but never set or change one | Paths, remote URLs, branch names, worktree folder names, or any name the teen didn't approve. The label is no event field, ledger record or tier-1 field |

DESIGN 4.4, a new server row after "Commit records, project fingerprints":

| Where | Data | Kept |
|---|---|---|
| Server | Project names the teen turned on: the label and the project key it names, shown only on the teen's own signed-in dashboard and its JSON, never on a public page, a program read, an export for anyone else or /demo | Until the teen turns the name off on the computer or hides it on the dashboard, either of which deletes the row at once; the device's purge 90 days after the device is removed, with its other project rows; or account deletion |

## For Karim

The orchestrator decided this for you, so nothing waits on you here. The client, the signed endpoint and the dashboard's name and "Hide this name" button come in a code PR after this one, with the `retention.json` entry that TestDesignPeriods holds to the 4.4 row above.
