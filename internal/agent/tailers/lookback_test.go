// SPDX-License-Identifier: AGPL-3.0-or-later

package tailers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/pkg/event"
)

// upper is a Trimmer the tests can see at work: it upper-cases the line.
func upper(_ event.Harness, line []byte) ([]byte, error) { return bytes.ToUpper(line), nil }

// pipeChild runs ServeLookback in this process, as `pairtime lookback`
// would in its own, over pipes. hold, when set, keeps the child from
// reading its job until it is closed; cut, when above 0, makes it fail
// after that many of its lines.
type pipeChild struct {
	trim    Trimmer
	hold    chan struct{}
	cut     int
	started int
	mu      sync.Mutex
}

func (p *pipeChild) start() (Child, error) {
	p.mu.Lock()
	p.started++
	p.mu.Unlock()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		if p.hold != nil {
			<-p.hold
		}
		var w io.Writer = outW
		if p.cut > 0 {
			w = &cutWriter{w: outW, lines: p.cut}
		}
		err := ServeLookback(inR, w, p.trim)
		outW.CloseWithError(err)
		inR.Close()
		done <- err
	}()
	var once sync.Once
	var werr error
	return Child{In: inW, Out: outR,
		Wait: func() error { once.Do(func() { werr = <-done }); return werr },
		Kill: func() { outR.CloseWithError(errors.New("killed")); inW.Close() }}, nil
}

// cutWriter passes lines through until it has passed lines of them, and
// then fails, as a child that crashes does.
type cutWriter struct {
	w     io.Writer
	lines int
}

func (c *cutWriter) Write(b []byte) (int, error) {
	n := 0
	for _, part := range bytes.SplitAfter(b, []byte("\n")) {
		if len(part) == 0 {
			continue
		}
		if c.lines == 0 {
			return n, errors.New("the child crashed")
		}
		if _, err := c.w.Write(part); err != nil {
			return n, err
		}
		n += len(part)
		if bytes.HasSuffix(part, []byte("\n")) {
			c.lines--
		}
	}
	return n, nil
}

// lookbackRig is a rig whose tailer has lookback through child.
func lookbackRig(t *testing.T, child func() (Child, error)) *rig {
	r := &rig{t: t, dir: t.TempDir(), store: &memStore{m: map[string]json.RawMessage{}}, now: time.Now(), lookback: child}
	r.open()
	return r
}

// settle waits for the lookback that runs to end.
func (r *rig) settle() {
	r.tl.lb.wg.Wait()
}

// lookbacks returns the Lookback of the lines taken since from.
func (r *rig) lookbacks(from int) []int64 {
	var out []int64
	for _, l := range r.got[from:] {
		out = append(out, l.Lookback)
	}
	return out
}

