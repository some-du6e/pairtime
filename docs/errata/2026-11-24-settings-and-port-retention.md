# Erratum: config.toml and port.json have no retention period, and "delete everything" keeps the names the teen gave their projects

Date 2026-11-24. Raised by WP-ERR1, for the gap that D-install-retention left (docs/errata/2026-11-24-install-state-retention.md, #306): "`config.toml` and `port.json`, with its bearer token, have no DESIGN 4.4 row either. That gap is outside this decision and goes to WP-ERR1." Status: decided 2026-11-24 by the orchestrator for Karim (D-wipe-project-labels, as amended the same day after this erratum's first draft). WP-IN3's PR stacked on #306 will apply it under `oracle-change`: the code, DESIGN 3.5, 4.4 and 8.8, docs/retention.json and the privacy notices' texts.

## Input

- DESIGN 3.5's last row, for `config.toml` and `port.json`, says they hold "Settings, `.pairtime-ignore` patterns, opt-ins; the local port and bearer token". Its Kept cell is empty. The paths above the table are the data folders.
- DESIGN 4.4: "Every field has a finite retention period, published at `https://<host>/privacy`". No device row names either file, and no entry in docs/retention.json does either. #306, still open, adds the rows for `install/` under D-install-retention.
- DESIGN 8.8 on main says that "Delete everything" wipes the data directory and `K_dev`. #306 rewords that bullet, as of d47708d7:

  > "Delete everything" wipes the data directory and `K_dev`, but keeps pairtime itself, its settings (`config.toml`) and install's record of what it changed (`install/`), which uninstall needs to put the teen's files back (4.4).

- config.toml is in the config folder: `%APPDATA%\pairtime\` on Windows, which roams with the user's profile, and `${XDG_CONFIG_HOME:-~/.config}/pairtime/` on Linux. On macOS and under `PAIRTIME_HOME` it is in the data folder (internal/agent/home; CP 7). An edit of a config.toml that is a symlink, such as one into a synced folder, changes the file it points to and keeps the link (`editTarget`, internal/agent/config's edit.go). So another computer can share the file, and keeps its own projects' tables there under its own keys.
- config.toml (internal/agent/config) holds:
  - `mode`, and `[server] url`, the server `pairtime login` links to;
  - `[http] port`, the local port the agent tries first;
  - `[display] shadow`, calibration's shadow display (DESIGN 6.7);
  - `[notifications]`: whether they're on, and the quiet hours;
  - `[privacy]`: whether prompt text is kept, and for how many days prompt text and file paths are kept;
  - a `[projects.<key>]` table for each project the teen named for their own dashboard, with its label, `name`, and `online`. A label stays in the file while its name is off, so turning the name on again shows it again (D-owner-names);
  - whatever comments and unknown keys the teen wrote, which pairtime ignores and every edit keeps byte for byte. Such lines can hold a project's name (internal/agent/config's masked.go).

  The git adapter's exclude patterns are to come from config.toml as well (internal/adapters/git's `Excludes`; DESIGN 3.5, 8.8), but nothing passes it the ones in config.toml yet.
- port.json (internal/agent/localhttp's `PortFile`) holds `v`, the local HTTP `port`, the bearer `token` that opens the local API, and the agent's `pid`, which is 0 once the agent has stopped cleanly.
  - `home.WritePrivate` writes it, with mode 0600, or on Windows with a DACL that lets only the user open it.
  - Each start of the agent rewrites it and keeps the token. The agent makes a new token only when the file is gone, doesn't read, or holds no valid token.
  - Only pairtime's own commands send the token (`agentCall`, cmd/pairtime/setup.go). The local dashboard signs in with a one-time code over the per-user socket and a session cookie instead (internal/agent/localhttp's session.go), and `pairtime dashboard` reads only the `pid`.
  - No install step copies the token anywhere, although `PortFile`'s comment says the installer writes it into hook settings that can't read files.
- `pairtime uninstall` deletes both files with the rest of pairtime's data and config folders. It does so also when the teen keeps the day rollups and coach records, since only `days/` and `coach/` can stay (cmd/pairtime/uninstall.go).
- `pairtime privacy delete`, DESIGN 8.8's "delete everything", keeps config.toml (`wipeKeeps`, cmd/pairtime/wipe.go). It sets `mode` back to local-only when the file said online, as `pairtime logout` does, since the sign-in goes with the keystore and account.json. It leaves the `[projects.<key>]` tables as they are, and it sends nothing to the server. It deletes port.json, and the agent, which it starts again if it was running, then makes a new token.
- "Delete everything" also deletes `K_dev`, and the git adapter's `K_project`, which keys every project, comes from `K_dev` (`projectSecret`, internal/agent/git.go). So afterwards none of this computer's projects has the key its table names.
- A name goes to the server only for a key the computer has seen, in the project map or the last two days' rollups, or for one the server gave a code (`OwnKeys`, internal/agent/upload.go; project-names-v1 2). "Delete everything" deletes the project map (state.json), the rollups (`days/`) and the codes (project-names.json). The device id is the ledger's chain id, so the next sign-in links a new device, and the server's codes for it name only new keys. So the labels are never sent again. They stay in config.toml until uninstall or a hand edit, and until then:
  - `pairtime projects` and the local dashboard's Projects page still list each one under its old key, since `projects.List` reads config.toml's tables;
  - after the next sign-in, doctor's names-elsewhere problem names those keys as if config.toml came from another computer;
  - a label that was on before the delete stays on the server's dashboard until the teen hides it there, or until the old device's purge 90 days after its removal (DESIGN 4.4's project names row).

## Expected

config.toml and port.json have a published retention period, in DESIGN 3.5 and 4.4 and in docs/retention.json (DESIGN 4.4). "Delete everything" leaves behind no data about the teen that nothing in pairtime will use again.

## Computed

Neither file has a retention period anywhere. The privacy notices don't say that pairtime keeps the names the teen gave their projects, or a token that opens the local API. "Delete everything" keeps those names in config.toml under keys this computer will never use again, and pairtime goes on listing them.

state.json, outbox.json, account.json and the `received/` journal have no 4.4 row or docs/retention.json entry either. They are outside this decision, and docs/errata/2026-11-24-device-stores-retention.md will raise them after this one. `inbox/` gets its rows from #363 (D-inbox-age-cap).

## Decision

"Delete everything" is per device: it wipes this computer's data and `K_dev`, so the labels whose keys can no longer match are this computer's. A label in a `[projects.<key>]` table is a name the teen gave one of their projects, so it is data about them, not a setting, and once no key of this computer's will match it, it is only personal data left behind. Another computer's labels in a shared file are live settings of that computer: removing them would turn its names off, make it send deletes the teen never asked for, and destroy the teen's data elsewhere.

So "delete everything" will remove the `[projects.<key>]` tables of the keys this computer knew, keep every other setting but `mode`, and say how many tables it left. `mode` is the one setting it changes besides the labels: it already sets it back to local-only, as `pairtime logout` does. It sends nothing to the server. port.json holds no setting and nothing about the teen. Uninstall and "delete everything" delete it, as they do today. D-wipe-project-labels also covers the texts below that only describe today's behaviour: 3.5's Holds and Kept cells, port.json's 4.4 row and `device.local_api`.

The cost: labels that an older release's "delete everything" orphaned stay in config.toml until the teen removes them.

1. DESIGN 3.5's row for `config.toml` and `port.json` will read:

| Path | Format | Holds | Kept |
|---|---|---|---|
| `config.toml`, `port.json` | TOML; JSON with user-only permissions | `config.toml`: settings, `.pairtime-ignore` patterns once the agent reads them from it (8.8), opt-ins, and the label of each project the teen named for their own dashboard (`[projects.<key>]`, D-owner-names). It is in the config folder: `%APPDATA%\pairtime\` on Windows, which roams with the profile, `${XDG_CONFIG_HOME:-~/.config}/pairtime/` on Linux, and the data folder on macOS and under `PAIRTIME_HOME` (CP 7). `port.json`: the local port, the bearer token and the agent's process id | `config.toml`: until the teen changes it, or uninstall; "delete everything" takes out the tables of the projects this computer knows of, sets `mode` back to local-only and keeps the rest (8.8). `port.json`: until uninstall or "delete everything"; each start of the agent rewrites it and keeps the token |

2. DESIGN 4.4 will get two Device rows, after #306's row for `install/` and before `K_dev`'s:

| Where | Data | Kept |
|---|---|---|
| Device | pairtime's settings, in `config.toml`: whether it uploads and which server it signs in to, the local port it tries first, notifications and quiet hours, whether prompt text is kept and how long prompt text and file paths are kept, and calibration's shadow display. In the same file, the label of each project the teen named for their own dashboard, which stays while that name is off, and any comments and settings of their own the teen wrote, which pairtime ignores | Until the teen changes them, or uninstall. "Delete everything" (8.8) deletes the labels of the projects this computer knows of, whose keys no project has once `K_dev` is gone, and keeps the settings, though it sets the mode back to local-only, as `pairtime logout` does, since the sign-in goes too. A table for a key this computer doesn't know at the time, as another computer sharing the file writes or an older release's "delete everything" left, stays, and the command says how many it left. "Delete everything" sends nothing to the server, so a label the dashboard already shows stays there until the teen hides it on the dashboard, or removes this computer there and the 90 days of the project names row pass |
| Device | The local API's port and bearer token, and the agent's process id, in `port.json`, an owner-only file | Until uninstall or "delete everything". The token stays across the agent's restarts, and the agent makes a new one when the file is gone or damaged |

3. DESIGN 8.8's bullet will read as follows, in place of #306's:

   > "Delete everything" wipes the data directory and `K_dev`, but keeps pairtime itself, its settings (`config.toml`), and install's record of what it changed (`install/`), which uninstall needs to put the teen's files back (4.4). From `config.toml` it removes the labels of the projects whose keys this computer knows when the wipe runs (each key's `[projects.<key>]` table and the entries under it) and sets `mode` back to local-only. A table for any other key stays, as another computer sharing the file writes or an older release left behind, and the command says how many it left. Local-only mode keeps every coach feature and uploads nothing (4.2).

4. docs/retention.json will get two entries in the teen's words, after `device.install` and before `device.key`. Neither has `days`, and neither kept text has a digit. Both privacy notices will render them, as they render every device entry, and no golden or test pins the list.

   ```json
   {
     "id": "device.settings",
     "where": "device",
     "data": "Your settings in config.toml: whether pairtime uploads and which server it signs in to, its notifications and quiet hours, whether it keeps your prompt text and how long it keeps that and your file paths, whether it shows your time as calibrating, and the local port it tries first. In the same file, the name you gave each project for your dashboard, which stays there while that name is turned off, and any notes you wrote there yourself",
     "kept": "Until you change them or uninstall pairtime. \"Delete everything\" deletes the names you gave the projects this computer knows of and keeps your settings, though pairtime stops uploading until you sign in again. Names under projects it doesn't know any more stay, such as those another computer sharing this file wrote, and pairtime tells you how many. \"Delete everything\" sends nothing to the pairtime server, so a name your dashboard already shows stays there until you hide it on your dashboard, or for as long after you remove this computer there as the entry for the project names you chose to show says"
   },
   {
     "id": "device.local_api",
     "where": "device",
     "data": "The secret code that pairtime's own commands on this computer use to talk to the agent, with the agent's port and process ID. It's plain text, not encrypted, in a file other people's accounts on this computer can't open, though an administrator can",
     "kept": "Until you uninstall pairtime or delete everything. The agent keeps the same code when it restarts, and makes a new one when the file is gone or damaged"
   }
   ```

   The exclude patterns will join the 4.4 row and `device.settings`' data in the PR that has the agent read them from config.toml.

5. The code. `pairtime privacy delete` will take the tables out with a TOML-aware edit:
   - The keys will be the ones this computer knew, read before the wipe: those of state.json's project map with their aliases, of every rollup in `days/`, and of project-names.json's codes, names and notes.
   - The edit will run once the key is gone, beside the mode change, with the agent stopped and its lock held, so no agent writes back a copy of config.toml it read. It will run also when some of the data couldn't be deleted, since a rerun can't read the keys again. A delete everything that stops at the key keeps the labels with everything else.
   - It will take out the table of each such key, however its header is quoted or spaced, with any table under it, such as `[projects.<key>.x]`. At the top of the file and inside a `[projects]` table, it will take out each dotted key or inline table that names such a key. Each goes with the comment lines right above it, with no blank line between. A removed table's lines go up to the next header, but for the comment lines right above that header, which stay with it. A table or entry for any other key stays, and so do an array of `[[projects]]` and a `projects` key inside another table, such as `[display]`.
   - The command will print how many tables it left alone and where config.toml is, never their names, so a teen on a computer that shares nothing can remove them by hand. It will print the count whether or not the "nothing to delete" check ends the command first.
   - When the edit refuses, as for a case variant such as `[Projects.x]` of a key this computer knew, which Parse doesn't read as projects but which can still hold a label, or a multi-line string it can't place, the rest of the wipe will still run. The command will then print "Left alone:" with config.toml's path and the table headers to remove by hand, as the file writes them, and exit 1. The keys are read before the wipe, so it can still name them.
   - The "nothing to delete" check will also require that config.toml holds no table of a key this computer knew.
   - Where the question, the closing messages and the usage say the names go, they will name config.toml by its own path, which on Windows and Linux is outside the data folder they name.
   - Its tests will check:
     - that after "delete everything" no entry for a key this computer knew is left among the file's TOML keys, read with `toml.Decode`, and every other line is byte for byte the same, apart from the comment lines right above a removed table and `mode`, which goes back to local-only when the file said online;
     - that a table for another key stays, and that the command prints how many it left and config.toml's path, never a label, also when there is nothing to delete;
     - that a refused edit lets the rest of the wipe run, names the headers under "Left alone:" and exits 1;
     - a run with `XDG_DATA_HOME` and `XDG_CONFIG_HOME` apart and `PAIRTIME_HOME` unset.
   - The texts:
     - wipe.go's usage, its question and its two closing messages, privacy.go's usage and summary, README.md, the delete-everything bullet in internal/server/pages/privacy.md, web/src/local/pages/Privacy.svelte with privacy.test.ts, and the table of device rows in internal/agent/retention, which will gain `device.settings` and `device.local_api`, will say that the names of the projects this computer knows of go;
     - the 4.4 row, `device.settings` and privacy.md's bullet will say that it sends nothing to the server, so a name the dashboard shows stays until the teen hides it there, or until 90 days after they remove this computer there, as wipe.go's usage already says of what the server has;
     - wipe.go's exit status 1 will also cover names that had to stay in config.toml.
   - WP-IN3's Touches in PLAN will gain `cmd/pairtime/` and `internal/agent/config/`.

6. docs/spec/project-names-v1.md section 2 will get, in the same PR: "`pairtime privacy delete` ("delete everything", DESIGN 8.8) takes the names of the projects this computer knows of out of `config.toml`, leaves the tables of keys it doesn't know at the time, deletes `project-names.json` and the sign-in, and sends nothing: a name the server shows for that device stays until the owner hides it on the dashboard, or until the device's purge 90 days after it is removed." docs/spec isn't an oracle.
