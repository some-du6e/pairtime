// SPDX-License-Identifier: AGPL-3.0-or-later

package focus

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
)

// fakeOS is an injected focus source: the test says which window comes to
// the front, and what the process table holds. No test moves real focus
// (JE must-fix 11).
type fakeOS struct {
	mu       sync.Mutex
	fn       func(Change)
	procs    []platform.Proc
	readErr  error
	startErr error
	started  int
	stopped  int
	reads    int
}

func (f *fakeOS) os() OS {
	return OS{
		Via:  "fake",
		GOOS: platform.Windows,
		Watch: func(fn func(Change)) (func(), error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.startErr != nil {
				return nil, f.startErr
			}
			f.started++
			f.fn = fn
			return func() {
				f.mu.Lock()
				defer f.mu.Unlock()
				f.stopped++
				f.fn = nil
			}, nil
		},
		Read: func() ([]platform.Proc, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.reads++
			return slices.Clone(f.procs), f.readErr
		},
	}
}

// front brings the window of pid to the front at at, as the OS would.
func (f *fakeOS) front(at time.Time, pid int) { f.send(Change{At: at, PID: pid}) }

func (f *fakeOS) send(c Change) {
	f.mu.Lock()
	fn := f.fn
	f.mu.Unlock()
	if fn != nil {
		fn(c)
	}
}

// rig is a Watcher on a fakeOS, with everything it calls recorded.
type rig struct {
	os *fakeOS
	w  *Watcher

	mu       sync.Mutex
	live     bool
	sessions map[platform.ID]string
	projects map[string]string
	// secret is K_app once the device key has opened, nil before.
	secret []byte
	// sampled is when presence's latest record is dated: presence moves a
	// change's time up to it, as FocusAt does.
	sampled  time.Time
	now      time.Time
	recs     []Record
	presence []event.AppClass
	kicks    int
	problems []string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{os: &fakeOS{procs: desktop()}, live: true, now: t0.Add(time.Hour)}
	tab := platform.NewTable(platform.Windows, r.os.procs)
	r.sessions = keyed(tab, desktopSessions)
	opts := Options{
		OS: r.os.os(),
		Sessions: func() map[platform.ID]string {
			r.mu.Lock()
			defer r.mu.Unlock()
			out := make(map[platform.ID]string, len(r.sessions))
			for id, k := range r.sessions {
				out[id] = k
			}
			return out
		},
		ProjectOf: func(s string) string {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.projects[s]
		},
		AppSecret: func() []byte {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.secret
		},
		Live: func() bool {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.live
		},
		Presence: func(at time.Time, c event.AppClass, write func(time.Time, bool)) {
			r.mu.Lock()
			r.presence = append(r.presence, c)
			live := r.live
			if r.sampled.After(at) {
				at = r.sampled
			}
			r.mu.Unlock()
			if write != nil {
				write(at, live)
			}
		},
		Kick: func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.kicks++
		},
		Record: func(rec Record) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.recs = append(r.recs, rec)
		},
		Problem: func(msg string) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.problems = append(r.problems, msg)
		},
		Now: func() time.Time {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.now
		},
	}
	r.w = New(opts)
	r.w.Start()
	t.Cleanup(r.w.Stop)
	return r
}

// step is one thing that happens, and what the watcher should make of it.
type step struct {
	name string
	do   func(r *rig)
	// recs are the records the step should write, kicks the scans it
	// should ask for, and presence the classes presence should hear.
	recs     []Record
	kicks    int
	presence []event.AppClass
	// unknown is what Status.Unknown should hold after the step, and
	// problem the last message Options.Problem has had by then, "" when
	// nothing is wrong.
	unknown, problem string
}

func focusRec(at time.Time, class event.AppClass, hosted ...string) Record {
	return Record{At: at, Data: &event.OSFocus{AppClass: class, HostedSessions: hosted}}
}

