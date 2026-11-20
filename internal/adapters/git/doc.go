// SPDX-License-Identifier: AGPL-3.0-or-later

// Package git is the git adapter: it says which project a folder belongs
// to, and which commits the person and their agents made there, without
// reading code or uploading anything a reader could turn back into a name
// (DESIGN 5.8; SP 4.2 to 4.4).
//
// # Projects
//
// A Resolver turns a working directory, such as a harness session's cwd or
// the folder of a file an editor reported, into a Location. It runs `git
// rev-parse` once per folder, which also finds linked worktrees and
// submodules, and caches the answer. A repo's project is its sorted set of
// root commits, so a fresh clone, a rename and a push to any forge keep the
// same project. A folder outside git, a repo with no commit yet and a
// shallow clone, whose true roots git doesn't have, are identified by their
// folder instead. Both kinds of key are HMACs under the device's project
// secret (see ProjectSecret), never plain hashes, so nobody can test a
// guessed root commit or path against one (AGENTS.md). The root commits
// themselves stay on the device until the uploader registers the project
// (DESIGN 4.3).
//
// A folder is out entirely when it or any folder above it holds a file
// named .pairtime-ignore, or when it matches one of the exclude patterns
// (DESIGN 8.8). An ignored folder resolves to nothing, is never watched,
// and yields no records.
//
// # Commits
//
// A Watcher watches each repo's git directory, not its working tree: one
// non-recursive watch on the git directory, which sees HEAD, index and
// ORIG_HEAD change, and one on its logs directory, which sees logs/HEAD
// grow (CP 3.2). A slow poll of the same few files backs the watch up. When
// something changed, a Scanner reads the HEAD reflog entries written since
// it last looked and turns them into git.change events and commit records:
// SHA, parents, author and committer times, when pairtime first saw the
// commit, `git diff --shortstat` against the first parent, the commits an
// amend or a rebase replaced, who ran the command, the agent a trailer
// names, and labels from git-ai's notes under refs/notes/ai.
//
// The adapter only reads. It never writes to a repo, never takes a lock
// that would get in git's way (GIT_OPTIONAL_LOCKS=0), never runs a hook,
// and never sets core.hooksPath, which would replace every repo's own
// hooks (SP 4.1). It reads commit messages only to find trailers, and
// author names and email addresses only to match a rebased commit with the
// one it replaced; none of them is kept or leaves the device.
package git
