// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	osclock "github.com/some-du6e/pairtime/internal/platform/clock"
	"github.com/some-du6e/pairtime/pkg/event"
)

// The enumerator's ops (TestMarkSequences), one byte each, so that a
// sequence of them is a string, as each failure names it.
const (
	opLocal   = 'f' // config.toml says local-only, and the watch notes it: a stretch begins
	opBack    = 't' // config.toml says online again, noted: the stretch ends, a switch back
	opNote    = 'n' // a stretch that begins and ends with no record made in it: f, then t
	opSeen    = 'e' // the agent saw a stretch end that the writer didn't know of (NoteSeen)
	opWrite   = 'w' // a record made now
	opLate    = 'x' // a record made in the newest stretch that ended, as a held call that came late
	opSeal    = 's' // a seal, while Waiting says no
	opHeld    = 'h' // a seal, while Waiting says yes, as for a held call whose time can't be read yet
	opDay     = 'D' // the clock moves into the next UTC day
	opSweep   = 'S' // the retention sweep by today, which drops the rows of the days before, while every record pending was made today
	opRestart = 'R' // a crash, which leaves the files as kill -9 does, and the agent's start
	opCut     = 'P' // a power cut, which takes what no flush covered, and the agent's start
)

// markAlphabet is every op, which the enumerator adds to each seed in
// every order.
const markAlphabet = "ftnewxshDSRP"

// markStep is how far each op moves the clock, in ms, so that every op's
// moments are distinct.
const markStep = 10_000

// TestMarkSequences runs the writer through every short sequence of ops
// after each seed, and judges it after every op (#359):
//
//   - no batch the uploader sends holds a record made in a stretch;
//   - no batch sealed local-only follows one sealed online in the
//     uploader's mode with no stretch begun since, which would stop the
//     batch roots until logout and login;
//   - a mark a batch took or ended never comes back;
//   - after a restart or a power cut, the mark the newest frame on disk
//     recorded is due, unless Open's own batch takes or ends it, or Open
//     reads config.toml as local-only.
//
// A mark the writer armed and no frame recorded is lost to a restart,
// unless the Seen the agent kept arms it again. The two seeds #359 named
// are its regressions, and so is the third, which a run one op deeper
// found: Open's own batch of what a crash left, sealed online, didn't end
// the mark the agent's Seen armed, so a held call's seal after it was
// local-only.
func TestMarkSequences(t *testing.T) {
	base, deep := markBase(t)
	t.Cleanup(func() { syncHook = nil })
	seeds := []struct {
		name, ops string
		// depth is how many ops the enumerator adds to the seed, in every
		// order, on tmpfs; elsewhere it adds one.
		depth int
	}{
		{"#359 item 1: a mark armed after the newest row a sweep kept, a row that ended the mark Open restored, is due after a restart, from the base frame", "fwstwhDwRnxSRws", 3},
		{"#359 item 2: a mark a batch ended stays ended after a power cut took records numbered below it and new records took their numbers", "fwstwsDwswweSPwswsRwh", 3},
		{"a mark the agent's Seen armed at Open, for a switch back no frame recorded, ends with Open's own batch of what a crash left, sealed online, so a held call's seal after it is online", "fwstwRwh", 3},
		{"installed and signed in, with the switch back's mark armed", "fwst", 4},
		{"signed in, with the first batch sealed online marked", "fwstws", 4},
		{"signed in, with the first batch after the switch back sealed local-only for a held call", "fwstwh", 4},
		{"signed in, with rows sealed online on two days", "fwstwsDws", 4},
	}
	failures := 0
	n := 0
	run := func(t *testing.T, ops string) (int, bool) {
		n++
		dir := filepath.Join(base, fmt.Sprint(n))
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(dir)
		return runMarks(t, dir, ops)
	}
	for _, s := range seeds {
		t.Run(s.name, func(t *testing.T) {
			ran, failed := run(t, s.ops)
			if failed {
				failures++
				return
			}
			if ran < len(s.ops) {
				t.Fatalf("op %d of the seed %q is one its state doesn't allow", ran+1, s.ops)
			}
			depth := s.depth
			if !deep {
				depth = 1
			}
			// A prefix that has an op its state doesn't allow, or failed, is
			// dead, and so is every sequence it begins.
			var dead []string
			runs, all := 0, sequences(markAlphabet, depth)
		next:
			for _, seq := range all {
				ops := s.ops + seq
				for _, d := range dead {
					if strings.HasPrefix(ops, d) {
						continue next
					}
				}
				ran, failed := run(t, ops)
				runs++
				switch {
				case failed:
					dead = append(dead, ops[:ran])
					if failures++; failures >= 20 {
						t.Fatalf("stopping after %d failures", failures)
					}
				case ran < len(ops):
					dead = append(dead, ops[:ran+1])
				}
			}
			t.Logf("of the %d sequences of %d ops after %q, %d ran", len(all), depth, s.ops, runs)
		})
	}
}

