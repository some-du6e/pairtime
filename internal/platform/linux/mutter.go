// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// GNOME's compositor, Mutter, exports its idle counter on the session bus
// (data/dbus-interfaces/org.gnome.Mutter.IdleMonitor.xml in GNOME/mutter;
// HS 13.4). It answers on Wayland, where no X11 call can see input to
// Wayland clients, and on X11.
const (
	mutterName  = "org.gnome.Mutter.IdleMonitor"
	mutterPath  = dbus.ObjectPath("/org/gnome/Mutter/IdleMonitor/Core")
	mutterIface = "org.gnome.Mutter.IdleMonitor"
)

// mutterIdle reads idle time from Mutter's IdleMonitor.GetIdletime, the
// milliseconds since the last user input. It keeps one session bus
// connection, and reconnects on the next sample after it drops.
type mutterIdle struct {
	connect func() (*dbus.Conn, error)

	mu   sync.Mutex
	conn *dbus.Conn
}

func (m *mutterIdle) idle(ctx context.Context) (time.Duration, error) {
	timeout, err := callTimeout(ctx)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conn != nil && !m.conn.Connected() {
		m.conn.Close()
		m.conn = nil
	}
	if m.conn == nil {
		conn, err := m.connect()
		if err != nil {
			return 0, err
		}
		m.conn = conn
	}
	var ms uint64
	// No auto-start: Mutter isn't activatable, and a bus that tried to
	// start something for this call would be doing the wrong thing.
	err = m.conn.Object(mutterName, mutterPath).
		CallWithContext(ctx, mutterIface+".GetIdletime", dbus.FlagNoAutoStart).
		Store(&ms)
	if err != nil {
		return 0, err
	}
	return time.Duration(ms) * time.Millisecond, nil
}

func (m *mutterIdle) name() string { return mutterName }

func (m *mutterIdle) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conn != nil {
		m.conn.Close()
		m.conn = nil
	}
}

// sessionBusAddress is the session bus the desktop gave its programs:
// DBUS_SESSION_BUS_ADDRESS, else the socket systemd's user manager keeps
// at $XDG_RUNTIME_DIR/bus. Unlike dbus.ConnectSessionBus, it never runs
// dbus-launch to start a bus of its own when there is none.
func sessionBusAddress(getenv func(string) string) (string, error) {
	if a := getenv("DBUS_SESSION_BUS_ADDRESS"); a != "" && a != "autolaunch:" {
		return a, nil
	}
	if dir := getenv("XDG_RUNTIME_DIR"); dir != "" {
		path := filepath.Join(dir, "bus")
		if fi, err := os.Stat(path); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return "unix:path=" + path, nil
		}
	}
	return "", errors.New("no session bus: DBUS_SESSION_BUS_ADDRESS is unset and $XDG_RUNTIME_DIR/bus doesn't exist")
}

// connectBus opens a private connection to the bus at address: dial,
// authenticate as this user, and say hello.
func connectBus(address string) (*dbus.Conn, error) {
	conn, err := dbus.Dial(address)
	if err != nil {
		return nil, err
	}
	if err := conn.Auth(nil); err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.Hello(); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}
