# Erratum: config.toml and port.json have no retention period, and "delete everything" keeps the names the teen gave their projects

Date 2026-11-24. Raised by WP-ERR1, for the gap D-install-retention left: "port.json (local port and bearer token) and config.toml have no 4.4 row either (3.5 lists them with an empty Kept)". Status: decided 2026-11-24 by the orchestrator for Karim. WP-IN3's next PR after #306, the next one that touches cmd/pairtime/wipe.go, will apply it, with its DESIGN text under `oracle-change`.

## Input

- DESIGN 3.5's last row, for `config.toml` and `port.json`, says they hold "Settings, `.pairtime-ignore` patterns, opt-ins; the local port and bearer token". Its Kept cell is empty.
- DESIGN 4.4: "Every field has a finite retention period, published at `https://<host>/privacy`". No device row names either file, and no entry in docs/retention.json does either, which both privacy notices render. #306, still open, adds the rows for `install/` under D-install-retention. Its erratum, docs/errata/2026-11-24-install-state-retention.md, leaves this gap to WP-ERR1.
- DESIGN 8.8 on main says that "Delete everything" wipes the data directory and `K_dev`. #306 rewords that bullet:

  > "Delete everything" wipes the data directory and `K_dev`, but keeps pairtime's settings (`config.toml`) and install's record of what it changed (`install/`), which uninstall needs to put the teen's files back (4.4).

- config.toml (internal/agent/config) holds:
  - `mode`, and `[server] url`, the server `pairtime login` links to;
  - `[http] port`, the local port the agent tries first;
  - `[display] shadow`, calibration's shadow display (DESIGN 6.7);
  - `[notifications]`: whether they're on, and the quiet hours;
  - `[privacy]`: whether prompt text is kept, and for how many days prompt text and file paths are kept;
  - a `[projects.<key>]` table for each project the teen named for their own dashboard, with its label, `name`, and `online`. A label stays in the file while its name is off, so turning the name on again shows it again (D-owner-names).

  The git adapter's exclude patterns are to come from config.toml as well (internal/adapters/git's `Excludes`; DESIGN 8.8), but nothing passes it the ones in config.toml yet.
