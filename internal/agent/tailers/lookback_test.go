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
