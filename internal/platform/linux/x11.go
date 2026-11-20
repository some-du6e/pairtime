// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// The X11 protocol subset this package speaks. Numbers are from the X
// Window System Protocol (X11R7.7) and the MIT-SCREEN-SAVER extension
// (xcb-proto's screensaver.xml). Every request goes out in little-endian
// byte order, which the connection setup asks for, so the server answers
// in it too, whatever the host.
const (
	xPacketError = 0
	xPacketReply = 1

	opChangeWindowAttributes = 2
	opInternAtom             = 16
	opGetProperty            = 20
	opQueryExtension         = 98

	eventPropertyNotify = 28
	eventGeneric        = 35 // the one event with a length field, like a reply

	cwEventMask        = 1 << 11
	propertyChangeMask = 1 << 22

	// Predefined atoms (Xatom.h).
	atomAtom            = 4
	atomCardinal        = 6
	atomString          = 31
	atomWindow          = 33
	atomWMClientMachine = 36

	screenSaverExtension = "MIT-SCREEN-SAVER"
	ssQueryVersion       = 0
	ssQueryInfo          = 1
)

// maxReplyBytes bounds the extra bytes of one reply or event. pairtime's
// largest reply is _NET_SUPPORTED, a few hundred atoms; anything near this
// is a broken or hostile server, and the connection is dropped.
const maxReplyBytes = 1 << 18

// xDisplay is a local X display named by DISPLAY.
type xDisplay struct {
	number string // the display number, spelled as Xauthority spells it
	screen int
}

// errRemoteDisplay means DISPLAY names a display on another host, such as
// one forwarded by ssh -X. Its input is someone else's, or nobody's.
var errRemoteDisplay = errors.New("DISPLAY names a display on another host")

// parseDisplay reads DISPLAY values of the forms ":N", ":N.S", "unix:N"
// and "unix:N.S". A host name means TCP to another machine, which pairtime
// doesn't measure (errRemoteDisplay).
func parseDisplay(s string) (xDisplay, error) {
	i := strings.LastIndexByte(s, ':')
	if s == "" || i < 0 {
		return xDisplay{}, errors.New("DISPLAY is not set to a display")
	}
	if host := s[:i]; host != "" && host != "unix" {
		return xDisplay{}, errRemoteDisplay
	}
	number, screen, hasScreen := strings.Cut(s[i+1:], ".")
	d := xDisplay{number: number}
	if !isDigits(number) {
		return xDisplay{}, errors.New("DISPLAY has no display number")
	}
	if hasScreen {
		n, err := strconv.Atoi(screen)
		if err != nil || !isDigits(screen) {
			return xDisplay{}, errors.New("DISPLAY has a malformed screen number")
		}
		d.screen = n
	}
	return d, nil
}

