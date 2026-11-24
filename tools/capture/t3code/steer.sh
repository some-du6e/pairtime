#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Send a short probe, then a steer at a set delay after that turn's Stop hook
# (README.md, step 4). steer-helper.js's helper must be loaded in the page.
#
#   steer.sh SESSION COMPOSER HOOKS SHORT STEER DELAY [PREFIX]
#
# SESSION is the agent-browser session, COMPOSER the composer's element ref in
# its latest snapshot (such as @e81), HOOKS the capture's hooks.jsonl, SHORT
# and STEER the probes' labels (such as twentysix and twentyseven), DELAY the
# seconds to wait after the Stop, and PREFIX text to put before the steer, such
# as T3 Code's guest wrapper for a guest stand-in. It prints the time it sent
# the steer.
set -eu
session=$1 composer=$2 hooks=$3 short=$4 steer=$5 delay=$6 prefix=${7:-}
stops() { grep -c '"event": "Stop"' "$hooks" || true; }
n0=$(stops)
agent-browser --session "$session" fill "$composer" "Probe $short. Reply with the single word ok." >/dev/null 2>&1
agent-browser --session "$session" press Enter >/dev/null 2>&1
while [ "$(stops)" -le "$n0" ]; do sleep 0.01; done
sleep "$delay"
date -u +%T.%N
agent-browser --session "$session" eval "__steer(\"${prefix}Probe $steer, a steer sent as the turn ends. Reply with the single word late.\")" >/dev/null 2>&1
