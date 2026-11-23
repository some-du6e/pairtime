// SPDX-License-Identifier: AGPL-3.0-or-later

package windows

import (
	"net"
	"testing"
)

// StartConnChild is startConnChild for the peer-check tests in package
// windows_test (peer_windows_test.go). It returns the end of the child's
// connection that ln accepted, and the child's pid.
func StartConnChild(t *testing.T, ln *net.TCPListener) (*net.TCPConn, int) {
	t.Helper()
	c := startConnChild(t, ln)
	return c.conn, c.cmd.Process.Pid
}
