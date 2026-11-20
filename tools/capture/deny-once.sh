#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# deny-once.sh RUN_DIR LABEL [ARG...]
#
# Gemini CLI's AfterAgent hook for a capture (SCRIPTS.md, Gemini CLI, step
# 19). It records every call with dump-hook.sh, like every other hook. Then,
# the first time a payload holds step 19's prompt, it prints Gemini's deny
# decision, which sends the reason back to the model as a new prompt, so the
# capture holds one AfterAgent deny-retry (HS 7.1; SM 2.2 rule 2). Every
# other call prints nothing. It always exits 0, and without arguments it
# exits before reading anything.

# Step 19's prompt, and the reason Gemini gets back. SCRIPTS.md has both
# word for word, so the scrub knows them as the script's text.
prompt='Name one fruit, in one word.'
reason='Name a different fruit, in one word.'

trap 'exit 0' EXIT
if [ "$#" -lt 2 ] || [ -z "$1" ]; then
	exit 0
fi
kit=$(dirname "$0")
mkdir -p "$1" 2>/dev/null || exit 0

# The payload goes to a file of this call's own, so the dumper gets it on
# stdin unchanged and the check below can read it after.
payload=$1/.deny-once.$$
if [ -t 0 ]; then
	: >"$payload"
else
	cat >"$payload"
fi 2>/dev/null
sh "$kit/dump-hook.sh" "$@" <"$payload" >/dev/null 2>&1

# mkdir is atomic, so only one call ever wins the denial.
if grep -qF "$prompt" "$payload" 2>/dev/null && mkdir "$1/deny-once" 2>/dev/null; then
	printf '{"decision":"deny","reason":"%s"}\n' "$reason"
fi
rm -f "$payload"
exit 0
