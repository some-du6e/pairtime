// SPDX-License-Identifier: AGPL-3.0-or-later

package darwin

import (
	"encoding/binary"
	"errors"
	"maps"
	"net/netip"
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/internal/platform"
)

// sockSpec describes a socket_fdinfo for sockBytes.
type sockSpec struct {
	so            uint64
	local, remote netip.AddrPort
	state         int32
	// v6 puts the addresses in in_sockinfo's IPv6 unions, as a dual-stack
	// socket has them, instead of the IPv4 ones.
	v6 bool
	// udp makes it SOCKINFO_IN, a socket that isn't TCP.
	udp bool
}

// sockBytes lays out s as PROC_PIDFDSOCKETINFO fills a socket_fdinfo:
// ports in network byte order inside an int, IPv4 addresses after three
// words of padding.
func sockBytes(s sockSpec) []byte {
	b := make([]byte, socketFDInfoSize)
	ne := binary.NativeEndian
	ne.PutUint64(b[offSo:], s.so)
	kind := uint32(sockinfoTCP)
	if s.udp {
		kind = 1 // SOCKINFO_IN
	}
	ne.PutUint32(b[offKind:], kind)
	port := func(off int, p uint16) {
		var n [2]byte
		binary.BigEndian.PutUint16(n[:], p)
		ne.PutUint32(b[off:], uint32(ne.Uint16(n[:])))
	}
	port(offLPort, s.local.Port())
	port(offFPort, s.remote.Port())
	if s.v6 {
		b[offVFlag] = iniIPv6
		l, r := s.local.Addr().As16(), s.remote.Addr().As16()
		copy(b[offLAddr:], l[:])
		copy(b[offFAddr:], r[:])
	} else {
		b[offVFlag] = iniIPv4
		l, r := s.local.Addr().As4(), s.remote.Addr().As4()
		copy(b[offLAddr+offAddr4:], l[:])
		copy(b[offFAddr+offAddr4:], r[:])
	}
	ne.PutUint32(b[offTCPState:], uint32(s.state))
	return b
}

var (
	agentEnd  = netip.MustParseAddrPort("127.0.0.1:7247")
	clientEnd = netip.MustParseAddrPort("127.0.0.1:54321")
	mapped    = func(ap netip.AddrPort) netip.AddrPort {
		return netip.AddrPortFrom(netip.AddrFrom16(ap.Addr().As16()), ap.Port())
	}
)

