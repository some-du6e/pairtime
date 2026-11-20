// SPDX-License-Identifier: AGPL-3.0-or-later

package inbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func goodMessage() Message {
	return Message{
		ID:   "0123456789abcdef0123456789abcdef",
		Kind: "hook",
		Sent: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC),
		Body: json.RawMessage("{\"prompt\": \"line one\\nline two\",\n \"n\": 1}"),
	}
}

func TestMessageCheck(t *testing.T) {
	tests := []struct {
		name    string
		edit    func(m *Message)
		wantErr bool
	}{
		{name: "a message with a hex id, a kind, a time and a JSON body is taken", edit: func(*Message) {}},
		{name: "a message without a body is taken", edit: func(m *Message) { m.Body = nil }},
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
				if got.ID != m.ID || got.Kind != m.Kind || !got.Sent.Equal(m.Sent) || !bytes.Equal(got.Body, want.Bytes()) {
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