- port.json (internal/agent/localhttp's `PortFile`) holds `v`, the local HTTP `port`, the bearer `token` that opens the local API, and the agent's `pid`, which is 0 once the agent has stopped cleanly.
  - `home.WritePrivate` writes it, with mode 0600, or on Windows with a DACL that lets only the user open it.
  - Each start of the agent rewrites it and keeps the token. The agent makes a new token only when the file is gone, doesn't read, or holds no valid token.
  - pairtime's own commands read it to reach the agent.
  - No install step copies the token anywhere, although `PortFile`'s comment says the installer writes it into hook settings that can't read files.
- `pairtime uninstall` deletes both files with the rest of pairtime's data and config folders. It does so also when the teen keeps the day rollups and coach records, since only `days/` and `coach/` can stay (cmd/pairtime/uninstall.go).
- `pairtime privacy delete`, DESIGN 8.8's "delete everything", keeps config.toml (`wipeKeeps`, cmd/pairtime/wipe.go). It sets `mode` back to local-only when the file said online, since the sign-in goes with the keystore and account.json, and it leaves the `[projects.<key>]` tables as they are. It deletes port.json. The agent, which it starts again if it was running, then makes a new token.
- "Delete everything" also deletes `K_dev`, and the git adapter's `K_project`, which keys every project, comes from `K_dev` (`projectSecret`, internal/agent/git.go). So afterwards no project has the keys that config.toml's tables name.
- A name goes to the server only for a key the computer has seen, in the project map or the last two days' rollups, or for one the server gave a code (`OwnKeys`, internal/agent/upload.go; project-names-v1 2). "Delete everything" deletes the project map (state.json), the rollups (`days/`) and the codes (project-names.json). The device id is the ledger's chain id, so the next sign-in links a new device, and the server's codes for it name only new keys. So the labels are never sent or shown again, and they stay in config.toml until uninstall or a hand edit.

## Expected

Every kind of data pairtime keeps has a published retention period, in DESIGN 3.5 and 4.4 and in docs/retention.json (DESIGN 4.4). "Delete everything" leaves behind no data about the teen that nothing in pairtime will use again.

## Computed

Neither file has a retention period anywhere. The privacy notices don't say that pairtime keeps the names the teen gave their projects, or a token that opens the local API. "Delete everything" keeps those names in config.toml, under keys nothing will use again.

## Decision

A label in a `[projects.<key>]` table is a name the teen gave one of their projects, so it is data about them, not a setting. After "delete everything" no key will match it again, so it would only be personal data left behind. So "delete everything" will remove config.toml's `[projects.<key>]` tables and keep every other setting, as #306's 8.8 wording keeps them. port.json holds no setting and nothing about the teen. Uninstall and "delete everything" delete it, as they do today, and the rows below say so.

1. DESIGN 3.5's row for `config.toml` and `port.json` will get this Kept cell:

   > `config.toml`: until the teen changes it, or uninstall; "delete everything" keeps it but takes out its `[projects.<key>]` tables (8.8). `port.json`: until uninstall or "delete everything"; each start of the agent rewrites it and keeps the token

2. DESIGN 4.4 will get two Device rows, after #306's row for `install/` and before `K_dev`'s:

| Where | Data | Kept |
|---|---|---|
| Device | pairtime's settings, in `config.toml`: whether it uploads and which server it signs in to, the local port it tries first, notifications and quiet hours, whether prompt text is kept and how long prompt text and file paths are kept, calibration's shadow display, the exclude patterns that keep folders out (8.8), and the label of each project the teen named for their own dashboard, which stays while that name is off | Until the teen changes them, or uninstall. "Delete everything" deletes the project labels, which no project's key names once `K_dev` is gone, keeps the other settings, and sets the mode back to local-only (8.8) |
| Device | The local API's port and bearer token, and the agent's process id, in `port.json`, an owner-only file | Until uninstall or "delete everything". The token stays across the agent's restarts, and the agent makes a new one when the file is gone or damaged |

3. DESIGN 8.8's bullet, as #306 words it, will say that the labels go:

   > "Delete everything" wipes the data directory and `K_dev`, but keeps pairtime's settings (`config.toml`), all but the label of each project the teen named (its `[projects.<key>]` table), and install's record of what it changed (`install/`), which uninstall needs to put the teen's files back (4.4). Local-only mode keeps every coach feature and uploads nothing (4.2).

4. docs/retention.json will get two entries in the teen's words, after `device.install` and before `device.key`. Neither has `days`, and neither kept text has a digit. Both privacy notices render them.

   ```json
   {
     "id": "device.settings",
     "where": "device",
     "data": "Your settings in config.toml: whether pairtime uploads and which server it signs in to, its notifications and quiet hours, whether it keeps your prompt text and how long it keeps that and your file paths, whether it shows your time as calibrating, the local port it tries first, and the name you gave each project for your dashboard, which stays there while that name is turned off",
     "kept": "Until you change them or uninstall pairtime. \"Delete everything\" deletes the names you gave your projects and keeps your other settings, and pairtime stops uploading until you sign in again"
   },
   {
     "id": "device.local_api",
     "where": "device",
     "data": "The secret code that lets pairtime's own commands and its dashboard on this computer talk to the agent, with the agent's port and process ID, in a file other people's accounts on this computer can't open, though an administrator can",
     "kept": "Until you uninstall pairtime or delete everything. The agent keeps the same code when it restarts, and makes a new one when the file is gone"
   }
   ```

   The exclude patterns will join `device.settings`' data in the PR that has the agent read them from config.toml.

5. The code: `pairtime privacy delete` will remove config.toml's `[projects.<key>]` tables with a TOML-aware edit that refuses case variants such as `[Projects.x]`, as config edits do. A test case will check that after "delete everything" no `[projects.*]` table is left and every other key is byte-for-byte the same, apart from `mode`, which goes back to local-only when it said online. Its usage and its closing message, which say that your settings in config.toml stay, will say that the names you gave your projects go.
