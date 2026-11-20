#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# deny-once.sh RUN_DIR LABEL [ARG...]
#
# Gemini CLI's AfterAgent hook for a capture (SCRIPTS.md, Gemini CLI, step
# 19). It records every call with dump-hook.sh, like every other hook. Then,
# the first time a payload holds step 19's prompt, it prints Gemini's deny
# decision, which sends the reason back to the model as a new prompt, so the
# capture holds one AfterAgent deny-retry (HS 7.1; SM 2.2 rule 2), and keeps
# that decision in RUN_DIR/deny-once, so the fixture shows what Gemini was
# sent. Every other call prints nothing. It always exits 0, and without
# arguments it exits before reading anything.

# Step 19's prompt, and the reason Gemini gets back. SCRIPTS.md has both
# word for word, so the scrub knows them as the script's text.
prompt='Name one fruit, in one word.'
reason='Name a different fruit, in one word.'

trap 'exit 0' EXIT
if [ "$#" -lt 2 ] || [ -z "$1" ]; then
	exit 0
fi
kit=$(dirname "$0")
mkdir -p "$1" 2>/dev/null
[ -d "$1" ] || exit 0

# The payload goes to a file of this call's own, so the dumper gets it on
# stdin unchanged and the check below can read it after.
payload=$1/.deny-once.$$
if [ -t 0 ]; then
	: >"$payload"
else
	cat >"$payload"
fi 2>/dev/null
sh "$kit/dump-hook.sh" "$@" <"$payload" >/dev/null 2>&1

# Only the call that creates deny-once turns the answer down. Under set -C
# the shell itself creates it with O_CREAT|O_EXCL, so only one call ever
# can. mkdir couldn't decide that: some versions tell two processes racing
# for one folder that each of them made it (see dump-hook.sh).
decision=$(printf '{"decision":"deny","reason":"%s"}' "$reason")
if grep -qF "$prompt" "$payload" 2>/dev/null && (set -C && printf '%s\n' "$decision" >"$1/deny-once") 2>/dev/null; then
	printf '%s\n' "$decision"
fi
rm -f "$payload"
exit 0
