#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# The witness sidecar's loop (deploy/compose.yml, service witness). pairtimed
# writes each day's signed witness root under /witness (WP-SV17; DESIGN 3.10,
# 7.4), and its distroless image has no git or ssh, so this container
# publishes them: every PAIRTIME_WITNESS_EVERY seconds it fetches the public
# witness repository, adds the files under /witness that the repository
# doesn't hold yet, and pushes them in one commit over SSH, with a deploy key
# that can write to that repository alone.
#
# It never changes or deletes a file the repository already holds: a
# published root stays as it was published, so a later, different copy of
# one is left out. Each round starts from the remote branch, so a failed
# push is retried whole on the next round.
#
#   PAIRTIME_WITNESS_REPO    owner/name, such as some-du6e/pairtime-witness
#   PAIRTIME_WITNESS_REMOTE  the remote to push to; git@github.com:<repo>.git
#   PAIRTIME_WITNESS_BRANCH  main unless set
#   PAIRTIME_WITNESS_EVERY   seconds between rounds, 300 unless set
#   PAIRTIME_WITNESS_ONCE    1 runs one round and exits with its status
#   PAIRTIME_WITNESS_TRACE   1 traces every command, for the restore drill
set -eu
if [ "${PAIRTIME_WITNESS_TRACE:-}" = 1 ]; then
	set -x
fi

repo=${PAIRTIME_WITNESS_REPO:?set PAIRTIME_WITNESS_REPO to owner/name}
remote=${PAIRTIME_WITNESS_REMOTE:-git@github.com:$repo.git}
branch=${PAIRTIME_WITNESS_BRANCH:-main}
every=${PAIRTIME_WITNESS_EVERY:-300}
# Where the roots are and where the clone goes; tests move them.
src=${PAIRTIME_WITNESS_SRC:-/witness}
work=${PAIRTIME_WITNESS_WORK:-/repo}

# ssh with the deploy key alone, and GitHub's published host keys alone
# (deploy/witness/known_hosts, from api.github.com/meta), never asking.
GIT_SSH_COMMAND="ssh -i /run/witness/deploy_key -o IdentitiesOnly=yes -o UserKnownHostsFile=/etc/witness/known_hosts -o StrictHostKeyChecking=yes -o BatchMode=yes"
# git and ssh want a home, and the root filesystem is read-only.
HOME=/tmp
export GIT_SSH_COMMAND HOME

log() {
	echo "witness: $*" >&2
}

# publish runs one round. Under set -e a function called in a condition
# doesn't stop at a failed command, so each step returns on its own.
publish() {
	if [ ! -d "$work/.git" ]; then
		git clone --quiet "$remote" "$work" || return 1
	fi
	git -C "$work" fetch --quiet origin || return 1
	if git -C "$work" rev-parse --verify --quiet "refs/remotes/origin/$branch" > /dev/null; then
		git -C "$work" checkout --quiet -B "$branch" "refs/remotes/origin/$branch" || return 1
		git -C "$work" reset --quiet --hard "refs/remotes/origin/$branch" || return 1
	else
		# An empty repository: the first push makes the branch.
		git -C "$work" checkout --quiet --orphan "$branch" 2> /dev/null || true
	fi
	git -C "$work" clean --quiet -f -d -x || return 1
	if [ -z "$(find "$src" -mindepth 1 -print | head -n 1)" ]; then
		return 0
	fi
	# What the repository lacks, file by file, never over what it holds.
	# Not cp -R -n: BusyBox's skips a directory that exists, such as the
	# clone itself, where GNU's goes into it.
	(cd "$src" && find . -type f -print) | while IFS= read -r f; do
		if [ ! -e "$work/$f" ]; then
			mkdir -p "$work/${f%/*}" || exit 1
			cp "$src/$f" "$work/$f" || exit 1
		fi
	done || return 1
	if [ -z "$(git -C "$work" status --porcelain)" ]; then
		return 0
	fi
	git -C "$work" add -A || return 1
	added=$(git -C "$work" diff --cached --name-only | head -n 5 | tr '\n' ' ')
	git -C "$work" -c user.name="pairtimed witness" -c user.email="witness@pairtime.invalid" \
		commit --quiet -m "witness: publish ${added% }" || return 1
	git -C "$work" push --quiet origin "HEAD:refs/heads/$branch" || return 1
	log "pushed $(git -C "$work" rev-parse --short HEAD) to $repo: $added"
}

log "publishing $src to $repo ($branch) every ${every}s"
while :; do
	if publish; then
		status=0
	else
		status=1
		log "this round failed; the next one, in ${every}s, starts over"
	fi
	if [ "${PAIRTIME_WITNESS_ONCE:-}" = 1 ]; then
		exit "$status"
	fi
	sleep "$every"
done
