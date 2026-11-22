// SPDX-License-Identifier: AGPL-3.0-or-later

package tailers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// Lookback reads what the harnesses logged before pairtime did: the
// history of every file a root's first listing took up, the lines before
// where it took it up, from LookbackWindow before then on (SM 2.8; DESIGN
// 5.9 step 9; WP-AD3). A child process reads the files, at the lowest CPU
// and I/O priority its OS gives (internal/platform), and sends each line
// trimmed to what its adapter reads (adapters.LogReader.TrimLog), so the
// agent's own share is the decoding of what it records, at its own
// priority, and its memory stays its own.
//
// The lines come to the agent as the live ones do, through Deliver, in
// file order, so a file's adapter reads its history first and then what
// comes after it: the tailer reads none of a file live until its history
// is in. Each history line carries Line.Lookback, the moment its records
// may start at, and the agent keeps only the records that fall before its
// ledger began, which a hook may have reported since (ledger.ChainInfo).
// The cursor moves with each line taken, so a restart goes on where it
// was, the lines its adapter needs again coming as replays. A child that
// fails is started again after a pause, up to maxLookbackTries times in a
// run of the agent; after that what is left of each history stays unread,
// and the tailer reads each file on after it.

// LookbackWindow is how far back lookback reads, from when the tailer took
// a file up: 30 days (SM 2.8).
const LookbackWindow = 30 * 24 * time.Hour

// maxLookbackTries bounds the children started in one run of the agent,
// and lookbackPause is the pause before the next, doubled each time.
const (
	maxLookbackTries = 3
	lookbackPause    = time.Minute
)

// maxJobBytes bounds the job a child reads.
const maxJobBytes = 16 << 20

// Child is the lookback reader's child process, as Options.Lookback
// started it: its standard input, which takes the job, its standard
// output, which gives the lines, how to wait for it to exit, and how to
// stop it.
type Child struct {
	In   io.WriteCloser
	Out  io.ReadCloser
	Wait func() error
	Kill func()
}

// Progress is how far lookback has read: the files with a history, and
// their history's bytes, all and done.
type Progress struct {
	Files, FilesDone int
	Bytes, BytesDone int64
	// Running says a child is reading now. Failures counts the children
	// that failed in this run of the agent, and Err says why the last did.
	Running  bool
	Failures int
	Err      string
}

// Done says every history was read, or is left unread for good.
func (p Progress) Done() bool { return !p.Running && p.FilesDone == p.Files }

// lookbackJob is what the agent writes to a child's standard input.
type lookbackJob struct {
	Files []jobFile `json:"files"`
}

// jobFile is one file to read: its lines from From that end by To.
type jobFile struct {
	Harness event.Harness `json:"h"`
	Path    string        `json:"p"`
	From    int64         `json:"from"`
	To      int64         `json:"to"`
}

// childLine is one line of a child's standard output: a line of file F
// from O to E, with its Hash H and its trimmed bytes L, or the error X its
// adapter gave, or marked too long; or the end of file F's lines, with
// Gone set when the child couldn't read it; or, last of all, Done.
type childLine struct {
	F    int             `json:"f"`
	O    int64           `json:"o,omitempty"`
	E    int64           `json:"e,omitempty"`
	H    string          `json:"h,omitempty"`
	L    json.RawMessage `json:"l,omitempty"`
	X    string          `json:"x,omitempty"`
	Long bool            `json:"long,omitempty"`
	End  bool            `json:"end,omitempty"`
	Gone bool            `json:"gone,omitempty"`
	Done bool            `json:"done,omitempty"`
}

// Trimmer trims a line of a harness's log (adapters.LogReader.TrimLog).
type Trimmer func(h event.Harness, line []byte) ([]byte, error)

// ServeLookback is the lookback reader's child process, which `pairtime
// lookback` runs once it has lowered its priority: it reads the job from
// in and writes each line of each file's range to out, trimmed, in the
// tailer's order and with its spans, so the lines it sends are the lines
// the tailer itself would hand over. It returns once every file is done,
// or when out fails, as when the agent that started it is gone.
func ServeLookback(in io.Reader, out io.Writer, trim Trimmer) error {
	var job lookbackJob
	if err := json.NewDecoder(io.LimitReader(in, maxJobBytes)).Decode(&job); err != nil {
		return fmt.Errorf("lookback: the job doesn't parse: %w", err)
	}
	bw := bufio.NewWriterSize(out, readBuffer)
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false)
	for i, jf := range job.Files {
		if err := serveFile(i, jf, enc, trim); err != nil {
			return err
		}
		if err := bw.Flush(); err != nil {
			return err
		}
	}
	if err := enc.Encode(childLine{Done: true}); err != nil {
		return err
	}
	return bw.Flush()
}