func TestLookback(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"a file the root's first listing takes up has its history read through the child, then its new lines live", func(t *testing.T) {
			pc := &pipeChild{trim: upper}
			r := lookbackRig(t, pc.start)
			r.write("a.jsonl", "{\"n\":1}\n\n{\"n\":2}\n")
			r.open()
			r.step(0, false)
			r.settle()
			r.write("a.jsonl", "{\"n\":3}\n")
			r.step(HotPoll, false)
			if got := r.texts(0); !slices.Equal(got, []string{`{"N":1}`, `{"N":2}`, `{"n":3}`}) {
				t.Fatalf("handed over %v, want the history trimmed and then the new line", got)
			}
			from := r.now.Add(-HotPoll - LookbackWindow).UnixMilli()
			if got := r.lookbacks(0); !slices.Equal(got, []int64{from, from, 0}) {
				t.Errorf("Lookback %v, want the window's start on the history alone", got)
			}
			if got := r.replays(0); !slices.Equal(got, []bool{false, false, false}) {
				t.Errorf("replays %v", got)
			}
			if r.got[1].Log.Offset != 8 || r.got[2].Log.Offset != 17 || r.got[1].Hash != hashLine([]byte(`{"n":2}`)) {
				t.Errorf("the spans are %+v and %+v, want the tailer's own", r.got[1].Log, r.got[2].Log)
			}
		}},
		{"Progress counts each history's files and bytes, and says when they are done", func(t *testing.T) {
			pc := &pipeChild{trim: upper}
			r := lookbackRig(t, pc.start)
			r.write("b.jsonl", "{\"n\":1}\n{\"n\":2}\n")
			r.open()
			r.step(0, false)
			r.settle()
			if p := r.tl.Progress(); p.Files != 1 || p.FilesDone != 1 || p.Bytes != 16 || p.BytesDone != 16 || !p.Done() {
				t.Errorf("progress %+v", p)
			}
		}},
		{"a file last written longer ago than LookbackWindow has no history", func(t *testing.T) {
			pc := &pipeChild{trim: upper}
			r := lookbackRig(t, pc.start)
			r.write("c.jsonl", "{\"n\":1}\n")
			old := r.now.Add(-LookbackWindow - time.Hour)
			if err := os.Chtimes(r.path("c.jsonl"), old, old); err != nil {
				t.Fatal(err)
			}
			r.open()
			r.step(0, false)
			r.settle()
			if len(r.got) != 0 || pc.started != 0 || r.tl.Progress().Files != 0 {
				t.Errorf("handed over %v with %d children", r.texts(0), pc.started)
			}
		}},
		{"while its history is read, the tailer reads none of the file live, and its new lines come after it", func(t *testing.T) {
			pc := &pipeChild{trim: upper, hold: make(chan struct{})}
			r := lookbackRig(t, pc.start)
			r.write("d.jsonl", "{\"n\":1}\n")
			r.open()
			r.step(0, false)
			r.write("d.jsonl", "{\"n\":2}\n")
			r.step(HotPoll, false)
			if len(r.got) != 0 {
				t.Fatalf("before the history: %v", r.texts(0))
			}
			close(pc.hold)
			r.settle()
			r.step(HotPoll, false)
			if got := r.texts(0); !slices.Equal(got, []string{`{"N":1}`, `{"n":2}`}) {
				t.Errorf("handed over %v", got)
			}
		}},
		{"after a child failed in a history's middle, the next goes on from the cursor, the lines its adapter needs again as replays", func(t *testing.T) {
			pc := &pipeChild{trim: upper, cut: 2}
			r := lookbackRig(t, pc.start)
			r.resume = func(Line) int64 { return 0 } // the adapter holds content of the file's first line
			r.write("e.jsonl", "{\"n\":1}\n{\"n\":2}\n{\"n\":3}\n")
			r.open()
			r.step(0, false)
			r.settle()
			if got := r.texts(0); !slices.Equal(got, []string{`{"N":1}`, `{"N":2}`}) {
				t.Fatalf("the failing child handed over %v", got)
			}
			if _, err := r.tl.Put(); err != nil {
				t.Fatal(err)
			}
			pc.cut = 0
			r.open() // the agent's next start
			from := len(r.got)
			r.step(time.Second, false)
			r.settle()
			if got := r.texts(from); !slices.Equal(got, []string{`{"N":1}`, `{"N":2}`, `{"N":3}`}) {
				t.Fatalf("the next child handed over %v", got)
			}
			if got := r.replays(from); !slices.Equal(got, []bool{true, true, false}) {
				t.Errorf("replays %v", got)
			}
		}},
		{"lines the agent doesn't take wait for Poke, as live ones do", func(t *testing.T) {
			pc := &pipeChild{trim: upper}
			r := lookbackRig(t, pc.start)
			var mu sync.Mutex
			refuse := true
			r.take = func(l Line) bool {
				mu.Lock()
				defer mu.Unlock()
				return !refuse || string(l.Bytes) != `{"N":2}`
			}
			r.write("f.jsonl", "{\"n\":1}\n{\"n\":2}\n{\"n\":3}\n")
			r.open()
			r.step(0, false)
			deadline := time.Now().Add(10 * time.Second)
			for r.tl.Progress().BytesDone < 8 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			mu.Lock()
			refuse = false
			mu.Unlock()
			r.tl.Poke()
			r.settle()
			if got := r.texts(0); !slices.Equal(got, []string{`{"N":1}`, `{"N":2}`, `{"N":3}`}) {
				t.Errorf("handed over %v", got)
			}
		}},
		{"a child that can't start is tried again after its pause, and after the last try the history stays unread and the file is read on live", func(t *testing.T) {
			tries := 0
			r := lookbackRig(t, func() (Child, error) {
				tries++
				return Child{}, errors.New("no binary")
			})
			r.write("g.jsonl", "{\"n\":1}\n")
			r.open()
			r.step(0, false)
			r.settle()
			for i := range maxLookbackTries - 1 {
				r.step(lookbackPause<<i, false)
				r.settle()
			}
			r.write("g.jsonl", "{\"n\":2}\n")
			r.step(HotPoll, false)
			p := r.tl.Progress()
			if got := r.texts(0); tries != maxLookbackTries || !slices.Equal(got, []string{`{"n":2}`}) || r.got[0].Log.Offset != 8 {
				t.Fatalf("%d tries, then handed over %v", tries, got)
			}
			if !r.got[0].Fresh {
				t.Error("the line after the history isn't fresh, so an adapter with no memory of the file would have it read again from its start")
			}
			if p.Failures != maxLookbackTries || !strings.Contains(p.Err, "no binary") || !p.Done() {
				t.Errorf("progress %+v", p)
			}
		}},
		{"a stop while the agent hasn't taken a history's lines leaves the history to the next run", func(t *testing.T) {
			pc := &pipeChild{trim: upper}
			r := lookbackRig(t, pc.start)
			var offered atomic.Int32
			r.take = func(Line) bool {
				offered.Add(1)
				return false // the agent is stopping: dropLogs answers none taken
			}
			r.write("j.jsonl", "{\"n\":1}\n")
			r.open()
			r.step(0, false)
			deadline := time.Now().Add(10 * time.Second)
			for offered.Load() == 0 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			r.tl.mu.Lock()
			r.tl.stopped = true
			r.tl.mu.Unlock()
			close(r.tl.stop)
			r.settle()
			if p := r.tl.Progress(); offered.Load() == 0 || p.FilesDone != 0 || p.Failures != 0 {
				t.Errorf("offered %d times, then progress %+v; want the history still to read and no failure", offered.Load(), p)
			}
		}},
		{"a Rewind sends the lines read before through the child, trimmed, as replays", func(t *testing.T) {
			pc := &pipeChild{trim: upper}
			r := lookbackRig(t, pc.start)
			r.write("k.jsonl", "{\"n\":1}\n{\"n\":2}\n")
			r.open()
			r.step(0, false)
			r.settle()
			rewound := false
			r.take = func(l Line) bool {
				if string(l.Bytes) == `{"n":3}` && !rewound {
					rewound = true
					r.tl.Rewind(l.Log.File) // the agent's, for an adapter that forgot the file
					return false
				}
				return true
			}
			r.write("k.jsonl", "{\"n\":3}\n")
			from := len(r.got)
			r.step(HotPoll, false)
			r.settle()
			r.step(HotPoll, false)
			if got := r.texts(from); !slices.Equal(got, []string{`{"N":1}`, `{"N":2}`, `{"n":3}`}) || pc.started != 2 {
				t.Fatalf("after the rewind %v from %d children, want the history again through a child and then the new line", got, pc.started)
			}
			if got := r.replays(from); !slices.Equal(got, []bool{true, true, false}) {
				t.Errorf("replays %v", got)
			}
		}},
		{"a root's first listing gives no history when the ledger began LookbackWindow ago", func(t *testing.T) {
			pc := &pipeChild{trim: upper}
			r := lookbackRig(t, pc.start)
			r.until = func() time.Time { return r.now.Add(-LookbackWindow - time.Hour) }
			r.write("m.jsonl", "{\"n\":1}\n")
			r.open()
			r.step(0, false)
			r.settle()
			r.write("m.jsonl", "{\"n\":2}\n")
			r.step(HotPoll, false)
			if got := r.texts(0); !slices.Equal(got, []string{`{"n":2}`}) || pc.started != 0 || r.tl.Progress().Files != 0 {
				t.Errorf("handed over %v from %d children, want the new line alone", got, pc.started)
			}
		}},
		{"the idle marker at a history's end carries its Lookback, and one after the file's live lines doesn't", func(t *testing.T) {
			pc := &pipeChild{trim: upper}
			r := lookbackRig(t, pc.start)
			r.resume = func(Line) int64 { return 0 } // the adapter holds back the last turn
			r.write("n.jsonl", "{\"n\":1}\n")
			r.open()
			from := r.now.Add(-LookbackWindow).UnixMilli()
			r.step(0, false)
			r.settle()
			r.step(IdleAfter+time.Second, false)
			r.write("n.jsonl", "{\"n\":2}\n")
			r.step(HotPoll, false)
			r.step(IdleAfter+time.Second, false)
			if got := r.texts(0); !slices.Equal(got, []string{`{"N":1}`, "idle", `{"n":2}`, "idle"}) {
				t.Fatalf("handed over %v", got)
			}
			if got := r.lookbacks(0); !slices.Equal(got, []int64{from, from, 0, 0}) {
				t.Errorf("Lookback %v, want the window's start on the history and the marker at its end", got)
			}
		}},
		{"a Rewind while a job runs starts the job again, the rewound file first, and Progress leaves it out", func(t *testing.T) {
			pc := &pipeChild{trim: upper, hold: make(chan struct{})}
			r := lookbackRig(t, pc.start)
			r.write("l.jsonl", "{\"l\":1}\n")
			old := r.now.Add(-LookbackWindow - time.Hour)
			if err := os.Chtimes(r.path("l.jsonl"), old, old); err != nil {
				t.Fatal(err)
			}
			r.write("h.jsonl", "{\"h\":1}\n{\"h\":2}\n")
			earlier := r.now.Add(-time.Hour)
			if err := os.Chtimes(r.path("h.jsonl"), earlier, earlier); err != nil {
				t.Fatal(err)
			}
			r.open()
			r.step(0, false)
			deadline := time.Now().Add(10 * time.Second)
			for {
				pc.mu.Lock()
				started := pc.started
				pc.mu.Unlock()
				if started > 0 || time.Now().After(deadline) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			rewound := false
			r.take = func(l Line) bool {
				if string(l.Bytes) == `{"l":2}` && !rewound {
					rewound = true
					r.tl.Rewind(l.Log.File) // the agent's, for an adapter that forgot the file
					return false
				}
				return true
			}
			r.write("l.jsonl", "{\"l\":2}\n")
			r.step(HotPoll, true) // the rescan finds the quiet file grew
			close(pc.hold)
			r.settle()
			r.step(HotPoll, false)
			if got := r.texts(0); !slices.Equal(got, []string{`{"L":1}`, `{"H":1}`, `{"H":2}`, `{"l":2}`}) || pc.started != 2 {
				t.Fatalf("handed over %v from %d children, want the rewound file through the job started again, first", got, pc.started)
			}
			if got := r.replays(0); !slices.Equal(got, []bool{true, false, false, false}) {
				t.Errorf("replays %v", got)
			}
			if p := r.tl.Progress(); p.Files != 1 || p.Bytes != 16 || !p.Done() {
				t.Errorf("progress %+v, want the history alone", p)
			}
		}},
		{"a Rewind with a failed child's pause still to wait out has the tailer read the file again itself", func(t *testing.T) {
			tries := 0
			r := lookbackRig(t, func() (Child, error) {
				tries++
				return Child{}, errors.New("no binary")
			})
			r.write("q.jsonl", "{\"q\":1}\n")
			old := r.now.Add(-LookbackWindow - time.Hour)
			if err := os.Chtimes(r.path("q.jsonl"), old, old); err != nil {
				t.Fatal(err)
			}
			r.write("w.jsonl", "{\"w\":1}\n") // a history, whose child fails
			r.open()
			r.step(0, false)
			r.settle()
			rewound := false
			r.take = func(l Line) bool {
				if string(l.Bytes) == `{"q":2}` && !rewound {
					rewound = true
					r.tl.Rewind(l.Log.File)
					return false
				}
				return true
			}
			r.write("q.jsonl", "{\"q\":2}\n")
			r.step(HotPoll, true) // the rescan finds the quiet file grew
			r.step(time.Second, false)
			if got := r.texts(0); tries != 1 || !slices.Equal(got, []string{`{"q":1}`, `{"q":2}`}) {
				t.Fatalf("%d tries, then handed over %v, want the file again at once", tries, got)
			}
			if got := r.replays(0); !slices.Equal(got, []bool{true, false}) {
				t.Errorf("replays %v", got)
			}
		}},
		{"a Rewind's lines go to the tailer at once when the child after the Rewind fails, of a history that ends in a blank line too", func(t *testing.T) {
			calls := 0
			r := lookbackRig(t, func() (Child, error) {
				calls++
				if calls > 1 {
					return Child{}, errors.New("no binary")
				}
				pc := &pipeChild{trim: upper}
				return pc.start()
			})
			// A blank line at the history's end, which the history leaves
			// to the line after it.
			r.write("s.jsonl", "{\"s\":1}\n\n")
			r.write("u.jsonl", "{\"u\":1}\n")
			earlier := r.now.Add(-time.Hour)
			if err := os.Chtimes(r.path("u.jsonl"), earlier, earlier); err != nil {
				t.Fatal(err)
			}
			r.open()
			r.step(0, false)
			r.settle()
			if got := r.texts(0); !slices.Equal(got, []string{`{"S":1}`, `{"U":1}`}) {
				t.Fatalf("the histories handed over %v", got)
			}
			if p := r.tl.Progress(); p.Bytes != 16 {
				t.Errorf("progress %+v, want s's history to end at its last line, before the blank one", p)
			}
			rewound := false
			r.take = func(l Line) bool {
				if string(l.Bytes) == `{"s":2}` && !rewound {
					rewound = true
					r.tl.Rewind(l.Log.File)
					return false
				}
				return true
			}
			r.write("s.jsonl", "{\"s\":2}\n")
			r.step(HotPoll, false) // the Rewind, and a child for its lines that can't start
			r.settle()
			r.step(time.Second, false)
			if got := r.texts(2); calls != 2 || !slices.Equal(got, []string{`{"s":1}`, `{"s":2}`}) {
				t.Fatalf("%d children, then handed over %v, want the file again from its start at once", calls, got)
			}
			if got := r.replays(2); !slices.Equal(got, []bool{true, false}) || r.got[2].Fresh || r.got[3].Fresh {
				t.Errorf("replays %v, fresh %v and %v; want the line read before as a replay, and neither fresh", got, r.got[2].Fresh, r.got[3].Fresh)
			}
		}},
		{"a history whose end came before where it was taken up, as in a file cut while lookback read it, keeps its lines when the tailer takes its Rewind over", func(t *testing.T) {
			calls := 0
			pc := &pipeChild{trim: upper, hold: make(chan struct{})}
			r := lookbackRig(t, func() (Child, error) {
				calls++
				if calls > 1 {
					return Child{}, errors.New("no binary")
				}
				return pc.start()
			})
			r.write("c.jsonl", "{\"c\":1}\n{\"c\":9}\n")
			r.open()
			r.step(0, false)
			deadline := time.Now().Add(10 * time.Second)
			for {
				pc.mu.Lock()
				started := pc.started
				pc.mu.Unlock()
				if started > 0 || time.Now().After(deadline) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := os.Truncate(r.path("c.jsonl"), 8); err != nil {
				t.Fatal(err)
			}
			close(pc.hold)
			r.settle()
			if got := r.texts(0); !slices.Equal(got, []string{`{"C":1}`}) {
				t.Fatalf("the history handed over %v", got)
			}
			rewound := false
			r.take = func(l Line) bool {
				if string(l.Bytes) == `{"c":2}` && !rewound {
					rewound = true
					r.tl.Rewind(l.Log.File)
					return false
				}
				return true
			}
			r.write("c.jsonl", "{\"c\":2}\n")
			r.step(HotPoll, false) // the Rewind, and a child for its lines that can't start
			r.settle()
			r.step(time.Second, false)
			if got := r.texts(1); calls != 2 || !slices.Equal(got, []string{`{"c":1}`, `{"c":2}`}) {
				t.Fatalf("%d children, then handed over %v, want the file again from its start", calls, got)
			}
			if got := r.replays(1); !slices.Equal(got, []bool{true, false}) {
				t.Errorf("replays %v", got)
			}
		}},
		{"a file replaced with lines none of which were read loses its history, so its lines carry no Lookback", func(t *testing.T) {
			pc := &pipeChild{trim: upper}
			r := lookbackRig(t, pc.start)
			r.write("y.jsonl", "{\"h\":1}\n")
			r.open()
			r.step(0, false)
			r.settle()
			r.replace("y.jsonl", "{\"y\":1}\n{\"y\":2}\n")
			r.step(HotPoll, false)
			if got := r.texts(1); !slices.Equal(got, []string{`{"y":1}`, `{"y":2}`}) {
				t.Fatalf("after the replace, handed over %v", got)
			}
			if got := r.lookbacks(1); !slices.Equal(got, []int64{0, 0}) || r.tl.Progress().Files != 0 {
				t.Errorf("Lookback %v, progress %+v; want neither line the old file's history's, and no history", got, r.tl.Progress())
			}
		}},
		{"a file cut below what was read keeps its history up to the cut, so its old line keeps its window and a new one has none", func(t *testing.T) {
			pc := &pipeChild{trim: upper}
			r := lookbackRig(t, pc.start)
			r.write("k.jsonl", "{\"k\":1}\n{\"k\":2}\n")
			r.open()
			from := r.now.Add(-LookbackWindow).UnixMilli()
			r.step(0, false)
			r.settle()
			if err := os.Truncate(r.path("k.jsonl"), 8); err != nil {
				t.Fatal(err)
			}
			r.step(HotPoll, false)
			r.write("k.jsonl", "{\"k\":3}\n")
			r.step(HotPoll, false)
			if got := r.texts(2); !slices.Equal(got, []string{`{"k":1}`, `{"k":3}`}) {
				t.Fatalf("after the cut, handed over %v", got)
			}
			if got := r.lookbacks(2); !slices.Equal(got, []int64{from, 0}) {
				t.Errorf("Lookback %v, want the window on the history's line alone", got)
			}
		}},
		{"a file replaced by a copy that lost lines before what was read keeps its history up to the same line, so a line after it has no window", func(t *testing.T) {
			pc := &pipeChild{trim: upper}
			r := lookbackRig(t, pc.start)
			r.write("p.jsonl", "{\"p\":1}\n{\"p\":2}\n")
			r.open()
			from := r.now.Add(-LookbackWindow).UnixMilli()
			r.step(0, false)
			r.settle()
			r.replace("p.jsonl", "{\"p\":2}\n{\"p\":3}\n")
			r.step(HotPoll, false)
			if got := r.texts(2); !slices.Equal(got, []string{`{"p":2}`, `{"p":3}`}) {
				t.Fatalf("after the replace, handed over %v", got)
			}
			if got := r.lookbacks(2); !slices.Equal(got, []int64{from, 0}) {
				t.Errorf("Lookback %v, want the window on the history's line alone", got)
			}
			if got := r.replays(2); !slices.Equal(got, []bool{true, false}) {
				t.Errorf("replays %v", got)
			}
		}},
		{"a history read to its end without its end is done once its file is cut, and the tailer reads it at once", func(t *testing.T) {
			calls := 0
			r := lookbackRig(t, func() (Child, error) {
				calls++
				if calls > 1 {
					return Child{}, errors.New("no binary")
				}
				pc := &pipeChild{trim: upper, cut: 2} // a's lines, and then it crashes
				return pc.start()
			})
			r.write("a.jsonl", "{\"a\":1}\n{\"a\":2}\n")
			r.write("b.jsonl", "{\"b\":1}\n")
			earlier := r.now.Add(-time.Hour)
			if err := os.Chtimes(r.path("b.jsonl"), earlier, earlier); err != nil {
				t.Fatal(err)
			}
			r.open()
			r.step(0, false)
			r.settle()
			if got := r.texts(0); calls != 1 || !slices.Equal(got, []string{`{"A":1}`, `{"A":2}`}) {
				t.Fatalf("%d children, then handed over %v", calls, got)
			}
			// The agent has the file's first line wait, so the file is read
			// again after a Poke, with the failed child's pause still to run.
			waited := false
			r.take = func(Line) bool {
				if !waited {
					waited = true
					return false
				}
				return true
			}
			if err := os.Truncate(r.path("a.jsonl"), 8); err != nil {
				t.Fatal(err)
			}
			r.step(HotPoll, false)
			r.tl.Poke()
			r.step(time.Second, false)
			if got := r.texts(2); !waited || calls != 1 || !slices.Equal(got, []string{`{"a":1}`}) {
				t.Errorf("after the cut, %d children, handed over %v; want the file read at once", calls, got)
			}
		}},
		{"a given-up history whose file is replaced by a copy with a line more keeps its place, and the new line is live and fresh", func(t *testing.T) {
			r := lookbackRig(t, func() (Child, error) { return Child{}, errors.New("no binary") })
			r.write("g.jsonl", "{\"g\":1}\n{\"g\":2}\n")
			r.open()
			r.step(0, false)
			r.settle()
			for i := range maxLookbackTries - 1 {
				r.step(lookbackPause<<i, false)
				r.settle()
			}
			r.replace("g.jsonl", "{\"g\":1}\n{\"g\":2}\n{\"g\":3}\n")
			r.step(HotPoll, false)
			if got := r.texts(0); !slices.Equal(got, []string{`{"g":3}`}) {
				t.Fatalf("after the replace, handed over %v, want the new line alone", got)
			}
			if got := r.lookbacks(0); !slices.Equal(got, []int64{0}) || !r.got[0].Fresh {
				t.Errorf("Lookback %v, fresh %v; want no window, and the line after the history fresh", got, r.got[0].Fresh)
			}
		}},
		{"a given-up history whose file is replaced by another file loses its history, so its lines have no window", func(t *testing.T) {
			r := lookbackRig(t, func() (Child, error) { return Child{}, errors.New("no binary") })
			r.write("o.jsonl", "{\"o\":1}\n{\"o\":2}\n")
			r.open()
			r.step(0, false)
			r.settle()
			for i := range maxLookbackTries - 1 {
				r.step(lookbackPause<<i, false)
				r.settle()
			}
			r.replace("o.jsonl", "{\"n\":1}\n{\"n\":2}\n{\"n\":3}\n")
			r.step(HotPoll, false)
			if got := r.texts(0); !slices.Equal(got, []string{`{"n":1}`, `{"n":2}`, `{"n":3}`}) {
				t.Fatalf("after the replace, handed over %v", got)
			}
			if got := r.lookbacks(0); !slices.Equal(got, []int64{0, 0, 0}) || r.tl.Progress().Files != 0 {
				t.Errorf("Lookback %v, progress %+v; want no window and no history", got, r.tl.Progress())
			}
		}},
		{"a history whose last line was too long to hash, replaced by a copy with a line more, keeps its window, as it can't be told from another file", func(t *testing.T) {
			pc := &pipeChild{trim: upper}
			r := lookbackRig(t, pc.start)
			long := strings.Repeat("y", MaxLine+5)
			r.write("l.jsonl", "{\"l\":1}\n"+long+"\n")
			r.open()
			from := r.now.Add(-LookbackWindow).UnixMilli()
			r.step(0, false)
			r.settle()
			if got := r.texts(0); !slices.Equal(got, []string{`{"L":1}`, "too long"}) {
				t.Fatalf("the history handed over %v", got)
			}
			r.replace("l.jsonl", "{\"l\":1}\n"+long+"\n{\"l\":2}\n")
			r.step(HotPoll, false)
			if got := r.texts(2); !slices.Equal(got, []string{`{"l":1}`, "too long", `{"l":2}`}) {
				t.Fatalf("after the replace, handed over %v", got)
			}
			if got := r.lookbacks(2); !slices.Equal(got, []int64{from, from, 0}) {
				t.Errorf("Lookback %v, want the window on the history's lines alone", got)
			}
		}},
		{"a history batch of a file forgotten while the agent was offered it ends that file, and lookback reads on", func(t *testing.T) {
			pc := &pipeChild{trim: upper}
			r := lookbackRig(t, pc.start)
			r.write("x.jsonl", "{\"x\":1}\n{\"x\":2}\n")
			r.write("y.jsonl", "{\"y\":1}\n")
			earlier := r.now.Add(-time.Hour)
			if err := os.Chtimes(r.path("y.jsonl"), earlier, earlier); err != nil {
				t.Fatal(err)
			}
			r.take = func(l Line) bool {
				if string(l.Bytes) != `{"X":1}` {
					return true
				}
				// The tailer found the file gone, and the agent forgot it
				// before it took the batch, so the line isn't taken.
				r.tl.mu.Lock()
				f := r.tl.files[l.Log.File]
				r.tl.mu.Unlock()
				if f != nil {
					r.tl.gone(f)
				}
				return false
			}
			r.open()
			r.step(0, false)
			deadline := time.Now().Add(10 * time.Second)
			for r.tl.Progress().Running && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if r.tl.Progress().Running {
				t.Fatal("lookback still waits on the forgotten file's batch")
			}
			r.settle()
			if got := r.texts(0); !slices.Equal(got, []string{`{"Y":1}`}) {
				t.Errorf("handed over %v, want the next file's history", got)
			}
		}},
		{"a Rewind of a file whose history lookback gave up on reads it again from where that history ended", func(t *testing.T) {
			r := lookbackRig(t, func() (Child, error) { return Child{}, errors.New("no binary") })
			r.write("v.jsonl", "{\"v\":1}\n")
			r.open()
			r.step(0, false)
			r.settle()
			for i := range maxLookbackTries - 1 {
				r.step(lookbackPause<<i, false)
				r.settle()
			}
			r.write("v.jsonl", "{\"v\":2}\n")
			r.step(HotPoll, false)
			rewound := false
			r.take = func(l Line) bool {
				if string(l.Bytes) == `{"v":3}` && !rewound {
					rewound = true
					r.tl.Rewind(l.Log.File) // the agent's, for an adapter that forgot the file
					return false
				}
				return true
			}
			r.write("v.jsonl", "{\"v\":3}\n")
			r.step(HotPoll, false)
			r.step(time.Second, false)
			if got := r.texts(0); !rewound || !slices.Equal(got, []string{`{"v":2}`, `{"v":2}`, `{"v":3}`}) {
				t.Fatalf("handed over %v, want the line after the history, then it again and the new line", got)
			}
			if got := r.replays(1); !slices.Equal(got, []bool{true, false}) || !r.got[1].Fresh || r.got[2].Fresh {
				t.Errorf("replays %v, fresh %v and %v; want the history's end again as a fresh replay", got, r.got[1].Fresh, r.got[2].Fresh)
			}
		}},
		{"after the last failure, a root listed later gives its files no history, so the tailer reads them", func(t *testing.T) {
			r := lookbackRig(t, func() (Child, error) { return Child{}, errors.New("no binary") })
			r.write("g.jsonl", "{\"g\":1}\n")
			r.open()
			r.step(0, false)
			r.settle()
			for i := range maxLookbackTries - 1 {
				r.step(lookbackPause<<i, false)
				r.settle()
			}
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "p"), 0o700); err != nil {
				t.Fatal(err)
			}
			z := filepath.Join(dir, "p", "z.jsonl")
			if err := os.WriteFile(z, []byte("{\"z\":1}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			r.tl.AddRoots([]Root{{Harness: event.HarnessClaudeCode, LogRoot: adapters.LogRoot{Dir: dir, Depth: 2,
				Match: func(rel string) bool { return strings.HasSuffix(rel, ".jsonl") }}}})
			r.step(0, false)
			fh, err := os.OpenFile(z, os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fh.WriteString("{\"z\":2}\n"); err != nil {
				t.Fatal(err)
			}
			fh.Close()
			r.step(HotPoll, false)
			r.settle()
			if got := r.texts(0); !slices.Equal(got, []string{`{"z":2}`}) || r.tl.Progress().Files != 1 {
				t.Errorf("handed over %v, progress %+v; want the new root's new line, and no history for it", got, r.tl.Progress())
			}
		}},
		{"after the last failure, a file read to its history's end without its end isn't left to lookback once it is cut", func(t *testing.T) {
			calls := 0
			r := lookbackRig(t, func() (Child, error) {
				calls++
				if calls > 1 {
					return Child{}, errors.New("no binary")
				}
				pc := &pipeChild{trim: upper, cut: 2} // a's lines, and then it crashes
				return pc.start()
			})
			r.write("a.jsonl", "{\"a\":1}\n{\"a\":2}\n")
			r.write("b.jsonl", "{\"b\":1}\n")
			earlier := r.now.Add(-time.Hour)
			if err := os.Chtimes(r.path("b.jsonl"), earlier, earlier); err != nil {
				t.Fatal(err)
			}
			r.open()
			r.step(0, false)
			r.settle()
			for i := range maxLookbackTries - 1 {
				r.step(lookbackPause<<i, false)
				r.settle()
			}
			if got := r.texts(0); calls != maxLookbackTries || !slices.Equal(got, []string{`{"A":1}`, `{"A":2}`}) {
				t.Fatalf("%d children, then handed over %v", calls, got)
			}
			// The file is cut to its first line, which keeps its place in
			// the history, and read from its start; the agent has the line
			// wait, so the file is read again after a Poke.
			waited := false
			r.take = func(Line) bool {
				if !waited {
					waited = true
					return false
				}
				return true
			}
			if err := os.Truncate(r.path("a.jsonl"), 8); err != nil {
				t.Fatal(err)
			}
			r.step(HotPoll, false)
			r.tl.Poke()
			r.step(time.Second, false)
			if got := r.texts(2); !waited || !slices.Equal(got, []string{`{"a":1}`}) {
				t.Errorf("after the cut, handed over %v, want the file read live", got)
			}
		}},
		{"a child isn't tried again before its pause", func(t *testing.T) {
			tries := 0
			r := lookbackRig(t, func() (Child, error) {
				tries++
				return Child{}, errors.New("no binary")
			})
			r.write("h.jsonl", "{\"n\":1}\n")
			r.open()
			r.step(0, false)
			r.settle()
			r.step(lookbackPause/2, false)
			r.settle()
			if tries != 1 {
				t.Errorf("%d tries within the pause", tries)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

// serve runs ServeLookback over a job, and returns what it wrote.
func serve(t *testing.T, job lookbackJob, trim Trimmer) []childLine {
	t.Helper()
	b, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := ServeLookback(bytes.NewReader(b), &out, trim); err != nil {
		t.Fatal(err)
	}
	var lines []childLine
	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		var cl childLine
		if err := json.Unmarshal(sc.Bytes(), &cl); err != nil {
			t.Fatalf("%q: %v", sc.Text(), err)
		}
		lines = append(lines, cl)
	}
	return lines
}

func TestServeLookback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.jsonl")
	text := "{\"n\":1}\n\n{\"n\":2}\n{\"n\":3}\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	h := event.HarnessClaudeCode
	refuse := func(_ event.Harness, line []byte) ([]byte, error) {
		if bytes.Contains(line, []byte("2")) {
			return nil, errors.New("not a line it reads")
		}
		return line, nil
	}
	notJSON := func(_ event.Harness, line []byte) ([]byte, error) { return []byte("not json"), nil }
	cases := []struct {
		name string
		job  lookbackJob
		trim Trimmer
		want []childLine
	}{
		{"each line from From that ends by To, trimmed, with its span and hash, then the file's end", lookbackJob{Files: []jobFile{{h, path, 0, 17}}}, upper,
			[]childLine{{F: 0, O: 0, E: 8, H: hashLine([]byte(`{"n":1}`)), L: json.RawMessage(`{"N":1}`)},
				{F: 0, O: 8, E: 17, H: hashLine([]byte(`{"n":2}`)), L: json.RawMessage(`{"N":2}`)},
				{F: 0, O: 17, End: true}, {Done: true}}},
		{"a line that runs past To isn't sent", lookbackJob{Files: []jobFile{{h, path, 8, 20}}}, upper,
			[]childLine{{F: 0, O: 8, E: 17, H: hashLine([]byte(`{"n":2}`)), L: json.RawMessage(`{"N":2}`)},
				{F: 0, O: 17, End: true}, {Done: true}}},
		{"a line its trimmer refuses goes with the trimmer's error", lookbackJob{Files: []jobFile{{h, path, 8, 17}}}, refuse,
			[]childLine{{F: 0, O: 8, E: 17, H: hashLine([]byte(`{"n":2}`)), X: "not a line it reads"},
				{F: 0, O: 17, End: true}, {Done: true}}},
		{"a trimmer that gives no JSON gives an error for the line", lookbackJob{Files: []jobFile{{h, path, 0, 8}}}, notJSON,
			[]childLine{{F: 0, O: 0, E: 8, H: hashLine([]byte(`{"n":1}`)), X: "the log reader trimmed a line to something that isn't JSON"},
				{F: 0, O: 8, End: true}, {Done: true}}},
		{"a file it can't open is gone, and the next is read", lookbackJob{Files: []jobFile{{h, filepath.Join(dir, "none.jsonl"), 0, 8}, {h, path, 17, 26}}}, upper,
			[]childLine{{F: 0, End: true, Gone: true},
				{F: 1, O: 17, E: 25, H: hashLine([]byte(`{"n":3}`)), L: json.RawMessage(`{"N":3}`)},
				{F: 1, O: 25, End: true}, {Done: true}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := serve(t, tc.job, tc.trim)
			gb, _ := json.Marshal(got)
			wb, _ := json.Marshal(tc.want)
			if !bytes.Equal(gb, wb) {
				t.Errorf("wrote\n%s\nwant\n%s", gb, wb)
			}
		})
	}
}
