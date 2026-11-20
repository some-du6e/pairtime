// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

// Session is the kind of graphical session the agent runs in.
type Session string

const (
	SessionX11     Session = "x11"
	SessionWayland Session = "wayland"
	// SessionNone is a console, an ssh login, or an agent started without
	// the desktop's environment.
	SessionNone Session = "none"
)

// detectSession reads the session type from the environment the desktop
// gives its programs.
//
// XDG_SESSION_TYPE comes first, but only its "wayland" and "x11" values
// settle it: startx and a compositor started from a console both run in a
// session logind registered as "tty", so those fall through to the display
// variables. WAYLAND_DISPLAY beats DISPLAY, because a Wayland compositor
// also sets DISPLAY for XWayland, whose idle counter and active window only
// cover X clients.
func detectSession(getenv func(string) string) Session {
	switch getenv("XDG_SESSION_TYPE") {
	case "wayland":
		return SessionWayland
	case "x11":
		if getenv("DISPLAY") != "" {
			return SessionX11
		}
		return SessionNone
	}
	if getenv("WAYLAND_DISPLAY") != "" {
		return SessionWayland
	}
	if getenv("DISPLAY") != "" {
		return SessionX11
	}
	return SessionNone
}