// markBase is where the enumerator's ledgers go, and whether it adds more
// than one op to each seed: on Linux, a directory on tmpfs, where a flush
// writes nothing to a disk, and elsewhere, as on platform.yml's runners,
// the test's own.
func markBase(t *testing.T) (string, bool) {
	if runtime.GOOS == "linux" {
		if dir, err := os.MkdirTemp("/dev/shm", "pairtime-marks-"); err == nil {
			t.Cleanup(func() { os.RemoveAll(dir) })
			return dir, true
		}
	}
	return t.TempDir(), false
}

// sequences is every string of n bytes of alphabet.
func sequences(alphabet string, n int) []string {
	out := []string{""}
	for range n {
		var longer []string
		for _, s := range out {
			for i := range len(alphabet) {
				longer = append(longer, s+alphabet[i:i+1])
			}
		}
		out = longer
	}
	return out
}

// runMarks runs ops on a new rig in dir. It stops at the first op the
// state doesn't allow, or after the first that fails, and reports how many
// ops it ran and whether one failed.
func runMarks(t *testing.T, dir, ops string) (ran int, failed bool) {
	t.Helper()
	r := newMarkRig(t, dir)
	defer func() {
		if r.w != nil {
			abandon(r.w)
		}
	}()
	for i := range len(ops) {
		if r.failed {
			return i, true
		}
		if !r.allowed(ops[i]) {
			return i, false
		}
		r.do(ops[i])
	}
	return len(ops), r.failed
}

// markRig is a ledger the enumerator drives, whose writer seals only when
// an op seals: its goroutine is stopped, so the kick of a note or of a
// stretch's first record seals nothing (Writer.loop), as when other ops
// come before the writer's own seal. It keeps how much of each file a
// flush covered, which a power cut can't take (syncHook), every batch
// sealed, when each record was made, and what the ops alone say of the
// mark and the stretches (markModel).
type markRig struct {
	t     *testing.T
	dir   string
	clk   *clock
	ms    int64 // the clock, in ms after testT
	file  bool  // config.toml says online
	waits bool  // Waiting says yes
	// seen is what the agent saw of config.toml, as state.json keeps it
	// across a restart (Options.Seen).
	seen Seen
	// onlineAt is the newest moment the writer knew config.toml said
	// online, in ms after testT: a seal's or Open's online read, or a
	// stretch's end, where a stretch the writer learns of begins
	// (Writer.learned).
	onlineAt int64
	w        *Writer
	rng      *rand.Rand
	flushed  map[string]int64 // how much of each file a flush covered, by name
	made     map[uint64]int64 // when the record of each seq was made, in ms after testT
	batches  []Batch          // every batch sealed, oldest first
	settled  int              // how many of batches settle took in
	ops      string           // the ops so far
	m        markModel
	failed   bool
}

