// SPDX-License-Identifier: AGPL-3.0-or-later

package darwin_test

import (
	"net"
	"net/netip"
	"os"
	"testing"

	"github.com/some-du6e/pairtime/internal/agent/peer"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/darwin"
)

// TestPeerChecks runs the agent's peer-process checks over a real loopback
// connection from a child, this test binary, which is neither wakatime-cli
// nor a harness. They run here, and not only in internal/agent/peer,
// because platform.yml's path filter doesn't cover internal/agent/peer
// (PLAN, WP-OS13).
func TestPeerChecks(t *testing.T) {
	tests := []struct {
		name  string
		check func(c *peer.Checker, client, server netip.AddrPort) peer.Result
		want  peer.Reason
	}{
		{
			name: "a heartbeat from a child that isn't wakatime-cli fails the check, which names the child",
			check: func(c *peer.Checker, client, server netip.AddrPort) peer.Result {
				return c.Heartbeat(client, server)
			},
			want: peer.NotWakaTime,
		},
		{
			name: "OTLP from a child that isn't a harness fails the check, though named",
			check: func(c *peer.Checker, client, server netip.AddrPort) peer.Result {
				return c.OTLP(client, server, func(platform.ID) bool { return true })
			},
			want: peer.NotHarness,
		},
	}
	c := peer.New(peer.Options{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ln, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { ln.Close() })
			conn, pid := darwin.StartConnChild(t, ln.(*net.TCPListener))
			client, server, err := peer.ConnAddrs(conn)
			if err != nil {
				t.Fatal(err)
			}
			r := tt.check(c, client, server)
			if r.Reason != tt.want {
				t.Fatalf("= %s (%v), want %s; chain %v", r.Reason, r.Err, tt.want, r.Chain)
			}
			if r.PID != pid || r.Chain[0].PID != pid {
				t.Errorf("PID %d, Chain[0] %v; want the child, %d", r.PID, r.Chain[0], pid)
			}
			if len(r.Chain) < 2 || r.Chain[1].PID != os.Getpid() {
				t.Errorf("Chain %v, want this process, %d, as the child's parent", r.Chain, os.Getpid())
			}
		})
	}
}
