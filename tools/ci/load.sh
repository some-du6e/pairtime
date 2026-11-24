#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# load.sh NAME DIR
#
# Runs internal/agent's TestSustainedLoad in the checkout DIR under
# -cpuprofile, for load.yml (#393), writing $RUNNER_TEMP/NAME.json, the
# test's numbers, NAME.cpu.out and NAME.log. While the test marks the last
# 30 s of its active span (PAIRTIME_LOAD_MARKS), bpftrace counts that
# process's opens, directory reads, stats, reads and writes into
# NAME.syscalls.txt, when it is installed. The span the test measures the
# CPU over has nothing tracing it.
set -euo pipefail

name=$1
dir=$2
tmp=${RUNNER_TEMP:?}
marks="$tmp/marks-$name"
mkdir -p "$marks"

(
	cd "$dir" || exit 1
	PAIRTIME_LOAD_REPORT="$tmp/$name.json" PAIRTIME_LOAD_MARKS="$marks" \
		go test ./internal/agent -run '^TestSustainedLoad$' -count=1 -v -timeout 25m -cpuprofile "$tmp/$name.cpu.out"
) > "$tmp/$name.log" 2>&1 &
test=$!

# wait_for FILE waits until FILE is there, or the test has ended without
# it, and says which.
wait_for() {
	until [ -f "$1" ] || ! kill -0 "$test" 2> /dev/null; do
		sleep 0.5
	done
	[ -f "$1" ]
}

if command -v bpftrace > /dev/null && wait_for "$marks/count-start"; then
	pid=$(< "$marks/count-start")
	prog=""
	for call in openat getdents64 newfstatat statx read write; do
		prog+="tracepoint:syscalls:sys_enter_$call /pid == $pid/ { @[\"$call\"] = count(); } "
	done
	sudo bpftrace -q -o "$tmp/$name.syscalls.txt" -e "$prog" &
	trace=$!
	wait_for "$marks/count-end" || true
	sudo kill -INT "$trace" 2> /dev/null || true
	wait "$trace" || true
fi

status=0
wait "$test" || status=$?
cat "$tmp/$name.log"
exit "$status"
