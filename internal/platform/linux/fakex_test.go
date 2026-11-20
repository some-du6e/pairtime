// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeX is an X server that speaks the protocol subset this package uses,
// on a Unix socket in Linux's abstract namespace, so it needs no files. It
// answers the way the protocol says a server must, which the unit tests
// check the client against; the desktop tests check it against Xvfb.
type fakeX struct {
	fakeXConfig
	ln   net.Listener
	addr string

	mu       sync.Mutex
	idleMS   uint32
	atoms    map[string]uint32
	props    map[fakePropKey]fakeProp
	windows  map[uint32]bool
	pids     map[uint32]uint32   // the pid the server took from the socket of each window's client
	selected map[uint32]uint32   // event masks clients selected, by window
	reads    map[fakePropKey]int // GetProperty requests, by window and property
	conns    []net.Conn
	accepted int // connections accepted
	seq      map[net.Conn]uint16
	gotName  string // the authorization a client sent
	gotData  []byte
}

// fakeXConfig is how a fake X server behaves. It doesn't change once the
// server is listening.
type fakeXConfig struct {
	roots    []uint32      // the root window of each screen
	refuse   string        // when set, refuse the connection with this reason
	noSaver  bool          // lack MIT-SCREEN-SAVER
	xres     string        // X-Resource: "" for version 1.2, "1.1" for one without QueryClientIds, "none" for none
	seqStart uint16        // the sequence number before the first request
	noise    bool          // send an event before every reply and error
	delay    time.Duration // wait this long before the setup reply and before each reply and error
}

type fakePropKey struct{ window, atom uint32 }

type fakeProp struct {
	typ    uint32
	format byte
	data   []byte
}

// The major opcodes the fake gives its extensions.
const (
	fakeSaverOpcode = 140
	fakeXResOpcode  = 141
)

// newFakeX starts a fake X server.
func newFakeX(t *testing.T, cfg fakeXConfig) *fakeX {
	t.Helper()
	var b [8]byte
	rand.Read(b[:])
	f, err := startFakeX(cfg, "@pairtime-fakex-"+hex.EncodeToString(b[:]))
	if err != nil {
		t.Fatalf("listening for the fake X server: %v", err)
	}
	t.Cleanup(f.close)
	return f
}

// newFakeDisplay starts a fake X server where Open looks for a display, the
// abstract socket of a display numbered past any real one, and returns it
// with the DISPLAY that names it.
func newFakeDisplay(t *testing.T, cfg fakeXConfig) (*fakeX, string) {
	t.Helper()
	var err error
	for range 5 {
		var b [4]byte
		rand.Read(b[:])
		number := fmt.Sprint(100000 + le32(b[:])%900000)
		var f *fakeX
		if f, err = startFakeX(cfg, "@"+xDisplay{number: number}.socketPath()); err == nil {
			t.Cleanup(f.close)
			return f, ":" + number
		}
	}
	t.Fatalf("listening for the fake X display: %v", err)
	return nil, ""
}

func startFakeX(cfg fakeXConfig, addr string) (*fakeX, error) {
	f := &fakeX{
		fakeXConfig: cfg,
		addr:        addr,
		atoms:       map[string]uint32{"ATOM": atomAtom, "CARDINAL": atomCardinal, "STRING": atomString, "WINDOW": atomWindow, "WM_CLIENT_MACHINE": atomWMClientMachine},
		props:       map[fakePropKey]fakeProp{},
		windows:     map[uint32]bool{},
		pids:        map[uint32]uint32{},
		selected:    map[uint32]uint32{},
		reads:       map[fakePropKey]int{},
		seq:         map[net.Conn]uint16{},
	}
	for _, r := range cfg.roots {
		f.windows[r] = true
	}
	ln, err := net.Listen("unix", f.addr)
	if err != nil {
		return nil, err
	}
	f.ln = ln
	go f.accept()
	return f, nil
}

func (f *fakeX) close() {
	f.ln.Close()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		c.Close()
	}
}

