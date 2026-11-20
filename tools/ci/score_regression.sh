#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# OLD_VERSION=... NEW_VERSION=... [SCORE_BASE=rev SCORE_HEAD=rev] \
#   score_regression.sh < NAME_STATUS
#
# Fails a pull request that changes the scorer's output without a new
# scoring version in pkg/score/VERSION (DESIGN 6.6, SM 7.6; docs/PLAN.md,
# WP-SC4). Every timeline under testdata/timelines has a golden.jsonl holding
# the minute buckets pkg/score writes for it, and go test fails when the
# output and the golden differ, so a PR that changes the output rewrites a
# golden.
#
# Input: `git diff --name-status -M` lines on stdin, comparing the PR's base
# with its merge commit, as for oracle_guard.sh; ci.yml pipes them in.
# OLD_VERSION and NEW_VERSION hold pkg/score/VERSION at the base and at the
# merge commit, trimmed here as pkg/score's Version() trims them. SCORE_BASE
# and SCORE_HEAD name the same two commits for git show; only a PATCH-only
# bump needs them.
#
# A golden that is modified, retyped or renamed with changes is a change of
# output. A new golden isn't, since its timeline had no output before, and
# neither is a deleted one, which goes with its timeline, or a renamed one
# that keeps every byte. Nor is a golden whose own timeline's inputs change
# in the same diff (any other .json or .jsonl file in its directory, such as
# events.jsonl or expected.json): that PR changes an oracle, which the oracle
# guard rules on, so it needs no new version (decided 2026-11-20 by the
# orchestrator for Karim, issue #132).
#
# Any other change of output needs a version higher than the base's as
# MAJOR.MINOR.PATCH. A higher MAJOR or MINOR covers it. A PATCH-only bump
# covers it only when each such golden differs from the base's in v alone,
# since PATCH is a fix that changes no output (SM 7.6, DESIGN 6.6), and every
# bucket carries v. An edit that leaves the trimmed version as it was, or
# lowers it, is no bump.
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