// serveFile writes the lines of one file of the job, and its end.
func serveFile(i int, jf jobFile, enc *json.Encoder, trim Trimmer) error {
	fh, err := openLog(jf.Path)
	if err != nil {
		return enc.Encode(childLine{F: i, End: true, Gone: true})
	}
	defer fh.Close()
	if _, err := fh.Seek(jf.From, io.SeekStart); err != nil {
		return enc.Encode(childLine{F: i, End: true, Gone: true})
	}
	br := bufio.NewReaderSize(fh, readBuffer)
	start, pos := jf.From, jf.From
	for pos < jf.To {
		line, n, tooLong, err := readLine(br)
		if err != nil || pos+n > jf.To {
			// The file ends before To, or a line runs past it: the file
			// isn't what the tailer took up, and the live reads sort out
			// what it is now (relocate).
			break
		}
		pos += n
		body := bytes.TrimRight(line, "\r\n")
		if !tooLong && len(bytes.TrimSpace(body)) == 0 {
			continue
		}
		cl := childLine{F: i, O: start, E: pos, Long: tooLong}
		if !tooLong {
			cl.H = hashLine(body)
			if b, err := trim(jf.Harness, body); err != nil {
				cl.X = err.Error()
			} else {
				cl.L = b
			}
		}
		if err := enc.Encode(cl); err != nil {
			if !errors.As(err, new(*json.MarshalerError)) {
				return err
			}
			// A trimmer that gave no JSON: the line can't be read.
			cl.L, cl.X = nil, "the log reader trimmed a line to something that isn't JSON"
			if err := enc.Encode(cl); err != nil {
				return err
			}
		}
		start = pos
	}
	return enc.Encode(childLine{F: i, O: start, End: true})
}

// inHistory says f's history is still to be read: the tailer leaves f to
// lookback. t.mu is held.
func (f *file) inHistory() bool { return f.hist > 0 && !f.histDone && f.read < f.hist }

// lookbackDue starts lookback when files wait for their history and no
// child reads, unless the last one failed less than its pause ago.
func (t *Tailer) lookbackDue(now time.Time) {
	if t.opts.Lookback == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lb.running || t.stopped || now.Before(t.lb.next) {
		return
	}
	for _, f := range t.files {
		if f.inHistory() {
			t.lb.running = true
			t.lb.wg.Add(1)
			go t.runLookback()
			return
		}
	}
}

// lookbackState is what the tailer keeps of lookback in this run of the
// agent.
type lookbackState struct {
	running  bool
	failures int
	err      string
	// next is when a child may start again after one failed.
	next time.Time
	// child is the one reading now, poke wakes a runner whose lines the
	// agent didn't all take, and wg is the runner.
	child *Child
	poke  chan struct{}
	wg    sync.WaitGroup
}

// progress returns how far lookback has read. t.mu is held.
func (t *Tailer) progress() Progress {
	p := Progress{Running: t.lb.running, Failures: t.lb.failures, Err: t.lb.err}
	for _, f := range t.files {
		if f.hist == 0 {
			continue
		}
		p.Files++
		p.Bytes += f.hist
		if f.histDone || f.read >= f.hist {
			p.FilesDone++
			p.BytesDone += f.hist
		} else {
			p.BytesDone += f.read
		}
	}
	return p
}

// Progress returns how far lookback has read.
func (t *Tailer) Progress() Progress {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.progress()
}

// report hands the progress to Options.Progress.
func (t *Tailer) report() {
	if t.opts.Progress == nil {
		return
	}
	t.mu.Lock()
	p := t.progress()
	t.mu.Unlock()
	t.opts.Progress(p)
}

// runLookback runs one child over the files that wait for their history,
// written last first, so a session that is being written is read on soon.
func (t *Tailer) runLookback() {
	defer t.lb.wg.Done()
	err := t.lookbackOnce()
	t.mu.Lock()
	t.lb.running, t.lb.child = false, nil
	if err != nil && !t.stopped {
		t.lb.failures++
		t.lb.err = err.Error()
		t.lb.next = t.opts.Now().Add(lookbackPause << (t.lb.failures - 1))
		t.opts.Log.Warn("tailers: lookback's reader failed", "err", err, "failures", t.lb.failures)
		if t.lb.failures >= maxLookbackTries {
			// What is left of each history stays unread, and the tailer
			// reads on after it, at the agent's priority; the file's
			// adapter starts afresh there.
			for _, f := range t.files {
				if f.inHistory() {
					f.read, f.resume, f.pos, f.hash = f.hist, f.hist, f.hist, ""
					f.histDone, f.due = true, true
				}
			}
		}
	}
	t.mu.Unlock()
	t.report()
	t.wake()
}

// restartError says a file was rewound while its history was read, so the
// job starts again from the cursors. It is a type rather than an
// errors.New value, so the package does no work at init: the hook's
// binary links it.
type restartError struct{}

