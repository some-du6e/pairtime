# Erratum: state.json, outbox.json, account.json and the received/ journal have no published retention period

Date 2026-11-24. Raised by WP-ERR1, while checking docs/errata/2026-11-24-settings-and-port-retention.md. Status: waiting for a decision.

## Input

- DESIGN 4.4: "Every field has a finite retention period, published at `https://<host>/privacy`". docs/retention.json's entries are DESIGN 4.4's rows, in its order (internal/server/retention's policy.go), and both privacy notices render its device entries. Neither has a row for the four stores below. `inbox/` gets its rows from #363 (D-inbox-age-cap) and isn't part of this erratum.
- state.json. DESIGN 3.5 says it holds "Scorer state, tailer cursors, agent-app reader cursors (5.1), upload cursor, project map, consent answers", and its Kept cell says "Rebuildable from the ledger and the harness logs". That says how the file can come back, not how long it stays. The git adapter's project map in it holds each repo's common git directory, which is a local folder path, and its root commits (internal/adapters/git's `ProjectMap`: "It holds local paths and root commits, so it never leaves the device"). DESIGN 4.4 keeps the vault's file paths 30 days.
- outbox.json. DESIGN 3.5 says it holds "The body of the request that waits for its retry, as `pairtime privacy show` printed it, so the retry sends the same bytes with the same `Idempotency-Key`", kept "Until its answer comes". The body is an upload's, with tier-1 numbers, batch roots and labels, never content (DESIGN 4.3). Project names requests don't use it.
- account.json (internal/agent/account's `Linked`) holds the server's address, the device id, the registered public key and when the computer was linked, and no secret. `home.WritePrivate` writes it, and `pairtime logout` deletes it. DESIGN 3.5 has no row for it.
- `received/` (internal/agent/inbox's `Journal`) has one line per message the agent took in, in a file per UTC day. Each line holds the message's id, kind and route, the sender's process id, when it was sent and received, the size of its body, how many processes the agent's walk of the sender held and what the agent made of them, and whether a hook call was a prompt. It never holds a body. It keeps its day files `KeepDays` = 7 days. DESIGN 3.5 has no row for it.
- `pairtime uninstall` deletes all four (`pairtimeEntries`, cmd/pairtime/uninstall.go), and so does "delete everything", whose `wipeKeeps` (cmd/pairtime/wipe.go) names none of them.

## Expected

Each store has a DESIGN 3.5 row, and each kind of data in it has a published retention period in DESIGN 4.4 and docs/retention.json (DESIGN 4.4).

## Computed

None of the four has a DESIGN 4.4 row or a docs/retention.json entry, so neither privacy notice mentions them. account.json and `received/` have no DESIGN 3.5 row either, and state.json's Kept cell gives no period. The project map keeps local folder paths with no period, where DESIGN 4.4 keeps the vault's paths 30 days.

## For the decision

1. **Rows.** A proposal, for the decision to change or replace:
   - DESIGN 3.5 gets rows for account.json and `received/`, and state.json's Kept cell names a period.
   - DESIGN 4.4 gets Device rows, before `K_dev`'s:

| Where | Data | Kept |
|---|---|---|
| Device | The agent's working state, in `state.json`: the scorer's state, the readers' and the uploader's cursors, consent answers, and the git adapter's project map, with each repo's local folder and root commits | Until uninstall or "delete everything", or the period item 2 decides for a repo's entry |
| Device | An upload waiting for its retry, in `outbox.json`: its tier-1 numbers, batch roots and labels, as `pairtime privacy show` printed them | Until the server answers it; uninstall and "delete everything" delete it |
| Device | Which server the computer is linked to, in `account.json`: its address, the device id, the device's public key and when it was linked | Until `pairtime logout`, "delete everything" or uninstall |
| Device | A line for each message the agent took in, in `received/`: its id, kind and route, the sender's process id, its times and size, what the agent made of the sender, and whether it was a prompt, never its body | 7 days |

   - docs/retention.json gets an entry for each in the teen's words, with `received/`'s 7 days as its `days` and a TestDesignPeriods case for them.
2. **The project map's paths.** Should a repo's entry go once the computer hasn't seen the repo for a period, such as the ledger's 180 days? Or does it stay until uninstall or "delete everything", as the key that keeps a project's identity across restarts, with that period stated?
3. **`received/`.** Does it take a 4.4 row of its own, or a clause in the row for hook calls the agent hasn't parsed yet, since it records the same arrivals and never their bodies?
