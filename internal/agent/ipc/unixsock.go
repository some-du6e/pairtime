// SPDX-License-Identifier: AGPL-3.0-or-later

package ipc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"runtime"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
)

// ErrInUse means an agent already listens on the socket, which happens when
// two data directories share one runtime directory.
var ErrInUse = errors.New("ipc: another agent already listens on the socket")

// sunPath is the room for a socket path: sun_path is 108 bytes on Linux and
// Windows and 104 on macOS (CP 6.3), and the path needs a NUL after it.
func sunPath() int {
	if runtime.GOOS == "darwin" {
		return 104
	}
	return 108
}

// listenUnix listens on the socket path, after removing a socket that a
// dead agent left there.
func listenUnix(path string) (*net.UnixListener, error) {
	if len(path) >= sunPath() {
		return nil, fmt.Errorf("ipc: the socket path %s is %d bytes, and this OS takes at most %d; set %s to a shorter directory",
			path, len(path), sunPath()-1, home.EnvHome)
	}
	if err := removeStale(path); err != nil {
		return nil, err
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := secureSocket(path); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

// removeStale deletes the socket file at path when nothing answers on it.
// An agent that was killed leaves its socket file behind, and binding to
// the path fails until the file is gone. One that answers is left alone.
func removeStale(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// On Windows an AF_UNIX socket file is a reparse point, which Go
	// doesn't report as a socket on every version.
	if fi.IsDir() || (runtime.GOOS != "windows" && fi.Mode().Type() != fs.ModeSocket) {
		return fmt.Errorf("ipc: %s is in the socket's place and isn't a socket", path)
	}
	c, err := net.DialTimeout("unix", path, time.Second)
	if err == nil {
		c.Close()
		return ErrInUse
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// dialSocket connects to the socket at path and checks that the process
// listening on it runs as want.
func dialSocket(ctx context.Context, path string, want identity) (net.Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		c.Close()
		return nil, fmt.Errorf("ipc: dialing a socket gave a %T", c)
	}
	id, _, err := unixPeer(uc)
	if err != nil {
		uc.Close()
		return nil, fmt.Errorf("%w: %w", ErrNotAgent, err)
	}
	if id != want {
		uc.Close()
		return nil, fmt.Errorf("%w: it runs as %s", ErrNotAgent, id)
	}
	return uc, nil
}