func TestWatcherSequences(t *testing.T) {
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	front := func(s, pid int) func(*rig) { return func(r *rig) { r.os.front(at(s), pid) } }
	tests := []struct {
		name  string
		steps []step
	}{
		{
			name: "a sequence of foreground changes becomes one os.focus per change, dated when focus moved",
			steps: []step{
				{"Windows Terminal comes to the front", front(0, 6100), []Record{focusRec(at(0), event.AppTerminal, "s_tab1", "s_tab2")}, 1, []event.AppClass{event.AppTerminal}, "", ""},
				{"Chrome comes to the front", front(5, 9600), []Record{focusRec(at(5), event.AppBrowser)}, 1, []event.AppClass{event.AppBrowser}, "", ""},
				{"VS Code comes to the front", front(9, 9000), []Record{focusRec(at(9), event.AppEditor, "s_code")}, 1, []event.AppClass{event.AppEditor}, "", ""},
				{"a classic cmd window comes to the front", front(20, 7000), []Record{focusRec(at(20), event.AppTerminal, "s_cmd")}, 1, []event.AppClass{event.AppTerminal}, "", ""},
				{"the Claude desktop app comes to the front", front(31, 9500), []Record{focusRec(at(31), event.AppAgentApp, "s_desk")}, 1, []event.AppClass{event.AppAgentApp}, "", ""},
				{"Notepad comes to the front", front(40, 9700), []Record{focusRec(at(40), event.AppOther)}, 1, []event.AppClass{event.AppOther}, "", ""},
			},
		},
		{
			name: "a switch to another window of the app in front writes nothing new",
			steps: []step{
				{"Windows Terminal comes to the front", front(0, 6100), []Record{focusRec(at(0), event.AppTerminal, "s_tab1", "s_tab2")}, 1, []event.AppClass{event.AppTerminal}, "", ""},
				{"its other window comes to the front", front(3, 6100), nil, 1, []event.AppClass{event.AppTerminal}, "", ""},
				{"Windows names the OpenConsole of one of its tabs", front(4, 6144), nil, 1, []event.AppClass{event.AppTerminal}, "", ""},
			},
		},
		{
			name: "focus pairtime can't name writes no os.focus and Status says why (decision 2)",
			steps: []step{
				{"Windows Terminal comes to the front", front(0, 6100), []Record{focusRec(at(0), event.AppTerminal, "s_tab1", "s_tab2")}, 1, []event.AppClass{event.AppTerminal}, "", ""},
				{"a UAC prompt's secure desktop takes the input", func(r *rig) { r.os.send(Change{At: at(5), Unknown: "the secure desktop"}) }, nil, 0, []event.AppClass{event.AppNone}, "the secure desktop", ""},
				{"the terminal is back in front, which is what the record before says", front(9, 6100), nil, 1, []event.AppClass{event.AppTerminal}, "", ""},
				{"Chrome comes to the front", front(12, 9600), []Record{focusRec(at(12), event.AppBrowser)}, 1, []event.AppClass{event.AppBrowser}, "", ""},
				{"a window whose process exited at once comes to the front", front(15, 31337), nil, 0, []event.AppClass{event.AppNone}, "the process of the window in front exited before pairtime read the process table", ""},
			},
		},
		{
			name: "nothing is recorded outside a project context, and the window in front is recorded when one goes live (D14)",
			steps: []step{
				{"with no context live, Chrome comes to the front", func(r *rig) { r.setLive(false); r.os.front(at(0), 9600) }, nil, 0, []event.AppClass{event.AppBrowser}, "", ""},
				{"Notepad comes to the front", front(4, 9700), nil, 0, []event.AppClass{event.AppOther}, "", ""},
				{"a hook makes a context live and the agent refreshes", func(r *rig) { r.setLive(true); r.w.Refresh() }, []Record{focusRec(t0.Add(time.Hour), event.AppOther)}, 0, nil, "", ""},
				{"another refresh finds nothing new", func(r *rig) { r.w.Refresh() }, nil, 0, nil, "", ""},
			},
		},
		{
			name: "a session that starts or ends under the window in front gets a new record for the same window",
			steps: []step{
				{"Windows Terminal comes to the front with one tab's session known", func(r *rig) {
					r.mu.Lock()
					for id, k := range r.sessions {
						if k == "s_tab2" {
							delete(r.sessions, id)
						}
					}
					r.mu.Unlock()
					r.os.front(at(0), 6100)
				}, []Record{focusRec(at(0), event.AppTerminal, "s_tab1")}, 1, []event.AppClass{event.AppTerminal}, "", ""},
				{"the scan finds the other tab's session", func(r *rig) {
					r.mu.Lock()
					r.sessions = keyed(platform.NewTable(platform.Windows, desktop()), desktopSessions)
					r.mu.Unlock()
					r.w.Refresh()
				}, []Record{focusRec(t0.Add(time.Hour), event.AppTerminal, "s_tab1", "s_tab2")}, 0, nil, "", ""},
				{"a refresh with nothing new writes nothing", func(r *rig) { r.w.Refresh() }, nil, 0, nil, "", ""},
				{"the first tab's claude exits, and the scan forgets its session", func(r *rig) {
					r.os.mu.Lock()
					r.os.procs = slices.DeleteFunc(r.os.procs, func(p platform.Proc) bool { return p.PID == 6210 })
					r.os.mu.Unlock()
					r.mu.Lock()
					for id, k := range r.sessions {
						if k == "s_tab1" {
							delete(r.sessions, id)
						}
					}
					r.now = r.now.Add(time.Minute)
					r.mu.Unlock()
					r.w.Refresh()
				}, []Record{focusRec(t0.Add(time.Hour+time.Minute), event.AppTerminal, "s_tab2")}, 0, nil, "", ""},
			},
		},
		{
			name: "focus_project is the project of the sessions a terminal or an editor hosts",
			steps: []step{
				{"a cmd window whose one session is in a known project comes to the front", func(r *rig) {
					r.mu.Lock()
					r.projects = map[string]string{"s_cmd": "p_9f2c", "s_code": "p_41ab"}
					r.mu.Unlock()
					r.os.front(at(0), 7000)
				}, []Record{{At: at(0), Data: &event.OSFocus{AppClass: event.AppTerminal, HostedSessions: []string{"s_cmd"}, FocusProject: "p_9f2c"}}}, 1, []event.AppClass{event.AppTerminal}, "", ""},
				{"VS Code, whose integrated terminal runs a session in another project, comes to the front", front(9, 9000), []Record{{At: at(9), Data: &event.OSFocus{AppClass: event.AppEditor, HostedSessions: []string{"s_code"}, FocusProject: "p_41ab"}}}, 1, []event.AppClass{event.AppEditor}, "", ""},
			},
		},
		{
			name: "app_hash is keyed with K_app once the agent has it, and left out before",
			steps: []step{
				{"Chrome comes to the front before the device key has opened", front(0, 9600), []Record{focusRec(at(0), event.AppBrowser)}, 1, []event.AppClass{event.AppBrowser}, "", ""},
				{"the key opens, and Windows Terminal comes to the front", func(r *rig) {
					r.mu.Lock()
					r.secret = testSecret()
					r.mu.Unlock()
					r.os.front(at(4), 6100)
				}, []Record{{At: at(4), Data: &event.OSFocus{AppClass: event.AppTerminal, AppHash: "af96f58c5388218fcd6f6cd72bb8e793f", HostedSessions: []string{"s_tab1", "s_tab2"}}}}, 1, []event.AppClass{event.AppTerminal}, "", ""},
				{"Chrome comes to the front again", front(9, 9600), []Record{{At: at(9), Data: &event.OSFocus{AppClass: event.AppBrowser, AppHash: "a18ecfcc1abf15c0fce0859c46e557fa6"}}}, 1, []event.AppClass{event.AppBrowser}, "", ""},
			},
		},
		{
			name: "a change's record takes the time presence dates the change's sample, which a record presence took meanwhile moves up",
			steps: []step{
				{"presence samples while the table is read for Windows Terminal, which came to the front a moment before", func(r *rig) {
					r.mu.Lock()
					r.sampled = at(0).Add(20 * time.Millisecond)
					r.mu.Unlock()
					r.os.front(at(0), 6100)
				}, []Record{focusRec(at(0).Add(20*time.Millisecond), event.AppTerminal, "s_tab1", "s_tab2")}, 1, []event.AppClass{event.AppTerminal}, "", ""},
			},
		},
		{
			name: "a change the OS dates before the record written last is recorded at that record's time, so records stay in time order",
			steps: []step{
				{"with no context live, Chrome comes to the front", func(r *rig) { r.setLive(false); r.os.front(at(0), 9600) }, nil, 0, []event.AppClass{event.AppBrowser}, "", ""},
				{"a context goes live, and the refresh records Chrome as of then", func(r *rig) { r.setLive(true); r.w.Refresh() }, []Record{focusRec(t0.Add(time.Hour), event.AppBrowser)}, 0, nil, "", ""},
				{"Windows Terminal comes to the front at a time dated before that refresh", front(5, 6100), []Record{focusRec(t0.Add(time.Hour), event.AppTerminal, "s_tab1", "s_tab2")}, 1, []event.AppClass{event.AppTerminal}, "", ""},
			},
		},
		{
			name: "a session key that isn't an opaque key is left out of the record",
			steps: []step{
				{"a session with a bad key runs in the cmd window", func(r *rig) {
					r.mu.Lock()
					p, _ := platform.NewTable(platform.Windows, desktop()).Get(7010)
					r.sessions[p.ID()] = "pid 7010"
					r.mu.Unlock()
					r.os.front(at(0), 7000)
				}, []Record{focusRec(at(0), event.AppTerminal)}, 1, []event.AppClass{event.AppTerminal}, "", ""},
			},
		},
		{
			name: "a process table that can't be read writes no record and is a problem until it can",
			steps: []step{
				{"the read fails as Windows Terminal comes to the front", func(r *rig) {
					r.os.mu.Lock()
					r.os.readErr = errors.New("access denied")
					r.os.mu.Unlock()
					r.os.front(at(0), 6100)
				}, nil, 0, []event.AppClass{event.AppNone}, "couldn't read the process table: access denied", "couldn't read the process table, so focus changes go unrecorded: access denied"},
				{"the read works again as Chrome comes to the front", func(r *rig) {
					r.os.mu.Lock()
					r.os.readErr = nil
					r.os.mu.Unlock()
					r.os.front(at(3), 9600)
				}, []Record{focusRec(at(3), event.AppBrowser)}, 1, []event.AppClass{event.AppBrowser}, "", ""},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			for _, st := range tt.steps {
				r.mu.Lock()
				nRecs, nKicks, nPres := len(r.recs), r.kicks, len(r.presence)
				r.mu.Unlock()
				st.do(r)
				r.w.sync()
				r.mu.Lock()
				recs, kicks, pres := slices.Clone(r.recs[nRecs:]), r.kicks-nKicks, slices.Clone(r.presence[nPres:])
				problem := ""
				if n := len(r.problems); n > 0 {
					problem = r.problems[n-1]
				}
				r.mu.Unlock()
				if !sameRecords(recs, st.recs) {
					t.Errorf("%s: records %s, want %s", st.name, show(recs), show(st.recs))
				}
				for _, rec := range recs {
					if err := rec.Data.Validate(); err != nil {
						t.Errorf("%s: the record doesn't validate: %v", st.name, err)
					}
				}
				if kicks != st.kicks {
					t.Errorf("%s: %d scans asked for, want %d", st.name, kicks, st.kicks)
				}
				if !slices.Equal(pres, st.presence) {
					t.Errorf("%s: presence heard %v, want %v", st.name, pres, st.presence)
				}
				if got := r.w.Status().Unknown; got != st.unknown {
					t.Errorf("%s: Status.Unknown %q, want %q", st.name, got, st.unknown)
				}
				if problem != st.problem {
					t.Errorf("%s: the last problem is %q, want %q", st.name, problem, st.problem)
				}
			}
		})
	}
}

