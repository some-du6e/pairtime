// SPDX-License-Identifier: AGPL-3.0-or-later

package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/inbox"
)

// answers is an Options.Answer that answers from a map and records what it
// was asked.
type answers struct {
	mu    sync.Mutex
	by    map[string]json.RawMessage
	err   error // when set, every answer fails with it
	asked []string
}

func (a *answers) answer(q string) (json.RawMessage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asked = append(a.asked, q)
	if a.err != nil {
		return nil, a.err
	}
	ans, ok := a.by[q]
	if !ok {
		return nil, ErrUnknownQuestion
	}
	return ans, nil
}

func (a *answers) questions() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.asked...)
}

func askOne(t *testing.T, ep Endpoint, q string) (json.RawMessage, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return Ask(ctx, ep, q)
}

// oldAgent listens on ep's socket and serves one connection the way an
// agent from before questions did: it decodes every frame as a message,
// replies, and hangs up after a frame that isn't one. stored reports
// whether a frame passed as a message.
func oldAgent(t *testing.T, ep Endpoint) (stored func() bool) {
	t.Helper()
	ln, err := net.Listen("unix", ep.Socket)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	took := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(10 * time.Second))
		body, err := ReadFrame(c, inbox.MaxMessage)
		if err != nil {
			return
		}
		rep := reply{OK: true}
		if m, err := inbox.Decode(body); err != nil {
			rep = reply{Error: err.Error()}
		} else {
			mu.Lock()
			took = true
			mu.Unlock()
			rep.ID = m.ID
		}
		b, _ := json.Marshal(rep)
		WriteFrame(c, b, maxReply)
	}()
	t.Cleanup(func() {
		ln.Close() // wakes an Accept that no client reached
		<-done
	})
	return func() bool {
		mu.Lock()
		defer mu.Unlock()
		return took
	}
}

