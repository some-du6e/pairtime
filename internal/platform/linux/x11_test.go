// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseDisplay(t *testing.T) {
	cases := []struct {
		name    string
		display string
		want    xDisplay
		wantErr string
	}{
		{"a bare display number is display 0's screen 0", ":0", xDisplay{number: "0"}, ""},
		{"a screen number after the dot picks the screen", ":1.2", xDisplay{number: "1", screen: 2}, ""},
		{"the unix host is this machine", "unix:3", xDisplay{number: "3"}, ""},
		{"the unix host takes a screen too", "unix:3.1", xDisplay{number: "3", screen: 1}, ""},
		{"a host name is a display on another machine", "localhost:10.0", xDisplay{}, "another host"},
		{"an IPv6 host is a display on another machine", "[::1]:0", xDisplay{}, "another host"},
		{"an empty DISPLAY names no display", "", xDisplay{}, "not set"},
		{"a DISPLAY without a colon names no display", "0", xDisplay{}, "not set"},
		{"the display number has to be digits", ":x", xDisplay{}, "no display number"},
		{"the display number can't be empty", ":", xDisplay{}, "no display number"},
		{"the screen number has to be digits", ":0.x", xDisplay{}, "screen number"},
		{"the screen number can't be empty", ":0.", xDisplay{}, "screen number"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDisplay(tc.display)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("parseDisplay(%q) = %+v, %v; want an error about %q", tc.display, got, err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("parseDisplay(%q) = %+v, %v; want %+v", tc.display, got, err, tc.want)
			}
			if path := got.socketPath(); path != "/tmp/.X11-unix/X"+tc.want.number {
				t.Errorf("socket path %q", path)
			}
		})
	}
}

func TestSetupX11(t *testing.T) {
	cookie := bytes.Repeat([]byte{0xa5}, 16)
	cases := []struct {
		name     string
		roots    []uint32
		refuse   string
		screen   int
		cookie   []byte
		wantRoot uint32
		wantErr  string
		wantAuth string
	}{
		{name: "the root is screen 0's when DISPLAY names no screen", roots: []uint32{0x100, 0x200}, wantRoot: 0x100},
		{name: "the root is the root of the screen DISPLAY names", roots: []uint32{0x100, 0x200}, screen: 1, wantRoot: 0x200},
		{name: "a screen the server doesn't have is an error", roots: []uint32{0x100}, screen: 1, wantErr: "no screen 1"},
		{name: "a cookie goes out as MIT-MAGIC-COOKIE-1", roots: []uint32{0x100}, cookie: cookie, wantRoot: 0x100, wantAuth: cookieAuth},
		{name: "no cookie sends no authorization", roots: []uint32{0x100}, wantRoot: 0x100},
		{name: "a refusal carries the server's reason", roots: []uint32{0x100}, refuse: "No protocol specified", wantErr: "No protocol specified"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeX(t, fakeXConfig{roots: tc.roots, refuse: tc.refuse})
			c, err := f.dial(tc.screen, tc.cookie)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("setup = %v; want an error about %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer c.close()
			if c.root != tc.wantRoot {
				t.Errorf("root window %#x, want %#x", c.root, tc.wantRoot)
			}
			name, data := f.auth()
			if name != tc.wantAuth || !bytes.Equal(data, tc.cookie) {
				t.Errorf("the server got authorization %q %x, want %q %x", name, data, tc.wantAuth, tc.cookie)
			}
		})
	}
}

func TestX11Idle(t *testing.T) {
	cases := []struct {
		name     string
		saver    bool
		noise    bool
		seqStart uint16
		idleMS   []uint32 // the server's counter at each sample
		want     []time.Duration
		wantErr  error
	}{
		{
			name:  "idle is the X server's milliseconds since the last input",
			saver: true, idleMS: []uint32{4321, 12}, want: []time.Duration{4321 * time.Millisecond, 12 * time.Millisecond},
		},
		{
			name:  "events that arrive before a reply are skipped, not taken for it",
			saver: true, noise: true, idleMS: []uint32{900, 1800}, want: []time.Duration{900 * time.Millisecond, 1800 * time.Millisecond},
		},
		{
			name:  "replies still match their requests when the 16-bit sequence number wraps",
			saver: true, seqStart: 65533, idleMS: []uint32{1, 2, 3, 4, 5}, want: []time.Duration{1e6, 2e6, 3e6, 4e6, 5e6},
		},
		{
			name:  "a server without MIT-SCREEN-SAVER gives no idle time",
			saver: false, idleMS: []uint32{1}, wantErr: errNoScreenSaver,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeX(t, fakeXConfig{roots: []uint32{0x100}, noSaver: !tc.saver, noise: tc.noise, seqStart: tc.seqStart})
			src := &x11Idle{open: func(time.Time) (*xconn, error) {
				c, err := f.dial(0, nil)
				if c != nil {
					c.seq = tc.seqStart
				}
				return c, err
			}}
			defer src.close()
			for i, ms := range tc.idleMS {
				f.setIdle(ms)
				got, err := src.idle(context.Background())
				if tc.wantErr != nil {
					if !errors.Is(err, tc.wantErr) {
						t.Fatalf("sample %d: %v, %v; want %v", i, got, err, tc.wantErr)
					}
					return
				}
				if err != nil || got != tc.want[i] {
					t.Fatalf("sample %d: %v, %v; want %v", i, got, err, tc.want[i])
				}
			}
		})
	}
}