func (r *rig) setLive(live bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.live = live
}

func testSecret() []byte {
	kDev := make([]byte, 32)
	for i := range kDev {
		kDev[i] = byte(i)
	}
	s, err := AppSecret(kDev)
	if err != nil {
		panic(err)
	}
	return s
}

func sameRecords(a, b []Record) bool {
	return slices.EqualFunc(a, b, func(x, y Record) bool {
		return x.At.Equal(y.At) && samePayload(x.Data, y.Data)
	})
}

func show(recs []Record) string {
	var parts []string
	for _, r := range recs {
		d := r.Data
		parts = append(parts, r.At.UTC().Format("15:04:05")+" "+string(d.AppClass)+" ["+strings.Join(d.HostedSessions, " ")+"] "+d.FocusProject+" "+d.AppHash)
	}
	return "{" + strings.Join(parts, "; ") + "}"
}

func TestWatcherLifecycle(t *testing.T) {
	t.Run("a source that fails to start records nothing and is a problem", func(t *testing.T) {
		f := &fakeOS{procs: desktop(), startErr: errors.New("no desktop")}
		var problems []string
		w := New(Options{OS: f.os(), Problem: func(m string) { problems = append(problems, m) }})
		w.Start()
		w.Refresh()
		w.Stop()
		if st := w.Status(); !strings.HasPrefix(st.Unknown, "the focus source didn't start") {
			t.Errorf("Status.Unknown %q, want why there is no source", st.Unknown)
		}
		if len(problems) == 0 || !strings.Contains(problems[len(problems)-1], "no desktop") {
			t.Errorf("problems %q, want the source's error", problems)
		}
	})
	t.Run("with no focus source on the OS, Status says so and Stop returns", func(t *testing.T) {
		w := New(Options{})
		w.Start()
		w.Refresh()
		w.sync()
		if st := w.Status(); st.Unknown != "pairtime has no focus source on this OS yet" || st.Via != "" {
			t.Errorf("Status %+v, want no source", st)
		}
		w.Stop()
		w.Stop()
	})
	t.Run("Stop stops the source and nothing is recorded after it", func(t *testing.T) {
		r := newRig(t)
		r.os.front(t0, 6100)
		r.w.sync()
		r.w.Stop()
		r.os.front(t0.Add(time.Second), 9600)
		r.w.Refresh()
		r.mu.Lock()
		n := len(r.recs)
		r.mu.Unlock()
		if n != 1 {
			t.Errorf("%d records, want the one before Stop", n)
		}
		r.os.mu.Lock()
		started, stopped := r.os.started, r.os.stopped
		r.os.mu.Unlock()
		if started != 1 || stopped != 1 {
			t.Errorf("the source started %d times and stopped %d, want once each", started, stopped)
		}
	})
	t.Run("Stop before Start leaves nothing running", func(t *testing.T) {
		f := &fakeOS{procs: desktop()}
		w := New(Options{OS: f.os()})
		w.Stop()
		w.Start()
		if f.started != 0 {
			t.Errorf("the source started %d times after Stop", f.started)
		}
	})
	t.Run("a refresh while the known sessions are unchanged doesn't walk the process table", func(t *testing.T) {
		r := newRig(t)
		r.os.front(t0, 6100)
		r.w.sync()
		reads := func() int {
			r.os.mu.Lock()
			defer r.os.mu.Unlock()
			return r.os.reads
		}
		before := reads()
		for range 3 {
			r.w.Refresh()
			r.w.sync()
		}
		if n := reads() - before; n != 0 {
			t.Errorf("%d walks for three refreshes that had nothing new, want none", n)
		}
		r.mu.Lock()
		for id := range r.sessions {
			delete(r.sessions, id)
			break
		}
		r.mu.Unlock()
		r.w.Refresh()
		r.w.sync()
		if n := reads() - before; n != 1 {
			t.Errorf("%d walks after a session went, want 1", n)
		}
	})
	t.Run("Status counts the changes the OS reported and the records written", func(t *testing.T) {
		r := newRig(t)
		r.os.front(t0, 6100)
		r.w.sync()
		r.os.front(t0.Add(time.Second), 6100)
		r.w.sync()
		st := r.w.Status()
		if st.Changes != 2 || st.Records != 1 || st.Class != event.AppTerminal || st.Hosted != 2 || st.Via != "fake" {
			t.Errorf("Status %+v, want 2 changes, 1 record, a terminal hosting 2 sessions", st)
		}
	})
}

