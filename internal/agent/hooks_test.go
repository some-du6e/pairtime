// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/focus"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/presence"
	"github.com/some-du6e/pairtime/internal/agent/procs"
	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

// recorder stands in for the hook sink's steps and for the sink after it,
// and records what the hook sink did.
type recorder struct {
	mu        sync.Mutex
	secret    []byte // K_session; nil while it isn't known
	presence  []presenceCall
	claims    []int
	refreshes int
	stored    []inbox.Delivery
}

type presenceCall struct {
	key  string
	kind event.Kind
}

func (r *recorder) Deliver(d inbox.Delivery) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stored = append(r.stored, d)
	return nil
}

func (r *recorder) steps() hookSteps {
	return hookSteps{
		claim: func(p platform.Proc) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.claims = append(r.claims, p.PID)
		},
		presence: func(key string, k event.Kind) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.presence = append(r.presence, presenceCall{key, k})
		},
		secret: func() []byte { return r.secret },
		refresh: func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.refreshes++
		},
	}
}

// hookMessage is a Claude Code hook call of the event name with stdin
// payload, sent at at by the process whose chain entry is self.
func hookMessage(t *testing.T, name, payload string, self inbox.Proc, at time.Time) inbox.Message {
	t.Helper()
	b := hookpath.Body{Harness: "claude_code", Event: name, Bytes: len(payload)}
	if payload != "" {
		b.Payload = json.RawMessage(payload)
	}
	body, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return inbox.Message{ID: inbox.NewID(), Kind: hookpath.Kind, Sent: at, Chain: []inbox.Proc{self}, Body: body}
}

