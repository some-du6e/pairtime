// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bufio"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/ledger"
)

// The crash loop's child reads where to write, and its seed, from these.
const (
	crashDirEnv  = "PAIRTIME_LEDGER_CRASH_DIR"
	crashSeedEnv = "PAIRTIME_LEDGER_CRASH_SEED"
)

// crashKills is how many times TestCrashLoop kills the writer (docs/PLAN.md,
// WP-LG5).
const crashKills = 200

// TestCrashLoopChild is the writer TestCrashLoop kills: it opens the ledger
// TestCrashLoop names, recovering what the last kill left, and then writes
// records of every kind and seals now and then until it is killed. It
// reports on stdout when it starts to open the ledger and when it has, each
// flush just before it makes it, and each sealed batch. It skips unless
// TestCrashLoop started it.
func TestCrashLoopChild(t *testing.T) {
	dir := os.Getenv(crashDirEnv)
	if dir == "" {
		t.Skip("TestCrashLoop runs this as the writer it kills")
	}
	seed, err := strconv.ParseUint(os.Getenv(crashSeedEnv), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	// One write call per line, so lines from two goroutines don't mix and
	// a kill leaves at most a cut-off last line. A flush's line ends in a
	// dot, so a cut-off one, whose flush was never made, doesn't count.
	out := os.Stdout
	fail := func(err error) {
		fmt.Fprintf(out, "error %v\n", err)
		os.Exit(3)
	}
	keys, err := ledger.DeriveKeys(testKDev)
	if err != nil {
		fail(err)
	}
	syncHook = func(name string, size int64) { fmt.Fprintf(out, "sync %s %d .\n", filepath.Base(name), size) }
	fmt.Fprintln(out, "opening")
	w, err := Open(Options{Dir: dir, Keys: keys, KeyID: testKeyID, Sealed: func(b Batch) {
		fmt.Fprintf(out, "sealed %d %s\n", b.Row.B, b.Row.Head)
	}})
	if err != nil {
		fail(err)
	}
	fmt.Fprintln(out, "ready")
	rng := rand.New(rand.NewPCG(seed, 0x5eed))
	for {
		if _, _, err := write(w, rng, kinds[rng.IntN(len(kinds))], time.Now().UnixMilli()); err != nil {
			fail(err)
		}
		if rng.IntN(16) == 0 {
			if _, _, err := w.Seal(); err != nil {
				fail(err)
			}
		}
		if rng.IntN(4) == 0 {
			time.Sleep(time.Duration(rng.IntN(300)) * time.Microsecond)
		}
	}
}

// durability is how much of each file in the ledger directory a flush
// covered, by name, from the flushes the writers report just before they
// make them (syncHook). A power cut can take away only what comes after.
// A flush reported and never made only makes the loop's power cuts
// gentler, so the sizes only grow: a cut, which flushes too, only ever
// cuts a torn tail that no flush covered.
type durability struct {
	mu   sync.Mutex
	size map[string]int64
}

func (d *durability) flushed(name string, size int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	base := filepath.Base(name)
	d.size[base] = max(d.size[base], size)
}

func (d *durability) of(name string) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.size[filepath.Base(name)]
}

// child is one run of TestCrashLoopChild.
type child struct {
	cmd     *exec.Cmd
	opening chan struct{} // closed once it starts to open the ledger, or ends
	ready   chan struct{} // closed once it has opened it, or ends
	lines   chan string   // everything it printed, closed at EOF
	sealed  map[uint64]ledger.Hash
	began   bool // it said it was opening the ledger
	opened  bool // it said it had opened the ledger
	errors  []string
}

func startChild(t *testing.T, dir string, seed int) *child {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashLoopChild$", "-test.count=1")
	cmd.Env = append(os.Environ(), crashDirEnv+"="+dir, crashSeedEnv+"="+strconv.Itoa(seed))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &child{cmd: cmd, opening: make(chan struct{}), ready: make(chan struct{}), lines: make(chan string, 1<<14),
		sealed: map[uint64]ledger.Hash{}}
	go func() {
		defer close(c.lines)
		sc := bufio.NewScanner(stdout)
		opening, ready := false, false
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "opening" && !opening:
				opening = true
				close(c.opening)
			case line == "ready" && !ready:
				ready = true
				close(c.ready)
			}
			c.lines <- line
		}
		// It ended without saying so, and stop says why.
		if !opening {
			close(c.opening)
		}
		if !ready {
			close(c.ready)
		}
	}()
	return c
}

// stop kills the child and collects what it reported: the flushes it made,
// which d keeps, the batches it saw sealed, which were on disk before it
// said so, and any error.
func (c *child) stop(t *testing.T, d *durability) {
	t.Helper()
	c.cmd.Process.Kill()
	for line := range c.lines {
		f := strings.Fields(line)
		switch {
		case len(f) == 4 && f[0] == "sync" && f[3] == ".":
			if size, err := strconv.ParseInt(f[2], 10, 64); err == nil {
				d.flushed(f[1], size)
			}
		case len(f) == 3 && f[0] == "sealed":
			b, err := strconv.ParseUint(f[1], 10, 64)
			var h ledger.Hash
			if err == nil && h.UnmarshalText([]byte(f[2])) == nil {
				c.sealed[b] = h
			}
		case line == "opening":
			c.began = true
		case line == "ready":
			c.opened = true
		case len(f) > 0 && f[0] == "error":
			c.errors = append(c.errors, line)
		}
	}
	c.cmd.Wait()
}