// dial connects a client and runs the connection setup for screen.
func (f *fakeX) dial(screen int, cookie []byte) (*xconn, error) {
	return f.dialBy(screen, cookie, time.Now().Add(5*time.Second))
}

// dialBy is dial with the setup done by deadline.
func (f *fakeX) dialBy(screen int, cookie []byte, deadline time.Time) (*xconn, error) {
	conn, err := net.Dial("unix", f.addr)
	if err != nil {
		return nil, err
	}
	c, err := setupX11(conn, screen, cookie, deadline)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

// dropClients closes every client connection, as a server that went away.
func (f *fakeX) dropClients() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		c.Close()
	}
	f.conns = nil
}

func (f *fakeX) accept() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conns = append(f.conns, conn)
		f.accepted++
		f.mu.Unlock()
		go f.serve(conn)
	}
}

func (f *fakeX) serve(conn net.Conn) {
	defer conn.Close()
	head := make([]byte, 12)
	if _, err := io.ReadFull(conn, head); err != nil {
		return
	}
	nameLen, dataLen := int(le16(head[6:])), int(le16(head[8:]))
	rest := make([]byte, nameLen+pad4(nameLen)+dataLen+pad4(dataLen))
	if _, err := io.ReadFull(conn, rest); err != nil {
		return
	}
	f.mu.Lock()
	f.gotName = string(rest[:nameLen])
	f.gotData = append([]byte(nil), rest[nameLen+pad4(nameLen):][:dataLen]...)
	f.seq[conn] = f.seqStart
	f.mu.Unlock()

	time.Sleep(f.delay)
	if f.refuse != "" {
		reply := make([]byte, 8, 8+len(f.refuse)+pad4(len(f.refuse)))
		reply[1] = byte(len(f.refuse))
		put16(reply[2:], 11)
		put16(reply[6:], uint16((len(f.refuse)+pad4(len(f.refuse)))/4))
		reply = append(reply, f.refuse...)
		reply = append(reply, make([]byte, pad4(len(f.refuse)))...)
		conn.Write(reply)
		return
	}
	if _, err := conn.Write(setupReply(f.roots)); err != nil {
		return
	}
	for {
		h := make([]byte, 4)
		if _, err := io.ReadFull(conn, h); err != nil {
			return
		}
		n := int(le16(h[2:])) * 4
		if n < 4 {
			return
		}
		req := make([]byte, n)
		copy(req, h)
		if _, err := io.ReadFull(conn, req[4:]); err != nil {
			return
		}
		f.handle(conn, req)
	}
}

// setupReply is a successful connection setup with one pixmap format and,
// per screen, one depth with one visual, as a real server's reply has.
func setupReply(roots []uint32) []byte {
	const vendor = "pairtime fake"
	d := make([]byte, 32)
	put32(d[0:], 12101011) // release
	put32(d[4:], 0x00400000)
	put32(d[8:], 0x001fffff)
	put16(d[16:], uint16(len(vendor)))
	put16(d[18:], 0xffff)
	d[20] = byte(len(roots))
	d[21] = 1
	d = append(d, vendor...)
	d = append(d, make([]byte, pad4(len(vendor)))...)
	d = append(d, 24, 32, 32, 0, 0, 0, 0, 0)
	for _, root := range roots {
		screen := make([]byte, 40)
		put32(screen[0:], root)
		screen[38] = 24
		screen[39] = 1
		depth := make([]byte, 8)
		depth[0] = 24
		put16(depth[2:], 1)
		d = append(d, screen...)
		d = append(d, depth...)
		d = append(d, make([]byte, 24)...)
	}
	head := make([]byte, 8)
	head[0] = 1
	put16(head[2:], 11)
	put16(head[6:], uint16(len(d)/4))
	return append(head, d...)
}

