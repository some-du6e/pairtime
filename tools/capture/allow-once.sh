#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# allow-once.sh RUN_DIR LABEL [ARG...]
#
# Claude Code's PermissionRequest hook for a capture (SCRIPTS.md, Claude
# Code, step 22). It records every call with dump-hook.sh, like every other
# hook. Then, the first time a payload names step 22's file, it waits 2
# seconds and prints Claude Code's allow decision, so the capture holds one
# permission prompt that a hook answered, not the person (SM 1.2), and keeps
# that decision in RUN_DIR/allow-once, so the fixture shows what Claude Code
# was sent. Every other call prints nothing, which leaves the prompt to the
# person. It always exits 0, and without arguments it exits before reading
# anything.

# Step 22's file, which its prompt in SCRIPTS.md names, and the decision:
# allow this one call, with no rule kept for later ones.
file='hook-allowed.txt'
decision='{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}'

trap 'exit 0' EXIT
if [ "$#" -lt 2 ] || [ -z "$1" ]; then
	exit 0
fi
kit=$(dirname "$0")
mkdir -p "$1" 2>/dev/null
[ -d "$1" ] || exit 0

# The payload goes to a file of this call's own, so the dumper gets it on
# stdin unchanged and the check below can read it after.
payload=$1/.allow-once.$$
if [ -t 0 ]; then
	: >"$payload"
else
	cat >"$payload"
fi 2>/dev/null
sh "$kit/dump-hook.sh" "$@" <"$payload" >/dev/null 2>&1

# Only the call that creates allow-once answers. Under set -C the shell
# itself creates it with O_CREAT|O_EXCL, so only one call ever can, whatever
# mkdir says (see dump-hook.sh). The wait makes the hook's answer come
# seconds after the request, as a person's would, so a reader can't tell
# the two apart by timing and has to go by what the harness reports.
if grep -qF "$file" "$payload" 2>/dev/null && (set -C && printf '%s\n' "$decision" >"$1/allow-once") 2>/dev/null; then
	sleep 2
	printf '%s\n' "$decision"
fi
rm -f "$payload"
exit 0
