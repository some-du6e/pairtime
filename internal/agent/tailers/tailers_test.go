// SPDX-License-Identifier: AGPL-3.0-or-later

package tailers

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/pkg/event"
)

// memStore is state.json in memory.
type memStore struct{ m map[string]json.RawMessage }

func (s *memStore) Get(key string, v any) (bool, error) {
	raw, ok := s.m[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, v)
}

func (s *memStore) Put(key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.m[key] = b
	return nil
}

// rig runs a Tailer over a root of its own, with a clock of its own, and
// takes its lines as the agent does, calling Advance for each it takes.
type rig struct {
	t     *testing.T
	dir   string
	store *memStore
	now   time.Time
	tl    *Tailer
	// got are the lines taken, forgot the files the tailer forgot.
	got    []Line
	forgot []string
	// take, when set, says whether to take a line; the first it refuses
	// ends the batch. resume, when set, gives a line's resume, which is its
	// end otherwise: what the adapter needs a restart to start from.
	take   func(Line) bool
	resume func(Line) int64
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, dir: t.TempDir(), store: &memStore{m: map[string]json.RawMessage{}}, now: time.Now()}
	r.open()
	return r
}

// open makes a Tailer from what the store holds, as an agent's start does.
func (r *rig) open() {
	r.tl = New(Options{
		Roots: []Root{{Harness: event.HarnessClaudeCode, LogRoot: adapters.LogRoot{Name: "test", Dir: r.dir, Depth: 2,
			Match: func(rel string) bool { return strings.Count(rel, "/") == 1 && strings.HasSuffix(rel, ".jsonl") }}}},
		Store:    r.store,
		Deliver:  r.deliver,
		Forget:   func(_ event.Harness, file string) { r.forgot = append(r.forgot, file) },
		Now:      func() time.Time { return r.now },
		NoNotify: true,
	})
}

func (r *rig) deliver(b Batch) int {
	for i, l := range b {
		if r.take != nil && !r.take(l) {
			return i
		}
		r.got = append(r.got, l)
		resume := l.Log.End
		if r.resume != nil {
			resume = r.resume(l)
		}
		r.tl.Advance(l.Log.File, l.Log.End, l.Hash, resume)
	}
	return len(b)
}

// path is the root's file p/name.
func (r *rig) path(name string) string { return filepath.Join(r.dir, "p", name) }

// write appends s to p/name, making it.
func (r *rig) write(name, s string) {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Join(r.dir, "p"), 0o700); err != nil {
		r.t.Fatal(err)
	}
	f, err := os.OpenFile(r.path(name), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		r.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		r.t.Fatal(err)
	}
}

// replace puts s in place of p/name, as a new file renamed over it.
func (r *rig) replace(name, s string) {
	r.t.Helper()
	tmp := r.path(name + ".new")
	if err := os.WriteFile(tmp, []byte(s), 0o600); err != nil {
		r.t.Fatal(err)
	}
	if err := os.Rename(tmp, r.path(name)); err != nil {
		r.t.Fatal(err)
	}
}

// step moves the clock on by d and lets the tailer do what is due, with
// the roots listed again when rescan is set.
func (r *rig) step(d time.Duration, rescan bool) {
	r.now = r.now.Add(d)
	if rescan {
		r.tl.mu.Lock()
		r.tl.scanDue = true
		r.tl.mu.Unlock()
	}
	r.tl.Step()
}

// texts returns the bytes of the lines taken since from, idle markers as
// "idle", and too long ones as "too long".
func (r *rig) texts(from int) []string {
	var out []string
	for _, l := range r.got[from:] {
		switch {
		case l.Log.Idle:
			out = append(out, "idle")
		case l.Log.TooLong:
			out = append(out, "too long")
		default:
			out = append(out, string(l.Bytes))
		}
	}
	return out
}

func (r *rig) replays(from int) []bool {
	var out []bool
	for _, l := range r.got[from:] {
		out = append(out, l.Log.Replay)
	}
	return out
}