// markModel is what the ops alone say of the mark and the stretches, by
// which the enumerator judges the writer.
type markModel struct {
	// stretches are the local-only stretches the ops made, [from, to] in
	// ms after testT: from -1 for the chain's first, which begins at the
	// beginning of time, and to -1 while one goes on.
	stretches [][2]int64
	// armed is set while a mark the ops armed is due: a switch back, or a
	// Seen of a newer end, arms one, and a stretch's beginning, a seal or
	// an Open that reads config.toml as local-only, or a batch that takes
	// the mark (Back) or ends it (back_ended) clears it. durable is armed as
	// the newest frame on disk recorded it: a seal frame, or the base frame
	// of a sweep that wrote batches.seg again. consumed is set once a batch
	// took or ended the mark, until an op arms one again.
	armed, durable, consumed bool
	// online is set once a batch was sealed online, and began while a
	// stretch began after the newest one.
	online, began bool
}

// newMarkRig opens a rig's writer on dir, config.toml saying online.
func newMarkRig(t *testing.T, dir string) *markRig {
	r := &markRig{t: t, dir: filepath.Clean(dir), clk: newClock(testT), file: true, rng: rand.New(rand.NewPCG(359, 3)),
		flushed: map[string]int64{}, made: map[uint64]int64{}}
	syncHook = func(name string, size int64) {
		if filepath.Dir(name) == r.dir {
			r.flushed[filepath.Base(name)] = size
		}
	}
	if r.open() {
		// The new chain's batches.seg was written whole, by atomic rename.
		r.durableBatches()
	}
	return r
}

// open opens the writer, as the agent starts, and stops its goroutine,
// and reports whether Open succeeded. Open seals its own batch of what a
// crash left meanwhile.
func (r *markRig) open() bool {
	w, err := openErr(r.dir, r.clk, func(o *Options) {
		o.Online = func() bool { return r.file }
		o.Seen = func() Seen { return r.seen }
		o.Sealed = func(b Batch) { r.batches = append(r.batches, b) }
		o.Waiting = func(func(event.Envelope) bool) bool { return r.waits }
		o.Clock = func() osclock.Stamp {
			now := r.clk.now()
			return osclock.Stamp{Wall: now, Mono: now.UnixMilli() - testT + 1000, BootID: lateBoot}
		}
	})
	if err != nil {
		r.w = nil
		r.fail("Open: %v", err)
		return false
	}
	r.w = w
	w.stopOnce.Do(func() { close(w.stop) })
	<-w.done
	if r.file {
		r.onlineAt = r.ms // Open's seal read config.toml as online
	}
	return true
}

// durableBatches takes batches.seg as it is on disk as flushed whole, as
// after a write by atomic rename (home.WriteFile), which syncHook doesn't
// hear.
func (r *markRig) durableBatches() {
	if fi, err := os.Stat(filepath.Join(r.dir, batchesName)); err == nil {
		r.flushed[batchesName] = fi.Size()
	}
}

// tick moves the clock on by one op.
func (r *markRig) tick() {
	r.ms += markStep
	r.clk.set(testT + r.ms)
}

// allowed reports whether the state allows op: a stretch begins only while
// none goes on, and ends only while one does, the agent sees a stretch end
// only while config.toml says online, a record is made late only in a
// stretch that ended, and the sweep by today runs only while every record
// the writer holds without a batch was made today. The agent's sweep cuts
// 180 days back, and the writer seals what it holds within a minute, so no
// record still waiting for its batch was made before the cut, which folds
// the stretches before it into one (Writer.foldStretches).
func (r *markRig) allowed(op byte) bool {
	switch op {
	case opLocal, opNote, opSeen:
		return r.file
	case opBack:
		return !r.file
	case opLate:
		_, ok := r.late()
		return ok
	case opSweep:
		return !r.pendingBefore(cutOf(dayOf(r.clk.now())))
	}
	return true
}

