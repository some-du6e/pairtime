// SPDX-License-Identifier: AGPL-3.0-or-later

package hookpath

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/clock"
)

var testStamp = clock.Stamp{Wall: time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC), Mono: 81163000, BootID: "b-51c2"}

func fixedStamp() clock.Stamp { return testStamp }

// testChain is a hook under Claude Code in a terminal, as the hook would
// read its own.
var testChain = []platform.Proc{
	{PID: 300, PPID: 200, Name: "pairtime", Start: testStamp.Wall, Ticks: 8116290},
	{PID: 200, PPID: 100, Name: "claude", Start: testStamp.Wall.Add(-time.Hour)},
	{PID: 100, PPID: 1, Name: "kitty", Start: testStamp.Wall.Add(-2 * time.Hour)},
}

// testSelf reads the hook's own process, as testChain has it.
func testSelf() (platform.Proc, error) { return testChain[0], nil }

// panicReader is a reader of stdin, or of the process table, with a bug.
type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("a reader with a bug") }

// shortDir makes a directory whose path leaves room for a socket name,
// which t.TempDir's can't on macOS (sun_path is 104 bytes there).
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func endpointIn(t *testing.T, dir string) ipc.Endpoint {
	t.Helper()
	ep, err := ipc.EndpointFor(home.Dirs{Data: dir, Config: dir, Runtime: dir})
	if err != nil {
		t.Fatal(err)
	}
	return ep
}

// sink keeps what an agent's server delivers. Until hold is released, it
// blocks every delivery, as a stuck agent would.
type sink struct {
	mu   sync.Mutex
	got  []inbox.Delivery
	hold chan struct{} // nil: deliver at once
}

func (s *sink) Deliver(d inbox.Delivery) error {
	if s.hold != nil {
		<-s.hold
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, d)
	return nil
}

func (s *sink) deliveries() []inbox.Delivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]inbox.Delivery(nil), s.got...)
}

// serve runs an agent's end of ep, delivering into s, until the test ends.
func serve(t *testing.T, ep ipc.Endpoint, s *sink) {
	t.Helper()
	srv, err := ipc.Listen(ep, ipc.Options{})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(s) }()
	t.Cleanup(func() {
		srv.Close()
		<-done
	})
}

