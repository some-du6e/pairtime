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
)

var testStamp = Stamp{Wall: time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC), Mono: 81163000, BootID: "b-51c2"}

func fixedStamp() Stamp { return testStamp }

// testChain is a hook under Claude Code in a terminal, as the hook would
// read its own.
var testChain = []platform.Proc{
	{PID: 300, PPID: 200, Name: "pairtime", Start: testStamp.Wall},
	{PID: 200, PPID: 100, Name: "claude", Start: testStamp.Wall.Add(-time.Hour)},
	{PID: 100, PPID: 1, Name: "kitty", Start: testStamp.Wall.Add(-2 * time.Hour)},
}

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

// checkMessage checks that m is the hook call made with stdin `{"session_id":"s1"}`.
func checkMessage(t *testing.T, m inbox.Message, id string) {
	t.Helper()
	if m.ID != id || m.Kind != Kind {
		t.Errorf("message %s of kind %q, want %s of kind %q", m.ID, m.Kind, id, Kind)
	}
	if !m.Sent.Equal(testStamp.Wall) || m.Mono != testStamp.Mono || m.BootID != testStamp.BootID {
		t.Errorf("stamped %v, %d, %q, want the hook's own %v, %d, %q", m.Sent, m.Mono, m.BootID, testStamp.Wall, testStamp.Mono, testStamp.BootID)
	}
	if len(m.Chain) != len(testChain) || m.Chain[1].PID != 200 || m.Chain[1].Name != "claude" || !m.Chain[1].Start.Equal(testChain[1].Start) {
		t.Errorf("the message's chain is %+v, want the hook's own", m.Chain)
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
	opts := func(ep ipc.Endpoint) Options {
		return Options{Endpoint: ep, Now: fixedStamp, Chain: func() ([]platform.Proc, error) { return testChain, nil }}
	}
	tests := []struct {
		name string
		run  func(t *testing.T, ep ipc.Endpoint)
	}{
		{
			name: "with no agent listening the call is spooled once, and the agent is to be started",
			run: func(t *testing.T, ep ipc.Endpoint) {
				res := Send(call(), opts(ep))
				if res.Via != inbox.ViaInbox || !res.StartAgent {
					t.Fatalf("Send = %+v, want the spool and a start", res)
				}
				got := spooled(t, ep)
				if len(got) != 1 {
					t.Fatalf("the spool has %d messages, want 1", len(got))
				}
				checkMessage(t, got[0], res.ID)
			},
		},
		{
			name: "an agent that acks takes the call over the socket, and nothing is spooled",
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
				checkMessage(t, got[0].Message, res.ID)
				if n := len(spooled(t, ep)); n != 0 {
					t.Errorf("the spool has %d messages, want none", n)
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
				checkMessage(t, got[0], res.ID)
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
				for i := range long {
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
			name: "a chain that can't be read is left out, and the call still goes",
			run: func(t *testing.T, ep ipc.Endpoint) {
				o := opts(ep)
				o.Chain = func() ([]platform.Proc, error) { return nil, errors.New("no /proc") }
				res := Send(call(), o)
				got := spooled(t, ep)
				if res.Via != inbox.ViaInbox || len(got) != 1 || got[0].Chain != nil {
					t.Fatalf("Send = %+v and the spool has %+v, want one message with no chain", res, got)
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

func TestNow(t *testing.T) {
	switch runtime.GOOS {
	case platform.Linux, platform.Darwin, platform.Windows:
	default:
		t.Skipf("pairtime reads no monotonic clock on %s", runtime.GOOS)
	}
	tests := []struct {
		name  string
		check func(t *testing.T, a, b Stamp)
	}{
		{"every stamp has a boot id the inbox takes, and a monotonic reading", func(t *testing.T, a, b Stamp) {
			for _, s := range []Stamp{a, b} {
				if s.Mono <= 0 || !validBootID(s.BootID) {
					t.Errorf("stamp %+v has no monotonic reading or no boot id", s)
				}
				m := inbox.Message{ID: inbox.NewID(), Kind: Kind, Sent: s.Wall, Mono: s.Mono, BootID: s.BootID}
				if err := m.Check(); err != nil {
					t.Errorf("the inbox doesn't take the stamp: %v", err)
				}
			}
		}},
		{"the boot id is the same from one stamp to the next", func(t *testing.T, a, b Stamp) {
			if a.BootID != b.BootID {
				t.Errorf("boot ids %q and %q, want one boot", a.BootID, b.BootID)
			}
		}},
		{"the monotonic clock moves on with the wall clock", func(t *testing.T, a, b Stamp) {
			mono := time.Duration(b.Mono-a.Mono) * time.Millisecond
			wall := b.Wall.Sub(a.Wall)
			if mono <= 0 || mono-wall > time.Second || wall-mono > time.Second {
				t.Errorf("the monotonic clock moved %v while the wall clock moved %v", mono, wall)
			}
		}},
		{"the wall time is UTC with no Go monotonic reading", func(t *testing.T, a, _ Stamp) {
			if a.Wall.Location() != time.UTC || strings.Contains(a.Wall.String(), "m=") {
				t.Errorf("wall time %s isn't plain UTC", a.Wall)
			}
		}},
	}
	a := Now()
	time.Sleep(100 * time.Millisecond)
	b := Now()
	t.Logf("stamps %+v and %+v", a, b)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.check(t, a, b) })
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
	}
	chain, err := ReadChain(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.check(t, chain) })
	}
}
