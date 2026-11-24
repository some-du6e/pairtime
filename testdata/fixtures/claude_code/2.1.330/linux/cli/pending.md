# Why this run has no expected records yet

The Claude Code adapter has no expected records for this run: `internal/adapters/claudecode/fixtures/` has none under `2.1.330/linux/cli/`. So `adapters.Conform` and `pairtime adapters test` report the run as pending instead of failing it.

The run was captured for the kit's `flags` and `entrypoint` files (WP-AD1), and it covers SCRIPTS.md's steps 1, 2, 20, 16 and 18 only. Its expected records come with the adapter work that uses it, as `internal/adapters/claudecode/fixtures/2.1.330/linux/cli/expected.jsonl`. That PR can leave this file for a fixtures PR to delete, since fixtures don't change in a code PR.