func isDigits(s string) bool {
	if s == "" || len(s) > 6 {
		return false
	}
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// socketPath is where a local X server listens: Xorg, Xvfb and XWayland
// all create it.
func (d xDisplay) socketPath() string {
	return "/tmp/.X11-unix/X" + d.number
}

// dialDisplay connects to the display's socket in the file system, then in
// Linux's abstract namespace, where Xorg also listens. The file comes
// first because sandboxes share the directory but not the abstract names.
func dialDisplay(d xDisplay, timeout time.Duration) (net.Conn, error) {
	path := d.socketPath()
	conn, err := net.DialTimeout("unix", path, timeout)
	if err == nil {
		return conn, nil
	}
	conn, err2 := net.DialTimeout("unix", "@"+path, timeout)
	if err2 == nil {
		return conn, nil
	}
	return nil, fmt.Errorf("can't connect to the X display: %w", err)
}

// xconn is one X client connection. It isn't safe for concurrent use: each
// is owned by one goroutine, which writes a request and reads until its
// reply, handing any event read on the way to onEvent.
type xconn struct {
	conn net.Conn
	seq  uint16 // the sequence number of the last request sent
	root uint32 // the root window of the screen DISPLAY names

	// onEvent, when set, is called with each event read while waiting
	// for a reply. Without it events are dropped: a connection that
	// selects none still gets MappingNotify.
	onEvent func(p []byte)
}

// xProtoError is an error packet the server sent for a request.
type xProtoError struct {
	code  byte
	major byte
	minor uint16
}

func (e *xProtoError) Error() string {
	return fmt.Sprintf("X error %d for request %d.%d", e.code, e.major, e.minor)
}

const xBadWindow = 3

// isProtoError reports whether err is the server refusing one request,
// which leaves the connection usable, rather than the connection failing.
func isProtoError(err error) bool {
	var pe *xProtoError
	return errors.As(err, &pe)
}

// openX11 connects to the display, authenticates with the cookie from the
// Xauthority file if there is one, and finds the root window. timeout
// bounds the connect and, separately, the handshake.
func openX11(d xDisplay, getenv func(string) string, hostname string, timeout time.Duration) (*xconn, error) {
	conn, err := dialDisplay(d, timeout)
	if err != nil {
		return nil, err
	}
	cookie := readCookie(getenv, hostname, d.number)
	c, err := setupX11(conn, d.screen, cookie, time.Now().Add(timeout))
	if err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

// setupX11 runs the connection setup on conn (X11 protocol, "Connection
// Setup"). A nil cookie sends no authorization.
func setupX11(conn net.Conn, screen int, cookie []byte, deadline time.Time) (*xconn, error) {
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	var name []byte
	if cookie != nil {
		name = []byte(cookieAuth)
	}
	req := make([]byte, 12, 12+len(name)+pad4(len(name))+len(cookie)+pad4(len(cookie)))
	req[0] = 'l' // little-endian
	put16(req[2:], 11)
	put16(req[4:], 0)
	put16(req[6:], uint16(len(name)))
	put16(req[8:], uint16(len(cookie)))
	req = append(req, name...)
	req = append(req, make([]byte, pad4(len(name)))...)
	req = append(req, cookie...)
	req = append(req, make([]byte, pad4(len(cookie)))...)
	if _, err := conn.Write(req); err != nil {
		return nil, fmt.Errorf("X connection setup: %w", err)
	}

	var head [8]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return nil, fmt.Errorf("X connection setup: %w", err)
	}
	data := make([]byte, int(le16(head[6:]))*4)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, fmt.Errorf("X connection setup: %w", err)
	}
	switch head[0] {
	case 0: // Failed: head[1] is the length of the reason at the start of data.
		n := min(int(head[1]), len(data))
		return nil, fmt.Errorf("the X server refused the connection: %s", printable(data[:n]))
	case 2: // Authenticate: the reason fills data, padded with NULs.
		return nil, fmt.Errorf("the X server wants more authentication: %s", printable(data))
	case 1:
	default:
		return nil, fmt.Errorf("X connection setup: unknown status %d", head[0])
	}
	root, err := rootWindow(data, screen)
	if err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	return &xconn{conn: conn, root: root}, nil
}

// rootWindow finds the root window of screen in the setup reply's data,
// the part after its 8-byte header.
func rootWindow(data []byte, screen int) (uint32, error) {
	bad := errors.New("X connection setup: malformed reply")
	if len(data) < 32 {
		return 0, bad
	}
	vendor := int(le16(data[16:]))
	screens := int(data[20])
	formats := int(data[21])
	off := 32 + vendor + pad4(vendor) + 8*formats
	if screen >= screens {
		return 0, fmt.Errorf("the X display has no screen %d", screen)
	}
	for i := 0; ; i++ {
		if off+40 > len(data) {
			return 0, bad
		}
		if i == screen {
			return le32(data[off:]), nil
		}
		depths := int(data[off+39])
		off += 40
		for range depths {
			if off+8 > len(data) {
				return 0, bad
			}
			off += 8 + 24*int(le16(data[off+2:]))
		}
	}
}

// printable keeps the readable ASCII of a server's message.
func printable(b []byte) string {
	var s strings.Builder
	for _, c := range b {
		if c >= ' ' && c <= '~' {
			s.WriteByte(c)
		}
	}
	return strings.TrimSpace(s.String())
}

func (c *xconn) close() error { return c.conn.Close() }

// send writes one request, whose length field the caller has set.
func (c *xconn) send(req []byte) error {
	c.seq++
	_, err := c.conn.Write(req)
	return err
}

// roundTrip sends req and reads until its reply or error. Replies come in
// request order, so the first reply or error carrying req's sequence
// number is its answer; an error for an earlier request that has no reply
// carries an older number and is skipped.
func (c *xconn) roundTrip(req []byte) ([]byte, error) {
	if err := c.send(req); err != nil {
		return nil, err
	}
	want := c.seq
	for {
		p, err := c.readPacket()
		if err != nil {
			return nil, err
		}
		switch p[0] {
		case xPacketError:
			if le16(p[2:]) == want {
				return nil, &xProtoError{code: p[1], major: p[10], minor: le16(p[8:])}
			}
		case xPacketReply:
			if le16(p[2:]) == want {
				return p, nil
			}
		default:
			if c.onEvent != nil {
				c.onEvent(p)
			}
		}
	}
}