func TestTailer(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, r *rig)
	}{
		{"a root's first listing takes each file up at its end and hands over only what comes after", func(t *testing.T, r *rig) {
			r.write("a.jsonl", "{\"n\":1}\n{\"n\":2}\n")
			r.open()
			r.step(0, false)
			if len(r.got) != 0 {
				t.Fatalf("the first listing handed over %v", r.texts(0))
			}
			r.write("a.jsonl", "{\"n\":3}\n")
			r.step(HotPoll, false)
			if got := r.texts(0); !slices.Equal(got, []string{`{"n":3}`}) || r.got[0].Log.Offset != 16 {
				t.Errorf("handed over %v at %d, want the third line at 16", got, r.got[0].Log.Offset)
			}
		}},
		{"a file that comes after the first listing is read from its start, each line starting where the last ended", func(t *testing.T, r *rig) {
			r.step(0, false)
			r.write("b.jsonl", "{\"n\":1}\n{\"n\":22}\n{\"n\":3}\n")
			r.step(HotPoll, true)
			if got := r.texts(0); !slices.Equal(got, []string{`{"n":1}`, `{"n":22}`, `{"n":3}`}) {
				t.Fatalf("handed over %v", got)
			}
			var at int64
			for i, l := range r.got {
				if l.Log.Offset != at || l.Log.Replay || l.Log.File != r.path("b.jsonl") {
					t.Errorf("line %d: %+v, want it at %d, no replay", i, l.Log, at)
				}
				at = l.Log.End
			}
		}},
		{"a last line waits for its newline", func(t *testing.T, r *rig) {
			r.step(0, false)
			r.write("c.jsonl", "{\"n\":1}\n{\"n\"")
			r.step(HotPoll, true)
			r.write("c.jsonl", ":2}\n")
			r.step(HotPoll, false)
			if got := r.texts(0); !slices.Equal(got, []string{`{"n":1}`, `{"n":2}`}) {
				t.Errorf("handed over %v", got)
			}
		}},
		{"blank lines are taken into the next line's span", func(t *testing.T, r *rig) {
			r.step(0, false)
			r.write("d.jsonl", "\n\r\n{\"n\":1}\r\n")
			r.step(HotPoll, true)
			if len(r.got) != 1 || string(r.got[0].Bytes) != `{"n":1}` || r.got[0].Log.Offset != 0 || r.got[0].Log.End != 12 {
				t.Errorf("handed over %v, want one line from 0 to 12 without its CR", r.got)
			}
		}},
		{"a line longer than MaxLine comes marked too long, without its bytes, and the next one whole", func(t *testing.T, r *rig) {
			r.step(0, false)
			r.write("e.jsonl", strings.Repeat("x", MaxLine+10)+"\n{\"n\":2}\n")
			r.step(HotPoll, true)
			if got := r.texts(0); !slices.Equal(got, []string{"too long", `{"n":2}`}) || r.got[0].Bytes != nil {
				t.Errorf("handed over %v", got)
			}
		}},
		{"lines the agent doesn't take wait until Poke, and then come again in order", func(t *testing.T, r *rig) {
			refused := false
			r.take = func(l Line) bool {
				if string(l.Bytes) == `{"n":2}` && !refused {
					refused = true
					return false
				}
				return true
			}
			r.step(0, false)
			r.write("f.jsonl", "{\"n\":1}\n{\"n\":2}\n{\"n\":3}\n")
			r.step(HotPoll, true)
			r.step(HotPoll, false)
			if got := r.texts(0); !slices.Equal(got, []string{`{"n":1}`}) {
				t.Fatalf("before Poke: %v", got)
			}
			r.tl.Poke()
			r.step(time.Second, false)
			if got := r.texts(0); !slices.Equal(got, []string{`{"n":1}`, `{"n":2}`, `{"n":3}`}) {
				t.Errorf("after Poke: %v", got)
			}
		}},
		{"after a restart a file comes again from its resume, the lines up to read as replays", func(t *testing.T, r *rig) {
			r.resume = func(Line) int64 { return 0 } // the adapter holds content of the file's first line
			r.step(0, false)
			r.write("g.jsonl", "{\"n\":1}\n{\"n\":2}\n")
			r.step(HotPoll, true)
			if _, err := r.tl.Put(); err != nil {
				t.Fatal(err)
			}
			r.write("g.jsonl", "{\"n\":3}\n")
			r.open()
			from := len(r.got)
			r.step(time.Second, false)
			if got := r.texts(from); !slices.Equal(got, []string{`{"n":1}`, `{"n":2}`, `{"n":3}`}) {
				t.Fatalf("handed over %v", got)
			}
			if got := r.replays(from); !slices.Equal(got, []bool{true, true, false}) {
				t.Errorf("replays %v, want the two lines read before", got)
			}
		}},
		{"a truncated file is read from its start again", func(t *testing.T, r *rig) {
			r.step(0, false)
			r.write("h.jsonl", "{\"n\":1}\n{\"n\":2}\n")
			r.step(HotPoll, true)
			if err := os.WriteFile(r.path("h.jsonl"), []byte("{\"m\":1}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			from := len(r.got)
			r.step(HotPoll, false)
			if got := r.texts(from); !slices.Equal(got, []string{`{"m":1}`}) || r.got[from].Log.Offset != 0 || r.got[from].Log.Replay {
				t.Errorf("handed over %v", r.got[from:])
			}
		}},
		{"a file replaced by a copy that keeps what was read where it was goes on after it", func(t *testing.T, r *rig) {
			r.step(0, false)
			r.write("i.jsonl", "{\"n\":1}\n{\"n\":2}\n")
			r.step(HotPoll, true)
			r.replace("i.jsonl", "{\"n\":1}\n{\"n\":2}\n{\"n\":3}\n")
			from := len(r.got)
			r.step(HotPoll, false)
			if got := r.texts(from); !slices.Equal(got, []string{`{"n":3}`}) || r.got[from].Log.Replay {
				t.Errorf("handed over %v", got)
			}
		}},
		{"a replaced file whose last read line moved is read from its start, as replays up to that line", func(t *testing.T, r *rig) {
			r.step(0, false)
			r.write("j.jsonl", "{\"n\":1}\n{\"n\":2}\n")
			r.step(HotPoll, true)
			r.replace("j.jsonl", "{\"x\":0}\n{\"n\":1}\n{\"n\":2}\n{\"n\":3}\n")
			from := len(r.got)
			r.step(HotPoll, false)
			if got := r.texts(from); !slices.Equal(got, []string{`{"x":0}`, `{"n":1}`, `{"n":2}`, `{"n":3}`}) {
				t.Fatalf("handed over %v", got)
			}
			if got := r.replays(from); !slices.Equal(got, []bool{true, true, true, false}) {
				t.Errorf("replays %v", got)
			}
		}},
		{"a file that is gone is forgotten, by the tailer and its adapter", func(t *testing.T, r *rig) {
			r.step(0, false)
			r.write("k.jsonl", "{\"n\":1}\n")
			r.step(HotPoll, true)
			if err := os.Remove(r.path("k.jsonl")); err != nil {
				t.Fatal(err)
			}
			r.step(HotPoll, false)
			if !slices.Equal(r.forgot, []string{r.path("k.jsonl")}) {
				t.Errorf("forgot %v", r.forgot)
			}
			if _, err := r.tl.Put(); err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(r.store.m[stateKey], []byte("k.jsonl")) {
				t.Error("the cursor of the gone file is still saved")
			}
		}},
		{"an adapter that holds something back hears the file is idle, again every IdleAfter", func(t *testing.T, r *rig) {
			r.resume = func(l Line) int64 { return 0 }
			r.step(0, false)
			r.write("l.jsonl", "{\"n\":1}\n")
			r.step(HotPoll, true)
			r.step(IdleAfter, false)
			r.step(IdleAfter, false)
			if got := r.texts(0); !slices.Equal(got, []string{`{"n":1}`, "idle", "idle"}) {
				t.Errorf("handed over %v", got)
			}
			if l := r.got[1]; l.Log.Offset != 8 || l.Log.End != 8 {
				t.Errorf("the idle marker is at %d to %d, want 8", l.Log.Offset, l.Log.End)
			}
		}},
		{"an adapter that holds nothing back hears of no idle file", func(t *testing.T, r *rig) {
			r.step(0, false)
			r.write("m.jsonl", "{\"n\":1}\n")
			r.step(HotPoll, true)
			r.step(IdleAfter, false)
			if got := r.texts(0); !slices.Equal(got, []string{`{"n":1}`}) {
				t.Errorf("handed over %v", got)
			}
		}},
		{"Rewind reads the file again from its start, the lines read before as replays", func(t *testing.T, r *rig) {
			r.step(0, false)
			r.write("n.jsonl", "{\"n\":1}\n{\"n\":2}\n")
			r.step(HotPoll, true)
			rewound := false
			r.take = func(l Line) bool {
				if string(l.Bytes) == `{"n":3}` && !rewound {
					rewound = true
					r.tl.Rewind(l.Log.File)
					return false
				}
				return true
			}
			r.write("n.jsonl", "{\"n\":3}\n")
			from := len(r.got)
			r.step(HotPoll, false)
			r.step(time.Second, false)
			if got := r.texts(from); !slices.Equal(got, []string{`{"n":1}`, `{"n":2}`, `{"n":3}`}) {
				t.Fatalf("handed over %v", got)
			}
			if got := r.replays(from); !slices.Equal(got, []bool{true, true, false}) {
				t.Errorf("replays %v", got)
			}
		}},
		{"a hint reads a new file of a listed root at once", func(t *testing.T, r *rig) {
			r.step(0, false)
			r.write("o.jsonl", "{\"n\":1}\n")
			r.tl.Hint(event.HarnessClaudeCode, r.path("o.jsonl"), adapters.LogRoot{})
			r.step(time.Second, false)
			if got := r.texts(0); !slices.Equal(got, []string{`{"n":1}`}) {
				t.Errorf("handed over %v", got)
			}
		}},
		{"a root a hint names first takes its files up at their end, the hinted one too", func(t *testing.T, r *rig) {
			other := t.TempDir()
			old := filepath.Join(other, "q", "old.jsonl")
			if err := os.MkdirAll(filepath.Dir(old), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(old, []byte("{\"n\":1}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			r.step(0, false)
			root := adapters.LogRoot{Name: "other", Dir: other, Depth: 2, Match: func(rel string) bool { return strings.HasSuffix(rel, ".jsonl") }}
			r.tl.Hint(event.HarnessClaudeCode, old, root)
			r.step(time.Second, false)
			f, err := os.OpenFile(old, os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			f.WriteString("{\"n\":2}\n")
			f.Close()
			r.step(HotPoll, false)
			if got := r.texts(0); !slices.Equal(got, []string{`{"n":2}`}) {
				t.Errorf("handed over %v, want the line written after the hint alone", got)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.run(t, newRig(t))
		})
	}
}

// TestLastLine checks where a file's last complete line ends, which a
// file taken up at its end goes on from.
func TestLastLine(t *testing.T) {
	cases := []struct {
		name, text string
		end        int64
		hashOf     string
	}{
		{"a file of whole lines ends after its last newline", "{\"a\":1}\n{\"b\":2}\n", 16, `{"b":2}`},
		{"a last line without its newline isn't one", "{\"a\":1}\n{\"b\"", 8, `{"a":1}`},
		{"a file of one line is that line", "{\"a\":1}\r\n", 9, `{"a":1}`},
		{"an empty file has none", "", 0, ""},
		{"a file with no newline has none", "{\"a\":1}", 0, ""},
		{"a last line longer than the read buffer is read back whole", "{\"a\":1}\n" + strings.Repeat("y", 3*readBuffer) + "\n", 8 + 3*readBuffer + 1, strings.Repeat("y", 3*readBuffer)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "f.jsonl")
			if err := os.WriteFile(p, []byte(tc.text), 0o600); err != nil {
				t.Fatal(err)
			}
			fh, err := os.Open(p)
			if err != nil {
				t.Fatal(err)
			}
			defer fh.Close()
			end, hash, err := lastLine(fh, int64(len(tc.text)))
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if tc.hashOf != "" {
				want = hashLine([]byte(tc.hashOf))
			}
			if end != tc.end || hash != want {
				t.Errorf("lastLine = %d, %q; want %d, %q", end, hash, tc.end, want)
			}
		})
	}
}