// spooled returns the messages in ep's spool.
func spooled(t *testing.T, ep ipc.Endpoint) []inbox.Message {
	t.Helper()
	f, err := os.Open(filepath.Join(ep.Inbox, inbox.SpoolName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []inbox.Message
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, inbox.MaxMessage+2)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		m, err := inbox.Decode(sc.Bytes())
		if err != nil {
			t.Fatalf("a spool line isn't a message: %v", err)
		}
		out = append(out, m)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// checkMessage checks that m is the hook call made with stdin
// `{"session_id":"s1"}`: its first chain entry is the hook itself, and the
// rest of the hook's chain follows when wantChain is set, as it does for a
// spooled call.
func checkMessage(t *testing.T, m inbox.Message, id string, wantChain bool) {
	t.Helper()
	if m.ID != id || m.Kind != Kind {
		t.Errorf("message %s of kind %q, want %s of kind %q", m.ID, m.Kind, id, Kind)
	}
	if !m.Sent.Equal(testStamp.Wall) || m.Mono != testStamp.Mono || m.BootID != testStamp.BootID {
		t.Errorf("stamped %v, %d, %q, want the hook's own %v, %d, %q", m.Sent, m.Mono, m.BootID, testStamp.Wall, testStamp.Mono, testStamp.BootID)
	}
	if len(m.Chain) == 0 || m.Chain[0].PID != 300 || !m.Chain[0].Start.Equal(testChain[0].Start) || m.Chain[0].Ticks != testChain[0].Ticks {
		t.Fatalf("the message's chain is %+v, want the hook's own pid, start and ticks first", m.Chain)
	}
	switch {
	case !wantChain && len(m.Chain) != 1:
		t.Errorf("the message carries the chain %+v, want the hook's own entry alone: the agent walks the chain of a call over the socket itself", m.Chain)
	case wantChain && (len(m.Chain) != len(testChain) || m.Chain[1].PID != 200 || m.Chain[1].Name != "claude" || !m.Chain[1].Start.Equal(testChain[1].Start)):
		t.Errorf("the spooled message's chain is %+v, want the hook's own", m.Chain)
	}
	b, err := DecodeBody(m)
	if err != nil {
		t.Fatal(err)
	}
	if b.Harness != "claude_code" || b.Event != "UserPromptSubmit" || string(b.Payload) != `{"session_id":"s1"}` || b.Bytes != 19 || b.Cut {
		t.Errorf("the body is %+v", b)
	}
}

func TestSend(t *testing.T) {
	call := func() Call {
		return Call{Harness: "claude_code", Event: "UserPromptSubmit", Stdin: strings.NewReader(`{"session_id":"s1"}`)}
	}
	// reads counts the reads of the hook's own chain, and prepared the
	// calls to Prepare.
	var reads, prepared int
	opts := func(ep ipc.Endpoint) Options {
		reads, prepared = 0, 0
		return Options{Endpoint: ep, Now: fixedStamp, Self: testSelf,
			Chain:   func() ([]platform.Proc, error) { reads++; return testChain, nil },
			Prepare: func() error { prepared++; return nil }}
	}
	tests := []struct {
		name string
		run  func(t *testing.T, ep ipc.Endpoint)
	}{
		{
			name: "with no agent listening the call is spooled once, after Prepare, and the agent is to be started",
			run: func(t *testing.T, ep ipc.Endpoint) {
				res := Send(call(), opts(ep))
				if res.Via != inbox.ViaInbox || !res.StartAgent {
					t.Fatalf("Send = %+v, want the spool and a start", res)
				}
				got := spooled(t, ep)
				if len(got) != 1 {
					t.Fatalf("the spool has %d messages, want 1", len(got))
				}
				checkMessage(t, got[0], res.ID, true)
				if prepared != 1 {
					t.Errorf("Prepare ran %d times, want once before the spool", prepared)
				}
			},
		},
		{
			name: "with no agent listening, a spool whose first call was sent 8 days ago is deleted unread before the call is spooled, so the call starts a fresh spool (D-inbox-age-cap)",
			run: func(t *testing.T, ep ipc.Endpoint) {
				old := inbox.New("test", json.RawMessage(`{}`))
				old.Sent = time.Now().Add(-8 * 24 * time.Hour).UTC()
				b, err := old.Encode()
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(ep.Inbox, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(ep.Inbox, inbox.SpoolName), append(append([]byte{'\n'}, b...), '\n'), 0o600); err != nil {
					t.Fatal(err)
				}
				res := Send(call(), opts(ep))
				got := spooled(t, ep)
				if res.Via != inbox.ViaInbox || len(got) != 1 || got[0].ID != res.ID {
					t.Fatalf("Send = %+v and the spool has %+v, want the hook's call alone", res, got)
				}
			},
		},
		{
			name: "a call Prepare refuses isn't spooled, and says why",
			run: func(t *testing.T, ep ipc.Endpoint) {
				o := opts(ep)
				o.Prepare = func() error { return errors.New("another user owns the data directory") }
				res := Send(call(), o)
				if res.Via != "" || res.Err == nil || !strings.Contains(res.Err.Error(), "another user") {
					t.Errorf("Send = %+v, want a lost call with Prepare's error", res)
				}
				if n := len(spooled(t, ep)); n != 0 {
					t.Errorf("the spool has %d messages, want none", n)
				}
			},
		},
		{
			name: "an agent that acks takes the call over the socket, with only the hook's own entry in its chain, and nothing is spooled or prepared",
			run: func(t *testing.T, ep ipc.Endpoint) {
				s := &sink{}
				serve(t, ep, s)
				res := Send(call(), opts(ep))
				if res.Via == inbox.ViaInbox || res.Via == "" || res.StartAgent || res.Err != nil {
					t.Fatalf("Send = %+v, want the agent's ack", res)
				}
				got := s.deliveries()
				if len(got) != 1 {
					t.Fatalf("the agent has %d messages, want 1", len(got))
				}
				checkMessage(t, got[0].Message, res.ID, false)
				if n := len(spooled(t, ep)); n != 0 {
					t.Errorf("the spool has %d messages, want none", n)
				}
				if reads != 0 || prepared != 0 {
					t.Errorf("the hook read its own chain %d times and prepared the spool %d times, want neither on the way to a running agent", reads, prepared)
				}
			},
		},
		{
			name: "an agent that doesn't ack in time gets the call through the spool, and isn't started again",
			run: func(t *testing.T, ep ipc.Endpoint) {
				s := &sink{hold: make(chan struct{})}
				serve(t, ep, s)
				t.Cleanup(func() { close(s.hold) }) // before serve's cleanup closes the server
				o := opts(ep)
				o.Timeout = 50 * time.Millisecond
				res := Send(call(), o)
				if res.Via != inbox.ViaInbox || res.StartAgent {
					t.Fatalf("Send = %+v, want the spool and no start", res)
				}
				got := spooled(t, ep)
				if len(got) != 1 {
					t.Fatalf("the spool has %d messages, want 1", len(got))
				}
				checkMessage(t, got[0], res.ID, true)
			},
		},
		{
			name: "a call that even the spool can't take is lost, and says why",
			run: func(t *testing.T, ep ipc.Endpoint) {
				file := filepath.Join(ep.Inbox, "..", "not-a-dir")
				if err := os.WriteFile(file, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				ep.Inbox = filepath.Join(file, "inbox")
				res := Send(call(), opts(ep))
				if res.Via != "" || res.Err == nil {
					t.Errorf("Send = %+v, want a lost call with its error", res)
				}
			},
		},
		{
			name: "the hook's own chain goes in the message at most inbox.MaxChain long",
			run: func(t *testing.T, ep ipc.Endpoint) {
				long := make([]platform.Proc, 100)
				long[0] = testChain[0]
				for i := 1; i < len(long); i++ {
					long[i] = platform.Proc{PID: 1000 - i, PPID: 999 - i, Name: "sh"}
				}
				o := opts(ep)
				o.Chain = func() ([]platform.Proc, error) { return long, nil }
				Send(call(), o)
				got := spooled(t, ep)
				if len(got) != 1 {
					t.Fatalf("the spool has %d messages, want 1", len(got))
				}
				if n := len(got[0].Chain); n != inbox.MaxChain {
					t.Errorf("the message's chain holds %d processes, want %d", n, inbox.MaxChain)
				}
			},
		},
		{
			name: "a chain that can't be read leaves the hook's own entry alone, and the call still goes",
			run: func(t *testing.T, ep ipc.Endpoint) {
				o := opts(ep)
				o.Chain = func() ([]platform.Proc, error) { return nil, errors.New("no /proc") }
				res := Send(call(), o)
				got := spooled(t, ep)
				if res.Via != inbox.ViaInbox || len(got) != 1 {
					t.Fatalf("Send = %+v and the spool has %+v, want one message", res, got)
				}
				checkMessage(t, got[0], res.ID, false)
			},
		},
		{
			name: "a reader of the chain that panics leaves the hook's own entry alone, and the call still goes",
			run: func(t *testing.T, ep ipc.Endpoint) {
				o := opts(ep)
				o.Chain = func() ([]platform.Proc, error) { panic("a reader with a bug") }
				res := Send(call(), o)
				got := spooled(t, ep)
				if res.Via != inbox.ViaInbox || len(got) != 1 {
					t.Fatalf("Send = %+v and the spool has %+v, want one message", res, got)
				}
				checkMessage(t, got[0], res.ID, false)
			},
		},
		{
			name: "a chain that starts with another process adds nothing to the hook's own entry",
			run: func(t *testing.T, ep ipc.Endpoint) {
				o := opts(ep)
				o.Chain = func() ([]platform.Proc, error) { return testChain[1:], nil }
				res := Send(call(), o)
				got := spooled(t, ep)
				if len(got) != 1 {
					t.Fatalf("the spool has %d messages, want 1", len(got))
				}
				checkMessage(t, got[0], res.ID, false)
			},
		},
		{
			name: "a hook that can't read its own process records its pid alone",
			run: func(t *testing.T, ep ipc.Endpoint) {
				o := opts(ep)
				o.Self = func() (platform.Proc, error) { return platform.Proc{}, errors.New("no /proc") }
				Send(call(), o)
				got := spooled(t, ep)
				if len(got) != 1 || len(got[0].Chain) == 0 {
					t.Fatalf("the spool has %+v, want one message with a chain", got)
				}
				if c := got[0].Chain[0]; c.PID != os.Getpid() || !c.Start.IsZero() || c.Ticks != 0 {
					t.Errorf("the first chain entry is %+v, want this process's pid %d and no start", c, os.Getpid())
				}
			},
		},
		{
			name: "a reader of its own process that panics leaves the pid alone, and the call still goes",
			run: func(t *testing.T, ep ipc.Endpoint) {
				o := opts(ep)
				o.Self = func() (platform.Proc, error) { panic("a reader with a bug") }
				res := Send(call(), o)
				got := spooled(t, ep)
				if res.Via != inbox.ViaInbox || len(got) != 1 || got[0].Chain[0].PID != os.Getpid() {
					t.Fatalf("Send = %+v and the spool has %+v, want one message from this process", res, got)
				}
			},
		},
		{
			name: "stdin whose reader panics is read as empty, and the call still goes",
			run: func(t *testing.T, ep ipc.Endpoint) {
				c := call()
				c.Stdin = panicReader{}
				res := Send(c, opts(ep))
				got := spooled(t, ep)
				if res.Via != inbox.ViaInbox || len(got) != 1 {
					t.Fatalf("Send = %+v and the spool has %+v, want one message", res, got)
				}
				if b, err := DecodeBody(got[0]); err != nil || b.Bytes != 0 || b.Payload != nil {
					t.Errorf("the body is %+v (%v), want no stdin", b, err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t, endpointIn(t, shortDir(t)))
		})
	}
}

func TestBody(t *testing.T) {
	c := Call{Harness: "claude_code", Event: "Stop"}
	tests := []struct {
		name         string
		call         Call
		data         string
		whole, cut   bool
		wantPayload  string
		wantText     string
		wantCut      bool
		wantDropped  bool
		wantHarness  string
		wantProblem  string
		wantBytesLen int
	}{
		{name: "stdin that is one JSON value goes in as the payload", call: c, data: `{"session_id":"s1"}`, whole: true,
			wantPayload: `{"session_id":"s1"}`, wantHarness: "claude_code", wantBytesLen: 19},
		{name: "a payload is stored compact", call: c, data: "{ \"a\" : 1 }\n", whole: true,
			wantPayload: `{"a":1}`, wantHarness: "claude_code", wantBytesLen: 12},
		{name: "stdin that isn't JSON goes in as text", call: c, data: `{"session_id":`,
			wantText: `{"session_id":`, wantHarness: "claude_code", wantBytesLen: 14},
		{name: "stdin cut at the limit goes in as text and says it was cut", call: c, data: `{"prompt":"aaa`, cut: true,
			wantText: `{"prompt":"aaa`, wantCut: true, wantHarness: "claude_code", wantBytesLen: 14},
		{name: "empty stdin leaves both out", call: c, wantHarness: "claude_code"},
		{name: "a harness name longer than any real one is cut", call: Call{Harness: strings.Repeat("h", 1000), Event: "Stop"},
			wantHarness: strings.Repeat("h", maxArg)},
		{name: "a problem with the arguments goes in the body", call: Call{Event: "Stop", Problem: "no --harness"},
			wantProblem: "no --harness"},
		{name: "text too big for a message is dropped, and says so", call: c, data: strings.Repeat("\x01", MaxStdin),
			wantDropped: true, wantHarness: "claude_code", wantBytesLen: MaxStdin},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := body(tt.call, []byte(tt.data), tt.whole, tt.cut)
			if len(raw) > inbox.MaxMessage-bodyRoom {
				t.Fatalf("the body is %d bytes, over the room a message has", len(raw))
			}
			b, err := DecodeBody(inbox.Message{Kind: Kind, Body: raw})
			if err != nil {
				t.Fatal(err)
			}
			if string(b.Payload) != tt.wantPayload || b.Text != tt.wantText || b.Cut != tt.wantCut || b.Dropped != tt.wantDropped {
				t.Errorf("payload %q, text %q, cut %v, dropped %v; want %q, %q, %v, %v",
					b.Payload, shorten([]byte(b.Text)), b.Cut, b.Dropped, tt.wantPayload, tt.wantText, tt.wantCut, tt.wantDropped)
			}
			if b.Harness != tt.wantHarness || b.Problem != tt.wantProblem || b.Bytes != tt.wantBytesLen {
				t.Errorf("harness %q, problem %q, bytes %d; want %q, %q, %d", shorten([]byte(b.Harness)), b.Problem, b.Bytes, shorten([]byte(tt.wantHarness)), tt.wantProblem, tt.wantBytesLen)
			}
		})
	}
}

func TestDecodeBody(t *testing.T) {
	tests := []struct {
		name string
		m    inbox.Message
	}{
		{"a message of another kind isn't a hook call", inbox.Message{Kind: "test", Body: json.RawMessage(`{}`)}},
		{"a hook call needs a body", inbox.Message{Kind: Kind}},
		{"a body that isn't a hook call's is an error", inbox.Message{Kind: Kind, Body: json.RawMessage(`{"bytes":"many"}`)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeBody(tt.m); err == nil {
				t.Error("DecodeBody returned no error")
			}
		})
	}
}

func TestReadChain(t *testing.T) {
	switch runtime.GOOS {
	case platform.Linux, platform.Darwin, platform.Windows:
	default:
		t.Skipf("pairtime can't read the process table on %s", runtime.GOOS)
	}
	tests := []struct {
		name  string
		check func(t *testing.T, chain []platform.Proc)
	}{
		{"this process's chain starts with it", func(t *testing.T, chain []platform.Proc) {
			if len(chain) == 0 || chain[0].PID != os.Getpid() {
				t.Fatalf("the chain %+v doesn't start at pid %d", chain, os.Getpid())
			}
		}},
		{"the chain goes on to the parent", func(t *testing.T, chain []platform.Proc) {
			if len(chain) < 2 || chain[1].PID != os.Getppid() {
				t.Errorf("the chain %+v doesn't go on to the parent, pid %d", chain, os.Getppid())
			}
		}},
		{"the hook's own entry has the pid and start the agent's table read gives it, so the agent's walk counts", func(t *testing.T, chain []platform.Proc) {
			me := ownEntry(nil)
			if len(chain) == 0 || me.ID() != chain[0].ID() || me.ID().Start == 0 {
				t.Errorf("the hook records itself as %+v (%+v), and the table has %+v", me, me.ID(), chain)
			}
			d := inbox.Delivery{Via: inbox.ViaSocket, Walk: chain, Message: inbox.Message{Chain: []inbox.Proc{me}}}
			if !counts(d) {
				t.Error("the agent's walk of this process doesn't count")
			}
		}},
	}
	chain, err := ReadChain(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.check(t, chain) })
	}
}
