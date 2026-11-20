#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# score_regression.sh < NAME_STATUS
#
# Fails a pull request that changes the scorer's output without changing
# pkg/score/VERSION (DESIGN 6.6, SM 7.6; docs/PLAN.md, WP-SC4). Every
# timeline under testdata/timelines has a golden.jsonl holding the minute
# buckets pkg/score writes for it, and go test fails when the output and the
# golden differ, so a PR that changes the output rewrites a golden. One that
# rewrites a golden must change pkg/score/VERSION too.
#
# Input: `git diff --name-status -M` lines on stdin, comparing the PR's base
# with its merge commit, as for oracle_guard.sh; ci.yml pipes them in.
#
# A golden that is modified, retyped or renamed with changes counts as a
# change of output. A new golden doesn't, since its timeline had no output
# before, and neither does a deleted one, which goes with its timeline, or a
# renamed one that keeps every byte. A modified or added pkg/score/VERSION is
# the version change.
#
# Exit status: 0 pass, 1 blocked, 2 bad input.
set -euo pipefail

version=pkg/score/VERSION

# In a case pattern * also matches /, so this covers nested timelines such
# as testdata/timelines/sidedoor/<case>/golden.jsonl.
is_golden() {
	case "$1" in
	testdata/timelines/*/golden.jsonl) return 0 ;;
	esac
	return 1
}

changed=""
note() { changed="$changed$1	$2
"; }
bumped=false
lines=0
while IFS=$'\t' read -r status first second || [ -n "$status" ]; do
	[ -n "$status" ] || continue
	lines=$((lines + 1))
	# git quotes a path with unusual characters ("a\tb"). The parts the
	# patterns look at, the directory and the file name, are plain ASCII
	# either way, so dropping the quotes is enough to classify it.
	first=${first#\"}
	first=${first%\"}
	second=${second:-}
	second=${second#\"}
	second=${second%\"}
	if [ -z "$first" ]; then
		echo "::error::score_regression.sh: malformed name-status line: $status" >&2
		exit 2
	fi
	case "$status" in
	A)
		if [ "$first" = "$version" ]; then bumped=true; fi
		;;
	M | T)
		if [ "$first" = "$version" ]; then bumped=true; fi
		if is_golden "$first"; then note "$first" modified; fi
		;;
	D) ;;
	R100) ;;
	R*)
		if is_golden "$first" || is_golden "$second"; then note "$second" "renamed with changes"; fi
		if [ "$second" = "$version" ]; then bumped=true; fi
		;;
	C*)
		if [ "$second" = "$version" ]; then bumped=true; fi
		;;
	*)
		# Unmerged, unknown or broken pairs: treat as a change, to be safe.
		if is_golden "$first"; then note "$first" "changed ($status)"; fi
		;;
	esac
done

count=$(printf '%s' "$changed" | grep -c . || true)
echo "score-regression: $lines changed paths, $count goldens changed, $version changed: $bumped"

if [ "$count" -eq 0 ] || [ "$bumped" = true ]; then
	exit 0
fi

printf '%s' "$changed" | while IFS=$'\t' read -r path what; do
	echo "::error file=$path::golden $what without a change to $version (DESIGN 6.6)"
done
cat <<EOF
This PR changes what pkg/score outputs for a timeline, since it rewrites
its golden.jsonl, but leaves $version as it is. A change of output needs a
new scoring version in the same PR (SM 7.6): MAJOR for a rule, MINOR for a
parameter value; PATCH is only for fixes that change no output. Bump
$version, or, if the output shouldn't change, fix the code and restore the
goldens.
EOF
exit 1