func TestHookSink(t *testing.T) {
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	proc := func(pid, ppid int, name string, ago time.Duration) platform.Proc {
		return platform.Proc{PID: pid, PPID: ppid, Name: name, PGID: pid, TTY: platform.TTYAttached, TPGID: pid, Start: at.Add(-ago), Owner: "uid:1000"}
	}
	hook := proc(400, 300, "pairtime", 0)
	claude := proc(300, 200, "claude", time.Minute)
	bash := proc(200, 100, "bash", time.Hour)
	kitty := proc(100, 1, "kitty", 2*time.Hour)
	// git has the hook's pid, which it got after the hook exited.
	git := proc(400, 300, "git", -time.Second)
	self := inbox.Proc{PID: hook.PID, Name: hook.Name, Start: hook.Start}
	walk := []platform.Proc{hook, claude, bash, kitty}
	secret := bytes.Repeat([]byte{7}, 32)

	socket := func(name, payload string) inbox.Delivery {
		return inbox.Delivery{Message: hookMessage(t, name, payload, self, at), Via: inbox.ViaSocket, PeerPID: hook.PID, Walk: walk}
	}
	s1 := `{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"hi"}`
	s2 := `{"session_id":"s2","hook_event_name":"SessionStart"}`
	key := func(id string) string { return hookpath.SessionKey(secret, id) }

	tests := []struct {
		name     string
		noSecret bool // K_session isn't known yet
		calls    func() []inbox.Delivery
		check    func(t *testing.T, r *recorder, ties *hookTies)
	}{
		{
			name: "a socket call under a harness, with a session_id, reaches presence once with an s_ key and its kind, and claims the harness",
			calls: func() []inbox.Delivery {
				return []inbox.Delivery{socket("UserPromptSubmit", s1)}
			},
			check: func(t *testing.T, r *recorder, _ *hookTies) {
				if len(r.presence) != 1 || !strings.HasPrefix(r.presence[0].key, "s_") || r.presence[0].kind != event.KindPromptSubmit {
					t.Errorf("presence heard %+v, want one prompt under an s_ key", r.presence)
				}
				if !slices.Equal(r.claims, []int{300}) {
					t.Errorf("the scan was told of %v, want the harness, 300", r.claims)
				}
				if d := r.stored[0]; d.EventVia != event.ViaHook || d.Harness != event.HarnessClaudeCode || len(d.Walk) != 4 {
					t.Errorf("the journal got via %q, harness %q and a walk of %d, want hook, claude_code and 4", d.EventVia, d.Harness, len(d.Walk))
				}
			},
		},
		{
			name: "the call ties the harness process to its session's key, the one hookpath.SessionKey gives, and focus looks again",
			calls: func() []inbox.Delivery {
				return []inbox.Delivery{socket("UserPromptSubmit", s1)}
			},
			check: func(t *testing.T, r *recorder, ties *hookTies) {
				want := map[platform.ID]string{claude.ID(): key("s1")}
				if got := ties.merged(nil); !maps.Equal(got, want) {
					t.Errorf("focus gets the sessions %v, want %v", got, want)
				}
				if r.refreshes != 1 {
					t.Errorf("focus looked again %d times, want once", r.refreshes)
				}
			},
		},
		{
			name: "a spooled call reaches neither presence nor the scan nor focus, and is plugin",
			calls: func() []inbox.Delivery {
				m := hookMessage(t, "UserPromptSubmit", s1, self, at)
				m.Chain = []inbox.Proc{self, {PID: 300, Name: "claude"}, {PID: 200, Name: "bash"}}
				return []inbox.Delivery{{Message: m, Via: inbox.ViaInbox}}
			},
			check: func(t *testing.T, r *recorder, ties *hookTies) {
				if len(r.presence) != 0 || len(r.claims) != 0 || r.refreshes != 0 || len(ties.merged(nil)) != 0 {
					t.Errorf("presence heard %v, the scan %v, focus %d times, and the ties are %v; want none", r.presence, r.claims, r.refreshes, ties.merged(nil))
				}
				if d := r.stored[0]; d.EventVia != event.ViaPlugin || d.Harness != event.HarnessClaudeCode {
					t.Errorf("the journal got via %q and harness %q, want plugin and the harness the line names", d.EventVia, d.Harness)
				}
			},
		},
		{
			name: "a payload without a session id gives presence the key \"\" and ties nothing",
			calls: func() []inbox.Delivery {
				return []inbox.Delivery{socket("UserPromptSubmit", `{"prompt":"hi"}`)}
			},
			check: func(t *testing.T, r *recorder, ties *hookTies) {
				if len(r.presence) != 1 || r.presence[0].key != "" {
					t.Errorf("presence heard %+v, want one call with the key \"\"", r.presence)
				}
				if len(ties.merged(nil)) != 0 || r.refreshes != 0 {
					t.Errorf("the ties are %v after %d refreshes, want none", ties.merged(nil), r.refreshes)
				}
			},
		},
		{
			name: "a socket sender whose pid has passed to a newer process under the harness gets no walked chain, and claims and ties nothing",
			calls: func() []inbox.Delivery {
				d := socket("UserPromptSubmit", s1)
				d.Walk = []platform.Proc{git, claude, bash, kitty}
				return []inbox.Delivery{d}
			},
			check: func(t *testing.T, r *recorder, ties *hookTies) {
				if d := r.stored[0]; d.Walk != nil || d.EventVia != event.ViaPlugin {
					t.Errorf("the journal got a walk of %d and via %q, want no walk and plugin", len(d.Walk), d.EventVia)
				}
				if len(r.claims) != 0 || len(ties.merged(nil)) != 0 {
					t.Errorf("the scan was told of %v and the ties are %v, want neither", r.claims, ties.merged(nil))
				}
				if len(r.presence) != 1 {
					t.Errorf("presence heard %v, want the call, which came over the socket", r.presence)
				}
			},
		},
		{
			name: "a second call of a session in the same process is no news for focus",
			calls: func() []inbox.Delivery {
				return []inbox.Delivery{socket("UserPromptSubmit", s1), socket("Stop", `{"session_id":"s1"}`)}
			},
			check: func(t *testing.T, r *recorder, _ *hookTies) {
				if r.refreshes != 1 || len(r.presence) != 2 {
					t.Errorf("focus looked again %d times and presence heard %v, want once and two calls", r.refreshes, r.presence)
				}
			},
		},
		{
			name: "a session's start and end each have focus look again, and its end unties it",
			calls: func() []inbox.Delivery {
				return []inbox.Delivery{socket("SessionStart", `{"session_id":"s1"}`), socket("SessionEnd", `{"session_id":"s1"}`)}
			},
			check: func(t *testing.T, r *recorder, ties *hookTies) {
				if r.refreshes != 2 || len(ties.merged(nil)) != 0 {
					t.Errorf("focus looked again %d times and the ties are %v, want twice and none", r.refreshes, ties.merged(nil))
				}
			},
		},
		{
			name: "a new session in a process the hook path tied takes the old one's place",
			calls: func() []inbox.Delivery {
				return []inbox.Delivery{socket("UserPromptSubmit", s1), socket("SessionStart", s2)}
			},
			check: func(t *testing.T, r *recorder, ties *hookTies) {
				want := map[platform.ID]string{claude.ID(): key("s2")}
				if got := ties.merged(nil); !maps.Equal(got, want) || r.refreshes != 2 {
					t.Errorf("the ties are %v after %d refreshes, want %v after 2", got, r.refreshes, want)
				}
			},
		},
		{
			name:     "until K_session is known a call claims its harness but ties nothing",
			noSecret: true,
			calls: func() []inbox.Delivery {
				return []inbox.Delivery{socket("UserPromptSubmit", s1)}
			},
			check: func(t *testing.T, r *recorder, ties *hookTies) {
				if !slices.Equal(r.claims, []int{300}) || len(ties.merged(nil)) != 0 || r.refreshes != 0 {
					t.Errorf("the scan was told of %v, the ties are %v after %d refreshes; want 300, none and none", r.claims, ties.merged(nil), r.refreshes)
				}
			},
		},
		{
			name: "a call whose body can't be read still claims the harness the walk found, and reaches neither presence nor focus",
			calls: func() []inbox.Delivery {
				d := socket("UserPromptSubmit", s1)
				d.Body = json.RawMessage(`{"bytes":"many"}`)
				return []inbox.Delivery{d}
			},
			check: func(t *testing.T, r *recorder, ties *hookTies) {
				if !slices.Equal(r.claims, []int{300}) || len(r.presence) != 0 || len(ties.merged(nil)) != 0 {
					t.Errorf("the scan was told of %v, presence heard %v and the ties are %v; want 300 and nothing else", r.claims, r.presence, ties.merged(nil))
				}
			},
		},
		{
			name: "a message that isn't a hook call goes straight to the journal",
			calls: func() []inbox.Delivery {
				d := socket("UserPromptSubmit", s1)
				d.Kind = "test"
				return []inbox.Delivery{d}
			},
			check: func(t *testing.T, r *recorder, _ *hookTies) {
				if len(r.presence) != 0 || len(r.claims) != 0 || r.stored[0].EventVia != "" || len(r.stored[0].Walk) != 4 {
					t.Errorf("presence heard %v, the scan %v, and the journal got %+v", r.presence, r.claims, r.stored[0])
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &recorder{secret: secret}
			if tt.noSecret {
				r.secret = nil
			}
			ties := newHookTies()
			sink := newHookSink(r, platform.Linux, ties, r.steps())
			calls := tt.calls()
			for _, d := range calls {
				if err := sink.Deliver(d); err != nil {
					t.Fatal(err)
				}
			}
			if len(r.stored) != len(calls) {
				t.Fatalf("the journal got %d calls, want %d", len(r.stored), len(calls))
			}
			tt.check(t, r, ties)
		})
	}
}

func TestHookTies(t *testing.T) {
	a := platform.ID{PID: 300, Start: 1}
	b := platform.ID{PID: 310, Start: 2}
	scanned := platform.ID{PID: 320, Start: 3}
	tests := []struct {
		name  string
		check func(t *testing.T, ties *hookTies)
	}{
		{"focus gets the scan's sessions and the hook path's together, and a tie wins over a report of one process", func(t *testing.T, ties *hookTies) {
			ties.tie(a, "s1")
			got := ties.merged(map[platform.ID]string{a: "p_scan", scanned: "p_other"})
			want := map[platform.ID]string{a: "s1", scanned: "p_other"}
			if !maps.Equal(got, want) {
				t.Errorf("merged = %v, want %v", got, want)
			}
		}},
		{"a tie is news once", func(t *testing.T, ties *hookTies) {
			if !ties.tie(a, "s1") || ties.tie(a, "s1") || !ties.tie(a, "s2") {
				t.Error("tie reported news wrongly")
			}
		}},
		{"a session gets a project only while a process runs it, a project is news once, and a dropped one is forgotten", func(t *testing.T, ties *hookTies) {
			if ties.setProject("s1", "g1") || ties.projectOf("s1") != "" {
				t.Error("a session no process runs got a project")
			}
			ties.tie(a, "s1")
			if !ties.setProject("s1", "g1") || ties.setProject("s1", "g1") || !ties.setProject("s1", "g2") {
				t.Error("setProject reported news wrongly")
			}
			if got := ties.projectOf("s1"); got != "g2" {
				t.Errorf("project %q, want the newest, g2", got)
			}
			if !ties.dropProject("s1") || ties.dropProject("s1") || ties.projectOf("s1") != "" {
				t.Error("dropProject didn't forget the project once")
			}
		}},
		{"a session's end unties only that session from the process", func(t *testing.T, ties *hookTies) {
			ties.tie(a, "s2")
			ties.end(a, "s1")
			if got := ties.merged(nil); got[a] != "s2" {
				t.Errorf("after the end of another session, the process runs %q, want s2", got[a])
			}
			ties.end(a, "s2")
			if got := ties.merged(nil); len(got) != 0 {
				t.Errorf("after its session's end, the ties are %v", got)
			}
		}},
		{"prune forgets the processes the scan no longer has claimed, and the projects of their sessions", func(t *testing.T, ties *hookTies) {
			ties.tie(a, "s1")
			ties.tie(b, "s2")
			ties.setProject("s1", "g1")
			ties.setProject("s2", "g2")
			ties.prune(func(id platform.ID) bool { return id == b })
			if got := ties.merged(nil); !maps.Equal(got, map[platform.ID]string{b: "s2"}) {
				t.Errorf("after prune the ties are %v, want only 310's", got)
			}
			if ties.projectOf("s1") != "" || ties.projectOf("s2") != "g2" {
				t.Errorf("projects %q and %q, want none for s1 and g2 for s2", ties.projectOf("s1"), ties.projectOf("s2"))
			}
		}},
		{"a session's project stays while another of its processes runs it", func(t *testing.T, ties *hookTies) {
			ties.tie(a, "s1")
			ties.tie(b, "s1")
			ties.setProject("s1", "g1")
			ties.prune(func(id platform.ID) bool { return id == b })
			if got := ties.projectOf("s1"); got != "g1" {
				t.Errorf("project %q, want g1", got)
			}
		}},
		{"a nil table holds no ties and changes nothing", func(t *testing.T, _ *hookTies) {
			var none *hookTies
			scan := map[platform.ID]string{scanned: "p_other"}
			none.tie(a, "s1")
			none.end(a, "s1")
			none.prune(func(platform.ID) bool { return false })
			if got := none.merged(scan); !maps.Equal(got, scan) || none.projectOf("s1") != "" {
				t.Errorf("merged = %v, projectOf = %q", got, none.projectOf("s1"))
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.check(t, newHookTies()) })
	}
}

// TestHookedFocus checks the hook path's part of the focus wiring (WP-AG3,
// WP-OS2) with the real focus component, presence, process scan and ledger
// over a process table the test supplies: a hook call over the socket from
// a claude under the terminal in front makes the terminal host the
// session, under the session_key hookpath.SessionKey gives with this
// installation's K_session; the session's end, or its process's exit,
// leaves the terminal hosting none; and a spooled call of the same session
// ties nothing.
func TestHookedFocus(t *testing.T) {
	store, problem, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil || problem != "" {
		t.Fatalf("state.Open: %v %q", err, problem)
	}
	dirs := home.Dirs{Data: t.TempDir(), Config: t.TempDir()}
	dirs.Runtime = dirs.Data
	a := &agent{
		dirs:      dirs,
		log:       slog.New(slog.DiscardHandler),
		sched:     sched.New(),
		state:     store,
		problems:  make(map[string]string),
		keyStores: []keys.Store{}, // K_dev goes in a key file, off the OS's store
		hooked:    newHookTies(),
	}
	var stops []func()
	var stopOnce sync.Once
	stopAll := func() {
		stopOnce.Do(func() {
			for i := len(stops) - 1; i >= 0; i-- {
				stops[i]()
			}
		})
	}
	t.Cleanup(stopAll)
	stops = append(stops, a.startLedger(), a.startPresence(presence.OS{}))
	deadline := time.Now().Add(20 * time.Second)
	for a.sessionSecret() == nil {
		if time.Now().After(deadline) {
			t.Fatal("the agent had no K_session 20 s after its keystore started opening")
		}
		time.Sleep(10 * time.Millisecond)
	}

	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	const me = "session:1"
	desk := []platform.Proc{
		{PID: 4, Name: "System", Start: now.Add(-time.Hour)},
		{PID: 4520, PPID: 4480, Name: "explorer", Start: now.Add(-50 * time.Minute), Owner: me},
		{PID: 6100, PPID: 4520, Name: "WindowsTerminal", Start: now.Add(-10 * time.Minute), Owner: me},
		{PID: 6200, PPID: 6100, Name: "pwsh", Start: now.Add(-9 * time.Minute), Owner: me},
	}
	claude := platform.Proc{PID: 6210, PPID: 6200, Name: "claude", Start: now.Add(-time.Minute), Owner: me}
	hook := platform.Proc{PID: 6300, PPID: 6210, Name: "pairtime", Start: now, Owner: me}
	var mu sync.Mutex
	table := append(slices.Clone(desk), claude)
	read := func() ([]platform.Proc, error) {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(table), nil
	}
	setTable := func(procs []platform.Proc) {
		mu.Lock()
		defer mu.Unlock()
		table = procs
	}
	a.procs = procs.New(a.procsOptions(procs.Options{Read: read, OS: platform.Windows, Self: me, Boot: "test-boot", Now: func() time.Time { return now }}))
	var front func(focus.Change)
	a.newFocus(focus.OS{
		Via:  "fake",
		GOOS: platform.Windows,
		Read: read,
		Watch: func(fn func(focus.Change)) (func(), error) {
			front = fn
			return func() {}, nil
		},
	})
	stops = append(stops, a.startFocus())
	sink := newHookSink(&recorder{}, platform.Windows, a.hooked, a.hookSteps())
	self := inbox.Proc{PID: hook.PID, Name: hook.Name, Start: hook.Start}
	call := func(name string) inbox.Delivery {
		return inbox.Delivery{Message: hookMessage(t, name, `{"session_id":"stub-session"}`, self, now), Via: inbox.ViaSocket, PeerPID: hook.PID,
			Walk: []platform.Proc{hook, claude, desk[3], desk[2], desk[1]}}
	}
	deliver := func(t *testing.T, d inbox.Delivery) {
		t.Helper()
		if err := sink.Deliver(d); err != nil {
			t.Fatal(err)
		}
	}

	steps := []struct {
		name   string
		do     func(t *testing.T)
		hosted int
	}{
		{"focus on the terminal, with a claude no hook has spoken for yet, hosts nothing the scan hasn't reported", func(*testing.T) {
			front(focus.Change{At: now, PID: 6100})
		}, 0},
		{"a spooled call of the claude's session ties nothing", func(t *testing.T) {
			d := call("UserPromptSubmit")
			d.Via, d.Walk = inbox.ViaInbox, nil
			deliver(t, d)
			a.refreshFocus()
		}, 0},
		{"a hook call over the socket from under the claude makes the terminal host its session", func(t *testing.T) {
			deliver(t, call("UserPromptSubmit"))
		}, 1},
		{"the session's end leaves the terminal hosting none", func(t *testing.T) {
			deliver(t, call("SessionEnd"))
		}, 0},
		{"a hook of the session's next start hosts it again", func(t *testing.T) {
			deliver(t, call("SessionStart"))
		}, 1},
		{"the claude's exit, once the scan sees it, leaves the terminal hosting none", func(t *testing.T) {
			setTable(slices.Clone(desk))
			if _, err := a.procs.Scan(); err != nil {
				t.Fatal(err)
			}
		}, 0},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			step.do(t)
			deadline := time.Now().Add(5 * time.Second)
			for {
				st := a.focus.Status()
				if st.Class == event.AppTerminal && st.Hosted == step.hosted {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("focus status %+v, want a terminal hosting %d", st, step.hosted)
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}

	stopAll()
	var hosted []string
	if err := ledger.ReadBatches(dirs.Ledger(), 1, func(b ledger.Batch) error {
		for _, r := range b.Records {
			body, err := pkgledger.ParseBody(r.Body)
			if err != nil {
				return err
			}
			ev, err := body.Event()
			if err != nil {
				return err
			}
			if d, ok := ev.Data.(*event.OSFocus); ok {
				hosted = append(hosted, d.HostedSessions...)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("reading the ledger back: %v", err)
	}
	var kDev []byte
	ks, err := keys.Open(keys.Options{Dir: dirs.Data, Stores: []keys.Store{}})
	if err == nil {
		err = ks.WithDeviceKey(func(k []byte) error {
			kDev = bytes.Clone(k)
			return nil
		})
	}
	if err != nil {
		t.Fatalf("reading this installation's K_dev back: %v", err)
	}
	secret, err := hookpath.SessionSecret(kDev)
	if err != nil {
		t.Fatal(err)
	}
	want := hookpath.SessionKey(secret, "stub-session")
	t.Run("the os.focus records list the session under the key its events carry, keyed with this installation's K_session", func(t *testing.T) {
		if len(hosted) != 2 || hosted[0] != want || hosted[1] != want {
			t.Errorf("hosted_sessions across the records %v, want %s twice", hosted, want)
		}
	})
}

// TestListenAgain checks that an agent whose socket can't listen tries
// again at every drain (startIPC): a regular file where the runtime
// directory goes stops the socket, and once it is gone the next drain
// listens, and a hook call reaches the agent over the socket.
func TestListenAgain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("on Windows a socket that can't listen falls back to the named pipe (ipc.Listen)")
	}
	// Short, for the socket's name: sun_path is 104 bytes on macOS.
	dir, err := os.MkdirTemp("", "pt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	data := filepath.Join(dir, "data")
	dirs := home.Dirs{Data: data, Config: data, Runtime: filepath.Join(dir, "run")}
	if err := os.WriteFile(dirs.Runtime, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("agent log:\n%s", logs.String())
		}
	})
	r := startAgent(t, Options{Dirs: dirs, Log: slog.New(slog.NewTextHandler(logs, nil)), Ports: []int{0}, KeyStores: []keys.Store{},
		DrainEvery: 100 * time.Millisecond})

	steps := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"with a file where the runtime directory goes, the agent has no listener and says so", func(t *testing.T) {
			waitStatus(t, r, func(s statusReply) bool {
				return s.IPC.Via == "" && strings.Contains(s.Problems["ipc"], "can't listen")
			})
		}},
		{"once the file is gone, a drain listens again on the socket", func(t *testing.T) {
			if err := os.Remove(dirs.Runtime); err != nil {
				t.Fatal(err)
			}
			waitStatus(t, r, func(s statusReply) bool { return s.IPC.Via == inbox.ViaSocket && s.Problems["ipc"] == "" })
		}},
		{"a hook call then reaches the agent over the socket, and is journaled as come by it", func(t *testing.T) {
			ep, err := ipc.EndpointFor(dirs)
			if err != nil {
				t.Fatal(err)
			}
			res := hookpath.Send(hookpath.Call{Harness: "claude_code", Event: "UserPromptSubmit", Stdin: strings.NewReader(`{"session_id":"s1"}`)},
				hookpath.Options{Endpoint: ep, Timeout: 5 * time.Second})
			if res.Via != inbox.ViaSocket || res.Err != nil {
				t.Fatalf("Send = %+v, want the socket", res)
			}
			files, err := filepath.Glob(filepath.Join(dirs.Data, "received", "*.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, f := range files {
				b, err := os.ReadFile(f)
				if err != nil {
					t.Fatal(err)
				}
				for line := range strings.SplitSeq(string(b), "\n") {
					var l struct {
						ID  string    `json:"id"`
						Via inbox.Via `json:"via"`
					}
					if json.Unmarshal([]byte(line), &l) == nil && l.ID == res.ID && l.Via == inbox.ViaSocket {
						found = true
					}
				}
			}
			if !found {
				t.Errorf("the journal in %v has no line for %s via the socket", files, res.ID)
			}
		}},
	}
	for _, step := range steps {
		if !t.Run(step.name, step.run) {
			break // each step builds on the one before
		}
	}
}
