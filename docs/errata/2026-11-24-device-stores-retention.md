# Erratum: state.json, outbox.json, account.json, the sign-in's secrets and the received/ journal have no published retention period

Date 2026-11-24. Raised by WP-ERR1, while checking docs/errata/2026-11-24-settings-and-port-retention.md (#369). Status: waiting for a decision.

## Input

- DESIGN 4.4: "Every field has a finite retention period, published at `https://<host>/privacy`". docs/retention.json's device entries are DESIGN 4.4's eleven Device rows, in its order, and both privacy notices render them. Neither names the stores below. Three other gaps are covered elsewhere:
  - `inbox/` gets its rows from #363 (D-inbox-age-cap);
  - `install/` gets them from #306 (D-install-retention);
  - config.toml and port.json get them from the settings erratum (#369, D-wipe-project-labels).
- state.json. DESIGN 3.5 says it holds "Scorer state, tailer cursors, agent-app reader cursors (5.1), upload cursor, project map, consent answers". Its Kept cell says "Rebuildable from the ledger and the harness logs", which says how the file can come back, not how long it stays.
  - On main its keys are `adapters`, `agent`, `projects`, `gitwatch`, `notifier`, `procs`, `scorer`, `tailers`, `modes` and `uploader`.
  - None holds a consent answer yet. internal/agent/state's package doc lists consent answers among what later work packages bring, and install's consent screen writes its answer to config.toml.
  - Four keys hold local paths or a harness's own ids:
    - `projects` is the git adapter's project map: each repo's common git directory, a local folder path, with its root commits (internal/adapters/git's `ProjectMap`). Nothing drops a repo's entry.
    - `gitwatch` holds each worktree's cursor by its git directory, and each repo's recent commit SHAs by its common git directory, kept while the git directory is there (internal/adapters/git's watch.go).
    - `tailers` holds each transcript file's path, such as Claude Code's `~/.claude/projects/<project>/<session>.jsonl`, kept until the file is gone, so for as long as the harness keeps it (internal/agent/tailers).
    - `adapters` holds Claude Code's own session, prompt, tool and subagent ids (internal/adapters/claudecode's state.go).
  - DESIGN 4.4 keeps the vault's file paths 30 days.
- state.json.bad. A state.json this release can't read, damaged or of another format version, is renamed to state.json.bad, replacing an older one, and the agent starts afresh (internal/agent/state). The copy keeps that file's project map, cursors and ids. Nothing in the agent deletes it.
- outbox.json. DESIGN 3.5 says it holds "The body of the request that waits for its retry, as `pairtime privacy show` printed it, so the retry sends the same bytes with the same `Idempotency-Key`", kept "Until its answer comes". The body is an upload's, with tier-1 numbers, batch roots and labels, never content (DESIGN 4.3). Project names requests don't use it.
- account.json (internal/agent/account's `Linked`) holds the server's address, the device id, the registered public key and when the computer was linked. `home.WritePrivate` writes it, and `pairtime logout` deletes it. DESIGN 3.5 has no row for it.
- The sign-in's secrets. `pairtime login` keeps the `ptd_` device token and the seed of the Ed25519 key that signs uploads in the store that holds `K_dev` (internal/agent/keys' account.go). `pairtime logout` forgets them, and "delete everything" and uninstall destroy them with the rest of the keystore. DESIGN 4.4's only key row is `K_dev`'s, "Until uninstall or rotation", and docs/retention.json's `device.key` says only "Your device key".
- `received/` (internal/agent/inbox's `Journal`) has one line per message the agent took in, in a file per UTC day. A line holds the message's id, kind and route, the sender's process id, when it was sent and received, the size of its body, how many processes the agent's walk of the sender held and what the agent made of them, and whether a hook call was a prompt. It never holds a body.
  - `KeepDays` is 7, but a day's file goes only when the journal prunes: at the agent's start, and when the first message of a new UTC day arrives. Each prune keeps the files of the 7 UTC days before that day. So a line usually goes when it is 7 to 8 days old, and it stays longer while the agent runs and takes no message.
  - No timer prunes it, and the agent's retention look (internal/agent/retention.go) doesn't name it.
  - It also has a line for each hook call from a session in a folder kept out of pairtime. The hook sink marks such a session out and still stores the call (internal/agent/hooks.go), though DESIGN 8.8 says a `.pairtime-ignore` file or an exclude pattern "keeps a folder out entirely".
  - DESIGN 3.5 has no row for it.
- `pairtime uninstall` deletes all of these (`pairtimeEntries`, cmd/pairtime/uninstall.go, and the keystore). So does "delete everything", since `wipeKeeps` (cmd/pairtime/wipe.go) names none of them.

## Expected

Each store has a DESIGN 3.5 row, and each kind of data in it has a published retention period in DESIGN 4.4 and docs/retention.json (DESIGN 4.4).

## Computed

None of these has a DESIGN 4.4 row or a docs/retention.json entry, so neither privacy notice mentions them.

- account.json, state.json.bad and `received/` have no DESIGN 3.5 row either.
- state.json's Kept cell gives no period, and its Holds cell lists consent answers the file doesn't hold yet.
- The project map keeps local folder paths with no period, and so does state.json.bad, where DESIGN 4.4 keeps the vault's paths 30 days.
- `received/`'s 7 days aren't a bound the code keeps, and it records calls from folders DESIGN 8.8 keeps out.

## For the decision

1. **Rows.** A proposal, for the decision to change or replace:
   - DESIGN 3.5 gets rows for account.json, state.json.bad and `received/`. state.json's Kept cell names a period, and its Holds cell marks consent answers as planned.
   - DESIGN 4.4 gets Device rows, before `K_dev`'s:

| Where | Data | Kept |
|---|---|---|
| Device | The agent's working state, in `state.json`: the scorer's state; the readers' and the uploader's cursors, with each transcript's path, kept until the harness deletes the transcript, and the git watcher's, by each repo's git directory, kept while the directory is there; the harness's own session, prompt and tool ids; and the git adapter's project map, with each repo's local folder and root commits | Until uninstall or "delete everything", or the period item 2 decides for a repo's entry. A copy the agent couldn't read, in `state.json.bad`: the period item 3 decides |
| Device | An upload waiting for its retry, in `outbox.json`: its tier-1 numbers, batch roots and labels, as `pairtime privacy show` printed them | Until the server answers it; uninstall and "delete everything" delete it |
| Device | Which server the computer is linked to, in `account.json`: its address, the device id, the device's public key and when it was linked; and, in the keystore beside `K_dev`, the sign-in's device token and the key that signs uploads | Until `pairtime logout`, "delete everything" or uninstall |
| Device | A line for each message the agent took in, in `received/`: its id, kind and route, the sender's process id, its times and size, what the agent made of the sender, and whether it was a prompt, never its body | 7 days, once item 4 makes the code keep that bound |

   - docs/retention.json gets an entry for each in the teen's words, with `received/`'s 7 days as its `days` and a TestDesignPeriods case for them.
2. **The project map's paths.** Should a repo's entry go once the computer hasn't seen the repo for a period, such as the ledger's 180 days? Or does it stay until uninstall or "delete everything", as the key that keeps a project's identity across restarts, with that period stated?
3. **state.json.bad.** Should it go at the next start that reads state.json cleanly, or after a period of its own, or stay with state.json's period?
4. **`received/`.**
   - Should its prune join the agent's retention look, as #363's inbox cap does, so that a line goes at 7 days?
   - Should a hook call from a folder kept out of pairtime be journaled at all under DESIGN 8.8, or does that go in an erratum of its own?
   - Does `received/` take a 4.4 row of its own, or a clause in the row for hook calls the agent hasn't parsed yet, since it records the same arrivals and never their bodies?