// pendingBefore reports whether the writer holds a record without a batch
// that was made before cut, in Unix ms.
func (r *markRig) pendingBefore(cut int64) bool {
	if r.w == nil {
		return false
	}
	r.w.mu.Lock()
	defer r.w.mu.Unlock()
	for _, p := range r.w.pending {
		if p.has && p.env.T < cut {
			return true
		}
	}
	return false
}

// do runs op, which the state allows, and judges the writer after it.
func (r *markRig) do(op byte) {
	r.t.Helper()
	r.ops += string(op)
	switch op {
	case opLocal:
		r.tick()
		r.noteLocal()
	case opBack:
		r.tick()
		r.noteBack()
	case opNote:
		r.tick()
		r.noteLocal()
		r.tick()
		r.noteBack()
	case opSeen:
		r.tick()
		r.sawEnd()
	case opWrite:
		r.tick()
		r.write(r.ms)
	case opLate:
		at, _ := r.late()
		r.tick()
		r.write(at)
	case opSeal, opHeld:
		r.tick()
		r.waits = op == opHeld
		_, _, err := r.w.Seal()
		r.waits = false
		if err != nil {
			r.fail("Seal: %v", err)
			return
		}
		if r.file {
			r.onlineAt = r.ms
		} else {
			r.m.armed = false // the seal read config.toml as local-only
		}
	case opDay:
		day := time.UnixMilli(testT + r.ms).UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
		r.ms = day.UnixMilli() - testT + 1000
		r.clk.set(testT + r.ms)
	case opSweep:
		r.tick()
		r.sweep()
	case opRestart, opCut:
		r.tick()
		r.restart(op == opCut)
	default:
		r.t.Fatalf("no op %q", op)
	}
	if r.failed || r.w == nil {
		return
	}
	r.settle()
	r.check()
}

// noteLocal has config.toml say local-only, and the watch note it: a
// stretch begins, the chain's first at the beginning of time
// (Writer.sawLocal), and the mark is cleared.
func (r *markRig) noteLocal() {
	r.file, r.seen.Local = false, true
	r.w.NoteMode(false)
	from := r.ms
	if len(r.m.stretches) == 0 {
		from = -1
	}
	r.m.stretches = append(r.m.stretches, [2]int64{from, -1})
	r.m.armed, r.m.began = false, true
}

// noteBack has config.toml say online again, and the watch note it, as at
// a pairtime login: the stretch ends there, the agent keeps the end
// (Options.Seen), and the switch back arms the mark (Writer.noteLocked).
func (r *markRig) noteBack() {
	r.file = true
	end := r.clk.now()
	r.seen = Seen{Ended: end, OS: osclock.Stamp{Wall: end, Mono: r.ms + 1000, BootID: lateBoot}}
	r.w.NoteMode(true)
	r.m.stretches[len(r.m.stretches)-1][1] = r.ms
	r.onlineAt = r.ms
	r.m.armed, r.m.consumed = true, false
}

// sawEnd has the agent see a stretch end the writer didn't know of, as one
// it saw while the ledger stayed shut: the stretch began just after the
// newest moment the writer knew config.toml said online and ended a moment
// later, before any record made since, so the writer takes the end as
// newer, learns the stretch, and arms the mark if none is due
// (Writer.seenLocked).
func (r *markRig) sawEnd() {
	from, to := r.onlineAt+1, r.onlineAt+2
	end := time.UnixMilli(testT + to).UTC()
	r.seen = Seen{Ended: end, OS: osclock.Stamp{Wall: end, Mono: to + 1000, BootID: lateBoot}}
	r.w.NoteSeen(r.seen)
	r.m.stretches = append(r.m.stretches, [2]int64{from, to})
	r.onlineAt = to
	r.m.began = true
	if !r.m.armed {
		r.m.armed, r.m.consumed = true, false
	}
}

// late is a moment in the newest stretch that ended, in ms after testT,
// as a record made in it has.
func (r *markRig) late() (int64, bool) {
	for i := len(r.m.stretches) - 1; i >= 0; i-- {
		switch s := r.m.stretches[i]; {
		case s[1] < 0:
		case s[0] < 0:
			return s[1] - 1000, true
		default:
			return (s[0] + s[1]) / 2, true
		}
	}
	return 0, false
}

