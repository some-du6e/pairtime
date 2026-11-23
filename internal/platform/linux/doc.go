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
//	focus            _NET_ACTIVE_WINDOW, with    unknown: Wayland gives     unknown
//	                 each window's process from  other programs no focus
//	                 X-Resource                  signal
//	suspend, resume  logind PrepareForSleep, with a delay lock, on every session type
//	lock, unlock     logind's LockedHint on the session, on every session type. GNOME
//	                 Shell and kscreenlocker set it; i3lock, slock and xsecurelock
//	                 don't, and their locks go unseen
//
// A signal the desktop doesn't give is reported as unknown, with the reason
// in Status, and never guessed. Where focus is unknown, the agent records
// an os.focus with app_class unknown, whose slots SM 2.5 credits at level
// C; the Focus channel says each time focus turns unknown, and its next
// window report says it is known again (Desktop.Focus). On Wayland the X11
// calls would only see XWayland's own clients, so they aren't used there
// even when DISPLAY is set.
//
// X11 is spoken directly over the display's socket, in pure Go, rather than
// through libX11 and libXss loaded with purego: linking purego makes the
// whole pairtime binary dynamically linked against glibc, Xlib's default
// error handlers exit the process when a window vanishes mid-query or the
// display goes away, and libXss isn't installed everywhere. The protocol
// subset is small: connection setup, InternAtom, GetProperty,
// ChangeWindowAttributes, QueryExtension, the screen saver extension's
// QueryVersion and QueryInfo, and X-Resource's QueryVersion and
// QueryClientIds.
//
// Nothing here reads input devices, records keys or reads window titles.
// The only window properties read are _NET_ACTIVE_WINDOW, _NET_SUPPORTED and
// _NET_SUPPORTING_WM_CHECK on the root window; _NET_SUPPORTING_WM_CHECK on
// the window that property names, which shows the window manager is alive;
// and WM_CLIENT_MACHINE on the focused window, with its _NET_WM_PID only on
// an X server without X-Resource 1.2. Otherwise the focused window's process
// comes from X-Resource's QueryClientIds, the server's record of which
// process opened the connection that created the window.
//
// Idle samples cost one round trip to the display server or Mutter, made
// when the agent asks. Focus, power and lock changes arrive as X events and
// D-Bus signals on blocked reads, so between changes the package wakes the
// CPU not at all and owns no timers. The exceptions are one armed for
// focusSettle after each change of _NET_ACTIVE_WINDOW, one while a suspend
// waits for Power.Ready, and one while focus waits to reconnect to an X
// server it lost.
//
// # The Secret Service
//
// SecretService is the device keystore's Linux half (internal/agent/keys;
// DESIGN 3.6, 3.12; CP 5.2): items in the desktop's Secret Service, which
// GNOME Keyring, KWallet and KeePassXC provide, through go-keyring over
// godbus. It and Mutter's idle time look for the session bus the same way,
// in sessionBusAddress, and neither ever starts one. Its tests that need a
// session bus are in secretservice_desktop_test.go, which platform.yml runs
// inside a private bus like the other *_desktop_test.go files; fakesecret
// is the Secret Service they talk to.
package linux