func (restartError) Error() string { return "tailers: a file was rewound during lookback" }

func (t *Tailer) lookbackOnce() error {
	for {
		err := t.lookbackJob()
		if _, again := err.(restartError); !again {
			return err
		}
	}
}

// lookbackJob runs one child over the files that wait now.
func (t *Tailer) lookbackJob() error {
	t.mu.Lock()
	var files []*file
	for _, f := range t.files {
		if f.inHistory() {
			files = append(files, f)
		}
	}
	slices.SortFunc(files, func(a, b *file) int { return b.mtime.Compare(a.mtime) })
	job := lookbackJob{Files: make([]jobFile, len(files))}
	reads := make([]int64, len(files))
	for i, f := range files {
		job.Files[i] = jobFile{Harness: f.harness, Path: f.path, From: f.pos, To: f.hist}
		reads[i] = f.read
	}
	t.mu.Unlock()
	if len(files) == 0 {
		return nil
	}
	child, err := t.opts.Lookback()
	if err != nil {
		return fmt.Errorf("starting lookback's reader: %w", err)
	}
	t.mu.Lock()
	t.lb.child = &child
	stopped := t.stopped
	t.mu.Unlock()
	done := false
	var waited sync.Once
	var waitErr error
	wait := func() error {
		waited.Do(func() { waitErr = child.Wait() })
		return waitErr
	}
	defer func() {
		if !done {
			child.Kill()
		}
		child.Out.Close()
		wait()
	}()
	if stopped {
		return nil
	}
	b, err := json.Marshal(job)
	if err != nil {
		return err
	}
	if _, err := child.In.Write(b); err != nil {
		child.In.Close()
		return fmt.Errorf("writing lookback's job: %w", err)
	}
	child.In.Close()
	t.report()

	br := bufio.NewReaderSize(child.Out, readBuffer)
	var batch Batch
	var batchFile *file
	bytesIn := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		b := batch
		batch, bytesIn = nil, 0
		if err := t.deliverHistory(batchFile, b); err != nil {
			return err
		}
		t.report()
		return nil
	}
	for {
		raw, _, tooLong, err := readLine(br)
		if err != nil {
			// What it sent before it stopped is the file's lines all the
			// same, and the next child goes on after them.
			if ferr := flush(); ferr != nil {
				return ferr
			}
			if werr := wait(); werr != nil {
				err = werr
			}
			return fmt.Errorf("lookback's reader stopped before it was done: %w", err)
		}
		var cl childLine
		if tooLong || json.Unmarshal(raw, &cl) != nil || cl.F < 0 || cl.F >= len(files) {
			return errors.New("lookback's reader sent a line that isn't one of its lines")
		}
		if cl.Done {
			if err := flush(); err != nil {
				return err
			}
			done = true
			return nil
		}
		f := files[cl.F]
		if f != batchFile {
			if err := flush(); err != nil {
				return err
			}
			batchFile = f
		}
		if cl.End {
			if err := flush(); err != nil {
				return err
			}
			t.mu.Lock()
			f.histDone, f.due = true, true
			t.mu.Unlock()
			t.wake()
			continue
		}
		l := Line{Harness: f.harness, Lookback: f.histFrom}
		l.Log.File, l.Log.Path, l.Log.Offset, l.Log.End = f.key, f.path, cl.O, cl.E
		l.Log.Replay, l.Log.TooLong = cl.E <= reads[cl.F], cl.Long
		if !cl.Long {
			l.Hash = cl.H
			if cl.X != "" {
				l.Log.DecodeErr = errors.New(cl.X)
			} else {
				l.Bytes = cl.L
			}
		}
		batch = append(batch, l)
		bytesIn += len(l.Bytes)
		if len(batch) >= batchLines || bytesIn >= batchBytes {
			if err := flush(); err != nil {
				return err
			}
		}
	}
}

// deliverHistory hands a batch of f's history to the agent, and waits for
// a Poke while the agent leaves some untaken. A Rewind of f starts the job
// again, and the tailer's stop ends it.
func (t *Tailer) deliverHistory(f *file, b Batch) error {
	for {
		t.mu.Lock()
		known := t.files[f.key] == f
		t.mu.Unlock()
		if !known {
			// Gone, and forgotten: its adapter has no use for the rest.
			return nil
		}
		n := t.opts.Deliver(b)
		if n >= len(b) {
			return nil
		}
		b = b[n:]
		t.mu.Lock()
		rewound, stopped := f.rewound, t.stopped
		f.rewound = false
		t.mu.Unlock()
		switch {
		case stopped:
			return nil
		case rewound:
			return restartError{}
		}
		select {
		case <-t.lb.poke:
		case <-t.stop:
			return nil
		}
	}
}
