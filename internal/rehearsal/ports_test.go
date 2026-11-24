// SPDX-License-Identifier: AGPL-3.0-or-later

package rehearsal

import (
	"net"
	"testing"
)

// freePorts returns n loopback ports, all different, that no listener
// holds. It keeps each listener it opens until it has read all n ports, so
// the kernel can't hand two of them the same one, then closes them all for
// the servers the rehearsal starts to take. One port at a time couldn't
// promise that: a port closed is free to be picked again at once, and
// pairtimed then refused a listen and an ops_listen on one port (#313).
func freePorts(t *testing.T, n int) []int {
	t.Helper()
	ls := make([]net.Listener, 0, n)
	defer func() {
		for _, l := range ls {
			l.Close()
		}
	}()
	ports := make([]int, 0, n)
	for range n {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ls = append(ls, l)
		ports = append(ports, l.Addr().(*net.TCPAddr).Port)
	}
	return ports
}

func TestFreePorts(t *testing.T) {
	tests := []struct {
		name string
		// n is how many ports one call picks, and times how often.
		n, times int
	}{
		{name: "the two ports pairtimed listens on always differ, picked 500 times over", n: 2, times: 500},
		{name: "any number of ports picked together always differ", n: 8, times: 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for range tt.times {
				ports := freePorts(t, tt.n)
				seen := make(map[int]bool, len(ports))
				for _, p := range ports {
					if p < 1 || p > 65535 || seen[p] {
						t.Fatalf("freePorts(%d) = %v, want %d different ports", tt.n, ports, tt.n)
					}
					seen[p] = true
				}
				if len(ports) != tt.n {
					t.Fatalf("freePorts(%d) = %v, want %d ports", tt.n, ports, tt.n)
				}
			}
		})
	}
}