// TestWatcherCoalesces checks that changes that come while the process
// table is being read cost one more walk, for the window that ended in
// front, and that nothing is lost about where focus ended.
func TestWatcherCoalesces(t *testing.T) {
	tests := []struct {
		name  string
		burst []int
		want  event.AppClass
	}{
		{"a burst of switches is recorded as the window that ended in front", []int{9600, 9700, 7000, 9000}, event.AppEditor},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gate := make(chan struct{})
			entered := make(chan struct{}, 16)
			f := &fakeOS{procs: desktop()}
			fos := f.os()
			read := fos.Read
			fos.Read = func() ([]platform.Proc, error) {
				entered <- struct{}{}
				<-gate
				return read()
			}
			var mu sync.Mutex
			var recs []Record
			w := New(Options{OS: fos, Record: func(r Record) { mu.Lock(); recs = append(recs, r); mu.Unlock() }})
			w.Start()
			defer w.Stop()
			f.front(t0, 6100)
			<-entered // the walk for the terminal is under way
			for i, pid := range tt.burst {
				f.front(t0.Add(time.Duration(i+1)*time.Second), pid)
			}
			close(gate)
			w.sync()
			mu.Lock()
			defer mu.Unlock()
			if len(recs) != 2 {
				t.Fatalf("%d records, want the first window and the last: %s", len(recs), show(recs))
			}
			if last := recs[1]; last.Data.AppClass != tt.want || !last.At.Equal(t0.Add(time.Duration(len(tt.burst))*time.Second)) {
				t.Errorf("the last record is %s, want %s at the last switch", show(recs[1:]), tt.want)
			}
		})
	}
}

