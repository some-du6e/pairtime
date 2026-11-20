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
// reports each sealed batch on stdout. It skips unless TestCrashLoop
// started it.
func TestCrashLoopChild(t *testing.T) {
	dir := os.Getenv(crashDirEnv)
	if dir == "" {
		t.Skip("TestCrashLoop runs this as the writer it kills")
	}
	seed, err := strconv.ParseUint(os.Getenv(crashSeedEnv), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	out := os.Stdout
	fail := func(err error) {
		fmt.Fprintf(out, "error %v\n", err)
		os.Exit(3)
	}
	keys, err := ledger.DeriveKeys(testKDev)
	if err != nil {
		fail(err)
	}
	w, err := Open(Options{Dir: dir, Keys: keys, KeyID: testKeyID, Sealed: func(b Batch) {
		// One write call per line, so a kill leaves at most a cut-off
		// last line.
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

// child is one run of TestCrashLoopChild.
type child struct {
	cmd    *exec.Cmd
	ready  chan struct{}
	lines  chan string // everything it printed, closed at EOF
	sealed map[uint64]ledger.Hash
	errors []string
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
	c := &child{cmd: cmd, ready: make(chan struct{}), lines: make(chan string, 1024), sealed: map[uint64]ledger.Hash{}}
	go func() {
		defer close(c.lines)
		sc := bufio.NewScanner(stdout)
		readied := false
		for sc.Scan() {
			line := sc.Text()
			if line == "ready" && !readied {
				readied = true
				close(c.ready)
			}
			c.lines <- line
		}
		if !readied {
			close(c.ready) // it ended without opening, and stop says why
		}
	}()
	return c
}

// stop kills the child and collects what it reported: the batches it saw
// sealed, which were on disk before it said so, and any error.
func (c *child) stop(t *testing.T) {
	t.Helper()
	c.cmd.Process.Kill()
	for line := range c.lines {
		f := strings.Fields(line)
		switch {
		case len(f) == 3 && f[0] == "sealed":
			b, err := strconv.ParseUint(f[1], 10, 64)
			var h ledger.Hash
			if err == nil && h.UnmarshalText([]byte(f[2])) == nil {
				c.sealed[b] = h
			}
		case len(f) > 0 && f[0] == "error":
			c.errors = append(c.errors, line)
		}
	}
	c.cmd.Wait()
}

// TestCrashLoop kills the writer at random points, 200 times: while it
// opens and recovers, mid-write, mid-seal, between a seal's flush and its
// row. After a third of the kills it also does what a power cut can: it
// drops the rows batches.seg wasn't made to flush, and cuts or adds
// garbage at the end of the newest segment after the last batch known to
// be on disk. After every restart, pkg/ledger verifies the chain, its
// numbers have no gap or repeat, every batch the writer reported sealed is
// there unchanged, and tools/ledger_ref.py check agrees on every batch
// sealed since the restart before (docs/PLAN.md, WP-LG5; decision
// E-sm-1.1-seq-gaps).
func TestCrashLoop(t *testing.T) {
	python := pythonPath(t)
	dir := t.TempDir()
	keys := keysOf(t, testKDev)
	rng := rand.New(rand.NewPCG(20261120, 200))
	reported := map[uint64]ledger.Hash{}
	var checked uint64 // batches already checked by the reference
	var head ledger.Hash
	var device string
	cuts, garbage, droppedRows := 0, 0, 0
	for kill := range crashKills {
		c := startChild(t, dir, kill)
		if rng.IntN(8) == 0 {
			// Kill it while it opens, which is when it recovers.
			time.Sleep(time.Duration(rng.IntN(5000)) * time.Microsecond)
		} else {
			select {
			case <-c.ready:
			case <-time.After(time.Minute):
				c.stop(t)
				t.Fatalf("kill %d: the writer didn't open within a minute: %v", kill, c.errors)
			}
			time.Sleep(time.Duration(rng.IntN(30_000)) * time.Microsecond)
		}
		c.stop(t)
		if len(c.errors) > 0 {
			t.Fatalf("kill %d: the writer failed: %v", kill, c.errors)
		}
		for b, h := range c.sealed {
			reported[b] = h
		}
		if rng.IntN(3) == 0 {
			// Every batch up to the last one checked or reported was on
			// disk: the writer flushed it before sealing it.
			durable := checked
			for b := range reported {
				durable = max(durable, b)
			}
			dropped, cut, added := powerCut(t, dir, rng, durable)
			droppedRows += dropped
			cuts += cut
			garbage += added
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

		// Every batch the writer said it sealed is still there with the
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

		// The batches sealed since the last restart: pkg/ledger verifies
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
	t.Logf("%d kills: %d batches and %d records; %d reported sealed; power cuts dropped %d rows, cut %d segment tails and left garbage after %d",
		crashKills, len(all), len(recs), len(reported), droppedRows, cuts, garbage)
	if len(reported) == 0 || len(recs) < crashKills {
		t.Errorf("the writers sealed too little for the loop to test much")
	}
}

// powerCut does to dir what a power cut can after a kill: it drops rows
// from the end of batches.seg, which a seal doesn't flush, and cuts the
// newest segment, or adds garbage to it, after the last point a flush
// covered. Batches up to durable were flushed, and so was every batch
// whose row is left in batches.seg, since a row is written only after its
// seal's flush. It reports what it did.
func powerCut(t *testing.T, dir string, rng *rand.Rand, durable uint64) (droppedRows, cut, garbage int) {
	t.Helper()
	days, err := segments(dir)
	if err != nil || len(days) == 0 {
		return 0, 0, 0
	}
	newestDay := days[len(days)-1]
	newest := segPath(dir, newestDay)
	flushed := int64(0)
	covers := func(p position) {
		if p.Seg == newestDay && p.Off > flushed {
			flushed = p.Off
		}
	}
	name := filepath.Join(dir, batchesName)
	bf, err := readBatchesFile(name)
	if err != nil {
		t.Fatalf("before the power cut: %v", err)
	}
	if durable > uint64(len(bf.rows)) {
		t.Fatalf("batch %d was sealed and batches.seg has %d rows", durable, len(bf.rows))
	}
	if durable > 0 {
		covers(bf.rows[durable-1].End)
	}
	if frames := framesOf(t, name); rng.IntN(2) == 0 && len(frames) > 1 {
		keep := 1 + rng.IntN(len(frames))
		size := frames[keep-1].end
		if keep < len(frames) {
			// Part of the next frame may have reached the disk.
			size += rng.Int64N(frames[keep].end - frames[keep].off)
		}
		truncate(t, name, size)
		droppedRows = len(frames) - keep
		if bf, err = readBatchesFile(name); err != nil {
			t.Fatalf("after dropping rows: %v", err)
		}
	}
	if n := len(bf.rows); n > 0 {
		covers(bf.rows[n-1].End)
	}
	if size := fileSize(t, newest); size > flushed && rng.IntN(2) == 0 {
		truncate(t, newest, flushed+rng.Int64N(size-flushed+1))
		cut = 1
	}
	if rng.IntN(3) == 0 {
		junk := make([]byte, 1+rng.IntN(512))
		if rng.IntN(2) == 0 {
			for i := range junk {
				junk[i] = byte(rng.Uint32())
			}
		}
		appendBytes(t, newest, junk)
		garbage = 1
	}
	return droppedRows, cut, garbage
}
