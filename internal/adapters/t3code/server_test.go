// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform"
)

// TestServerIn holds the process half of DESIGN 3.3's test of whether T3
// Code runs: the agent's process scan found T3's server, of this user, or
// of any session on Windows.
func TestServerIn(t *testing.T) {
	me, other := platform.UIDOwner(1000), platform.UIDOwner(1001)
	proc := func(name, owner string) platform.Proc {
		return platform.Proc{PID: 4242, PPID: 1, Name: name, Owner: owner}
	}
	cases := []struct {
		name  string
		goos  string
		procs []platform.Proc
		self  string
		want  bool
	}{
		{"T3 Code's t3 server running as this user is T3's server", platform.Linux, []platform.Proc{proc("t3", me)}, me, true},
		{"T3 Code's desktop app, which runs the server as itself, is T3's server", platform.Darwin,
			[]platform.Proc{proc("T3 Code (Beta)", me)}, me, true},
		{"another user's T3 Code server isn't this user's", platform.Linux, []platform.Proc{proc("t3", other)}, me, false},
		{"on Windows a T3 Code server in another session, as T3's scheduled service may run, is T3's server", platform.Windows,
			[]platform.Proc{proc("t3.exe", platform.SessionOwner(0))}, platform.SessionOwner(1), true},
		{"on Windows one whose session the scan couldn't read is T3's server too", platform.Windows,
			[]platform.Proc{proc("t3.exe", "")}, platform.SessionOwner(1), true},
		{"a harness isn't T3 Code's server", platform.Linux, []platform.Proc{proc("claude", me)}, me, false},
		{"a T3 run from its source, as node, isn't named, so it isn't found", platform.Linux, []platform.Proc{proc("node", me)}, me, false},
		{"a scan that failed has no table, and finds none", platform.Linux, nil, me, false},
		{"a scan that can't tell whose processes are this user's finds none", platform.Linux, []platform.Proc{proc("t3", "")}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ServerIn(tc.goos, tc.procs, tc.self); got != tc.want {
				t.Errorf("ServerIn = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRunning holds the reader's Status.Running to DESIGN 3.3's test, which
// the agent's cadence, presence's project context and doctor read: T3 runs
// when the last process scan found its server and the store has the -wal
// and -shm files T3 holds open. The files alone don't make it run, though
// the reader still reads them, and records what they hold. Counts.WithServer
// counts only the messages recorded while T3 ran, so doctor can tell a T3
// whose server pairtime doesn't name, as one run from its source under
// node, from one whose prompts skip pairtime's hooks.
func TestRunning(t *testing.T) {
	const text = "please fix the flaky parser test"
	cases := []struct {
		name    string
		server  bool // the agent's last process scan found T3's server
		open    bool // the store has its -wal and -shm files
		running bool
		records int
		// withServer is Counts.WithServer after the read.
		withServer int
	}{
		{"leftover -wal and -shm files with no server process don't make T3 run, though the reader reads them, and its record isn't counted as read with T3's server",
			false, true, false, 1, 0},
		{"T3's server process with its store's files makes T3 run, and its record is counted as read with T3's server", true, true, true, 1, 1},
		{"T3's server process with no -wal or -shm files doesn't make T3 run, and nothing is read", true, false, false, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			c := &clock{}
			c.set(0)
			r := s.reader(c, tracked(), State{})
			r.server = func() bool { return tc.server }
			mustPoll(t, r)
			s.sent("m1", 0, text)
			s.flush()
			if !tc.open {
				s.close()
			}
			c.set(time.Second)
			b, err := r.Poll(context.Background())
			if tc.open && err != nil || !tc.open && !errors.Is(err, ErrNotRunning) {
				t.Errorf("Poll gave %v", err)
			}
			st := r.Status()
			if st.Running != tc.running {
				t.Errorf("Running is %v, want %v", st.Running, tc.running)
			}
			if len(b.Messages) != tc.records {
				t.Errorf("got %d records, want %d", len(b.Messages), tc.records)
			}
			if st.Witnessed != tc.records || st.WithServer != tc.withServer {
				t.Errorf("the counts are %+v, want %d witnessed, %d of them with T3's server", st.Counts, tc.records, tc.withServer)
			}
		})
	}
}