func TestParseSocketFDInfo(t *testing.T) {
	tests := []struct {
		name   string
		b      []byte
		want   tcpSock
		wantOK bool
		err    bool
	}{
		{
			name:   "an IPv4 TCP socket gives its handle, addresses, ports and state",
			b:      sockBytes(sockSpec{so: 0xffffff80_12345678, local: clientEnd, remote: agentEnd, state: tsiEstablished}),
			want:   tcpSock{so: 0xffffff80_12345678, local: clientEnd, remote: agentEnd, state: tsiEstablished},
			wantOK: true,
		},
		{
			name:   "a dual-stack socket's IPv4-mapped addresses read as IPv4",
			b:      sockBytes(sockSpec{so: 9, local: mapped(clientEnd), remote: mapped(agentEnd), state: 5, v6: true}),
			want:   tcpSock{so: 9, local: clientEnd, remote: agentEnd, state: 5},
			wantOK: true,
		},
		{
			name: "an IPv6 socket's addresses read from the IPv6 unions",
			b: sockBytes(sockSpec{so: 10, local: netip.MustParseAddrPort("[::1]:54321"), remote: netip.MustParseAddrPort("[::1]:7247"),
				state: tsiEstablished, v6: true}),
			want:   tcpSock{so: 10, local: netip.MustParseAddrPort("[::1]:54321"), remote: netip.MustParseAddrPort("[::1]:7247"), state: tsiEstablished},
			wantOK: true,
		},
		{
			name: "a socket that isn't TCP isn't read",
			b:    sockBytes(sockSpec{so: 11, local: clientEnd, remote: agentEnd, udp: true}),
		},
		{
			name: "a TCP socket with neither address flag isn't read",
			b: func() []byte {
				b := sockBytes(sockSpec{so: 12, local: clientEnd, remote: agentEnd, state: tsiEstablished})
				b[offVFlag] = 0
				return b
			}(),
		},
		{
			name: "a socket_fdinfo of another size is an error, since its layout is unknown",
			b:    sockBytes(sockSpec{so: 13, local: clientEnd, remote: agentEnd})[:socketFDInfoSize-8],
			err:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := parseSocketFDInfo(tt.b)
			if (err != nil) != tt.err {
				t.Fatalf("parseSocketFDInfo error = %v, want an error: %v", err, tt.err)
			}
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("parseSocketFDInfo = %+v, %v; want %+v, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// fakeConnSource is a connSource over fixed descriptors.
type fakeConnSource struct {
	pidList []int
	// sockets holds each process's socket descriptors, by pid and
	// descriptor. A pid in pidList without an entry can't be listed.
	sockets map[int]map[int32][]byte
	table   []platform.Proc
	// afterProcs runs once procs has been read, so a case can change what
	// the second read of a descriptor finds.
	afterProcs func(f *fakeConnSource)
}

func (f *fakeConnSource) pids() ([]int, error) { return f.pidList, nil }

func (f *fakeConnSource) socketFDs(pid int) ([]int32, error) {
	m, ok := f.sockets[pid]
	if !ok {
		return nil, errors.New("proc_pidinfo failed")
	}
	return slices.Sorted(maps.Keys(m)), nil
}

func (f *fakeConnSource) socketInfo(pid int, fd int32) ([]byte, error) {
	b, ok := f.sockets[pid][fd]
	if !ok {
		return nil, errors.New("proc_pidfdinfo failed")
	}
	return b, nil
}

func (f *fakeConnSource) procs() ([]platform.Proc, error) {
	if f.afterProcs != nil {
		f.afterProcs(f)
	}
	return f.table, nil
}

func TestLoopbackOwnerSource(t *testing.T) {
	client := sockBytes(sockSpec{so: 1, local: clientEnd, remote: agentEnd, state: tsiEstablished})
	server := sockBytes(sockSpec{so: 2, local: agentEnd, remote: clientEnd, state: tsiEstablished})
	table := []platform.Proc{{PID: 1, Name: "launchd"}, {PID: 300, PPID: 1, Name: "wakatime-cli-darwin-arm64"}, {PID: 400, PPID: 1, Name: "pairtime"}}
	tests := []struct {
		name           string
		src            *fakeConnSource
		client, server netip.AddrPort
		want           int // 0 for an error
	}{
		{
			name: "the one process with a descriptor of the socket owns it",
			src: &fakeConnSource{
				pidList: []int{300, 400},
				sockets: map[int]map[int32][]byte{300: {3: client}, 400: {7: server}},
			},
			client: clientEnd, server: agentEnd, want: 300,
		},
		{
			name: "two descriptors of one socket in one process are one owner",
			src: &fakeConnSource{
				pidList: []int{300, 400},
				sockets: map[int]map[int32][]byte{300: {3: client, 4: client}, 400: {7: server}},
			},
			client: clientEnd, server: agentEnd, want: 300,
		},
		{
			name: "a dual-stack socket's IPv4-mapped addresses match, and so do mapped addresses given to the lookup",
			src: &fakeConnSource{
				pidList: []int{300},
				sockets: map[int]map[int32][]byte{300: {3: sockBytes(sockSpec{so: 1, local: mapped(clientEnd), remote: mapped(agentEnd), state: tsiEstablished, v6: true})}},
			},
			client: mapped(clientEnd), server: mapped(agentEnd), want: 300,
		},
		{
			name: "a process whose descriptors can't be listed is passed over",
			src: &fakeConnSource{
				pidList: []int{299, 300},
				sockets: map[int]map[int32][]byte{300: {3: client}},
			},
			client: clientEnd, server: agentEnd, want: 300,
		},
		{
			name: "a socket two processes hold has no one owner",
			src: &fakeConnSource{
				pidList: []int{300, 301},
				sockets: map[int]map[int32][]byte{300: {3: client}, 301: {5: client}},
			},
			client: clientEnd, server: agentEnd,
		},
		{
			name: "two sockets with the connection's addresses have no one owner",
			src: &fakeConnSource{
				pidList: []int{300},
				sockets: map[int]map[int32][]byte{300: {3: client, 4: sockBytes(sockSpec{so: 3, local: clientEnd, remote: agentEnd, state: tsiEstablished})}},
			},
			client: clientEnd, server: agentEnd,
		},
		{
			name: "a socket in CLOSE_WAIT isn't established, so it has no owner",
			src: &fakeConnSource{
				pidList: []int{300},
				sockets: map[int]map[int32][]byte{300: {3: sockBytes(sockSpec{so: 1, local: clientEnd, remote: agentEnd, state: 5})}},
			},
			client: clientEnd, server: agentEnd,
		},
		{
			name: "the server's own end isn't a client end",
			src: &fakeConnSource{
				pidList: []int{400},
				sockets: map[int]map[int32][]byte{400: {7: server}},
			},
			client: clientEnd, server: agentEnd,
		},
		{
			name: "a socket that isn't TCP doesn't count, whatever its addresses",
			src: &fakeConnSource{
				pidList: []int{300},
				sockets: map[int]map[int32][]byte{300: {3: sockBytes(sockSpec{so: 1, local: clientEnd, remote: agentEnd, udp: true})}},
			},
			client: clientEnd, server: agentEnd,
		},
		{
			name: "a socket_fdinfo of an unknown size fails the lookup",
			src: &fakeConnSource{
				pidList: []int{300, 301},
				sockets: map[int]map[int32][]byte{300: {3: client[:socketFDInfoSize-8]}, 301: {4: client}},
			},
			client: clientEnd, server: agentEnd,
		},
		{
			name: "an owner that closes its descriptor while the table is read has no owner",
			src: &fakeConnSource{
				pidList:    []int{300},
				sockets:    map[int]map[int32][]byte{300: {3: client}},
				afterProcs: func(f *fakeConnSource) { delete(f.sockets[300], 3) },
			},
			client: clientEnd, server: agentEnd,
		},
		{
			name: "an owner whose descriptor holds another socket when read again has no owner",
			src: &fakeConnSource{
				pidList: []int{300},
				sockets: map[int]map[int32][]byte{300: {3: client}},
				afterProcs: func(f *fakeConnSource) {
					f.sockets[300][3] = sockBytes(sockSpec{so: 99, local: clientEnd, remote: agentEnd, state: tsiEstablished})
				},
			},
			client: clientEnd, server: agentEnd,
		},
		{
			name: "an address that isn't loopback is refused",
			src: &fakeConnSource{
				pidList: []int{300},
				sockets: map[int]map[int32][]byte{300: {3: sockBytes(sockSpec{so: 1, local: netip.MustParseAddrPort("10.0.0.5:54321"), remote: agentEnd, state: tsiEstablished})}},
			},
			client: netip.MustParseAddrPort("10.0.0.5:54321"), server: agentEnd,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.src.table = table
			pid, procs, err := loopbackOwner(tt.src, tt.client, tt.server)
			if tt.want == 0 {
				if err == nil {
					t.Fatalf("loopbackOwner = %d, want an error", pid)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if pid != tt.want {
				t.Errorf("loopbackOwner = %d, want %d", pid, tt.want)
			}
			if len(procs) != len(table) {
				t.Errorf("loopbackOwner returned %d processes, want the %d procs read", len(procs), len(table))
			}
		})
	}
}
