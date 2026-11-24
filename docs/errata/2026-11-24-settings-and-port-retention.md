# Erratum: config.toml and port.json have no retention period, and "delete everything" keeps the names the teen gave their projects

Date 2026-11-24. Raised by WP-ERR1, for the gap that D-install-retention left (docs/errata/2026-11-24-install-state-retention.md, #306): "`config.toml` and `port.json`, with its bearer token, have no DESIGN 4.4 row either. That gap is outside this decision and goes to WP-ERR1." Status: decided 2026-11-24 by the orchestrator for Karim (D-wipe-project-labels). WP-IN3's next PR after #306, the next one that touches cmd/pairtime/wipe.go, will apply it, with its DESIGN text under `oracle-change`.

## Input

- DESIGN 3.5's last row, for `config.toml` and `port.json`, says they hold "Settings, `.pairtime-ignore` patterns, opt-ins; the local port and bearer token". Its Kept cell is empty. The paths above the table are the data folders.
- DESIGN 4.4: "Every field has a finite retention period, published at `https://<host>/privacy`". No device row names either file, and no entry in docs/retention.json does either. #306, still open, adds the rows for `install/` under D-install-retention.
- DESIGN 8.8 on main says that "Delete everything" wipes the data directory and `K_dev`. #306 rewords that bullet, as of d47708d7:

  > "Delete everything" wipes the data directory and `K_dev`, but keeps pairtime itself, its settings (`config.toml`) and install's record of what it changed (`install/`), which uninstall needs to put the teen's files back (4.4).

- config.toml is in the config folder: `%APPDATA%\pairtime\` on Windows, which roams with the user's profile, and `${XDG_CONFIG_HOME:-~/.config}/pairtime/` on Linux. On macOS and under `PAIRTIME_HOME` it is in the data folder (internal/agent/home; CP 7). An edit of a config.toml that is a symlink, such as one into a synced folder, changes the file it points to and keeps the link (`editTarget`, internal/agent/config's edit.go). So another computer can share the file.
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

state.json, outbox.json, `inbox/`, account.json and the `received/` journal have no 4.4 row or docs/retention.json entry either. They are outside this decision, and go to WP-ERR1's queue as another erratum.

## Decision

A label in a `[projects.<key>]` table is a name the teen gave one of their projects, so it is data about them, not a setting. After "delete everything", no key of this computer's will match it again, so it would only be personal data left behind. So "delete everything" will remove the `[projects.<key>]` tables of this computer's projects and keep every other setting, as #306's 8.8 wording keeps them. The one setting it changes is `mode`, which it already sets back to local-only, as `pairtime logout` does. port.json holds no setting and nothing about the teen. Uninstall and "delete everything" delete it, as they do today, and the rows below say so.

1. DESIGN 3.5's row for `config.toml` and `port.json` will read:

| Path | Format | Holds | Kept |
|---|---|---|---|
| `config.toml`, `port.json` | TOML; JSON with user-only permissions | `config.toml`: settings, `.pairtime-ignore` patterns, opt-ins, and the label of each project the teen named for their own dashboard (`[projects.<key>]`, D-owner-names). It is in the config folder: `%APPDATA%\pairtime\` on Windows, which roams with the profile, `${XDG_CONFIG_HOME:-~/.config}/pairtime/` on Linux, and the data folder on macOS and under `PAIRTIME_HOME` (CP 7). `port.json`: the local port, the bearer token and the agent's process id | `config.toml`: until the teen changes it, or uninstall; "delete everything" takes out the tables of this computer's projects and keeps the rest (8.8). `port.json`: until uninstall or "delete everything"; each start of the agent rewrites it and keeps the token |

2. DESIGN 4.4 will get two Device rows, after #306's row for `install/` and before `K_dev`'s:

| Where | Data | Kept |
|---|---|---|
| Device | pairtime's settings, in `config.toml`: whether it uploads and which server it signs in to, the local port it tries first, notifications and quiet hours, whether prompt text is kept and how long prompt text and file paths are kept, and calibration's shadow display. In the same file, the label of each project the teen named for their own dashboard, which stays while that name is off, and any comments the teen wrote | Until the teen changes them, or uninstall. "Delete everything" (8.8) deletes the labels of this computer's projects, whose keys no project has once `K_dev` is gone, and keeps the settings, though it sets the mode back to local-only, as `pairtime logout` does, since the sign-in goes too. A label the server already shows stays there until the teen hides it on the dashboard, or until the device's purge (the project names row) |
| Device | The local API's port and bearer token, and the agent's process id, in `port.json`, an owner-only file | Until uninstall or "delete everything". The token stays across the agent's restarts, and the agent makes a new one when the file is gone or damaged |

3. DESIGN 8.8's bullet, as #306 merges it, will say that the labels go. The applying PR changes only its label clause:

   > "Delete everything" wipes the data directory, `K_dev` and the label of each of this computer's projects the teen named (its `[projects.<key>]` table in `config.toml`), but keeps pairtime itself, its other settings (the rest of `config.toml`) and install's record of what it changed (`install/`), which uninstall needs to put the teen's files back (4.4). Local-only mode keeps every coach feature and uploads nothing (4.2).

4. docs/retention.json will get two entries in the teen's words, after `device.install` and before `device.key`. Neither has `days`, and neither kept text has a digit. Both privacy notices will render them, as they render every device entry, and no golden or test pins the list.

   ```json
   {
     "id": "device.settings",
     "where": "device",
     "data": "Your settings in config.toml: whether pairtime uploads and which server it signs in to, its notifications and quiet hours, whether it keeps your prompt text and how long it keeps that and your file paths, whether it shows your time as calibrating, and the local port it tries first. In the same file, the name you gave each project for your dashboard, which stays there while that name is turned off, and any notes you wrote there yourself",
     "kept": "Until you change them or uninstall pairtime. \"Delete everything\" deletes the names you gave this computer's projects and keeps your settings, though pairtime stops uploading until you sign in again. A name your dashboard already shows stays there until you hide it on your dashboard or remove this computer there"
   },
   {
     "id": "device.local_api",
     "where": "device",
     "data": "The secret code that pairtime's own commands on this computer use to talk to the agent, with the agent's port and process ID. It's plain text, not encrypted, in a file other people's accounts on this computer can't open, though an administrator can",
     "kept": "Until you uninstall pairtime or delete everything. The agent keeps the same code when it restarts, and makes a new one when the file is gone or damaged"
   }
   ```

   The exclude patterns will join the 4.4 row and `device.settings`' data in the PR that has the agent read them from config.toml.

5. The code. `pairtime privacy delete` will take the tables out with a TOML-aware edit, under the rules the orchestrator gave WP-IN3:
   - It runs after the key and the data are gone, beside the mode change, with the agent stopped and its lock held, so no agent writes back a copy of config.toml it read. A delete everything that stops at the key keeps the labels with everything else.
   - It takes out every table under `projects` that names one of this computer's projects, however its header is quoted or spaced, and each dotted key or inline table under `projects` at the top of the file, each with the comment lines right above it, with no blank line between. This computer's projects are the keys it knew before the wipe: those of state.json's project map with their aliases, of every rollup in `days/`, and of project-names.json's codes, names and notes. A table under another key, as in a config.toml another computer shares, stays. So does a `projects` key inside another table, such as `[display]`.
   - It refuses a file that spells `projects` in another upper and lower case, such as `[Projects.x]`, which Parse doesn't read as projects but which can still hold a label, the way `SetShadow` refuses `[Display]`. It refuses a file with a multi-line string too. config.toml then stays as it is, the closing message says under "Left alone:" what to delete by hand, and the exit status is 1.
   - The check that finds nothing to delete also looks for tables under `projects` in config.toml.
   - Where the question, the closing messages and the usage say the names go, they name config.toml by its own path, which on Windows and Linux is outside the data folder they name.
   - Its tests check:
     - that after "delete everything" no key under `projects` that names this computer's projects is left among the file's TOML keys, read with `toml.Decode`, and every other line is byte for byte the same, apart from the comment lines right above a removed table and `mode`, which goes back to local-only when it said online;
     - that another computer's table stays;
     - a run with `XDG_DATA_HOME` and `XDG_CONFIG_HOME` apart and `PAIRTIME_HOME` unset;
     - a run with nothing recorded but a table of this computer's, which goes, with exit status 0.
   - The texts that say what "delete everything" keeps or deletes will say that the names go: wipe.go's usage, its question, its two closing messages and its exit status 1; privacy.go's usage and summary; README.md; the delete-everything bullet in internal/server/pages/privacy.md; web/src/local/pages/Privacy.svelte, with privacy.test.ts; and the table of device rows in internal/agent/retention, which will gain `device.settings` and `device.local_api`.

6. docs/spec/project-names-v1.md section 2 will get, in the same PR: "`pairtime privacy delete` ("delete everything", DESIGN 8.8) takes the names of this computer's projects out of `config.toml`, deletes `project-names.json` and the sign-in, and sends nothing: a name the server shows for that device stays until the owner hides it on the dashboard, or until the device's purge 90 days after it is removed." docs/spec isn't an oracle.