func TestAsk(t *testing.T) {
	session := json.RawMessage(`{"shadow":false,"session":null}`)
	tests := []struct {
		name string
		run  func(t *testing.T, ep Endpoint)
	}{
		{
			name: "a question gets the answer Options.Answer gives, and the sink stores nothing",
			run: func(t *testing.T, ep Endpoint) {
				a := &answers{by: map[string]json.RawMessage{AskLastSession: session}}
				_, sink := serve(t, ep, Options{Answer: a.answer}, nil)
				got, err := askOne(t, ep, AskLastSession)
				if err != nil || string(got) != string(session) {
					t.Fatalf("Ask = %s, %v; want %s", got, err, session)
				}
				if q := a.questions(); len(q) != 1 || q[0] != AskLastSession {
					t.Errorf("Answer was asked %q, want %q once", q, AskLastSession)
				}
				if d := sink.deliveries(); len(d) != 0 {
					t.Errorf("the sink stored %+v", d)
				}
			},
		},
		{
			name: "an agent without Answer answers no question, which Ask reports as ErrUnknownQuestion",
			run: func(t *testing.T, ep Endpoint) {
				_, sink := serve(t, ep, Options{}, nil)
				if _, err := askOne(t, ep, AskLastSession); !errors.Is(err, ErrUnknownQuestion) {
					t.Errorf("Ask = %v, want ErrUnknownQuestion", err)
				}
				if d := sink.deliveries(); len(d) != 0 {
					t.Errorf("the sink stored %+v", d)
				}
			},
		},
		{
			name: "a question Answer has no answer for is ErrUnknownQuestion",
			run: func(t *testing.T, ep Endpoint) {
				a := &answers{by: map[string]json.RawMessage{}}
				serve(t, ep, Options{Answer: a.answer}, nil)
				if _, err := askOne(t, ep, "days/today"); !errors.Is(err, ErrUnknownQuestion) {
					t.Errorf("Ask = %v, want ErrUnknownQuestion", err)
				}
			},
		},
		{
			name: "an answer that fails is an error, not ErrUnknownQuestion, and its cause stays in the agent",
			run: func(t *testing.T, ep Endpoint) {
				a := &answers{err: errors.New("the bucket store is gone")}
				serve(t, ep, Options{Answer: a.answer}, nil)
				_, err := askOne(t, ep, AskLastSession)
				if err == nil || errors.Is(err, ErrUnknownQuestion) || strings.Contains(err.Error(), "bucket store") {
					t.Errorf("Ask = %v, want an error that isn't ErrUnknownQuestion and doesn't carry the cause", err)
				}
			},
		},
		{
			name: "an answer that says the agent can't answer yet is ErrStarting, for the asker to ask again",
			run: func(t *testing.T, ep Endpoint) {
				a := &answers{err: ErrStarting}
				serve(t, ep, Options{Answer: a.answer}, nil)
				if _, err := askOne(t, ep, AskLastSession); !errors.Is(err, ErrStarting) || errors.Is(err, ErrUnknownQuestion) {
					t.Errorf("Ask = %v, want ErrStarting", err)
				}
			},
		},
		{
			name: "an answer that isn't JSON is an error",
			run: func(t *testing.T, ep Endpoint) {
				a := &answers{by: map[string]json.RawMessage{AskLastSession: json.RawMessage(`{"shadow":`)}}
				serve(t, ep, Options{Answer: a.answer}, nil)
				if _, err := askOne(t, ep, AskLastSession); err == nil || errors.Is(err, ErrUnknownQuestion) {
					t.Errorf("Ask = %v, want an error", err)
				}
			},
		},
		{
			name: "an answer too long for a frame is an error, and the connection carries on",
			run: func(t *testing.T, ep Endpoint) {
				big := json.RawMessage(`"` + strings.Repeat("a", maxAnswer) + `"`)
				a := &answers{by: map[string]json.RawMessage{AskLastSession: big}}
				_, sink := serve(t, ep, Options{Answer: a.answer}, nil)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				c, err := Dial(ctx, ep)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				if _, err := c.Ask(ctx, AskLastSession); err == nil || errors.Is(err, ErrUnknownQuestion) {
					t.Errorf("Ask = %v, want an error", err)
				}
				if err := c.Send(ctx, message(1)); err != nil || len(sink.deliveries()) != 1 {
					t.Errorf("a message after the long answer: %v, %d stored", err, len(sink.deliveries()))
				}
			},
		},
		{
			name: "an answer short enough that only its escaping makes it too long for a frame is an error, and the connection carries on",
			run: func(t *testing.T, ep Endpoint) {
				// json.Marshal writes each < as six bytes.
				big := json.RawMessage(`"` + strings.Repeat("<", maxAnswer/5) + `"`)
				a := &answers{by: map[string]json.RawMessage{AskLastSession: big}}
				_, sink := serve(t, ep, Options{Answer: a.answer}, nil)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				c, err := Dial(ctx, ep)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				if _, err := c.Ask(ctx, AskLastSession); err == nil || errors.Is(err, ErrUnknownQuestion) || errors.Is(err, io.EOF) {
					t.Errorf("Ask = %v, want an error reply", err)
				}
				if err := c.Send(ctx, message(1)); err != nil || len(sink.deliveries()) != 1 {
					t.Errorf("a message after the long answer: %v, %d stored", err, len(sink.deliveries()))
				}
			},
		},
		{
			name: "questions and messages take turns on one connection",
			run: func(t *testing.T, ep Endpoint) {
				a := &answers{by: map[string]json.RawMessage{AskLastSession: session}}
				_, sink := serve(t, ep, Options{Answer: a.answer}, nil)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				c, err := Dial(ctx, ep)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				for n := range 2 {
					if got, err := c.Ask(ctx, AskLastSession); err != nil || string(got) != string(session) {
						t.Fatalf("question %d: %s, %v", n, got, err)
					}
					if err := c.Send(ctx, message(n)); err != nil {
						t.Fatalf("message %d: %v", n, err)
					}
				}
				if got := len(sink.deliveries()); got != 2 {
					t.Errorf("the sink has %d messages, want 2", got)
				}
			},
		},
		{
			name: "an agent from before questions refuses the frame and stores nothing, which Ask reports as ErrUnknownQuestion",
			run: func(t *testing.T, ep Endpoint) {
				stored := oldAgent(t, ep)
				if _, err := askOne(t, ep, AskLastSession); !errors.Is(err, ErrUnknownQuestion) {
					t.Errorf("Ask = %v, want ErrUnknownQuestion", err)
				}
				if stored() {
					t.Error("the old agent stored the question as a message")
				}
			},
		},
		{
			name: "a question without an id gets an error reply, and the agent hangs up without asking Answer",
			run: func(t *testing.T, ep Endpoint) {
				a := &answers{by: map[string]json.RawMessage{AskLastSession: session}}
				_, sink := serve(t, ep, Options{Answer: a.answer}, nil)
				replies, err := rawExchange(t, ep, frame([]byte(`{"ask":"sessions/last"}`)))
				if len(replies) != 1 || replies[0].OK || replies[0].Error == "" {
					t.Errorf("replies = %+v, want one error", replies)
				}
				if err == nil {
					t.Error("the agent kept the connection open")
				}
				if q := a.questions(); len(q) != 0 {
					t.Errorf("Answer was asked %q", q)
				}
				if len(sink.deliveries()) != 0 {
					t.Error("the sink got a message")
				}
			},
		},
		{
			name: "a name that can't be a question's is refused before anything is sent",
			run: func(t *testing.T, ep Endpoint) {
				a := &answers{by: map[string]json.RawMessage{}}
				serve(t, ep, Options{Answer: a.answer}, nil)
				for _, q := range []string{"", "Sessions/Last", "sessions last", strings.Repeat("a", 65)} {
					if _, err := askOne(t, ep, q); err == nil || errors.Is(err, ErrUnknownQuestion) {
						t.Errorf("Ask(%q) = %v, want it refused", q, err)
					}
				}
				if q := a.questions(); len(q) != 0 {
					t.Errorf("Answer was asked %q", q)
				}
			},
		},
		{
			name: "nothing is asked of an agent that runs as another user",
			run: func(t *testing.T, ep Endpoint) {
				// ask is Ask with the user the agent must run as given:
				// here not the one that runs it.
				a := &answers{by: map[string]json.RawMessage{AskLastSession: session}}
				serve(t, ep, Options{Answer: a.answer}, nil)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if got, err := ask(ctx, ep, otherUser(t), AskLastSession); !errors.Is(err, ErrNotAgent) {
					t.Errorf("ask = %s, %v; want ErrNotAgent", got, err)
				}
				if q := a.questions(); len(q) != 0 {
					t.Errorf("Answer was asked %q", q)
				}
			},
		},
		{
			name: "Ask fails at once when no agent listens",
			run: func(t *testing.T, ep Endpoint) {
				begin := time.Now()
				if _, err := askOne(t, ep, AskLastSession); err == nil || errors.Is(err, ErrUnknownQuestion) {
					t.Errorf("Ask = %v, want a dial error", err)
				}
				if took := time.Since(begin); took > 5*time.Second {
					t.Errorf("Ask took %v with no agent", took)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t, endpointIn(t, shortDir(t)))
		})
	}
	t.Run("a question that comes during the socket's self-test is turned away, and Answer isn't asked", func(t *testing.T) {
		a := &answers{by: map[string]json.RawMessage{AskLastSession: session}}
		s := &Server{answerer: a.answer, log: slog.New(slog.DiscardHandler)}
		qn := question{ID: inbox.NewID(), Ask: AskLastSession}
		b, err := json.Marshal(qn)
		if err != nil {
			t.Fatal(err)
		}
		rep, keep := s.take(b, nil, 0, nil, false)
		if rep.OK || rep.ID != qn.ID || rep.Error == "" || len(rep.Answer) != 0 || keep {
			t.Errorf("take = %+v, keep %v; want an error reply that ends the connection", rep, keep)
		}
		if q := a.questions(); len(q) != 0 {
			t.Errorf("Answer was asked %q", q)
		}
	})
	t.Run("a question's frame is no message to an agent from before questions", func(t *testing.T) {
		b, err := json.Marshal(question{ID: inbox.NewID(), Ask: AskLastSession})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inbox.Decode(b); err == nil {
			t.Errorf("inbox.Decode took %s for a message", b)
		}
	})
}