// readPacket reads one reply, error or event: 32 bytes, plus the length a
// reply or a generic event declares.
func (c *xconn) readPacket() ([]byte, error) {
	var head [32]byte
	if _, err := io.ReadFull(c.conn, head[:]); err != nil {
		return nil, err
	}
	extra := 0
	if head[0] == xPacketReply || head[0]&0x7f == eventGeneric {
		n := le32(head[4:])
		if n > maxReplyBytes/4 {
			return nil, fmt.Errorf("X server sent a %d-byte packet", uint64(n)*4)
		}
		extra = int(n) * 4
	}
	p := make([]byte, 32+extra)
	copy(p, head[:])
	if _, err := io.ReadFull(c.conn, p[32:]); err != nil {
		return nil, err
	}
	return p, nil
}

// queryExtension returns an extension's major opcode, or ok false when the
// server lacks it.
func (c *xconn) queryExtension(name string) (major byte, ok bool, err error) {
	req := make([]byte, 8+len(name)+pad4(len(name)))
	req[0] = opQueryExtension
	put16(req[2:], uint16(len(req)/4))
	put16(req[4:], uint16(len(name)))
	copy(req[8:], name)
	p, err := c.roundTrip(req)
	if err != nil {
		return 0, false, err
	}
	return p[9], p[8] != 0, nil
}

// internAtom returns the atom for name, creating it if no client has yet,
// so that a window manager that starts later sets the same atom.
func (c *xconn) internAtom(name string) (uint32, error) {
	req := make([]byte, 8+len(name)+pad4(len(name)))
	req[0] = opInternAtom
	put16(req[2:], uint16(len(req)/4))
	put16(req[4:], uint16(len(name)))
	copy(req[8:], name)
	p, err := c.roundTrip(req)
	if err != nil {
		return 0, err
	}
	return le32(p[8:]), nil
}

// selectPropertyChanges asks for PropertyNotify events on window. The
// request has no reply; an error for it would surface, and be skipped, in
// the next round trip.
func (c *xconn) selectPropertyChanges(window uint32) error {
	req := make([]byte, 16)
	req[0] = opChangeWindowAttributes
	put16(req[2:], 4)
	put32(req[4:], window)
	put32(req[8:], cwEventMask)
	put32(req[12:], propertyChangeMask)
	return c.send(req)
}

// getProperty reads up to words 32-bit units of a window property of the
// given type. A missing property, or one of another type, gives no value
// and no error.
func (c *xconn) getProperty(window, property, typ, words uint32) (format byte, value []byte, err error) {
	req := make([]byte, 24)
	req[0] = opGetProperty
	put16(req[2:], 6)
	put32(req[4:], window)
	put32(req[8:], property)
	put32(req[12:], typ)
	put32(req[16:], 0)
	put32(req[20:], words)
	p, err := c.roundTrip(req)
	if err != nil {
		return 0, nil, err
	}
	format = p[1]
	if le32(p[8:]) != typ || (format != 8 && format != 16 && format != 32) {
		return 0, nil, nil
	}
	n := int(le32(p[16:])) * int(format/8)
	if n > len(p)-32 {
		return 0, nil, errors.New("X server sent a short GetProperty reply")
	}
	return format, p[32 : 32+n], nil
}

// getCardinal reads a property holding one 32-bit value of type typ. ok is
// false when the window has no such property.
func (c *xconn) getCardinal(window, property, typ uint32) (v uint32, ok bool, err error) {
	format, value, err := c.getProperty(window, property, typ, 1)
	if err != nil || format != 32 || len(value) < 4 {
		return 0, false, err
	}
	return le32(value), true, nil
}

// screenSaverVersion checks that the MIT-SCREEN-SAVER extension at major
// speaks version 1, whose QueryInfo this package reads.
func (c *xconn) screenSaverVersion(major byte) error {
	req := []byte{major, ssQueryVersion, 2, 0, 1, 1, 0, 0}
	p, err := c.roundTrip(req)
	if err != nil {
		return err
	}
	if v := le16(p[8:]); v != 1 {
		return fmt.Errorf("MIT-SCREEN-SAVER version %d.%d, pairtime reads 1.x", v, le16(p[10:]))
	}
	return nil
}

// idleSince returns the server's milliseconds since the last user input,
// from MIT-SCREEN-SAVER's QueryInfo on the root window.
func (c *xconn) idleSince(major byte) (uint32, error) {
	req := make([]byte, 8)
	req[0] = major
	req[1] = ssQueryInfo
	put16(req[2:], 2)
	put32(req[4:], c.root)
	p, err := c.roundTrip(req)
	if err != nil {
		return 0, err
	}
	return le32(p[16:]), nil
}

func pad4(n int) int { return (4 - n%4) % 4 }

func le16(b []byte) uint16     { return binary.LittleEndian.Uint16(b) }
func le32(b []byte) uint32     { return binary.LittleEndian.Uint32(b) }
func put16(b []byte, v uint16) { binary.LittleEndian.PutUint16(b, v) }
func put32(b []byte, v uint32) { binary.LittleEndian.PutUint32(b, v) }