// TestCrashLoop kills the writer at random points, 200 times: mid-write,
// mid-seal, between a seal's flush and its row, and in an eighth of the
// rounds while it opens and recovers what the round before left, which the
// loop then doesn't recover itself first. After a third of the kills it
// also does what a power cut can: it takes away what the writers wrote
// after their last flush of each file, from any frame of batches.seg and
// any byte of a segment on, and leaves garbage at the end of the newest
// segment. After every recovery, pkg/ledger verifies the chain, its
// numbers have no gap or repeat, every batch a writer reported sealed is
// there unchanged, and tools/ledger_ref.py check agrees on every batch
// sealed since the recovery before (docs/PLAN.md, WP-LG5; decision
// E-sm-1.1-seq-gaps, decided; spec wording pending).
func TestCrashLoop(t *testing.T) {
	python := pythonPath(t)
	dir := t.TempDir()
	keys := keysOf(t, testKDev)
	rng := rand.New(rand.NewPCG(20261120, 200))
	d := &durability{size: map[string]int64{}}
	// The loop's own Opens report their flushes here; the children's come
	// through their output.
	syncHook = d.flushed
	t.Cleanup(func() { syncHook = nil })
	reported := map[uint64]ledger.Hash{}
	var checked uint64 // batches already checked by the reference
	var head ledger.Hash
	var device string
	cuts, garbage, droppedRows := 0, 0, 0
	inOpen, beforeOpen := 0, 0 // kills on a recovery left to the writer, and those before it was open
	killInOpen := false        // this round's writer opens on what the last round left
	for kill := range crashKills {
		c := startChild(t, dir, kill)
		if killInOpen {
			select {
			case <-c.opening:
			case <-time.After(time.Minute):
				c.stop(t, d)
				t.Fatalf("kill %d: the writer didn't start to open within a minute: %v", kill, c.errors)
			}
			time.Sleep(time.Duration(rng.IntN(5000)) * time.Microsecond)
		} else {
			select {
			case <-c.ready:
			case <-time.After(time.Minute):
				c.stop(t, d)
				t.Fatalf("kill %d: the writer didn't open within a minute: %v", kill, c.errors)
			}
			time.Sleep(time.Duration(rng.IntN(30_000)) * time.Microsecond)
		}
		c.stop(t, d)
		switch {
		case len(c.errors) > 0:
			t.Fatalf("kill %d: the writer failed: %v", kill, c.errors)
		case !c.began || !killInOpen && !c.opened:
			t.Fatalf("kill %d: the writer ended before it was killed, saying nothing", kill)
		}
		if killInOpen {
			inOpen++
			if !c.opened {
				beforeOpen++
			}
		}
		for b, h := range c.sealed {
			reported[b] = h
		}
		if rng.IntN(3) == 0 {
			dropped, cut, added := powerCut(t, dir, rng, d, reported)
			droppedRows += dropped
			cuts += cut
			garbage += added
		}
		// The next round's writer may be killed while it opens on what
		// this round left, so that its recovery has work to do: then the
		// loop doesn't recover it first.
		killInOpen = kill+1 < crashKills && rng.IntN(8) == 0
		if killInOpen {
			continue
		}

		// The restart: Open recovers, and seals whatever survived.
		w, err := Open(Options{Dir: dir, Keys: keys, KeyID: testKeyID})
		if err != nil {
			t.Fatalf("kill %d: Open after the kill: %v", kill, err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("kill %d: Close: %v", kill, err)
		}
		if device == "" {
			info, err := ReadChain(dir)
			if err != nil {
				t.Fatal(err)
			}
			device = info.DeviceID
		}

		// Every batch a writer said it sealed is still there with the
		// same head, so nothing that could have left the device changed.
		bf, err := readBatchesFile(filepath.Join(dir, batchesName))
		if err != nil {
			t.Fatalf("kill %d: %v", kill, err)
		}
		for b, h := range reported {
			if b > uint64(len(bf.rows)) || bf.rows[b-1].Head != h {
				t.Fatalf("kill %d: batch %d, reported sealed with head %s, is gone or changed", kill, b, h)
			}
		}

		// The batches sealed since the last recovery: pkg/ledger verifies
		// them, bodies and nonces included, as the run that follows the
		// batches checked before, and the reference recomputes them.
		var fresh []Batch
		if err := ReadBatches(dir, checked+1, func(b Batch) error {
			fresh = append(fresh, b)
			return nil
		}); err != nil {
			t.Fatalf("kill %d: ReadBatches: %v", kill, err)
		}
		if uint64(len(bf.rows)) != checked+uint64(len(fresh)) {
			t.Fatalf("kill %d: batches.seg has %d rows, and the segments %d batches", kill, len(bf.rows), checked+uint64(len(fresh)))
		}
		if len(fresh) == 0 {
			continue
		}
		run := ledger.Chain{DeviceID: device, Prev: head}
		for _, b := range fresh {
			run.Batches = append(run.Batches, ledger.BatchOf(b.Row, b.Records))
		}
		if err := run.Verify(ledger.Options{Keys: keys}); err != nil {
			t.Fatalf("kill %d: pkg/ledger refuses batches %d to %d: %v", kill, checked+1, checked+uint64(len(fresh)), err)
		}
		if checked > 0 {
			if want := bf.rows[checked-1].LastSeq + 1; fresh[0].Row.FirstSeq != want {
				t.Fatalf("kill %d: batch %d starts at record %d, after record %d", kill, checked+1, fresh[0].Row.FirstSeq, want-1)
			}
		}
		doc := vecFile{Format: vectorsFormat, Chains: []vecChain{chainVec(t, device, testKDev, head, fresh)}}
		if code, out := refCheck(t, python, doc); code != 0 {
			t.Fatalf("kill %d: tools/ledger_ref.py check exited %d on batches %d to %d:\n%s", kill, code, checked+1,
				checked+uint64(len(fresh)), out)
		}
		checked += uint64(len(fresh))
		head = fresh[len(fresh)-1].Row.Head
	}

	// The whole chain, from batch 1, once more: no batch changed since it
	// was checked, and the numbers run from 1 with no gap.
	all := verify(t, dir, keys)
	if uint64(len(all)) != checked || all[len(all)-1].Row.Head != head {
		t.Fatalf("the chain has %d batches ending in %s, and the loop checked %d ending in %s",
			len(all), all[len(all)-1].Row.Head, checked, head)
	}
	if code, out := refCheck(t, python, vecFile{Format: vectorsFormat,
		Chains: []vecChain{chainVec(t, device, testKDev, ledger.Hash{}, all)}}); code != 0 {
		t.Fatalf("tools/ledger_ref.py check exited %d on the whole chain:\n%s", code, out)
	}
	recs := records(all)
	t.Logf("%d kills: %d batches and %d records; %d reported sealed; %d kills on a recovery left to the writer, %d of them before it was open; power cuts dropped %d rows, cut %d segment tails and left garbage after %d",
		crashKills, len(all), len(recs), len(reported), inOpen, beforeOpen, droppedRows, cuts, garbage)
	if len(reported) == 0 || len(recs) < crashKills {
		t.Errorf("the writers sealed too little for the loop to test much")
	}
}

// powerCut does to dir what a power cut can after a kill. It takes away
// what was written to each file after its last flush, as d keeps it: rows
// from the end of batches.seg, from any frame a flush didn't cover on,
// with part of the next frame left behind, and the tail of any segment
// from any byte a flush didn't cover on. Then it may leave zeros or
// garbage at the end of the newest segment. Before that, it checks that
// every batch a writer reported sealed whose row is in batches.seg ends
// where a flush covered: a writer reports a batch only once it is on disk.
// It reports what it did.
func powerCut(t *testing.T, dir string, rng *rand.Rand, d *durability, reported map[uint64]ledger.Hash) (droppedRows, cut, garbage int) {
	t.Helper()
	days, err := segments(dir)
	if err != nil || len(days) == 0 {
		return 0, 0, 0
	}
	name := filepath.Join(dir, batchesName)
	bf, err := readBatchesFile(name)
	if err != nil {
		t.Fatalf("before the power cut: %v", err)
	}
	for b := range reported {
		if b > uint64(len(bf.rows)) {
			continue // a power cut took its row, which Open rebuilds
		}
		if end := bf.rows[b-1].End; end.Off > d.of(segPath(dir, end.Seg)) {
			t.Fatalf("batch %d was reported sealed, and its segment was flushed to %d bytes, before its end at %d",
				b, d.of(segPath(dir, end.Seg)), end.Off)
		}
	}

	// batches.seg's chain frame was written whole, by atomic rename.
	frames := framesOf(t, name)
	keepMin := 1
	for keepMin < len(frames) && frames[keepMin].end <= d.of(name) {
		keepMin++
	}
	if rng.IntN(2) == 0 && len(frames) > keepMin {
		keep := keepMin + rng.IntN(len(frames)-keepMin+1)
		size := frames[keep-1].end
		if keep < len(frames) {
			// Part of the next frame may have reached the disk.
			size += rng.Int64N(frames[keep].end - frames[keep].off)
		}
		truncate(t, name, size)
		droppedRows = len(frames) - keep
	}
	for _, day := range days {
		seg := segPath(dir, day)
		if size, flushed := fileSize(t, seg), d.of(seg); size > flushed && rng.IntN(2) == 0 {
			truncate(t, seg, flushed+rng.Int64N(size-flushed+1))
			cut++
		}
	}
	if rng.IntN(3) == 0 {
		junk := make([]byte, 1+rng.IntN(512))
		if rng.IntN(2) == 0 {
			for i := range junk {
				junk[i] = byte(rng.Uint32())
			}
		}
		appendBytes(t, segPath(dir, days[len(days)-1]), junk)
		garbage = 1
	}
	return droppedRows, cut, garbage
}
