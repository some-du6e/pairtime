# Erratum: install/ keeps copies of the files install edits, which can hold the teen's API keys and tokens, and no DESIGN row says how long

Date 2026-11-24. Raised by WP-IN3 (#306), when its `install/wakatime.json` setup file needed a DESIGN 4.4 row. Status: decided 2026-11-24 by the orchestrator for Karim (D-install-retention).

## Input

- DESIGN 3.5's table lists what the device keeps, with each file's format, what it holds and how long it is kept. It has no row for `install/`. Its last row, `config.toml` and `port.json`, has an empty Kept.
- DESIGN 4.4: "Every field has a finite retention period, published at `https://<host>/privacy`". Its device rows are prompt text, transcripts, file paths and tool commands, presence and focus, ledger records, minute buckets, held hook calls, commit records, records of past uploads, project names and `K_dev`. docs/retention.json's device entries are the same eleven, and both privacy notices render them. None names `install/`.
- DESIGN 8.8 says that "Delete everything" wipes the data directory and `K_dev`.
- On main, since WP-IN1, `<data>/install/` holds:
  - `manifest.json`: each file and registry value install wrote, and what it wrote;
  - `backups/`: a byte-for-byte copy of what each of them held before, such as the whole of Claude Code's `settings.json`, whose `env` block can hold tokens;
  - `setup.json`, which cmd/pairtime writes: where install put the binary and which folders it made.
- #306 adds `install/wakatime.json`, with the key of pairtime's line in `~/.wakatime.cfg` and that file's path, and a copy of `~/.wakatime.cfg` in `backups/`, which holds the teen's Hackatime `api_key`.
- `pairtime privacy delete` keeps `install/` (`wipeKeeps`, cmd/pairtime/wipe.go), and its usage says only that "pairtime itself and your settings in config.toml stay". `pairtime uninstall` reads the manifest and the copies to put the teen's files back, then deletes `install/` with the rest of the data directory. An uninstall that finishes deletes `install/` on every path, also when it keeps the day rollups and coach records and also with a target it couldn't put back. One that stops before it has deleted the data, as when the agent keeps starting or the device key can't be deleted, leaves `install/` until it runs again.

## Expected

Every kind of data pairtime keeps has a DESIGN 3.5 row and a published retention period in DESIGN 4.4 and docs/retention.json, and what "delete everything" leaves is said where it is described.

## Computed

Nothing names `install/`. The privacy notices never tell the teen that pairtime keeps copies of the files it edited, or that those copies can hold API keys and tokens the teen never gave pairtime. DESIGN 8.8 says delete everything wipes the data directory, but `install/` stays, and so does `config.toml` where it is in the data directory.

## Decision

`install/` is kept until uninstall, which takes out what install put in and then deletes it. "Delete everything" keeps it, so that uninstall can still undo install's edits: delete everything is about what pairtime recorded, not about what it changed outside its folders. A copy is never readable more widely than pairtime's other private files, even when the original was.

1. DESIGN 3.5 gets this row:

| Path | Format | Holds | Kept |
|---|---|---|---|
| `install/` | JSON written by atomic rename, and byte-for-byte copies; user-only: the folders 0700 and every file 0600, whatever the original's mode, which the manifest keeps for uninstall | What `pairtime install` changed outside pairtime's folders (5.9). `manifest.json`: each file and registry value install wrote, and what it wrote. `backups/`: a copy of what each held before, which can hold the teen's API keys and tokens, such as the Hackatime key in `~/.wakatime.cfg` or a token in the `env` block of Claude Code's `settings.json`. `setup.json`: where the binary went and which folders install made. `wakatime.json`: the key of pairtime's line in the WakaTime config and that file's path (5.6) | Until uninstall, which takes out what install put in and then deletes it; "delete everything" keeps it (8.8) |

2. DESIGN 4.4 gets this Device row, before `K_dev`'s:

| Where | Data | Kept |
|---|---|---|
| Device | What `pairtime install` changed outside pairtime's folders, in `install/`: the manifest of each file and registry value it wrote and what it wrote; a copy of what each held before, which can hold the teen's API keys and tokens, such as the Hackatime key in `~/.wakatime.cfg`; where the binary went and which folders install made; and the key of pairtime's line in the WakaTime config with that file's path. Owner-only files in an owner-only folder | Until uninstall, which takes out what install put in and then deletes them; "delete everything" keeps them, so uninstall can still do that (8.8). An uninstall that stops before it has deleted pairtime's data, as when a file changed while it was putting it back, the agent keeps starting, `K_dev` can't be deleted or another program has one of pairtime's files open, leaves them until it runs again. One that finishes deletes them on every path, also when the day rollups and coach records stay, and also with a file it couldn't put back, which it names |

3. DESIGN 8.8's bullet names what stays:

> "Delete everything" wipes the data directory and `K_dev`, but keeps pairtime itself, its settings (`config.toml`) and install's record of what it changed (`install/`), which uninstall needs to put the teen's files back (4.4). Local-only mode keeps every coach feature and uploads nothing (4.2).

4. docs/retention.json gets a `device.install` entry in the teen's words, with no days and no digit in its kept text. It says who can open the folder as `device.held_calls` says it: "in a folder other people's accounts on this computer can't open, though an administrator can".
5. `install/` and `backups/` are 0700 and every file in them 0600, whatever the original file's mode. The manifest keeps the original's mode, which uninstall puts back.
6. `pairtime privacy delete`'s usage says that install's record of what it changed stays too, with its copies of the files it edited, so that uninstall can put them back.

`config.toml` and `port.json`, with its bearer token, have no DESIGN 4.4 row either. That gap is outside this decision and goes to WP-ERR1.
