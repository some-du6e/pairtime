// SPDX-License-Identifier: AGPL-3.0-or-later

package inbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform"
)

func goodMessage() Message {
	return Message{
		ID:     "0123456789abcdef0123456789abcdef",
		Kind:   "hook",
		Sent:   time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC),
		Mono:   81163000,
		BootID: "6f0f6f6e-2b1c-4c5e-9d07-0e3c1b2a4f55",
		Chain: []Proc{
			{PID: 4242, Name: "pairtime", Start: time.Date(2026, 11, 19, 11, 59, 59, 0, time.UTC), Ticks: 8116290},
			{PID: 4100, Name: "claude", Start: time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)},
			{PID: 1},
		},
		Body: json.RawMessage("{\"prompt\": \"line one\\nline two\",\n \"n\": 1}"),
	}
}

func TestProcID(t *testing.T) {
	start := time.Date(2026, 11, 19, 11, 59, 59, 0, time.UTC)
	tests := []struct {
		name string
		p    Proc
		want platform.ID
	}{
		{"the ticks a Linux sender read name the process", Proc{PID: 7, Start: start, Ticks: 8116290}, platform.ID{PID: 7, Start: 8116290}},
		{"without ticks the start does, in Unix nanoseconds", Proc{PID: 7, Start: start}, platform.ID{PID: 7, Start: start.UnixNano()}},
		{"with neither only the pid is known", Proc{PID: 7}, platform.ID{PID: 7}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.ID(); got != tt.want {
				t.Errorf("ID = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// sameProc reports whether a and b are the same entry of a parent chain.
func sameProc(a, b Proc) bool {
	return a.PID == b.PID && a.Name == b.Name && a.Start.Equal(b.Start) && a.Ticks == b.Ticks
}

// chainOf returns a parent chain of n processes.
func chainOf(n int) []Proc {
	chain := make([]Proc, n)
	for i := range chain {
		chain[i] = Proc{PID: 1000 + i, Name: "sh"}
	}
	return chain
}

func TestMessageCheck(t *testing.T) {
	tests := []struct {
		name    string
		edit    func(m *Message)
		wantErr bool
	}{
		{name: "a message with a hex id, a kind, its sender's clock and parent chain and a JSON body is taken", edit: func(*Message) {}},
		{name: "a message without a body is taken", edit: func(m *Message) { m.Body = nil }},
		{name: "a message without its sender's monotonic clock or parent chain is taken", edit: func(m *Message) { m.Mono, m.BootID, m.Chain = 0, "", nil }},
		{name: "a negative monotonic reading is refused", edit: func(m *Message) { m.Mono = -1 }, wantErr: true},
		{name: "a monotonic reading without its boot id is refused", edit: func(m *Message) { m.BootID = "" }, wantErr: true},
		{name: "a boot id without a monotonic reading is refused", edit: func(m *Message) { m.Mono = 0 }, wantErr: true},
		{name: "a boot id with a character outside a-z A-Z 0-9 _ - is refused", edit: func(m *Message) { m.BootID = "boot 7" }, wantErr: true},
		{name: "a boot id over 128 characters is refused", edit: func(m *Message) { m.BootID = strings.Repeat("b", 129) }, wantErr: true},
		{name: "a parent chain of MaxChain processes is taken", edit: func(m *Message) { m.Chain = chainOf(MaxChain) }},
		{name: "a parent chain longer than MaxChain is refused", edit: func(m *Message) { m.Chain = chainOf(MaxChain + 1) }, wantErr: true},
		{name: "a process in the parent chain without a pid is refused", edit: func(m *Message) { m.Chain[1].PID = 0 }, wantErr: true},
		{name: "a kind may have dots, colons, dashes and underscores", edit: func(m *Message) { m.Kind = "plugin:my-tool_2.event" }},
		{name: "an id shorter than 32 digits is refused", edit: func(m *Message) { m.ID = "0123456789abcdef" }, wantErr: true},
		{name: "an id with capital hex digits is refused", edit: func(m *Message) { m.ID = strings.ToUpper(m.ID) }, wantErr: true},
		{name: "an id with a letter past f is refused", edit: func(m *Message) { m.ID = "g123456789abcdef0123456789abcdef" }, wantErr: true},
		{name: "an empty kind is refused", edit: func(m *Message) { m.Kind = "" }, wantErr: true},
		{name: "a kind with a capital letter is refused", edit: func(m *Message) { m.Kind = "Hook" }, wantErr: true},
		{name: "a kind with a slash is refused", edit: func(m *Message) { m.Kind = "hook/claude" }, wantErr: true},
		{name: "a kind over 64 characters is refused", edit: func(m *Message) { m.Kind = strings.Repeat("k", 65) }, wantErr: true},
		{name: "a message without the time it was sent is refused", edit: func(m *Message) { m.Sent = time.Time{} }, wantErr: true},
		{name: "a body that isn't JSON is refused", edit: func(m *Message) { m.Body = json.RawMessage(`{"prompt":`) }, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := goodMessage()
			tt.edit(&m)
			if err := m.Check(); (err != nil) != tt.wantErr {
				t.Errorf("Check() = %v, want an error: %v", err, tt.wantErr)
			}
		})
	}
}

func TestMessageEncoding(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "Decode gives back what Encode wrote",
			run: func(t *testing.T) {
				m := goodMessage()
				b, err := m.Encode()
				if err != nil {
					t.Fatal(err)
				}
				got, err := Decode(b)
				if err != nil {
					t.Fatal(err)
				}
				var want bytes.Buffer
				if err := json.Compact(&want, m.Body); err != nil {
					t.Fatal(err)
				}
				if got.ID != m.ID || got.Kind != m.Kind || !got.Sent.Equal(m.Sent) || got.Mono != m.Mono || got.BootID != m.BootID ||
					!slices.EqualFunc(got.Chain, m.Chain, sameProc) || !bytes.Equal(got.Body, want.Bytes()) {
					t.Errorf("Decode(Encode(m)) = %+v, want %+v", got, m)
				}
			},
		},
		{
			name: "an encoded message has no newline, even when its body had one",
			run: func(t *testing.T) {
				b, err := goodMessage().Encode()
				if err != nil {
					t.Fatal(err)
				}
				if bytes.ContainsAny(b, "\r\n") {
					t.Errorf("Encode() = %s, which has a line break", b)
				}
			},
		},
		{
			name: "a message over MaxMessage is refused",
			run: func(t *testing.T) {
				m := goodMessage()
				m.Body = json.RawMessage(`"` + strings.Repeat("a", MaxMessage) + `"`)
				if _, err := m.Encode(); !errors.Is(err, ErrTooBig) {
					t.Errorf("Encode() = %v, want ErrTooBig", err)
				}
			},
		},
		{
			name: "Decode refuses a message that Check refuses",
			run: func(t *testing.T) {
				if _, err := Decode([]byte(`{"id":"nope","kind":"hook","sent":"2026-11-19T12:00:00Z"}`)); err == nil {
					t.Error("Decode took a message with a bad id")
				}
			},
		},
		{
			name: "Decode ignores fields it doesn't know",
			run: func(t *testing.T) {
				b := []byte(`{"id":"0123456789abcdef0123456789abcdef","kind":"hook","sent":"2026-11-19T12:00:00Z","later":{"x":1}}`)
				if _, err := Decode(b); err != nil {
					t.Errorf("Decode() = %v, want nil", err)
				}
			},
		},
		{
			name: "NewID makes distinct ids that Check takes",
			run: func(t *testing.T) {
				seen := make(map[string]bool)
				for range 1000 {
					id := NewID()
					if !validID(id) || seen[id] {
						t.Fatalf("NewID() = %q, repeated or not 32 lowercase hex digits", id)
					}
					seen[id] = true
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}