func TestX11IdleReconnects(t *testing.T) {
	cases := []struct {
		name string
		drop bool // the server drops the client between samples
	}{
		{"a dropped connection fails one sample and the next reconnects", true},
		{"a live connection is kept between samples", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeX(t, fakeXConfig{roots: []uint32{0x100}})
			dials := 0
			src := &x11Idle{open: func(time.Time) (*xconn, error) {
				dials++
				return f.dial(0, nil)
			}}
			defer src.close()
			f.setIdle(5000)
			if _, err := src.idle(context.Background()); err != nil {
				t.Fatal(err)
			}
			if tc.drop {
				f.dropClients()
				if _, err := src.idle(context.Background()); err == nil {
					t.Fatal("a sample on a dropped connection succeeded")
				}
			}
			got, err := src.idle(context.Background())
			if err != nil || got != 5*time.Second {
				t.Fatalf("sample after: %v, %v", got, err)
			}
			wantDials := 1
			if tc.drop {
				wantDials = 2
			}
			if dials != wantDials {
				t.Errorf("%d connections, want %d", dials, wantDials)
			}
		})
	}
}

func TestX11IdleOneDeadline(t *testing.T) {
	cases := []struct {
		name  string
		delay time.Duration // how long the server takes over each answer
		ok    bool          // the sample succeeds
	}{
		// A sample that reconnects waits for four answers: the setup,
		// QueryExtension, QueryVersion and QueryInfo. At 150 ms each, any
		// one of them fits the 400 ms deadline, but not all four.
		{"a reconnecting sample whose steps each fit the deadline but together don't fails at the deadline", 150 * time.Millisecond, false},
		{"a reconnecting sample that fits the deadline gets the idle time", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeX(t, fakeXConfig{roots: []uint32{0x100}, delay: tc.delay})
			src := &x11Idle{open: func(deadline time.Time) (*xconn, error) { return f.dialBy(0, nil, deadline) }}
			defer src.close()
			f.setIdle(3000)
			ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
			defer cancel()
			start := time.Now()
			got, err := src.idle(ctx)
			took := time.Since(start)
			if tc.ok {
				if err != nil || got != 3*time.Second {
					t.Fatalf("sample %v, %v; want 3s", got, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("the sample took %v and succeeded, past its 400 ms deadline", took)
			}
			if took > time.Second {
				t.Errorf("the sample gave up after %v, want at its 400 ms deadline", took)
			}
		})
	}
}

// Windows the focus tests use. testHost is the machine the client runs on.
// Each window's client has the pid the X server took from its socket, which
// X-Resource reports, and most set the same pid as their _NET_WM_PID.
const (
	testHost   = "testhost"
	winA       = 0x501 // pid 4242, on testhost
	winB       = 0x502 // pid 777, on testhost under its full name
	winRemote  = 0x503 // pid 999 on another host, forwarded by the local ssh, pid 555
	winGone    = 0x504 // destroyed before pairtime reads it; a new client, pid 6666, took its client's slot
	winNoPID   = 0x505 // sets no _NET_WM_PID; the server knows pid 888
	winSandbox = 0x506 // a Flatpak app: _NET_WM_PID 2 from its sandbox, pid 31337 on the host
	winTCP     = 0x507 // connected over TCP, so the server knows no pid; _NET_WM_PID 4343
	winLast    = 0x5ff // the sentinel each test ends with, pid 1
	wmCheck    = 0x400 // the window manager's check window
)

// testRoot is the root window of the fake servers' one screen.
const testRoot = 0x100

// setWM gives f's root window the properties of a window manager: "ewmh"
// is one that keeps _NET_ACTIVE_WINDOW, "none" is none, "stale" left a
// check window that is gone, and "unlisted" doesn't list _NET_ACTIVE_WINDOW
// in _NET_SUPPORTED. active is the window _NET_ACTIVE_WINDOW names.
func setWM(f *fakeX, wm string, active uint32) {
	switch wm {
	case "ewmh", "unlisted":
		f.addWindow(wmCheck)
		f.setCardinal(testRoot, "_NET_SUPPORTING_WM_CHECK", atomWindow, wmCheck)
		f.setCardinal(wmCheck, "_NET_SUPPORTING_WM_CHECK", atomWindow, wmCheck)
	case "stale":
		f.setCardinal(testRoot, "_NET_SUPPORTING_WM_CHECK", atomWindow, wmCheck)
	}
	supported := []string{"_NET_SUPPORTED", "_NET_CLIENT_LIST", "_NET_ACTIVE_WINDOW"}
	if wm == "unlisted" {
		supported = supported[:2]
	}
	var list []byte
	for _, name := range supported {
		list = put32le(list, f.atom(name))
	}
	f.setProp(testRoot, "_NET_SUPPORTED", atomAtom, 32, list)
	f.setCardinal(testRoot, "_NET_ACTIVE_WINDOW", atomWindow, active)
}

// addTestWindows gives f the windows above.
func addTestWindows(f *fakeX) {
	for _, w := range []struct {
		id      uint32
		netPID  uint32 // _NET_WM_PID, or 0 for none
		hostPID uint32 // the pid the server knows, or 0 for none
		machine string
	}{
		{winA, 4242, 4242, testHost},
		{winB, 777, 777, testHost + ".example.org"},
		{winRemote, 999, 555, "elsewhere"},
		{winNoPID, 0, 888, testHost},
		{winSandbox, 2, 31337, testHost},
		{winTCP, 4343, 0, testHost},
		{winLast, 1, 1, testHost},
	} {
		f.addWindow(w.id)
		if w.netPID != 0 {
			f.setCardinal(w.id, "_NET_WM_PID", atomCardinal, w.netPID)
		}
		if w.hostPID != 0 {
			f.setClientPID(w.id, w.hostPID)
		}
		f.setProp(w.id, "WM_CLIENT_MACHINE", atomString, 8, []byte(w.machine))
	}
	f.setClientPID(winGone, 6666)
}

// focusStep is one thing the window manager does: it sets the root's
// _NET_ACTIVE_WINDOW to each window in active in turn, burstGap apart, or,
// when other is set, it changes that root property instead.
type focusStep struct {
	active []uint32
	other  string
}

// The cases that set _NET_ACTIVE_WINDOW more than once in a step give the
// watcher burstSettle to settle in, and leave burstGap between the changes.
// That gap gives a watcher that reads at once time to read, and leaves the
// burst well inside the settle time on a slow runner.
const (
	burstSettle = 300 * time.Millisecond
	burstGap    = 30 * time.Millisecond
)

func TestX11Focus(t *testing.T) {
	cases := []struct {
		name   string
		wm     string // "ewmh", "none", "stale" or "unlisted"
		xres   string // the server's X-Resource, as fakeXConfig.xres
		start  uint32 // _NET_ACTIVE_WINDOW at start
		steps  []focusStep
		settle time.Duration // the watcher's settle time; zero means focusSettle
		want   []Focus       // Window and PID of each report before the sentinel
		onEWMH bool          // the window manager was found only after start
	}{
		{
			name: "the window focused at start is reported once, with its pid",
			wm:   "ewmh", start: winA,
			want: []Focus{{Window: winA, PID: 4242}},
		},
		{
			name: "a change of _NET_ACTIVE_WINDOW reports the new window",
			wm:   "ewmh", start: winA,
			steps: []focusStep{{active: []uint32{winB}}},
			want:  []Focus{{Window: winA, PID: 4242}, {Window: winB, PID: 777}},
		},
		{
			name: "a change of another root property reports nothing and reads nothing",
			wm:   "ewmh", start: winA,
			steps: []focusStep{{other: "_NET_CLIENT_LIST_STACKING"}},
			want:  []Focus{{Window: winA, PID: 4242}},
		},
		{
			name: "the same window set again reports nothing",
			wm:   "ewmh", start: winA,
			steps: []focusStep{{active: []uint32{winA}}},
			want:  []Focus{{Window: winA, PID: 4242}},
		},
		{
			name: "None and then a window, within the settle time, are one report, of the window",
			wm:   "ewmh", start: winA, settle: burstSettle,
			steps: []focusStep{{active: []uint32{0, winB}}},
			want:  []Focus{{Window: winA, PID: 4242}, {Window: winB, PID: 777}},
		},
		{
			name: "None and then the same window again, within the settle time, report nothing",
			wm:   "ewmh", start: winA, settle: burstSettle,
			steps: []focusStep{{active: []uint32{0, winA}}},
			want:  []Focus{{Window: winA, PID: 4242}},
		},
		{
			name: "two windows in turn within the settle time are one report, of the second",
			wm:   "ewmh", start: winA, settle: burstSettle,
			steps: []focusStep{{active: []uint32{winB, winNoPID}}},
			want:  []Focus{{Window: winA, PID: 4242}, {Window: winNoPID, PID: 888}},
		},
		{
			name: "None that lasts past the settle time is reported, as after a click on the desktop",
			wm:   "ewmh", start: winA,
			steps: []focusStep{{active: []uint32{0}}, {active: []uint32{winB}}},
			want:  []Focus{{Window: winA, PID: 4242}, {Window: 0}, {Window: winB, PID: 777}},
		},
		{
			name: "a Flatpak app's window has its pid on the host, from the server, not its sandbox's _NET_WM_PID",
			wm:   "ewmh", start: winSandbox,
			want: []Focus{{Window: winSandbox, PID: 31337}},
		},
		{
			name: "a window without _NET_WM_PID has the pid the server knows",
			wm:   "ewmh", start: winNoPID,
			want: []Focus{{Window: winNoPID, PID: 888}},
		},
		{
			name: "a window from another host has no pid, though the server names the local ssh as its client",
			wm:   "ewmh", start: winRemote,
			want: []Focus{{Window: winRemote}},
		},
		{
			name: "a client the server knows no pid for, as over TCP, has none, whatever its _NET_WM_PID says",
			wm:   "ewmh", start: winTCP,
			want: []Focus{{Window: winTCP}},
		},
		{
			name: "a window that is gone has no pid, though a new client took its client's slot",
			wm:   "ewmh", start: winGone,
			want: []Focus{{Window: winGone}},
		},
		{
			name: "without X-Resource, the pid is the window's _NET_WM_PID",
			wm:   "ewmh", xres: "none", start: winA,
			want: []Focus{{Window: winA, PID: 4242}},
		},
		{
			name: "without X-Resource, a window without _NET_WM_PID has no pid",
			wm:   "ewmh", xres: "none", start: winNoPID,
			want: []Focus{{Window: winNoPID}},
		},
		{
			name: "without X-Resource, a window from another host has no pid",
			wm:   "ewmh", xres: "none", start: winRemote,
			want: []Focus{{Window: winRemote}},
		},
		{
			name: "X-Resource 1.1 has no QueryClientIds, so the pid is _NET_WM_PID",
			wm:   "ewmh", xres: "1.1", start: winA,
			want: []Focus{{Window: winA, PID: 4242}},
		},
		{
			name: "no focused window is window 0",
			wm:   "ewmh", start: 0,
			steps: []focusStep{{active: []uint32{winB}}},
			want:  []Focus{{Window: 0}, {Window: winB, PID: 777}},
		},
		{
			name: "without a window manager nothing is reported until _NET_ACTIVE_WINDOW changes",
			wm:   "none", start: winA,
			steps:  []focusStep{{other: "_NET_CLIENT_LIST"}},
			want:   nil,
			onEWMH: true,
		},
		{
			name: "a check window left by a dead window manager isn't a window manager",
			wm:   "stale", start: winA,
			want:   nil,
			onEWMH: true,
		},
		{
			name: "a window manager that doesn't list _NET_ACTIVE_WINDOW isn't followed until it sets it",
			wm:   "unlisted", start: winA,
			want:   nil,
			onEWMH: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeX(t, fakeXConfig{roots: []uint32{testRoot}, xres: tc.xres})
			addTestWindows(f)
			setWM(f, tc.wm, tc.start)
			const root = testRoot

			c, err := f.dial(0, nil)
			if err != nil {
				t.Fatal(err)
			}
			w, err := newX11Focus(c, testHost, time.Now().Add(5*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			defer w.close()
			if w.settle != focusSettle {
				t.Errorf("settle time %v, want focusSettle", w.settle)
			}
			settle := focusSettle
			if tc.settle != 0 {
				settle = tc.settle
			}
			w.settle = settle
			if got := f.selectedOn(root); got&propertyChangeMask == 0 {
				t.Fatalf("the root's event mask is %#x, without PropertyChangeMask", got)
			}
			if wantVia := map[string]string{"": focusViaXRes, "1.1": focusViaPID, "none": focusViaPID}[tc.xres]; w.via() != wantVia {
				t.Errorf("focus via %q, want %q", w.via(), wantVia)
			}
			found := false
			w.onEWMH = func() { found = true }
			out := make(chan Focus, 16)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- w.run(ctx, out) }()

			var got []Focus // reports without At, for comparing with want
			next := func() Focus {
				t.Helper()
				select {
				case r := <-out:
					got = append(got, Focus{Window: r.Window, PID: r.PID})
					return r
				case err := <-done:
					t.Fatalf("the watcher stopped: %v", err)
				case <-time.After(5 * time.Second):
					t.Fatalf("no focus report; so far %+v", got)
				}
				return Focus{}
			}
			reads := func() int { return f.readsOf(root, "_NET_ACTIVE_WINDOW") }
			// A report of a change is dated at the change, and comes at least
			// the settle time later.
			checkAt := func(r Focus, changed time.Time) {
				t.Helper()
				if r.At.Before(changed) {
					t.Errorf("window %#x is dated %v before the change it reports", r.Window, changed.Sub(r.At))
				}
				if late := time.Since(r.At); late < settle {
					t.Errorf("window %#x was reported %v after its date; want it dated at the change, at least the %v settle time before", r.Window, late, settle)
				}
			}
			// _NET_ACTIVE_WINDOW is read once at start when a window manager
			// keeps it, once for each step that sets it, a burst included,
			// and never for a change of another property. Each step waits
			// until the watcher is done with it, so that no read can see a
			// later step's window.
			reported, current, wantReads := false, uint32(0), 0
			if tc.wm == "ewmh" {
				r := next()
				reported, current, wantReads = true, r.Window, 1
			}
			if n := reads(); n != wantReads {
				t.Errorf("read _NET_ACTIVE_WINDOW %d times at start, want %d", n, wantReads)
			}
			for _, s := range tc.steps {
				before := reads()
				if s.other != "" {
					f.notify(root, s.other)
					// A watcher that took this for a focus change would read
					// once it settled.
					time.Sleep(settle + 100*time.Millisecond)
					if n := reads() - before; n != 0 {
						t.Errorf("a change of %s read _NET_ACTIVE_WINDOW %d times, want none", s.other, n)
					}
					continue
				}
				changed := time.Now()
				for i, win := range s.active {
					if i > 0 {
						time.Sleep(burstGap)
					}
					f.setActive(root, win)
				}
				if n := reads() - before; len(s.active) > 1 && n != 0 {
					t.Fatalf("read _NET_ACTIVE_WINDOW %d times within a burst, before it settled", n)
				}
				if win := s.active[len(s.active)-1]; reported && win == current {
					// Nothing is reported, so wait for the read that finds
					// the window unchanged.
					eventually(t, "a read of _NET_ACTIVE_WINDOW", func() bool { return reads() > before })
				} else {
					r := next()
					checkAt(r, changed)
					reported, current = true, r.Window
				}
				if n := reads() - before; n != 1 {
					t.Errorf("a change of _NET_ACTIVE_WINDOW read it %d times, want once", n)
				}
			}
			before, changed := reads(), time.Now()
			f.setActive(root, winLast)
			for {
				r := next()
				if r.Window == winLast {
					got = got[:len(got)-1]
					if r.PID != 1 {
						t.Errorf("the sentinel window's pid is %d, want 1", r.PID)
					}
					checkAt(r, changed)
					break
				}
			}
			if n := reads() - before; n != 1 {
				t.Errorf("the sentinel's change read _NET_ACTIVE_WINDOW %d times, want once", n)
			}
			if !equalFocus(got, tc.want) {
				t.Errorf("reports %+v, want %+v", got, tc.want)
			}
			if n := f.readsOfAny("_NET_WM_PID"); tc.xres == "" && n != 0 {
				t.Errorf("read _NET_WM_PID %d times on a server with X-Resource 1.2", n)
			}
			cancel()
			w.close()
			<-done
			if found != tc.onEWMH {
				t.Errorf("onEWMH called: %v, want %v", found, tc.onEWMH)
			}
		})
	}
}

func equalFocus(a, b []Focus) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Window != b[i].Window || a[i].PID != b[i].PID {
			return false
		}
	}
	return true
}

func put32le(b []byte, v uint32) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

func TestSameHost(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"the same name is the same host", "box", "box", true},
		{"case doesn't matter", "Box", "box", true},
		{"a domain doesn't make another host", "box.lan", "box", true},
		{"two domains on one name are one host", "box.lan", "box.example.org", true},
		{"another name is another host", "elsewhere", "box", false},
		{"a name that only starts the same is another host", "boxer", "box", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameHost(tc.a, tc.b); got != tc.want {
				t.Errorf("sameHost(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