// handle answers one request. f.mu is not held.
func (f *fakeX) handle(conn net.Conn, req []byte) {
	f.mu.Lock()
	f.seq[conn]++
	seq := f.seq[conn]
	f.mu.Unlock()

	switch req[0] {
	case opQueryExtension:
		name := string(req[8:][:le16(req[4:])])
		r := fakeReply(seq, 0)
		switch {
		case name == screenSaverExtension && !f.noSaver:
			r[8], r[9] = 1, fakeSaverOpcode
		case name == xResExtension && f.xres != "none":
			r[8], r[9] = 1, fakeXResOpcode
		}
		f.write(conn, r)
	case fakeSaverOpcode:
		r := fakeReply(seq, 0)
		switch req[1] {
		case ssQueryVersion:
			put16(r[8:], 1)
			put16(r[10:], 1)
		case ssQueryInfo:
			f.mu.Lock()
			put32(r[16:], f.idleMS)
			f.mu.Unlock()
		default:
			f.writeError(conn, seq, 1, req[0], uint16(req[1]))
			return
		}
		f.write(conn, r)
	case fakeXResOpcode:
		f.handleXRes(conn, seq, req)
	case opInternAtom:
		name := string(req[8:][:le16(req[4:])])
		r := fakeReply(seq, 0)
		put32(r[8:], f.atom(name))
		f.write(conn, r)
	case opChangeWindowAttributes:
		window, mask := le32(req[4:]), le32(req[8:])
		f.mu.Lock()
		exists := f.windows[window]
		if exists && mask == cwEventMask {
			f.selected[window] = le32(req[12:])
		}
		f.mu.Unlock()
		if !exists {
			// The request has no reply, but an error still comes back,
			// in order with the replies to later requests.
			f.writeError(conn, seq, 3, req[0], 0)
		}
	case opGetProperty:
		window, atom, typ, words := le32(req[4:]), le32(req[8:]), le32(req[12:]), le32(req[20:])
		f.mu.Lock()
		f.reads[fakePropKey{window, atom}]++
		exists := f.windows[window]
		p, ok := f.props[fakePropKey{window, atom}]
		f.mu.Unlock()
		if !exists {
			f.writeError(conn, seq, 3, req[0], 0)
			return
		}
		var value []byte
		var ptype uint32
		var format byte
		var after uint32
		switch {
		case !ok:
		case typ != 0 && typ != p.typ:
			ptype, format, after = p.typ, p.format, uint32(len(p.data))
		default:
			ptype, format = p.typ, p.format
			value = p.data[:min(len(p.data), int(words)*4)]
			after = uint32(len(p.data) - len(value))
		}
		r := fakeReply(seq, len(value)+pad4(len(value)))
		r[1] = format
		put32(r[8:], ptype)
		put32(r[12:], after)
		if format != 0 {
			put32(r[16:], uint32(len(value)/int(format/8)))
		}
		copy(r[32:], value)
		f.write(conn, r)
	default:
		f.writeError(conn, seq, 1, req[0], 0)
	}
}

// handleXRes answers X-Resource's QueryVersion and QueryClientIds, which
// version 1.1 lacks. A spec names a client by any resource it created, here
// always a window; the fake answers for windows it knows a pid for, and for
// a window that is gone whose client's slot has a new owner.
func (f *fakeX) handleXRes(conn net.Conn, seq uint16, req []byte) {
	switch {
	case req[1] == xResQueryVersion:
		r := fakeReply(seq, 0)
		put16(r[8:], 1)
		put16(r[10:], 2)
		if f.xres == "1.1" {
			put16(r[10:], 1)
		}
		f.write(conn, r)
	case req[1] == xResQueryClientIDs && f.xres != "1.1":
		n := int(le32(req[4:]))
		if len(req) != 8+8*n {
			f.writeError(conn, seq, 16, req[0], uint16(req[1])) // BadLength
			return
		}
		var ids []byte
		count := 0
		for i := range n {
			spec := req[8+8*i:]
			client, mask := le32(spec), le32(spec[4:])
			f.mu.Lock()
			pid, ok := f.pids[client]
			f.mu.Unlock()
			if !ok || mask&xResLocalClientPID == 0 {
				continue
			}
			v := make([]byte, 16)
			put32(v, client&^0x1fffff) // the client's resource base
			put32(v[4:], xResLocalClientPID)
			put32(v[8:], 4)
			put32(v[12:], pid)
			ids = append(ids, v...)
			count++
		}
		r := fakeReply(seq, len(ids))
		put32(r[8:], uint32(count))
		copy(r[32:], ids)
		f.write(conn, r)
	default:
		f.writeError(conn, seq, 1, req[0], uint16(req[1])) // BadRequest
	}
}

