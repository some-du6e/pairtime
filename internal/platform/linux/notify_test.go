// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// These need no session bus, so ci.yml runs them. The calls against a
// notification server are in notify_desktop_test.go.

func TestNotifyBusAddress(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		sockets []string
		want    string
		wantOK  bool
	}{
		{
			name:   "DBUS_SESSION_BUS_ADDRESS is used as it is",
			env:    map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/tmp/dbus-test,guid=1", "XDG_RUNTIME_DIR": "/run/user/1000"},
			want:   "unix:path=/tmp/dbus-test,guid=1",
			wantOK: true,
		},
		{
			name:    "without it, the bus socket in XDG_RUNTIME_DIR is used",
			env:     map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"},
			sockets: []string{"/run/user/1000/bus"},
			want:    "unix:path=/run/user/1000/bus",
			wantOK:  true,
		},
		{
			name:    "without XDG_RUNTIME_DIR, the bus socket in /run/user/<uid> is used",
			env:     map[string]string{},
			sockets: []string{"/run/user/1000/bus"},
			want:    "unix:path=/run/user/1000/bus",
			wantOK:  true,
		},
		{
			name:    "XDG_RUNTIME_DIR's socket comes before /run/user/<uid>'s",
			env:     map[string]string{"XDG_RUNTIME_DIR": "/tmp/runtime"},
			sockets: []string{"/tmp/runtime/bus", "/run/user/1000/bus"},
			want:    "unix:path=/tmp/runtime/bus",
			wantOK:  true,
		},
		{
			name:    "autolaunch counts as no address, because pairtime never starts a bus",
			env:     map[string]string{"DBUS_SESSION_BUS_ADDRESS": "autolaunch:"},
			sockets: nil,
			wantOK:  false,
		},
		{
			name:   "no address and no socket means no session bus, as on a machine with no desktop",
			env:    map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"},
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string { return tt.env[k] }
			isBus := func(p string, uid int) bool {
				if uid != 1000 {
					t.Errorf("isBus(%q, %d), want the uid notifyBusAddress was given, 1000", p, uid)
				}
				return slices.Contains(tt.sockets, p)
			}
			got, ok := notifyBusAddress(getenv, isBus, 1000)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("notifyBusAddress() = %q, %v, want %q, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// libdbus's _dbus_lookup_user_bus takes $XDG_RUNTIME_DIR/bus only when
// lstat says it is a socket and its owner is the caller's uid.
func TestNotifyBusSocket(t *testing.T) {
	// A short directory: a socket's path must fit in 108 bytes.
	dir := t.TempDir()
	sock := filepath.Join(dir, "bus")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	link := filepath.Join(dir, "link")
	if err := os.Symlink(sock, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	me := os.Getuid()
	tests := []struct {
		name string
		path string
		uid  int
		want bool
	}{
		{name: "a socket this user owns is the bus", path: sock, uid: me, want: true},
		{name: "a socket another user owns isn't", path: sock, uid: me + 1, want: false},
		{name: "a symlink to this user's socket isn't, since lstat doesn't follow it", path: link, uid: me, want: false},
		{name: "a file that isn't a socket isn't", path: file, uid: me, want: false},
		{name: "a path with nothing there isn't", path: filepath.Join(dir, "missing"), uid: me, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := notifyBusSocket(tt.path, tt.uid); got != tt.want {
				t.Errorf("notifyBusSocket(%q, %d) = %v, want %v", tt.path, tt.uid, got, tt.want)
			}
		})
	}
}

func TestEscapeNotifyMarkup(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain text is unchanged", in: "52 min on remind-bot.", want: "52 min on remind-bot."},
		{name: "a tag can't start, so it shows as text", in: "<b>bold</b>", want: "&lt;b&gt;bold&lt;/b&gt;"},
		{name: "an ampersand can't start an entity", in: "Tom & Jerry &amp;", want: "Tom &amp; Jerry &amp;amp;"},
		{name: "non-ASCII text is unchanged", in: "café ✓", want: "café ✓"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := escapeNotifyMarkup(tt.in); got != tt.want {
				t.Errorf("escapeNotifyMarkup(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNotifyCallError(t *testing.T) {
	serverSaid := "the server's own words, which pairtime doesn't repeat"
	tests := []struct {
		name         string
		err          error
		wantNoServer bool
		wantName     string // a substring the error must have
	}{
		{
			name:         "an unknown service means no notification server",
			err:          dbus.Error{Name: "org.freedesktop.DBus.Error.ServiceUnknown", Body: []any{serverSaid}},
			wantNoServer: true,
			wantName:     "ServiceUnknown",
		},
		{
			name:         "a name with no owner means no notification server",
			err:          dbus.Error{Name: "org.freedesktop.DBus.Error.NameHasNoOwner", Body: []any{serverSaid}},
			wantNoServer: true,
			wantName:     "NameHasNoOwner",
		},
		{
			name:         "a server the bus failed to start means no notification server",
			err:          &dbus.Error{Name: "org.freedesktop.DBus.Error.Spawn.ChildExited", Body: []any{serverSaid}},
			wantNoServer: true,
			wantName:     "Spawn.ChildExited",
		},
		{
			name:     "another D-Bus error keeps its name and not the server's message",
			err:      dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied", Body: []any{serverSaid}},
			wantName: "AccessDenied",
		},
		{
			name:     "an error that isn't from D-Bus is kept",
			err:      errors.New("dbus: connection closed by user"),
			wantName: "connection closed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := notifyCallError(context.Background(), "Notify", tt.err)
			if errors.Is(got, ErrNotifyNoServer) != tt.wantNoServer {
				t.Errorf("notifyCallError() = %v, wraps ErrNotifyNoServer: %v, want %v", got, !tt.wantNoServer, tt.wantNoServer)
			}
			if !strings.Contains(got.Error(), tt.wantName) {
				t.Errorf("notifyCallError() = %q, want it to contain %q", got, tt.wantName)
			}
			if strings.Contains(got.Error(), serverSaid) {
				t.Errorf("notifyCallError() = %q repeats the server's message", got)
			}
		})
	}
}

// godbus's Connect dials before it applies WithContext, so a session bus
// address whose TCP host never answered held Notify for about two minutes,
// past any deadline (#86).
func TestNotifyGivesUpOnATCPBusThatNeverAnswers(t *testing.T) {
	port := unansweredTCP(t)
	tests := []struct {
		name string
		n    Notifications
	}{
		{name: "a tcp: session bus whose host never answers is given up at ctx's deadline", n: Notifications{Address: "tcp:host=127.0.0.1,port=" + port}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := tt.n.Notify(ctx, "summary", "body")
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("Notify() error = %v, want ctx's deadline", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Notify was still connecting 5 s after a deadline of 300 ms")
			}
		})
	}
}

func TestNotifyFailsQuicklyWithoutABus(t *testing.T) {
	tests := []struct {
		name string
		n    Notifications
	}{
		{name: "a bus address whose socket doesn't exist means no session bus", n: Notifications{Address: "unix:path=" + filepath.Join(t.TempDir(), "no-bus-here")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			start := time.Now()
			_, err := tt.n.Notify(ctx, "summary", "body")
			if !errors.Is(err, ErrNotifyNoBus) {
				t.Fatalf("Notify() error = %v, want ErrNotifyNoBus", err)
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Errorf("Notify() took %v to find there is no bus", d)
			}
		})
	}
}
