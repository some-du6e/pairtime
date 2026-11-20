#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# oracle_guard.sh < NAME_STATUS
#
# Fails a pull request that changes an existing oracle together with the code
# it checks (docs/PLAN.md, ground rule 5). An oracle is a file that decides
# whether code is right, so a PR that edits both could grade its own work.
#
# Input: `git diff --name-status -M` lines on stdin, comparing the PR's base
# with its merge commit; ci.yml pipes them in. The PR's labels come from the
# pull_request event payload at $GITHUB_EVENT_PATH (pull_request.labels[].name
# in GitHub's documented payload). Without a payload there are no labels.
#
# A PR fails when it modifies, deletes, renames or retypes an oracle path and
# also touches cmd/, internal/, pkg/, web/, extension/ or integrations/,
# unless it carries the oracle-change label, which only Karim adds. Adding a
# new oracle file next to new code passes.
#
# Exit status: 0 pass, 1 blocked, 2 bad input or a missing tool.
set -euo pipefail

label=oracle-change

# The oracle paths of ground rule 5. In a case pattern * also matches /.
is_oracle() {
	case "$1" in
	docs/DESIGN.md | docs/BOUNTY.md | tools/ledger_ref.py) return 0 ;;
	docs/design/* | docs/research/*) return 0 ;;
	testdata/timelines/* | testdata/ledger/* | testdata/fixtures/*) return 0 ;;
	testdata/upstream/* | testdata/checks/* | testdata/redteam/*) return 0 ;;
	esac
	return 1
}

# integrations/ holds the program kit, which testdata/upstream's captures
# check (docs/PLAN.md, WP-PG0, WP-PG2, WP-PG8, WP-PG10).
is_code() {
	case "$1" in
	cmd/* | internal/* | pkg/* | web/* | extension/* | integrations/*) return 0 ;;
	esac
	return 1
}

# git quotes a path with unusual characters ("pkg/a\tb.go"). The directory
# prefix that the patterns look at is plain ASCII either way, so dropping the
# opening quote is enough to classify it.
unquote() {
	local p=$1
	p=${p#\"}
	printf '%s' "$p"
}

changed_oracles=""
changed_code=""
note_oracle() { changed_oracles="$changed_oracles$1	$2
"; }
note_code() {
	if is_code "$1"; then changed_code="$changed_code$1
"; fi
}

lines=0
while IFS=$'\t' read -r status first second || [ -n "$status" ]; do
	[ -n "$status" ] || continue
	lines=$((lines + 1))
	first=$(unquote "$first")
	second=$(unquote "${second:-}")
	if [ -z "$first" ]; then
		echo "::error::oracle_guard.sh: malformed name-status line: $status" >&2
		exit 2
	fi
	case "$status" in
	A)
		# A new oracle beside new code is how oracles are meant to arrive.
		note_code "$first"
		;;
	R*)
		# A rename moves the old oracle away, so it counts as a change.
		if is_oracle "$first"; then note_oracle "$first" renamed; fi
		note_code "$first"
		note_code "$second"
		;;
	C*)
		# A copy leaves the source untouched and adds the destination.
		note_code "$second"
		;;
	M) if is_oracle "$first"; then note_oracle "$first" modified; fi; note_code "$first" ;;
	D) if is_oracle "$first"; then note_oracle "$first" deleted; fi; note_code "$first" ;;
	T) if is_oracle "$first"; then note_oracle "$first" "changed type"; fi; note_code "$first" ;;
	*)
		# Unmerged, unknown or broken pairs: treat as a change, to be safe.
		if is_oracle "$first"; then note_oracle "$first" "changed ($status)"; fi
		note_code "$first"
		;;
	esac
done

labeled=false
if [ -n "${GITHUB_EVENT_PATH:-}" ]; then
	if ! command -v jq >/dev/null; then
		echo "::error::oracle_guard.sh needs jq to read the event payload" >&2
		exit 2
	fi
	if ! names=$(jq -r '.pull_request.labels[]?.name' "$GITHUB_EVENT_PATH"); then
		echo "::error::oracle_guard.sh can't read the event payload at $GITHUB_EVENT_PATH" >&2
		exit 2
	fi
	while IFS= read -r name; do
		if [ "$name" = "$label" ]; then labeled=true; fi
	done <<<"$names"
fi

oracle_count=$(printf '%s' "$changed_oracles" | grep -c . || true)
code_count=$(printf '%s' "$changed_code" | grep -c . || true)
echo "oracle-guard: $lines changed paths, $oracle_count existing oracles changed, $code_count code paths changed, $label label: $labeled"

if [ "$oracle_count" -eq 0 ] || [ "$code_count" -eq 0 ]; then
	exit 0
fi

if [ "$labeled" = true ]; then
	echo "::notice::the $label label allows this PR to change oracles and code together"
	printf '%s' "$changed_oracles" | while IFS=$'\t' read -r path what; do
		echo "  oracle $what: $path"
	done
	exit 0
fi

printf '%s' "$changed_oracles" | while IFS=$'\t' read -r path what; do
	echo "::error file=$path::oracle $what in a PR that also changes code (docs/PLAN.md, ground rule 5)"
done
echo "Code paths changed in the same PR:"
printf '%s' "$changed_code" | sed 's/^/  /'
cat <<EOF
This PR changes an existing oracle and the code it checks. An oracle decides
whether code is right, so the two change in separate PRs. Either move the
oracle change into its own PR, or, when an SM worked example or fixture
disagrees with the spec, write docs/errata/<date>-<topic>.md, add the erratum
label and stop. Karim adds the $label label when both really belong in one PR.
EOF
exit 1
