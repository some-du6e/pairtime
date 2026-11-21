# Erratum: DESIGN 3.5's store table has no row for the commit records that wait for the uploader

Date 2026-11-21. Raised by WP-AG7, from the review of #136 (#148). Status: decided 2026-11-21 by the orchestrator for Karim (D-commit-records-local). The agent keeps each change's commit records in `commits/` until the uploader sends them, and DESIGN 3.5 gets the row below. #136 writes the store. The uploader sends from it in a follow-up to WP-AG5, after #131.

## Input

- DESIGN 3.5 lists every store on the device, with what it holds and how long it is kept. DESIGN 4.4: "Every field has a finite retention period".
- DESIGN 4.3's tier-1 upload carries commit records `{sha, parents, project, observed_at, author_time, committer_time, files_changed, insertions, deletions, path_hmacs[], credited_seconds_since_parent, rewrites[], actor, agent_trailer}`, and never file names or paths. DESIGN 5.8 says the same fields, and that `rewrites[{old_sha, new_sha}]` come from the reflog.
- The git adapter (WP-AD5) gives each change it finds as a `git.Change`: the `git.change` event, and in `Commits` the change's commit records. `git.Commit` marshals exactly DESIGN 4.3's fields but `credited_seconds_since_parent`, which the scorer adds, and `path_hmacs`, which the adapter doesn't make yet. Everything else it reads, time zones, the reflog time, git-ai's note, stays in `git.CommitLocal`, which is never marshaled.
- The watcher moves a worktree's cursor past a change once its sink returns (`git.Watcher`'s release), and the cursor goes to `state.json`.
- WP-AG5's uploader (#131, `internal/agent/uploader/wire.go`) said commit records wait for the git adapter's wiring, and #136's PLAN entry said WP-AG5 decides where they wait.

## Expected

Every commit the agent sees keeps its tier-1 record on the device until it is uploaded, in a store DESIGN 3.5 lists, for a period DESIGN 4.4 can state.

## Computed

At #136's first review (a5345a5), the agent's git sink wrote only the `git.change` event and dropped `Commits`, and the cursor then moved past them. `pkg/event`'s `git.change` has no rewrites, author or committer times, or per-commit `observed_at`, so the ledger can't rebuild the records later. `rewrites[]` comes from reflog entries that git expires, after 30 days by default for unreachable ones, so a record dropped before the uploader exists can't be made again.

## The row

DESIGN 3.5's table gains, after `buckets/`:

| Path | Format | Holds | Kept |
|---|---|---|---|
| `commits/YYYY-MM-DD.jsonl` | JSONL, a file per UTC day of `observed_at` | DESIGN 4.3's commit records, which the git adapter's sink appends before the watcher's cursor moves past the change: tier-1 fields only, with no commit message and no path | 180 days, like the minute buckets. The uploader sends them from here |

A crash after a change's records are kept and before its cursor reaches `state.json` writes them again at the next start, so one sha can have more than one line. `pairtime uninstall` deletes `commits/` with the rest of the data directory, a linked one included.
