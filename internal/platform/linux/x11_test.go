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
			src := &x11Idle{open: func(time.Duration) (*xconn, error) {
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
			src := &x11Idle{open: func(time.Duration) (*xconn, error) {
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

// Windows the focus tests use. testHost is the machine the client runs on.
const (
	testHost  = "testhost"
	winA      = 0x501 // pid 4242, on testhost
	winB      = 0x502 // pid 777, on testhost under its full name
	winRemote = 0x503 // pid 999, on another host
	winGone   = 0x504 // destroyed before pairtime reads it
	winNoPID  = 0x505 // sets no _NET_WM_PID
	winLast   = 0x5ff // the sentinel each test ends with, pid 1
	wmCheck   = 0x400 // the window manager's check window
)

// focusStep changes the fake server, then sends a PropertyNotify.
type focusStep struct {
	active uint32 // set _NET_ACTIVE_WINDOW to this window
	notify string // the root property the PropertyNotify names
}

func TestX11Focus(t *testing.T) {
	cases := []struct {
		name   string
		wm     string // "ewmh", "none", "stale" or "unlisted"
		start  uint32 // _NET_ACTIVE_WINDOW at start
		steps  []focusStep
		want   []Focus // Window and PID of each report before the sentinel
		onEWMH bool    // the window manager was found only after start
	}{
		{
			name: "the window focused at start is reported once, with its pid",
			wm:   "ewmh", start: winA,
			want: []Focus{{Window: winA, PID: 4242}},
		},
		{
			name: "a change of _NET_ACTIVE_WINDOW reports the new window",
			wm:   "ewmh", start: winA,
			steps: []focusStep{{active: winB, notify: "_NET_ACTIVE_WINDOW"}},
			want:  []Focus{{Window: winA, PID: 4242}, {Window: winB, PID: 777}},
		},
		{
			name: "a change of another root property reports nothing",
			wm:   "ewmh", start: winA,
			steps: []focusStep{{active: winA, notify: "_NET_CLIENT_LIST_STACKING"}},
			want:  []Focus{{Window: winA, PID: 4242}},
		},
		{
			name: "the same window set again reports nothing",
			wm:   "ewmh", start: winA,
			steps: []focusStep{{active: winA, notify: "_NET_ACTIVE_WINDOW"}},
			want:  []Focus{{Window: winA, PID: 4242}},
		},
		{
			name: "a window from another host has no pid",
			wm:   "ewmh", start: winRemote,
			want: []Focus{{Window: winRemote}},
		},
		{
			name: "a window that is gone has no pid",
			wm:   "ewmh", start: winGone,
			want: []Focus{{Window: winGone}},
		},
		{
			name: "a window without _NET_WM_PID has no pid",
			wm:   "ewmh", start: winNoPID,
			want: []Focus{{Window: winNoPID}},
		},
		{
			name: "no focused window is window 0",
			wm:   "ewmh", start: 0,
			steps: []focusStep{{active: winB, notify: "_NET_ACTIVE_WINDOW"}},
			want:  []Focus{{Window: 0}, {Window: winB, PID: 777}},
		},
		{
			name: "without a window manager nothing is reported until _NET_ACTIVE_WINDOW changes",
			wm:   "none", start: winA,
			steps:  []focusStep{{active: winB, notify: "_NET_CLIENT_LIST"}},
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
			const root = 0x100
			f := newFakeX(t, fakeXConfig{roots: []uint32{root}})
			for _, w := range []struct {
				id      uint32
				pid     uint32
				machine string
			}{
				{winA, 4242, testHost},
				{winB, 777, testHost + ".example.org"},
				{winRemote, 999, "elsewhere"},
				{winNoPID, 0, testHost},
				{winLast, 1, testHost},
			} {
				f.addWindow(w.id)
				if w.pid != 0 {
					f.setCardinal(w.id, "_NET_WM_PID", atomCardinal, w.pid)
				}
				f.setProp(w.id, "WM_CLIENT_MACHINE", atomString, 8, []byte(w.machine))
			}
			switch tc.wm {
			case "ewmh", "unlisted":
				f.addWindow(wmCheck)
				f.setCardinal(root, "_NET_SUPPORTING_WM_CHECK", atomWindow, wmCheck)
				f.setCardinal(wmCheck, "_NET_SUPPORTING_WM_CHECK", atomWindow, wmCheck)
			case "stale":
				f.setCardinal(root, "_NET_SUPPORTING_WM_CHECK", atomWindow, wmCheck)
			}
			supported := []string{"_NET_SUPPORTED", "_NET_CLIENT_LIST", "_NET_ACTIVE_WINDOW"}
			if tc.wm == "unlisted" {
				supported = supported[:2]
			}
			var list []byte
			for _, name := range supported {
				list = put32le(list, f.atom(name))
			}
			f.setProp(root, "_NET_SUPPORTED", atomAtom, 32, list)
			f.setCardinal(root, "_NET_ACTIVE_WINDOW", atomWindow, tc.start)

			c, err := f.dial(0, nil)
			if err != nil {
				t.Fatal(err)
			}
			w, err := newX11Focus(c, testHost, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer w.close()
			if got := f.selectedOn(root); got&propertyChangeMask == 0 {
				t.Fatalf("the root's event mask is %#x, without PropertyChangeMask", got)
			}
			found := false
			w.onEWMH = func() { found = true }
			out := make(chan Focus, 16)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- w.run(ctx, out) }()

			var got []Focus
			next := func() Focus {
				t.Helper()
				select {
				case r := <-out:
					return Focus{Window: r.Window, PID: r.PID}
				case err := <-done:
					t.Fatalf("the watcher stopped: %v", err)
				case <-time.After(5 * time.Second):
					t.Fatalf("no focus report; so far %+v", got)
				}
				return Focus{}
			}
			// Each report is awaited before the next change, so the client
			// can't read a later value for an earlier notification.
			if tc.wm == "ewmh" {
				got = append(got, next())
			}
			current := tc.start
			for _, s := range tc.steps {
				f.setCardinal(root, "_NET_ACTIVE_WINDOW", atomWindow, s.active)
				f.notify(root, s.notify)
				if tc.wm == "ewmh" && s.notify == "_NET_ACTIVE_WINDOW" && s.active != current {
					got = append(got, next())
				}
				current = s.active
			}
			f.setCardinal(root, "_NET_ACTIVE_WINDOW", atomWindow, winLast)
			f.notify(root, "_NET_ACTIVE_WINDOW")
			for {
				r := next()
				if r.Window == winLast {
					if r.PID != 1 {
						t.Errorf("the sentinel window's pid is %d, want 1", r.PID)
					}
					break
				}
				got = append(got, r)
			}
			if !equalFocus(got, tc.want) {
				t.Errorf("reports %+v, want %+v", got, tc.want)
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