// write writes a record made at ms, an os.presence judged by its t alone,
// and keeps when it was made. One of a stretch a batch sealed online has
// passed is dropped (ErrLateLocal).
func (r *markRig) write(ms int64) {
	in := sampleInput(r.rng, "os.presence", testT+ms, "", "")
	in.Event.Mono = 0
	s, err := r.w.Write(in)
	switch {
	case errors.Is(err, ErrLateLocal):
	case err != nil:
		r.fail("Write: %v", err)
	default:
		r.made[s] = ms
	}
}

// sweep runs the retention sweep by today, which drops the rows whose
// batch's first record is in a segment of a day before (allowed). When it
// drops any, it writes batches.seg again, whole and flushed, with a base
// frame that records the mark.
func (r *markRig) sweep() {
	name := filepath.Join(r.dir, batchesName)
	was, err := os.Stat(name)
	if err != nil {
		r.t.Fatal(err)
	}
	if _, _, err := r.w.Sweep(dayOf(r.clk.now()), 0); err != nil {
		r.fail("Sweep: %v", err)
		return
	}
	now, err := os.Stat(name)
	if err != nil {
		r.t.Fatal(err)
	}
	if !os.SameFile(was, now) {
		r.durableBatches()
		r.m.durable = r.m.armed
	}
	for f := range r.flushed {
		if _, err := os.Lstat(filepath.Join(r.dir, f)); errors.Is(err, fs.ErrNotExist) {
			delete(r.flushed, f) // a segment the sweep deleted
		}
	}
}

// restart leaves the ledger as a crash does, or with cut as a power cut
// then does, and opens it again as the agent starts, with the Seen
// state.json kept. The mark the newest frame recorded is due again,
// unless Open's own batch takes it or ends it, or Open reads config.toml
// as local-only.
func (r *markRig) restart(cut bool) {
	abandon(r.w)
	r.w = nil
	if cut {
		r.cut()
	}
	if !r.seen.Ended.IsZero() || r.seen.Local {
		r.seen.Earlier = true
	}
	r.m.armed = r.m.durable && r.file
	if !r.open() {
		return
	}
	r.settle()
	if r.failed {
		return
	}
	due := r.due()
	if r.m.armed && due == 0 {
		r.fail("the mark the newest frame recorded isn't due after Open")
		return
	}
	// A Seen the agent kept can arm a mark no frame recorded.
	r.m.armed = due != 0
}

// cut does to the ledger what a power cut does after a crash: each file
// keeps what a flush covered and loses the rest (syncHook), so records no
// batch holds and rows written since batches.seg's last flush go.
func (r *markRig) cut() {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), segSuffix) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			r.t.Fatal(err)
		}
		if keep := r.flushed[e.Name()]; fi.Size() > keep {
			if err := os.Truncate(filepath.Join(r.dir, e.Name()), keep); err != nil {
				r.t.Fatal(err)
			}
		}
	}
}

// settle takes in the batches sealed since it last ran, oldest first:
// each is judged, and what it did to the mark, by its row's back_ended
// too, goes into the model.
func (r *markRig) settle() {
	if r.settled == len(r.batches) {
		return
	}
	bf, err := readBatchesFile(filepath.Join(r.dir, batchesName))
	if err != nil {
		r.fail("reading batches.seg: %v", err)
		return
	}
	for _, b := range r.batches[r.settled:] {
		row, ok := bf.row(b.Row.B)
		if !ok {
			r.fail("batch %d has no row", b.Row.B)
			return
		}
		if b.Online {
			r.m.online, r.m.began = true, false
		} else if r.m.online && !r.m.began {
			r.fail("batch %d was sealed local-only after a batch sealed online, with no stretch begun since: the roots stop until logout and login", b.Row.B)
			return
		}
		if b.Back || row.BackEnded {
			r.m.armed, r.m.consumed = false, true
		}
		r.m.durable = r.m.armed
	}
	r.settled = len(r.batches)
}