# is_input says whether a path is one of a timeline's inputs: a .json or
# .jsonl file under testdata/timelines other than a golden. Its directory is
# the timeline it belongs to.
is_input() {
	is_golden "$1" && return 1
	case "$1" in
	testdata/timelines/*.json | testdata/timelines/*.jsonl) return 0 ;;
	esac
	return 1
}

# trim strips leading and trailing white space, as strings.TrimSpace does.
trim() {
	local s=$1
	s=${s#"${s%%[![:space:]]*}"}
	s=${s%"${s##*[![:space:]]}"}
	printf '%s' "$s"
}

# A scoring version is MAJOR.MINOR.PATCH, each 1 to 9 digits, as pkg/event's
# isSemver has it. 10# below reads a part with a leading zero as decimal.
semver_re='^([0-9]{1,9})\.([0-9]{1,9})\.([0-9]{1,9})$'

old=$(trim "${OLD_VERSION:-}")
new=$(trim "${NEW_VERSION:-}")

# goldens holds each changed golden as "status<TAB>base path<TAB>head path",
# and inputs each directory with a changed input, one a line.
goldens=""
inputs=""
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
	for p in "$first" "$second"; do
		if [ -n "$p" ] && is_input "$p"; then inputs="$inputs${p%/*}
"; fi
	done
	case "$status" in
	A | D | R100 | C*) ;;
	M | T)
		if is_golden "$first"; then goldens="$goldens$status	$first	$first
"; fi
		;;
	R*)
		if is_golden "$first" || is_golden "$second"; then goldens="$goldens$status	$first	$second
"; fi
		;;
	*)
		# Unmerged, unknown or broken pairs: treat as a change, to be safe.
		if is_golden "$first"; then goldens="$goldens$status	$first	$first
"; fi
		;;
	esac
done

# has_input says whether a timeline directory's inputs changed. It matches
# whole lines of $inputs without a pipe, which pipefail could fail when grep
# -q stops reading early.
has_input() {
	case "
$inputs" in
	*"
$1
"*) return 0 ;;
	esac
	return 1
}

# changed holds the goldens that count as a change of output, and oracle
# those that changed with their timeline's inputs.
changed=""
oracle=0
while IFS=$'\t' read -r status before after; do
	[ -n "$status" ] || continue
	if has_input "${before%/*}" || has_input "${after%/*}"; then
		oracle=$((oracle + 1))
		continue
	fi
	changed="$changed$status	$before	$after
"
done <<<"$goldens"

count=$(printf '%s' "$changed" | grep -c . || true)
echo "score-regression: $lines changed paths, $count goldens changed with the scorer's output, $oracle with their timeline's inputs; $version ${old:-(none)} -> ${new:-(none)}"

if [ "$count" -eq 0 ]; then
	exit 0
fi

# fail reports each changed golden with why, then the fix, and exits 1.
fail() {
	local why=$1
	printf '%s' "$changed" | while IFS=$'\t' read -r status before after; do
		echo "::error file=$after::golden changed ($status) $why (DESIGN 6.6)"
	done
	cat <<EOF
This PR changes what pkg/score outputs for a timeline, since it rewrites
its golden.jsonl, but $why. A change of output needs a higher scoring
version in the same PR (SM 7.6): MAJOR for a rule, MINOR for a parameter
value. PATCH is only for a fix that changes no output, so with a PATCH-only
bump the goldens may differ in v alone. Bump $version, or, if the output
shouldn't change, fix the code and restore the goldens.
EOF
	exit 1
}

if [ -z "${OLD_VERSION+set}" ] || [ -z "${NEW_VERSION+set}" ]; then
	echo "::error::score_regression.sh: set OLD_VERSION and NEW_VERSION to $version at the base and at the head" >&2
	exit 2
fi
if ! [[ $new =~ $semver_re ]]; then
	fail "$version at the PR's head, \"$new\", isn't MAJOR.MINOR.PATCH"
fi
new_major=$((10#${BASH_REMATCH[1]}))
new_minor=$((10#${BASH_REMATCH[2]}))
new_patch=$((10#${BASH_REMATCH[3]}))

# A base without a version had no versioned output, so any version is new.
if [ -z "$old" ]; then
	exit 0
fi
if ! [[ $old =~ $semver_re ]]; then
	echo "::error::score_regression.sh: $version at the base, \"$old\", isn't MAJOR.MINOR.PATCH" >&2
	exit 2
fi
old_major=$((10#${BASH_REMATCH[1]}))
old_minor=$((10#${BASH_REMATCH[2]}))
old_patch=$((10#${BASH_REMATCH[3]}))

if ((new_major != old_major || new_minor != old_minor)); then
	if ((new_major > old_major || (new_major == old_major && new_minor > old_minor))); then
		exit 0
	fi
	fail "lowers $version from $old to $new"
fi
if ((new_patch <= old_patch)); then
	fail "leaves $version at $new, no higher than the base's $old"
fi

# A PATCH-only bump: each golden must be the base's with v replaced. Every
# line is a bucket whose JSON starts with its v.
if [ -z "${SCORE_BASE:-}" ] || [ -z "${SCORE_HEAD:-}" ]; then
	fail "bumps only PATCH, and without SCORE_BASE and SCORE_HEAD the guard can't check that only v changed"
fi
old_re=${old//./\\.}
moved=""
while IFS=$'\t' read -r status before after; do
	[ -n "$status" ] || continue
	if base_file=$(git show "$SCORE_BASE:$before" 2>/dev/null) &&
		head_file=$(git show "$SCORE_HEAD:$after" 2>/dev/null) &&
		[ "$(printf '%s\n' "$base_file" | sed -e "s/^{\"v\":\"$old_re\",/{\"v\":\"$new\",/")" = "$head_file" ]; then
		continue
	fi
	moved="$moved$status	$before	$after
"
done <<<"$changed"
if [ -n "$moved" ]; then
	changed=$moved
	fail "bumps only PATCH, $old to $new, while these goldens differ in more than v"
fi
exit 0
