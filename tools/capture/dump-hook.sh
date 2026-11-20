#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# dump-hook.sh RUN_DIR LABEL [ARG...]
#
# A hook command for capturing harness fixtures (tools/capture/README.md).
# Each call claims the next number under RUN_DIR/hooks/ and writes what the
# harness handed the hook, one file each:
#
#   time     when the call arrived: unix_ns, utc and the clock used
#   argv     the arguments, one per line (RUN_DIR and LABEL first)
#   env      the names of the environment variables, sorted. Never the values
#   cwd      the working directory
#   parents  pid and executable name of each parent process, nearest first.
#            Never command lines, which can hold prompts
#   stdin    the bytes on stdin, unchanged
#   stderr   only if something in this script failed
#
# LABEL is the hook event the entry was registered for, such as
# UserPromptSubmit, because some harnesses don't name the event in the payload.
#
# It prints nothing, never blocks the harness and always exits 0: harnesses
# feed a hook's stdout to the model on some events, and a non-zero exit can
# block a prompt or a tool call. Nothing is sent anywhere.

# The arrival time comes first, before anything slow.
clock=gnu-date
now_ns=$(date +%s%N 2>/dev/null)
case $now_ns in
'' | *[!0-9]*)
	# BSD date (macOS) has no %N. perl ships with macOS and has microseconds.
	clock=perl
	now_ns=$(perl -MTime::HiRes=gettimeofday -e '($s, $u) = gettimeofday(); printf("%d%06d000\n", $s, $u)' 2>/dev/null)
	case $now_ns in
	'' | *[!0-9]*)
		clock=date-seconds
		now_ns=$(date +%s)000000000
		;;
	esac
	;;
esac
utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)

exec >/dev/null 2>&1
trap 'exit 0' EXIT

if [ "$#" -lt 2 ] || [ -z "$1" ]; then
	exit 0
fi
hooks=$1/hooks
mkdir -p "$hooks" || exit 0

# entries DIR prints how many entries DIR has. Positional parameters are
# local to a function call, so set -- here leaves the script's alone.
entries() {
	set -- "$1"/*
	if [ -e "$1" ] || [ -L "$1" ]; then
		echo "$#"
	else
		echo 0
	fi
}

# Claim the next number. mkdir is atomic, so two hooks that fire at the same
# moment never share a directory.
n=$(entries "$hooks")
while :; do
	n=$((n + 1))
	if [ "$n" -gt 999999 ]; then
		exit 0
	fi
	call=$hooks/$(printf '%06d' "$n")
	if mkdir "$call"; then
		break
	fi
done
exec 2>>"$call/stderr"

printf 'unix_ns=%s\nutc=%s\nclock=%s\n' "$now_ns" "$utc" "$clock" >"$call/time"

for arg in "$@"; do
	printf '%s\n' "$arg"
done >"$call/argv"

# awk's ENVIRON holds the names apart from the values, so no value can be
# mistaken for a name, whatever it contains.
awk 'BEGIN { for (name in ENVIRON) print name }' | LC_ALL=C sort >"$call/env"

pwd >"$call/cwd"

# Walk up from our parent through one ps listing. comm is the executable
# name (a full path on macOS, so keep only its last part); unlike args it
# never includes the command line.
ps -A -o pid= -o ppid= -o comm= 2>/dev/null | awk -v start="$PPID" '
	{
		pid = $1
		ppid[pid] = $2
		line = $0
		sub(/^[ \t]*[0-9]+[ \t]+[0-9]+[ \t]*/, "", line)
		sub(/.*\//, "", line)
		name[pid] = line
	}
	END {
		p = start
		for (i = 0; i < 12 && p != "" && p != 0; i++) {
			if (!(p in name)) {
				print p " ?"
				break
			}
			print p " " name[p]
			if (ppid[p] == p) {
				break
			}
			p = ppid[p]
		}
	}' >"$call/parents"

# Last, because it can block until the harness closes the pipe. A terminal
# on stdin means a person ran this by hand: write an empty file and go.
if [ -t 0 ]; then
	: >"$call/stdin"
else
	cat >"$call/stdin"
fi

if [ ! -s "$call/stderr" ]; then
	rm -f "$call/stderr"
fi
exit 0
