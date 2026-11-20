#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# test_existing.sh [GO_TEST_FLAG...] -- DIR...
#
# Runs go test over DIR/... for each DIR that exists, and passes without
# running anything when none does. Workflows name the packages they test
# before those packages are written (docs/PLAN.md, WP-CI1), so a missing
# directory is normal, not an error.
#
# Written for bash 3.2, which is what /bin/bash is on macOS.
set -euo pipefail

flags=""
while [ $# -gt 0 ] && [ "$1" != "--" ]; do
	flags="$flags $1"
	shift
done
if [ $# -eq 0 ]; then
	echo "usage: test_existing.sh [GO_TEST_FLAG...] -- DIR..." >&2
	exit 2
fi
shift

pkgs=""
missing=""
for dir in "$@"; do
	if [ -d "$dir" ]; then
		pkgs="$pkgs ./$dir/..."
	else
		echo "skipping $dir: not in the tree yet"
		missing="$missing $dir"
	fi
done

# The job summary says what this step tested, so a run can be checked
# against the directories its PR meant to test (AGENTS.md, PLATFORM_DIRS).
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
	{
		echo "### go test$flags"
		echo
		echo "Tested:${pkgs:- nothing}"
		if [ -n "$missing" ]; then
			echo
			echo "Not in the tree yet:$missing"
		fi
	} >>"$GITHUB_STEP_SUMMARY"
fi

if [ -z "$pkgs" ]; then
	echo "::notice::none of these packages exist yet, so there is nothing to test: $*"
	exit 0
fi

set -x
# Word splitting is intended: flags and package patterns never contain spaces.
# shellcheck disable=SC2086
go test $flags $pkgs