// TestWatcherRefreshMeetsChange checks a refresh that a focus change
// interrupts: the change comes while the refresh reads the process table.
// The refresh writes no record of the window that has left the front,
// because one dated after the change would put that window back in front
// (pkg/score orders records by time), and the change's own record comes
// next, dated when focus moved.
func TestWatcherRefreshMeetsChange(t *testing.T) {
	tests := []struct {
		name string
		// known are the sessions the agent knows when the window comes to
		// the front, and found those the scan finds before the refresh.
		known, found []string
		want         []Record
	}{
		{
			name:  "a refresh that a focus change interrupts writes nothing, and the change's record follows",
			known: []string{"s_tab1"},
			found: []string{"s_tab1", "s_tab2"},
			want: []Record{
				focusRec(t0, event.AppTerminal, "s_tab1"),
				focusRec(t0.Add(30*time.Minute), event.AppBrowser),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			all := keyed(platform.NewTable(platform.Windows, desktop()), desktopSessions)
			only := func(keys []string) map[platform.ID]string {
				out := make(map[platform.ID]string)
				for id, k := range all {
					if slices.Contains(keys, k) {
						out[id] = k
					}
				}
				return out
			}
			f := &fakeOS{procs: desktop()}
			fos := f.os()
			read := fos.Read
			var gated atomic.Bool
			gate := make(chan struct{})
			entered := make(chan struct{}, 1)
			fos.Read = func() ([]platform.Proc, error) {
				if gated.Load() {
					entered <- struct{}{}
					<-gate
				}
				return read()
			}
			var mu sync.Mutex
			sessions := only(tt.known)
			var recs []Record
			w := New(Options{
				OS: fos,
				Sessions: func() map[platform.ID]string {
					mu.Lock()
					defer mu.Unlock()
					return maps.Clone(sessions)
				},
				Record: func(r Record) { mu.Lock(); recs = append(recs, r); mu.Unlock() },
				Now:    func() time.Time { return t0.Add(time.Hour) },
			})
			w.Start()
			defer w.Stop()
			f.front(t0, 6100)
			w.sync()

			mu.Lock()
			sessions = only(tt.found)
			mu.Unlock()
			gated.Store(true)
			w.Refresh()
			<-entered // the refresh is reading the table
			f.front(t0.Add(30*time.Minute), 9600)
			gated.Store(false)
			close(gate)
			w.sync()

			mu.Lock()
			defer mu.Unlock()
			if !sameRecords(recs, tt.want) {
				t.Errorf("records %s, want %s", show(recs), show(tt.want))
			}
		})
	}
}
