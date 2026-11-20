// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"context"
	"net"
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
	// connect connects to the session bus before ctx ends.
	connect func(ctx context.Context) (*dbus.Conn, error)

	mu     sync.Mutex
	conn   *dbus.Conn
	closed bool // after close, samples fail rather than reconnect
}

func (m *mutterIdle) idle(ctx context.Context) (time.Duration, error) {
	return m.sample(ctx, sampleTimeout)
}

// sample reads the idle time, connecting first when there is no live
// connection. Both are done within limit, or by ctx's deadline if that is
// sooner.
func (m *mutterIdle) sample(ctx context.Context, limit time.Duration) (time.Duration, error) {
	deadline, err := sampleDeadline(ctx, limit)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, net.ErrClosed
	}
	if m.conn != nil && !m.conn.Connected() {
		m.conn.Close()
		m.conn = nil
	}
	if m.conn == nil {
		conn, err := m.connect(ctx)
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
	m.closed = true
	if m.conn != nil {
		m.conn.Close()
		m.conn = nil
	}
}