// fakeReply is a reply header with extra bytes of data after its 32.
func fakeReply(seq uint16, extra int) []byte {
	r := make([]byte, 32+extra)
	r[0] = xPacketReply
	put16(r[2:], seq)
	put32(r[4:], uint32(extra/4))
	return r
}

func (f *fakeX) writeError(conn net.Conn, seq uint16, code, major byte, minor uint16) {
	e := make([]byte, 32)
	e[1] = code
	put16(e[2:], seq)
	put16(e[8:], minor)
	e[10] = major
	f.write(conn, e)
}

// write sends a packet, after an unrelated event when noise is on, so the
// client has to skip events to find its reply.
func (f *fakeX) write(conn net.Conn, p []byte) {
	time.Sleep(f.delay)
	if f.noise {
		mapping := make([]byte, 32)
		mapping[0] = 34 // MappingNotify, which every client gets
		f.mu.Lock()
		put16(mapping[2:], f.seq[conn])
		f.mu.Unlock()
		conn.Write(mapping)
	}
	conn.Write(p)
}

// atom returns the atom for name, interning it if it's new.
func (f *fakeX) atom(name string) uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.atomLocked(name)
}

func (f *fakeX) atomLocked(name string) uint32 {
	if a, ok := f.atoms[name]; ok {
		return a
	}
	a := uint32(100 + len(f.atoms))
	f.atoms[name] = a
	return a
}

func (f *fakeX) setIdle(ms uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.idleMS = ms
}

// addWindow makes window exist.
func (f *fakeX) addWindow(window uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.windows[window] = true
}

// setClientPID sets the pid the server took from the socket of the client
// that created window, which X-Resource reports.
func (f *fakeX) setClientPID(window, pid uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pids[window] = pid
}

// setProp sets a property of window. A nil data deletes it.
func (f *fakeX) setProp(window uint32, name string, typ uint32, format byte, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := fakePropKey{window, f.atomLocked(name)}
	if data == nil {
		delete(f.props, key)
		return
	}
	f.props[key] = fakeProp{typ: typ, format: format, data: data}
}

func (f *fakeX) setCardinal(window uint32, name string, typ uint32, v uint32) {
	b := make([]byte, 4)
	put32(b, v)
	f.setProp(window, name, typ, 32, b)
}

// notify sends PropertyNotify for window's property name to every client
// that selected property changes on window.
func (f *fakeX) notify(window uint32, name string) {
	f.mu.Lock()
	atom := f.atomLocked(name)
	selected := f.selected[window]&propertyChangeMask != 0
	conns := append([]net.Conn(nil), f.conns...)
	seqs := make([]uint16, len(conns))
	for i, c := range conns {
		seqs[i] = f.seq[c]
	}
	f.mu.Unlock()
	if !selected {
		return
	}
	for i, c := range conns {
		e := make([]byte, 32)
		e[0] = eventPropertyNotify
		put16(e[2:], seqs[i])
		put32(e[4:], window)
		put32(e[8:], atom)
		c.Write(e)
	}
}

// selectedOn returns the event mask clients selected on window.
func (f *fakeX) selectedOn(window uint32) uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.selected[window]
}

// readsOf counts the GetProperty requests for window's property name.
func (f *fakeX) readsOf(window uint32, name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads[fakePropKey{window, f.atomLocked(name)}]
}

// readsOfAny counts the GetProperty requests for property name on any
// window.
func (f *fakeX) readsOfAny(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	atom, n := f.atomLocked(name), 0
	for k, reads := range f.reads {
		if k.atom == atom {
			n += reads
		}
	}
	return n
}

// connections counts the connections the server has accepted.
func (f *fakeX) connections() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.accepted
}

func (f *fakeX) auth() (string, []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gotName, f.gotData
}
