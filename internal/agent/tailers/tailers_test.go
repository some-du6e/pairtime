// SPDX-License-Identifier: AGPL-3.0-or-later

package tailers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/fsnotify/fsnotify"

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
	// lookback, when set, starts lookback's child (Options.Lookback), and
	// until says when its records end (Options.LookbackUntil).
	lookback func() (Child, error)
	until    func() time.Time
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, dir: t.TempDir(), store: &memStore{m: map[string]json.RawMessage{}}, now: time.Now()}
	r.open()
	return r
}

// linkedFolder lists the rig's root once, with no file in it yet, and
// makes its folder p with a link q to it, returning q: a hook can name p's
// files through q, as it can by another case where the file system ignores
// it. It skips the test where links can't be made.
func linkedFolder(t *testing.T, r *rig) string {
	t.Helper()
	r.step(0, false)
	if err := os.MkdirAll(filepath.Join(r.dir, "p"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(r.dir, "q")
	if err := os.Symlink(filepath.Join(r.dir, "p"), link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	return link
}

// open makes a Tailer from what the store holds, as an agent's start does.
func (r *rig) open() {
	r.tl = New(Options{
		Roots: []Root{{Harness: event.HarnessClaudeCode, LogRoot: adapters.LogRoot{Dir: r.dir, Depth: 2,
			Match: func(rel string) bool { return strings.Count(rel, "/") == 1 && strings.HasSuffix(rel, ".jsonl") }}}},
		Store:         r.store,
		Deliver:       r.deliver,
		Forget:        func(_ event.Harness, file string) { r.forgot = append(r.forgot, file) },
		Now:           func() time.Time { return r.now },
		NoNotify:      true,
		Lookback:      r.lookback,
		LookbackUntil: r.until,
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
			if got := r.texts(0); !slices.Equal(got, []string{`{"n":3}`}) {
				t.Fatalf("handed over %v, want the third line", got)
			}
			if r.got[0].Log.Offset != 16 {
				t.Errorf("the third line is at %d, want 16", r.got[0].Log.Offset)
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
		{"after a restart, lines the agent didn't take before it stopped are read with nothing written since", func(t *testing.T, r *rig) {
			refused := true
			r.take = func(l Line) bool { return !refused || string(l.Bytes) != `{"n":2}` }
			r.step(0, false)
			r.write("p.jsonl", "{\"n\":1}\n{\"n\":2}\n{\"n\":3}\n")
			r.step(HotPoll, true)
			if _, err := r.tl.Put(); err != nil {
				t.Fatal(err)
			}
			refused = false
			r.open()
			from := len(r.got)
			r.step(time.Second, false)
			if got := r.texts(from); !slices.Equal(got, []string{`{"n":2}`, `{"n":3}`}) {
				t.Errorf("handed over %v", got)
			}
		}},
		{"after a restart, an adapter that needs lines again gets them with nothing written since", func(t *testing.T, r *rig) {
			r.resume = func(Line) int64 { return 0 }
			r.step(0, false)
			r.write("q.jsonl", "{\"n\":1}\n")
			r.step(HotPoll, true)
			if _, err := r.tl.Put(); err != nil {
				t.Fatal(err)
			}
			r.open()
			from := len(r.got)
			r.step(time.Second, false)
			if got := r.replays(from); len(got) == 0 || !got[0] {
				t.Errorf("handed over %v, want the line again as a replay", r.texts(from))
			}
		}},
		{"a Poke that comes while Deliver runs offers the lines again", func(t *testing.T, r *rig) {
			poked := false
			r.take = func(l Line) bool {
				if string(l.Bytes) == `{"n":2}` && !poked {
					poked = true
					r.tl.Poke() // the worker's, as a Track ends
					return false
				}
				return true
			}
			r.step(0, false)
			r.write("s.jsonl", "{\"n\":1}\n{\"n\":2}\n")
			r.step(HotPoll, true)
			r.step(time.Second, false)
			if got := r.texts(0); !slices.Equal(got, []string{`{"n":1}`, `{"n":2}`}) {
				t.Errorf("handed over %v", got)
			}
		}},
		{"a file deleted while nothing watched it is forgotten at a rescan", func(t *testing.T, r *rig) {
			r.step(0, false)
			r.write("u.jsonl", "{\"n\":1}\n")
			r.step(HotPoll, true)
			if err := os.Remove(r.path("u.jsonl")); err != nil {
				t.Fatal(err)
			}
			r.step(HotWindow+time.Minute, true) // cold, so no poll stats it
			if !slices.Equal(r.forgot, []string{r.path("u.jsonl")}) {
				t.Errorf("forgot %v", r.forgot)
			}
		}},
		{"an adapter that holds something back hears the file is idle while its last line is half written", func(t *testing.T, r *rig) {
			r.resume = func(Line) int64 { return 0 }
			r.step(0, false)
			r.write("v.jsonl", "{\"n\":1}\n{\"n\"")
			r.step(HotPoll, true)
			r.step(IdleAfter, false)
			if got := r.texts(0); !slices.Equal(got, []string{`{"n":1}`, "idle"}) || r.got[len(r.got)-1].Log.Offset != 8 {
				t.Errorf("handed over %v, want the whole line and an idle marker after it", got)
			}
		}},
		{"a replaced file that kept two like lines where they were goes on after them", func(t *testing.T, r *rig) {
			r.step(0, false)
			r.write("w.jsonl", "{\"n\":1}\n{\"n\":1}\n")
			r.step(HotPoll, true)
			r.replace("w.jsonl", "{\"n\":1}\n{\"n\":1}\n{\"n\":2}\n")
			from := len(r.got)
			r.step(HotPoll, false)
			if got := r.texts(from); !slices.Equal(got, []string{`{"n":2}`}) || slices.Contains(r.replays(from), true) {
				t.Errorf("handed over %v, want the new line alone", got)
			}
		}},
		{"a folder the first listing couldn't read has its files taken up at their end once it can", func(t *testing.T, r *rig) {
			if runtime.GOOS == "windows" || os.Geteuid() == 0 {
				t.Skip("a folder's mode keeps no one out here")
			}
			dir := filepath.Join(r.dir, "p")
			r.write("x.jsonl", "{\"n\":1}\n")
			if err := os.Chmod(dir, 0); err != nil {
				t.Fatal(err)
			}
			r.open()
			r.step(0, false)
			if err := os.Chmod(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			r.step(HotPoll, true)
			if len(r.got) != 0 {
				t.Fatalf("read %v from before pairtime", r.texts(0))
			}
			r.write("x.jsonl", "{\"n\":2}\n")
			r.step(HotPoll, false)
			if got := r.texts(0); !slices.Equal(got, []string{`{"n":2}`}) {
				t.Errorf("handed over %v, want the line written after", got)
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
			root := adapters.LogRoot{Dir: other, Depth: 2, Match: func(rel string) bool { return strings.HasSuffix(rel, ".jsonl") }}
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
		{"a hint that names a known file through a link to its folder reads the file once", func(t *testing.T, r *rig) {
			r.write("a.jsonl", "{\"n\":1}\n")
			r.step(0, false) // the first listing takes it up at its end
			link := filepath.Join(r.dir, "q")
			if err := os.Symlink(filepath.Join(r.dir, "p"), link); err != nil {
				t.Skipf("no symlinks here: %v", err)
			}
			r.tl.Hint(event.HarnessClaudeCode, filepath.Join(link, "a.jsonl"), adapters.LogRoot{})
			r.write("a.jsonl", "{\"n\":2}\n")
			r.step(HotPoll, true)
			r.step(HotPoll, false)
			r.tl.mu.Lock()
			n := len(r.tl.files)
			r.tl.mu.Unlock()
			if got := r.texts(0); n != 1 || !slices.Equal(got, []string{`{"n":2}`}) {
				t.Errorf("%d files handed over %v, want one that hands the new line over once", n, got)
			}
		}},
		{"a hint that names a known file in another case, where the file system ignores case, reads the file once", func(t *testing.T, r *rig) {
			r.write("a.jsonl", "{\"n\":1}\n")
			r.step(0, false)
			other := filepath.Join(r.dir, "P", "A.jsonl")
			if _, err := os.Stat(other); err != nil {
				t.Skip("this file system tells case apart")
			}
			r.tl.Hint(event.HarnessClaudeCode, other, adapters.LogRoot{})
			r.write("a.jsonl", "{\"n\":2}\n")
			r.step(HotPoll, true)
			r.step(HotPoll, false)
			r.tl.mu.Lock()
			n := len(r.tl.files)
			r.tl.mu.Unlock()
			if got := r.texts(0); n != 1 || !slices.Equal(got, []string{`{"n":2}`}) {
				t.Errorf("%d files handed over %v, want one that hands the new line over once", n, got)
			}
		}},
		{"a new file a hint names through a link to its folder is the one a rescan lists under its own name, read once", func(t *testing.T, r *rig) {
			link := linkedFolder(t, r)
			r.write("b.jsonl", "{\"n\":1}\n")
			r.tl.Hint(event.HarnessClaudeCode, filepath.Join(link, "b.jsonl"), adapters.LogRoot{})
			r.step(time.Second, false) // the hinted file is read from its start
			r.write("b.jsonl", "{\"n\":2}\n")
			r.step(HotPoll, true) // a rescan lists it as p/b.jsonl
			r.step(HotPoll, false)
			r.tl.mu.Lock()
			n := len(r.tl.files)
			r.tl.mu.Unlock()
			if got := r.texts(0); n != 1 || !slices.Equal(got, []string{`{"n":1}`, `{"n":2}`}) {
				t.Errorf("%d files handed over %v, want one that hands each line over once", n, got)
			}
		}},
		{"a hint that comes before its file is the file a listing then finds under another name, read once", func(t *testing.T, r *rig) {
			link := linkedFolder(t, r)
			r.tl.Hint(event.HarnessClaudeCode, filepath.Join(link, "b.jsonl"), adapters.LogRoot{}) // no file yet
			r.write("b.jsonl", "{\"n\":1}\n")
			r.step(HotPoll, true) // the listing comes before the hinted entry's first read
			r.step(HotPoll, false)
			r.tl.mu.Lock()
			n := len(r.tl.files)
			r.tl.mu.Unlock()
			if got := r.texts(0); n != 1 || !slices.Equal(got, []string{`{"n":1}`}) {
				t.Errorf("%d files handed over %v, want one that hands the line over once", n, got)
			}
		}},
		{"an event named by the listing's spelling of a hinted file marks the file due, with no rescan", func(t *testing.T, r *rig) {
			link := linkedFolder(t, r)
			r.write("b.jsonl", "{\"n\":1}\n")
			r.tl.Hint(event.HarnessClaudeCode, filepath.Join(link, "b.jsonl"), adapters.LogRoot{})
			r.step(time.Second, false)
			r.step(HotPoll, true) // a rescan finds the other spelling
			r.write("b.jsonl", "{\"n\":2}\n")
			r.tl.notified(fsnotify.Event{Name: r.path("b.jsonl"), Op: fsnotify.Write})
			r.tl.mu.Lock()
			f := r.tl.files[filepath.Join(link, "b.jsonl")]
			due, scan := f != nil && f.due, r.tl.scanDue
			r.tl.mu.Unlock()
			if !due || scan {
				t.Errorf("the file is due %v and a rescan due %v; want it due and no rescan", due, scan)
			}
		}},
		{"a file the first listing can't open is taken up at its end once it can be, after a restart too", func(t *testing.T, r *rig) {
			if runtime.GOOS == "windows" || os.Geteuid() == 0 {
				t.Skip("needs a file this user can't open")
			}
			r.write("s.jsonl", "{\"n\":1}\n")
			if err := os.Chmod(r.path("s.jsonl"), 0); err != nil {
				t.Fatal(err)
			}
			r.step(0, false) // the first listing can't open it
			if _, err := r.tl.Put(); err != nil {
				t.Fatal(err)
			}
			r.open() // the agent's next start
			if err := os.Chmod(r.path("s.jsonl"), 0o600); err != nil {
				t.Fatal(err)
			}
			r.step(HotPoll, true) // a later listing takes it up
			r.write("s.jsonl", "{\"n\":2}\n")
			r.step(HotPoll, false)
			if got := r.texts(0); !slices.Equal(got, []string{`{"n":2}`}) {
				t.Errorf("handed over %v, want the line written after it was taken up alone", got)
			}
		}},
		{"a file the first listing can't open, which a hint names through a link to its folder, is taken up at its end", func(t *testing.T, r *rig) {
			if runtime.GOOS == "windows" || os.Geteuid() == 0 {
				t.Skip("needs a file this user can't open")
			}
			r.write("s.jsonl", "{\"n\":1}\n")
			link := filepath.Join(r.dir, "q")
			if err := os.Symlink(filepath.Join(r.dir, "p"), link); err != nil {
				t.Skipf("no symlinks here: %v", err)
			}
			if err := os.Chmod(r.path("s.jsonl"), 0); err != nil {
				t.Fatal(err)
			}
			r.step(0, false) // the first listing can't open it
			if err := os.Chmod(r.path("s.jsonl"), 0o600); err != nil {
				t.Fatal(err)
			}
			r.tl.Hint(event.HarnessClaudeCode, filepath.Join(link, "s.jsonl"), adapters.LogRoot{})
			r.step(time.Second, false)
			r.write("s.jsonl", "{\"n\":2}\n")
			r.step(HotPoll, true)
			r.step(HotPoll, false)
			r.tl.mu.Lock()
			n := len(r.tl.files)
			r.tl.mu.Unlock()
			if got := r.texts(0); n != 1 || !slices.Equal(got, []string{`{"n":2}`}) {
				t.Errorf("%d files handed over %v, want one that hands over the line written after it was taken up alone", n, got)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.run(t, newRig(t))
		})
	}
}

// countedReader counts the reads made through it, and their bytes.
type countedReader struct {
	r     io.ReaderAt
	calls int
	read  int64
}

func (c *countedReader) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.r.ReadAt(p, off)
	c.calls++
	c.read += int64(n)
	return n, err
}

// TestPollEvery checks the hot poll's pace (CP 3.2 item 3): every HotPoll
// while a file grew in the last QuietAfter, every QuietPoll while one did in
// the last HotWindow, and no poll once none did. The hottest file sets it.
func TestPollEvery(t *testing.T) {
	cases := []struct {
		name string
		// quiet is how long ago the files were last written.
		quiet []time.Duration
		want  time.Duration
	}{
		{"a file written a minute ago is looked at every HotPoll", []time.Duration{time.Minute}, HotPoll},
		{"one quiet for longer than QuietAfter, every QuietPoll", []time.Duration{QuietAfter + time.Minute}, QuietPoll},
		{"one quiet for longer than HotWindow, never", []time.Duration{HotWindow + time.Minute}, 0},
		{"the hottest file sets the pace", []time.Duration{HotWindow + time.Minute, QuietAfter + time.Minute, time.Minute}, HotPoll},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			for i, quiet := range tc.quiet {
				name := fmt.Sprintf("f%d.jsonl", i)
				r.write(name, "{\"n\":1}\n")
				when := r.now.Add(-quiet)
				if err := os.Chtimes(r.path(name), when, when); err != nil {
					t.Fatal(err)
				}
			}
			r.open()
			r.step(0, false)
			r.tl.mu.Lock()
			got := r.tl.pollEvery(r.now)
			r.tl.mu.Unlock()
			if got != tc.want {
				t.Errorf("polls every %v, want %v", got, tc.want)
			}
		})
	}
}

// TestLastLine checks where a file's last complete line ends, which a
// file taken up at its end goes on from, read back with each byte read
// once at most, a long line in a few reads, and no more of one too long
// than tells it so.
func TestLastLine(t *testing.T) {
	cases := []struct {
		name, text string
		end        int64
		hashOf     string
		// calls bounds the reads and read the bytes they read, when set.
		calls int
		read  int64
	}{
		{"a file of whole lines ends after its last newline", "{\"a\":1}\n{\"b\":2}\n", 16, `{"b":2}`, 0, 0},
		{"a last line without its newline isn't one", "{\"a\":1}\n{\"b\"", 8, `{"a":1}`, 0, 0},
		{"a file of one line is that line", "{\"a\":1}\r\n", 9, `{"a":1}`, 0, 0},
		{"an empty file has none", "", 0, "", 0, 0},
		{"a file with no newline has none", "{\"a\":1}", 0, "", 0, 0},
		{"a last line longer than the read buffer is read back whole, in growing reads", "{\"a\":1}\n" + strings.Repeat("y", 3*readBuffer) + "\n", 8 + 3*readBuffer + 1, strings.Repeat("y", 3*readBuffer), 3, 0},
		{"a line being written that is longer than MaxLine is passed over", "{\"a\":1}\n" + strings.Repeat("z", MaxLine+5), 8, `{"a":1}`, 0, 0},
		{"a last line longer than MaxLine has its end and no hash", "{\"a\":1}\n" + strings.Repeat("y", MaxLine+5) + "\n", 8 + MaxLine + 6, "", 10, 0},
		{"of a last line too long after more than MaxLine of lines, no more is read than tells it so", strings.Repeat("{\"a\":1}\n", MaxLine/8+1) + strings.Repeat("y", MaxLine+5) + "\n",
			2*MaxLine + 14, "", 0, MaxLine + 3},
		{"a last line of MaxLine bytes and a CRLF, which readLine reads whole, has its hash", "{\"a\":1}\n" + strings.Repeat("y", MaxLine) + "\r\n", 8 + MaxLine + 2, strings.Repeat("y", MaxLine), 0, 0},
		{"blank lines after the last line are no lines, so the file ends at that line", "{\"a\":1}\n\n \r\n", 8, `{"a":1}`, 0, 0},
		{"blank lines past the read buffer are passed over too", "{\"a\":1}\n" + strings.Repeat("\n", 2*readBuffer+5) + strings.Repeat(" \r\n", readBuffer), 8, `{"a":1}`, 0, 0},
		{"a file of blank lines has none", "\n\n", 0, "", 0, 0},
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
			cr := &countedReader{r: fh}
			end, hash, err := lastLine(cr, int64(len(tc.text)))
			if err != nil {
				t.Fatal(err)
			}
			read := tc.read
			if read == 0 {
				read = int64(len(tc.text))
			}
			if cr.read > read || tc.calls > 0 && cr.calls > tc.calls {
				t.Errorf("%d reads of %d bytes in all, of a file of %d; want %d bytes at most, in %d reads at most", cr.calls, cr.read, len(tc.text), read, tc.calls)
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

// TestLinesReadError checks that a read that fails before the file's end
// is told from a last line not yet whole, so the file is read again
// rather than left until it changes.
func TestLinesReadError(t *testing.T) {
	cases := []struct {
		name   string
		r      io.Reader
		failed bool
	}{
		{"a last line without its newline is no failure", strings.NewReader("{\"n\":1}\n{\"n\""), false},
		{"an error before the end is the read's", io.MultiReader(strings.NewReader("{\"n\":1}\n{\"n\""), iotest.ErrReader(errors.New("disk gone"))), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tl := New(Options{NoNotify: true})
			batch, next, more, failed := tl.lines(bufio.NewReader(tc.r), &file{key: "k", path: "/k"}, 0, 0, 0, 0)
			if len(batch) != 1 || next != 8 || more || failed != tc.failed {
				t.Errorf("lines = %d lines, %d, more %v, failed %v; want 1, 8, false, %v", len(batch), next, more, failed, tc.failed)
			}
		})
	}
}

// TestNotifiedRemovedWatch checks that a watched folder fsnotify reports
// removed is no longer counted as watched, so it is watched again once it
// is back: the backend drops its watch with the folder.
func TestNotifiedRemovedWatch(t *testing.T) {
	cases := []struct {
		name string
		op   fsnotify.Op
		kept bool
	}{
		{"a removed folder is watched no more", fsnotify.Remove, false},
		{"a renamed folder is watched no more", fsnotify.Rename, false},
		{"a write keeps the watch", fsnotify.Write, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tl := New(Options{NoNotify: true})
			dir := filepath.Join(t.TempDir(), "p")
			tl.watching[dir] = true
			tl.notified(fsnotify.Event{Name: dir, Op: tc.op})
			if tl.watching[dir] != tc.kept {
				t.Errorf("watching %v, want %v", tl.watching[dir], tc.kept)
			}
		})
	}
}

// TestOpenLog checks that the tailer opens a regular file and refuses a
// folder at once, whatever the OS.
func TestOpenLog(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.jsonl")
	if err := os.WriteFile(file, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, path string
		ok         bool
	}{
		{"a regular file opens", file, true},
		{"a folder is refused", dir, false},
		{"a missing file is missing", filepath.Join(dir, "none.jsonl"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fh, err := openLog(tc.path)
			if fh != nil {
				fh.Close()
			}
			if (err == nil) != tc.ok {
				t.Errorf("openLog = %v, want it to open: %v", err, tc.ok)
			}
		})
	}
}
