// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/godbus/dbus/v5"
)

// The names from the Desktop Notifications Specification, version 1.2,
// which every current notification server implements.
const (
	notifyBusName = "org.freedesktop.Notifications"
	notifyPath    = dbus.ObjectPath("/org/freedesktop/Notifications")
	notifyIface   = "org.freedesktop.Notifications"
)

var (
	// ErrNotifyNoBus means this login has no session D-Bus, as on a machine
	// with no desktop. Notify never starts one.
	ErrNotifyNoBus = errors.New("notifications: no session D-Bus")
	// ErrNotifyNoServer means nothing on the session bus shows
	// notifications, and the bus can't start anything that does.
	ErrNotifyNoServer = errors.New("notifications: no notification server on the session bus")
)

// Notifications shows desktop notifications through
// org.freedesktop.Notifications.Notify on the session D-Bus, with godbus
// (DESIGN 3.3, 3.12). Each call opens its own connection and closes it
// before it returns, so nothing stays connected between notifications. The
// zero value is ready to use.
type Notifications struct {
	// Address is the session bus. "" means this login's:
	// $DBUS_SESSION_BUS_ADDRESS, else the socket $XDG_RUNTIME_DIR/bus, else
	// /run/user/<uid>/bus, each socket only if this user owns it. Notify
	// never starts a bus: godbus's dbus-launch fallback isn't used.
	Address string
	// AppName is the app_name argument. "" means "pairtime".
	AppName string
	// NoAutoStart stops the bus from starting a notification server that
	// isn't running. Without it the bus may start one, which is how minimal
	// desktops run servers such as dunst and mako.
	NoAutoStart bool
}

// Notify shows a notification with summary as its title and body as its
// text, and returns the server's id for it. It asks the server for its
// capabilities first, and escapes the markup characters in body for a
// server that reads markup in bodies, as the specification says to. The
// notification has normal urgency, no actions and the server's own expiry,
// and asks the server not to play a sound: it is DESIGN 1.2's one quiet
// notification.
//
// The call gives up when ctx is done, the connection included, even to a
// tcp: bus address whose host never answers. Errors wrap
// ErrNotifyNoBus or ErrNotifyNoServer when the desktop can't show
// notifications at all. They name D-Bus errors without repeating the
// server's message.
func (n Notifications) Notify(ctx context.Context, summary, body string) (uint32, error) {
	addr := n.Address
	if addr == "" {
		var ok bool
		if addr, ok = notifyBusAddress(os.Getenv, notifyBusSocket, os.Getuid()); !ok {
			return 0, ErrNotifyNoBus
		}
	}
	// connectBus bounds the dial and the handshake by ctx. WithContext then
	// closes the connection when ctx is done, which ends a call stuck on a
	// bus that doesn't answer.
	conn, err := connectBus(ctx, addr, dbus.WithContext(ctx))
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, fmt.Errorf("%w: connecting: %w", ErrNotifyNoBus, err)
	}
	defer conn.Close()

	var flags dbus.Flags
	if n.NoAutoStart {
		flags |= dbus.FlagNoAutoStart
	}
	obj := conn.Object(notifyBusName, notifyPath)
	var caps []string
	if err := obj.CallWithContext(ctx, notifyIface+".GetCapabilities", flags).Store(&caps); err != nil {
		return 0, notifyCallError(ctx, "GetCapabilities", err)
	}
	if slices.Contains(caps, "body-markup") {
		body = escapeNotifyMarkup(body)
	}
	appName := n.AppName
	if appName == "" {
		appName = "pairtime"
	}
	// Notify(STRING app_name, UINT32 replaces_id, STRING app_icon,
	// STRING summary, STRING body, as actions, a{sv} hints,
	// INT32 expire_timeout) -> UINT32 id: signature susssasa{sv}i.
	hints := map[string]dbus.Variant{
		"urgency":        dbus.MakeVariant(byte(1)), // normal
		"suppress-sound": dbus.MakeVariant(true),
	}
	var id uint32
	call := obj.CallWithContext(ctx, notifyIface+".Notify", flags,
		appName, uint32(0), "", summary, body, []string{}, hints, int32(-1))
	if err := call.Store(&id); err != nil {
		return 0, notifyCallError(ctx, "Notify", err)
	}
	return id, nil
}

// notifyBusAddress finds the session bus the way the reference D-Bus
// library, libdbus, does, minus autolaunch: $DBUS_SESSION_BUS_ADDRESS, or
// the socket bus that systemd's user session puts in $XDG_RUNTIME_DIR,
// which counts only when isBus says it is a socket that uid owns, as
// libdbus's _dbus_lookup_user_bus checks. "autolaunch:" counts as no
// address.
//
// Then, with the same check, comes /run/user/<uid>/bus, for an agent that
// a hook started from a shell without XDG_RUNTIME_DIR. That fallback is
// pairtime's own: libdbus has none.
func notifyBusAddress(getenv func(string) string, isBus func(name string, uid int) bool, uid int) (string, bool) {
	if a := getenv("DBUS_SESSION_BUS_ADDRESS"); a != "" && a != "autolaunch:" {
		return a, true
	}
	dirs := []string{"/run/user/" + strconv.Itoa(uid)}
	if d := getenv("XDG_RUNTIME_DIR"); d != "" {
		dirs = append([]string{d}, dirs...)
	}
	for _, d := range dirs {
		if p := filepath.Join(d, "bus"); isBus(p, uid) {
			return "unix:path=" + dbus.EscapeBusAddressValue(p), true
		}
	}
	return "", false
}

// notifyBusSocket reports whether name is a Unix socket that uid owns. It
// checks as libdbus does: with lstat, so a symlink doesn't count whatever
// it points to, and with the socket's owner, so a socket another user
// left in a runtime directory pairtime was pointed at doesn't either.
func notifyBusSocket(name string, uid int) bool {
	fi, err := os.Lstat(name)
	if err != nil || fi.Mode().Type() != fs.ModeSocket {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && uint64(st.Uid) == uint64(uid)
}

// escapeNotifyMarkup escapes the characters that would start markup in a
// notification body. The specification's body markup is a small XML-like
// subset, so the XML escapes are the ones servers read.
func escapeNotifyMarkup(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// notifyCallError says what a failed call means. A D-Bus error's message
// comes from the server and isn't pairtime's to repeat, so only its name is
// kept.
func notifyCallError(ctx context.Context, method string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var name string
	var de dbus.Error
	var dp *dbus.Error
	switch {
	case errors.As(err, &de):
		name = de.Name
	case errors.As(err, &dp) && dp != nil:
		name = dp.Name
	default:
		return fmt.Errorf("%s.%s: %w", notifyIface, method, err)
	}
	if notifyNoServer(name) {
		return fmt.Errorf("%w (%s)", ErrNotifyNoServer, name)
	}
	return fmt.Errorf("%s.%s: %s", notifyIface, method, name)
}

// notifyNoServer reports whether the D-Bus error name means that nobody
// owns the notification service's name and the bus couldn't start an owner.
func notifyNoServer(name string) bool {
	switch name {
	case "org.freedesktop.DBus.Error.ServiceUnknown", "org.freedesktop.DBus.Error.NameHasNoOwner":
		return true
	}
	return strings.HasPrefix(name, "org.freedesktop.DBus.Error.Spawn.")
}
