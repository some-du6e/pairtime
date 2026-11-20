// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// batchesName is the file of batch rows (DESIGN 3.5).
const batchesName = "batches.seg"

// segSuffix ends every day segment's name, YYYY-MM-DD.seg (DESIGN 3.5).
const segSuffix = ".seg"

// oldDir holds the chains of device keys this installation no longer
// writes with, each in a directory named after its key's id.
const oldDir = "old"

// dayOf is the UTC day of t, as a segment names it.
func dayOf(t time.Time) string { return t.UTC().Format(time.DateOnly) }

// isDay reports whether s is a day as segments name them.
func isDay(s string) bool {
	t, err := time.Parse(time.DateOnly, s)
	return err == nil && t.Format(time.DateOnly) == s
}

// segPath is the path of day's segment in dir.
func segPath(dir, day string) string { return filepath.Join(dir, day+segSuffix) }

// segments lists the days of the segments in dir, oldest first.
func segments(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var days []string
	for _, e := range entries {
		day, ok := strings.CutSuffix(e.Name(), segSuffix)
		if ok && isDay(day) && e.Type().IsRegular() {
			days = append(days, day)
		}
	}
	slices.Sort(days)
	return days, nil
}

// appendFile is a file the writer only ever appends to. It keeps its own
// size and writes at that offset, rather than open the file with
// O_APPEND, so that it can cut a failed write back off: on Windows a file
// opened for appending can't be truncated.
type appendFile struct {
	f    *os.File
	name string
	size int64
}

// openAppend opens name for appending, making it when create is set.
func openAppend(name string, create bool) (*appendFile, error) {
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE
	}
	f, err := os.OpenFile(name, flags, 0o600)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &appendFile{f: f, name: name, size: fi.Size()}, nil
}

// errStuck is a write that failed and couldn't be cut back off, so the
// file may end in part of a frame. The writer stops writing; the next Open
// cuts the torn frame off.
var errStuck = errors.New("ledger: a failed write couldn't be undone")

// write appends b. When the write fails, it cuts the file back to its
// size before, so the file never ends in part of a frame while the writer
// runs. It returns errStuck when that fails too.
func (a *appendFile) write(b []byte) error {
	_, err := a.f.WriteAt(b, a.size)
	if err == nil {
		a.size += int64(len(b))
		return nil
	}
	if terr := a.f.Truncate(a.size); terr != nil {
		return fmt.Errorf("%w: %s: %v, and cutting it back failed: %v", errStuck, filepath.Base(a.name), err, terr)
	}
	return err
}

// cut truncates the file to size, from a torn frame on.
func (a *appendFile) cut(size int64) error {
	if err := a.f.Truncate(size); err != nil {
		return err
	}
	a.size = size
	return a.sync()
}

// sync flushes the file. The caller holds the lock its writes take, so
// a.size is what the flush covers.
func (a *appendFile) sync() error { return a.syncTo(a.size) }

// syncTo flushes the file, which holds at least size bytes once the flush
// is done: seal reads the size under the writer's lock and flushes without
// it, while records go on being written.
func (a *appendFile) syncTo(size int64) error {
	if syncHook != nil {
		syncHook(a.name, size)
	}
	return a.f.Sync()
}

// syncHook, when set, hears of each flush just before it is made: the file
// and how much of it the flush covers. A power cut can't take away what a
// flush covered, and TestCrashLoop uses it to cut no more than a power cut
// can. It is nil outside tests.
var syncHook func(name string, size int64)

func (a *appendFile) close() error { return a.f.Close() }
