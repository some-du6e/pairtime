# Erratum: DESIGN 3.5's store table has no row for the commit records that wait for the uploader, nor for the hook calls that wait to be parsed

Date 2026-11-21. Raised by WP-AG7, from the review of #136 (#148). Status: decided 2026-11-21 by the orchestrator for Karim (D-commit-records-local, D-held-calls-inbox-class). The agent keeps each change's commit records in `commits/` until the uploader sends them, and keeps the hook calls it can't parse yet in `held.jsonl`, its pending log and its replay file, with `inbox/`'s protections and a 7-day bound. DESIGN 3.5 gets the two rows below. #136 writes both stores. The uploader sends the commit records from theirs in a follow-up to WP-AG5, after #131. Applied to DESIGN 3.5 on 2026-11-21 by #160.

## Input

- DESIGN 3.5 lists every store on the device, with what it holds and how long it is kept. DESIGN 4.4: "Every field has a finite retention period".
- DESIGN 4.3's tier-1 upload carries commit records `{sha, parents, project, observed_at, author_time, committer_time, files_changed, insertions, deletions, path_hmacs[], credited_seconds_since_parent, rewrites[], actor, agent_trailer}`, and never file names or paths. DESIGN 5.8 says the same fields, and that `rewrites[{old_sha, new_sha}]` come from the reflog.
- The git adapter (WP-AD5) gives each change it finds as a `git.Change`: the `git.change` event, and in `Commits` the change's commit records. `git.Commit` marshals exactly DESIGN 4.3's fields but `credited_seconds_since_parent`, which the scorer adds, and `path_hmacs`, which the adapter doesn't make yet. Everything else it reads, time zones, the reflog time, git-ai's note, stays in `git.CommitLocal`, which is never marshaled.
- The watcher moves a worktree's cursor past a change once its sink returns (`git.Watcher`'s release), and the cursor goes to `state.json`.
- WP-AG5's uploader (#131, `internal/agent/uploader/wire.go`) said commit records wait for the git adapter's wiring, and #136's PLAN entry said WP-AG5 decides where they wait.
- #136's adapter host (`internal/agent/adapters.go`) keeps a hook call it can't parse yet: in `held.jsonl.pending` from before the journal stores it until it is parsed or held, so an acked call outlives a kill, and in `held.jsonl` while the keystore or the ledger isn't open, which a replay renames to `held.jsonl.replay` while it reads it. Each line is a whole hook call, so it holds prompts, tool input, Claude Code's `tool_response` and the agent's last message, in plaintext.
- DESIGN 3.5's `inbox/` row holds what the spool holds, the same kind of content, and the spool writes only where this user alone can reach (`inbox.privateDir`, `privateFile`, `O_NOFOLLOW`).

## Expected

Every commit the agent sees keeps its tier-1 record on the device until it is uploaded, and every hook call the agent can't parse yet is kept no longer than a bound, as private as the inbox's, in stores DESIGN 3.5 lists for periods DESIGN 4.4 can state.

## Computed

At #136's first review (a5345a5), the agent's git sink wrote only the `git.change` event and dropped `Commits`, and the cursor then moved past them. `pkg/event`'s `git.change` has no rewrites, author or committer times, or per-commit `observed_at`, so the ledger can't rebuild the records later. `rewrites[]` comes from reflog entries that git expires, after 30 days by default for unreachable ones, so a record dropped before the uploader exists can't be made again.

The held files had no row, no bound but `inbox.MaxSpool` (256 MiB), and plain `os.OpenFile` opens. A ledger that can't open until a person restores it (`ledgerGivesUp`) would have had every call of every session held there, possibly for weeks.

## The rows

DESIGN 3.5's table gains, after `buckets/` and after `inbox/`:

| Path | Format | Holds | Kept |
|---|---|---|---|
| `commits/YYYY-MM-DD.jsonl` | JSONL, a file per UTC day of `observed_at` | DESIGN 4.3's commit records, which the git adapter's sink appends before the watcher's cursor moves past the change: tier-1 fields only, with no commit message and no path | 180 days, like the minute buckets. The uploader sends them from here |
| `held.jsonl`, `held.jsonl.pending`, `held.jsonl.replay` | JSONL, each line a hook call with an HMAC under a key the agent's run keeps in memory | Hook calls the agent hasn't parsed yet: acked and not yet parsed or held (`.pending`), waiting for the keystore and the ledger (`held.jsonl`), being replayed (`.replay`). Whole payloads, prompts included, kept as `inbox/` keeps its own: a folder and files only this user can reach, opened with `O_NOFOLLOW` where the spool is | Deleted once drained. A call older than 7 days is dropped at the next start, and none is held once the ledger can't open until `pairtime restore` runs |

A crash after a change's records are kept and before its cursor reaches `state.json` writes them again at the next start, so one sha can have more than one line. `pairtime uninstall` deletes `commits/` and the three held files with the rest of the data directory, a linked one included.

A held line's HMAC lets a run trust the via, walk and stamp of the lines it wrote itself (D-held-via-plugin); every other line reads as the spool's do, via plugin. `held.jsonl.pending` is emptied, not deleted, while the agent runs, since it stays open for the next call, and goes at its stop.
