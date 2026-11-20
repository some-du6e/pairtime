// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"context"
	"strings"
	"time"
)

// maxPID is Linux's PID_MAX_LIMIT. A larger _NET_WM_PID is garbage.
const maxPID = 1 << 22

// x11Focus follows the focused window through the window manager's
// _NET_ACTIVE_WINDOW property on the root window (EWMH 1.5), which changes
// with every focus change and announces itself with a PropertyNotify. The
// focused window's process comes from its _NET_WM_PID. It owns its own X
// connection, which sits in a blocked read between focus changes.
type x11Focus struct {
	c        *xconn
	hostname string

	active    uint32 // the _NET_ACTIVE_WINDOW atom
	pid       uint32 // _NET_WM_PID
	supported uint32 // _NET_SUPPORTED
	wmCheck   uint32 // _NET_SUPPORTING_WM_CHECK

	// ewmh is set once the window manager is known to keep
	// _NET_ACTIVE_WINDOW. Until then the root's property says nothing
	// about focus, so nothing is reported.
	ewmh bool
	// onEWMH is called the first time a PropertyNotify shows a window
	// manager keeping _NET_ACTIVE_WINDOW that the start-up check missed.
	onEWMH func()

	dirty bool   // _NET_ACTIVE_WINDOW changed since it was last read
	sent  bool   // a Focus has been reported
	last  uint32 // the window last reported
}

// newX11Focus takes over c, interns the atoms, starts listening for
// property changes on the root window and checks for a window manager
// that keeps _NET_ACTIVE_WINDOW. It closes c if it fails.
func newX11Focus(c *xconn, hostname string, timeout time.Duration) (*x11Focus, error) {
	w := &x11Focus{c: c, hostname: hostname}
	if err := w.setup(timeout); err != nil {
		c.close()
		return nil, err
	}
	return w, nil
}

func (w *x11Focus) setup(timeout time.Duration) error {
	if err := w.c.conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	w.c.onEvent = w.event
	for _, a := range []struct {
		atom *uint32
		name string
	}{
		{&w.active, "_NET_ACTIVE_WINDOW"},
		{&w.pid, "_NET_WM_PID"},
		{&w.supported, "_NET_SUPPORTED"},
		{&w.wmCheck, "_NET_SUPPORTING_WM_CHECK"},
	} {
		atom, err := w.c.internAtom(a.name)
		if err != nil {
			return err
		}
		*a.atom = atom
	}
	// Select before the first read, so a change between the two isn't
	// missed: it sets dirty, and run reads again.
	if err := w.c.selectPropertyChanges(w.c.root); err != nil {
		return err
	}
	ok, err := w.wmKeepsActiveWindow()
	if err != nil {
		return err
	}
	// A PropertyNotify during the check may have set both already.
	if ok {
		w.ewmh, w.dirty = true, true
	}
	return w.c.conn.SetDeadline(time.Time{})
}

// wmKeepsActiveWindow checks EWMH's two signs of a live window manager
// that maintains _NET_ACTIVE_WINDOW: _NET_SUPPORTING_WM_CHECK on the root
// names a window that names itself (a stale one from a dead window
// manager doesn't), and _NET_SUPPORTED lists _NET_ACTIVE_WINDOW.
func (w *x11Focus) wmKeepsActiveWindow() (bool, error) {
	check, ok, err := w.c.getCardinal(w.c.root, w.wmCheck, atomWindow)
	if err != nil || !ok {
		return false, err
	}
	self, ok, err := w.c.getCardinal(check, w.wmCheck, atomWindow)
	if isProtoError(err) {
		return false, nil // the window is gone: a stale check
	}
	if err != nil || !ok || self != check {
		return false, err
	}
	format, list, err := w.c.getProperty(w.c.root, w.supported, atomAtom, 4096)
	if err != nil || format != 32 {
		return false, err
	}
	for i := 0; i+4 <= len(list); i += 4 {
		if le32(list[i:]) == w.active {
			return true, nil
		}
	}
	return false, nil
}

// event notes a change to _NET_ACTIVE_WINDOW. Other root properties change
// too, such as the stacking list on every raise; they are dropped.
func (w *x11Focus) event(p []byte) {
	if p[0]&0x7f != eventPropertyNotify || le32(p[4:]) != w.c.root || le32(p[8:]) != w.active {
		return
	}
	if !w.ewmh {
		w.ewmh = true
		if w.onEWMH != nil {
			w.onEWMH()
		}
	}
	w.dirty = true
}

// run reports the focused window on out, once at the start and then once
// per change, until the connection fails or ctx ends. A burst of changes
// is read as one: each read takes the property's value at that moment.
func (w *x11Focus) run(ctx context.Context, out chan<- Focus) error {
	for {
		for w.dirty {
			w.dirty = false
			f, err := w.read()
			if err != nil {
				return err
			}
			if w.sent && f.Window == w.last {
				continue
			}
			w.sent, w.last = true, f.Window
			select {
			case out <- f:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		p, err := w.c.readPacket()
		if err != nil {
			return err
		}
		w.event(p)
	}
}

// read returns the focused window now. A window that vanished between the
// property change and this read reports no process.
func (w *x11Focus) read() (Focus, error) {
	win, _, err := w.c.getCardinal(w.c.root, w.active, atomWindow)
	if isProtoError(err) {
		win, err = 0, nil
	}
	if err != nil {
		return Focus{}, err
	}
	f := Focus{At: time.Now(), Window: win}
	if win != 0 {
		f.PID, err = w.windowPID(win)
	}
	return f, err
}

// windowPID returns the focused window's _NET_WM_PID, or 0 when the window
// doesn't say, is gone, or belongs to a client on another host: EWMH says a
// pid only means something together with WM_CLIENT_MACHINE, and a client
// forwarded over ssh -X or running in a container reports a pid from a
// process table that isn't this one.
func (w *x11Focus) windowPID(win uint32) (int, error) {
	pid, ok, err := w.c.getCardinal(win, w.pid, atomCardinal)
	if isProtoError(err) {
		return 0, nil
	}
	if err != nil || !ok || pid == 0 || pid > maxPID {
		return 0, err
	}
	_, machine, err := w.c.getProperty(win, atomWMClientMachine, atomString, 64)
	if isProtoError(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if len(machine) > 0 && w.hostname != "" && !sameHost(string(machine), w.hostname) {
		return 0, nil
	}
	return int(pid), nil
}

// sameHost compares two host names the way they differ on one machine:
// with or without a domain, and in any case.
func sameHost(a, b string) bool {
	short := func(s string) string {
		s, _, _ = strings.Cut(s, ".")
		return s
	}
	return strings.EqualFold(a, b) || strings.EqualFold(short(a), short(b))
}

func (w *x11Focus) close() { w.c.close() }