// check judges the writer after an op: no batch the uploader sends holds a
// record made in a stretch, and no mark a batch took or ended is due
// again.
func (r *markRig) check() {
	r.privacy()
	if due := r.due(); r.m.consumed && due != 0 && !r.failed {
		r.fail("the mark a batch took or ended is due again, from record %d", due)
	}
}

// privacy checks that no batch the uploader would send holds a record made
// in a stretch, by the uploader's mode rule (saved.mode): a batch sealed
// local-only opens a run, the first batch sealed online after it closes
// the run with itself inside, unless it is marked (Back), and the run open
// at the end holds every batch from its first on. Neither a run's roots nor
// its minutes ever go (D-local-gap).
func (r *markRig) privacy() {
	run := false
	for _, b := range r.batches {
		sent := false
		switch {
		case !b.Online:
			run = true
		case run:
			sent, run = b.Back, false
		default:
			sent = true
		}
		if !sent {
			continue
		}
		for s := b.Row.FirstSeq; s < b.Row.FirstSeq+b.Row.N; s++ {
			at, ok := r.made[s]
			if !ok {
				r.fail("record %d of batch %d is no record an op made", s, b.Row.B)
				return
			}
			if in, ok := r.inStretch(at); ok {
				r.fail("batch %d, which the uploader sends, holds record %d, made at %d ms in the stretch %v", b.Row.B, s, at, in)
				return
			}
		}
	}
}

// inStretch returns the stretch the ops made that holds ms, if one does.
func (r *markRig) inStretch(ms int64) ([2]int64, bool) {
	for _, s := range r.m.stretches {
		if (s[0] < 0 || ms >= s[0]) && (s[1] < 0 || ms <= s[1]) {
			return s, true
		}
	}
	return [2]int64{}, false
}

// due is the writer's mark, the first record of the batch it is due for,
// 0 for none (Writer.back).
func (r *markRig) due() uint64 {
	r.w.mu.Lock()
	defer r.w.mu.Unlock()
	return r.w.back
}

// fail fails the test once for the sequence, with what the ops made.
func (r *markRig) fail(format string, args ...any) {
	r.t.Helper()
	if r.failed {
		return
	}
	r.failed = true
	r.t.Errorf("after %q: %s\n%s", r.ops, fmt.Sprintf(format, args...), r.story())
}

// story tells what the ops made, for a failure: the stretches, the base
// frame, and every batch, with its row's mark and its records' times.
func (r *markRig) story() string {
	var b strings.Builder
	fmt.Fprintf(&b, "  stretches %v; the model's mark armed %v, durable %v, consumed %v", r.m.stretches, r.m.armed, r.m.durable, r.m.consumed)
	bf, err := readBatchesFile(filepath.Join(r.dir, batchesName))
	if err == nil {
		base, _ := json.Marshal(bf.base)
		fmt.Fprintf(&b, "\n  base frame %s", base)
	}
	for _, x := range r.batches {
		mode := "local-only"
		if x.Online {
			mode = "online"
		}
		fmt.Fprintf(&b, "\n  batch %d, records %d to %d, %s", x.Row.B, x.Row.FirstSeq, x.Row.FirstSeq+x.Row.N-1, mode)
		if x.Back {
			b.WriteString(", marked")
		}
		if err == nil {
			if row, ok := bf.row(x.Row.B); ok {
				fmt.Fprintf(&b, ", due %d", row.Due)
				if row.BackEnded {
					b.WriteString(", back_ended")
				}
			}
		}
		b.WriteString(", made at")
		for s := x.Row.FirstSeq; s < x.Row.FirstSeq+x.Row.N; s++ {
			fmt.Fprintf(&b, " %d", r.made[s])
		}
	}
	return b.String()
}
