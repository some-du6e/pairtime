// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

// Package linux reads what a Linux desktop says about the person in front
// of it: how long since the last keyboard or mouse input, which window has
// focus, and when the machine suspends, resumes, locks or unlocks (DESIGN
// 3.3; PLAN WP-OS4). The agent turns these into os.presence, os.focus and
// os.power events.
//
// Where each signal comes from:
//
//	                 X11 session                 GNOME on Wayland           other Wayland, console
//	idle time        MIT-SCREEN-SAVER            org.gnome.Mutter.          unknown (KDE and wlroots
//	                 (Mutter as a fallback)      IdleMonitor                come with ext-idle-notify-v1)
//	focus            _NET_ACTIVE_WINDOW and      unknown: no portable       unknown
//	                 _NET_WM_PID                 signal (SM 3.2)
//	suspend, resume  logind PrepareForSleep, with a delay lock, on every session type
//	lock, unlock     logind's LockedHint on the session, on every session type
//
// A signal the desktop doesn't give is reported as unknown, with the reason
// in Status, and never guessed. On Wayland the X11 calls would only see
// XWayland's own clients, so they aren't used there even when DISPLAY is
// set.
//
// X11 is spoken directly over the display's socket, in pure Go, rather than
// through libX11 and libXss loaded with purego: linking purego makes the
// whole pairtime binary dynamically linked against glibc, Xlib's default
// error handlers exit the process when a window vanishes mid-query or the
// display goes away, and libXss isn't installed everywhere. The protocol
// subset is small: connection setup, InternAtom, GetProperty,
// ChangeWindowAttributes, QueryExtension and the screen saver extension's
// QueryVersion and QueryInfo.
//
// Nothing here reads input devices, records keys or reads window titles.
// The only window properties read are _NET_ACTIVE_WINDOW on the root
// window, and _NET_WM_PID and WM_CLIENT_MACHINE on the focused window.
//
// Idle samples cost one round trip to the display server or Mutter, made
// when the agent asks. Focus, power and lock changes arrive as X events and
// D-Bus signals on blocked reads, so between changes the package wakes the
// CPU not at all and owns no timers, apart from one armed while a suspend
// waits for Power.Ready.
package linux
