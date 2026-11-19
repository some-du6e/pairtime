#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# desktop_session.sh COMMAND [ARG...]
#
# Runs COMMAND inside a throwaway Linux desktop: an Xvfb display with the
# openbox window manager, and a private session D-Bus from dbus-run-session.
# platform.yml's Linux job uses it for the *_desktop_test.go tests
# (AGENTS.md). Before COMMAND it checks that the display, the window manager,
# xdotool focus switching and the bus all work, so a broken runner image
# fails here with a clear message instead of inside a test.
#
# Needs: Xvfb, openbox, xterm, xdotool, xdpyinfo and xprop (x11-utils),
# dbus-run-session and dbus-send.
set -euo pipefail

if [ $# -eq 0 ]; then
	echo "usage: desktop_session.sh COMMAND [ARG...]" >&2
	exit 2
fi

for tool in Xvfb openbox xterm xdotool xdpyinfo xprop dbus-run-session dbus-send; do
	if ! command -v "$tool" >/dev/null; then
		echo "::error::desktop_session.sh needs $tool, which isn't installed"
		exit 1
	fi
done

export DISPLAY=:99
pids=""
cleanup() {
	# shellcheck disable=SC2086
	[ -z "$pids" ] || kill $pids 2>/dev/null || true
}
trap cleanup EXIT

# wait_for SECONDS COMMAND... polls COMMAND until it succeeds.
wait_for() {
	local deadline=$((SECONDS + $1))
	shift
	until "$@" >/dev/null 2>&1; do
		if [ "$SECONDS" -ge "$deadline" ]; then
			echo "::error::timed out waiting for: $*"
			return 1
		fi
		sleep 0.2
	done
}

Xvfb "$DISPLAY" -screen 0 1280x1024x24 -nolisten tcp &
pids="$pids $!"
wait_for 30 xdpyinfo

openbox &
pids="$pids $!"
# An EWMH window manager announces itself on the root window. The focus code
# reads _NET_ACTIVE_WINDOW, which only a window manager like this one keeps.
wait_for 30 sh -c 'xprop -root _NET_SUPPORTING_WM_CHECK | grep -q "window id"'

# Smoke test: xdotool can raise and focus a window, and the window manager
# reports it as active. This is what the focus tests build on.
xterm -class PairtimeSmoke -e sleep 600 &
smoke=$!
pids="$pids $smoke"
wid=$(timeout 30 xdotool search --sync --limit 1 --class PairtimeSmoke)
timeout 30 xdotool windowactivate --sync "$wid"
active=$(xdotool getactivewindow)
if [ "$active" != "$wid" ]; then
	echo "::error::xdotool activated window $wid but the active window is $active"
	exit 1
fi
kill "$smoke"
echo "display $DISPLAY: Xvfb and openbox up, focus switching works"

# COMMAND gets its own session bus, which dbus-run-session tears down after.
# The inner script is single-quoted on purpose: the bus address only exists
# inside the session.
# shellcheck disable=SC2016
dbus-run-session -- sh -c '
	dbus-send --session --print-reply --dest=org.freedesktop.DBus \
		/org/freedesktop/DBus org.freedesktop.DBus.GetId >/dev/null
	echo "private session bus up at $DBUS_SESSION_BUS_ADDRESS"
	exec "$@"
' desktop_session "$@"
