// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/pkg/event"
)

// ErrFinal is a Put that would change a minute whose bucket is final. A
// final bucket never changes (DESIGN 3.7, 6.2 D1).
var ErrFinal = errors.New("store: the minute's bucket is final")

// minuteMS is a minute in milliseconds.
const minuteMS = 60_000

// dayMS is a UTC day in milliseconds.
const dayMS = 24 * 60 * minuteMS

// Buckets is the bucket store, buckets/ in the data directory: one file
// per UTC day, named after the day of the minutes it holds, and in it one
// JSON line per revision, appended. A minute's latest revision is the one
// with the highest rev, which is also its last line. Its methods are safe
// for concurrent use.
//
// A write is one append and no flush; Sync flushes. A crash can leave the
// last line of a file torn, and the store cuts it off when it first reads
// the file, so the file goes on holding only whole lines. The scorer loop
// writes again whatever a crash cut, since it starts again from the last
// batch it saved as done.
//
// A flush that fails leaves the file to be flushed again, and the next
// flush writes the whole file again, by atomic rename: its whole lines as
// they read then, and the latest revision of each minute appended since
// the last flush that worked that they don't end with. A failed fsync can
// drop the pages it was writing and let the next one report success (the
// ledger writer stops after one for that reason), so only a fresh write
// gets what they held to disk.
type Buckets struct {
	dir string
	// syncFile flushes a file to disk, (*os.File).Sync but in tests.
	syncFile func(*os.File) error

	mu   sync.Mutex
	days map[string]*dayFile // the UTC days read so far, by YYYY-MM-DD
}

// dayFile is one day's file, read into memory.
type dayFile struct {
	latest map[int64]Bucket // each minute's latest revision, by its Unix ms
	// size is where the file's whole lines end: where the next line goes.
	size int64
	f    *os.File // open for appending once the store has written to it
	// dirty is set when the file was appended to, or marked Unflushed,
	// since the last flush that worked.
	dirty bool
	// unsynced holds the minutes whose latest revision this store appended
	// since the last flush that worked, and failed is set after a flush
	// that didn't (Buckets).
	unsynced map[int64]bool
	failed   bool
}

// OpenBuckets opens the bucket store in dir, making the directory if it
// isn't there.
func OpenBuckets(dir string) (*Buckets, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("store: the bucket directory must be an absolute path, not %q", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Buckets{dir: filepath.Clean(dir), syncFile: (*os.File).Sync, days: make(map[string]*dayFile)}, nil
}

// Dir returns the store's directory.
func (s *Buckets) Dir() string { return s.dir }

// DayOf returns the UTC day, YYYY-MM-DD, of the minute starting at ms.
func DayOf(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.DateOnly) }

func (s *Buckets) path(day string) string { return filepath.Join(s.dir, day+".jsonl") }

// day returns day's file, reading it the first time. s.mu is held.
func (s *Buckets) day(day string) (*dayFile, error) {
	if d, ok := s.days[day]; ok {
		return d, nil
	}
	d := &dayFile{latest: make(map[int64]Bucket), unsynced: make(map[int64]bool)}
	data, err := os.ReadFile(s.path(day))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		d.size = readLines(data, d.latest)
		if d.size < int64(len(data)) {
			// A torn last line. A file opened for appending can't be
			// truncated on Windows, so this goes by name.
			if err := os.Truncate(s.path(day), d.size); err != nil {
				return nil, err
			}
		}
	}
	s.days[day] = d
	return d, nil
}

// readLines reads a day file's revisions into latest and returns where its
// last whole line ends. A line that doesn't decode is skipped; only a
// crash in the middle of an append leaves one, and only at the end.
func readLines(data []byte, latest map[int64]Bucket) int64 {
	var end int64
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break // a torn last line
		}
		line := data[:i]
		data = data[i+1:]
		end += int64(i + 1)
		var b Bucket
		if err := json.Unmarshal(line, &b); err != nil || b.Minute.IsZero() {
			continue
		}
		m := b.At()
		if cur, ok := latest[m]; !ok || b.Rev >= cur.Rev {
			latest[m] = b
		}
	}
	return end
}

// Latest returns the latest revision of the minute starting at ms.
func (s *Buckets) Latest(ms int64) (Bucket, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.day(DayOf(ms))
	if err != nil {
		return Bucket{}, false, err
	}
	b, ok := d.latest[ms]
	return b, ok, nil
}

// Put stores b as its minute's next revision and returns it with the rev
// Put gave it: 0 for the minute's first, and the latest one's plus one
// after that. When the latest revision says the same apart from rev, Put
// writes nothing and returns that one with wrote false. It returns
// ErrFinal, and writes nothing, for a minute whose latest revision isn't
// pending and says something else.
func (s *Buckets) Put(b Bucket) (stored Bucket, wrote bool, err error) {
	if b.Minute.IsZero() || b.Minute.UnixMilli()%minuteMS != 0 {
		return Bucket{}, false, fmt.Errorf("store: a bucket's minute must be the start of a minute, got %v", b.Minute)
	}
	b.Minute = b.Minute.UTC()
	ms := b.At()
	day := DayOf(ms)
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.day(day)
	if err != nil {
		return Bucket{}, false, err
	}
	b.Rev = 0
	if cur, ok := d.latest[ms]; ok {
		if sameContent(cur, b) {
			return cur, false, nil
		}
		if cur.State != event.StatePending {
			return cur, false, fmt.Errorf("%w: %s", ErrFinal, b.Minute.Format(time.RFC3339))
		}
		b.Rev = cur.Rev + 1
	}
	line, err := json.Marshal(b)
	if err != nil {
		return Bucket{}, false, err
	}
	if err := s.write(day, d, append(line, '\n')); err != nil {
		return Bucket{}, false, err
	}
	d.latest[ms] = b
	d.unsynced[ms] = true
	return b, true, nil
}

// open opens day's file for appending, after cutting off what a failed
// append left past its whole lines, as day does a torn line. s.mu is held.
func (s *Buckets) open(day string, d *dayFile) error {
	name := s.path(day)
	if fi, err := os.Stat(name); err == nil && fi.Size() > d.size {
		if err := os.Truncate(name, d.size); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	d.f = f
	return nil
}

// write writes data at the end of day's whole lines. s.mu is held.
func (s *Buckets) write(day string, d *dayFile, data []byte) error {
	if d.f == nil {
		if err := s.open(day, d); err != nil {
			return err
		}
	}
	n, err := d.f.Write(data)
	d.size += int64(n)
	d.dirty = true
	if err != nil {
		// A part of a line may be on disk now. Close the file, so the next
		// append cuts it off again before writing.
		d.f.Close()
		d.f = nil
		d.size -= int64(n)
		return err
	}
	return nil
}

// flush gets day's file to disk, and after a flush that failed writes it
// again (rewrite). s.mu is held.
func (s *Buckets) flush(day string, d *dayFile) error {
	if d.failed {
		if err := s.rewrite(day, d); err != nil {
			return err
		}
	} else {
		if d.f == nil {
			if err := s.open(day, d); err != nil {
				return err
			}
		}
		if err := s.syncFile(d.f); err != nil {
			d.failed = true
			return err
		}
	}
	d.dirty, d.failed = false, false
	clear(d.unsynced)
	return nil
}

// rewrite replaces day's file, by atomic rename, with its whole lines as
// they read now and, after them, the latest revision of each minute in
// d.unsynced that they don't end with (Buckets). s.mu is held.
func (s *Buckets) rewrite(day string, d *dayFile) error {
	if d.f != nil {
		// Windows renames nothing over an open file.
		d.f.Close()
		d.f = nil
	}
	name := s.path(day)
	data, err := os.ReadFile(name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	read := map[int64]Bucket{}
	data = data[:readLines(data, read)]
	for _, ms := range slices.Sorted(maps.Keys(d.unsynced)) {
		want := d.latest[ms]
		if got, ok := read[ms]; ok && got.Rev == want.Rev && got.Batch == want.Batch && sameContent(got, want) {
			continue
		}
		line, err := json.Marshal(want)
		if err != nil {
			return err
		}
		data = append(append(data, line...), '\n')
	}
	if err := home.WriteFile(name, data); err != nil {
		return err
	}
	d.size = int64(len(data))
	return nil
}

// Sync flushes every file appended to since its last flush that worked,
// and every file marked Unflushed. It returns the first error, and a file
// whose flush failed is flushed again at the next Sync (Buckets).
func (s *Buckets) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var first error
	for _, day := range slices.Sorted(maps.Keys(s.days)) {
		if d := s.days[day]; d.dirty {
			if err := s.flush(day, d); err != nil && first == nil {
				first = err
			}
		}
	}
	return first
}

// Unflushed marks the minute starting at ms, and the file that holds it,
// to be flushed at the next Sync, as if this store had just appended the
// minute's latest revision: a run that a crash stopped before its flush
// appended it, so it may be in the OS's cache only.
func (s *Buckets) Unflushed(ms int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.day(DayOf(ms))
	if err != nil {
		return err
	}
	if _, ok := d.latest[ms]; ok {
		d.dirty = true
		d.unsynced[ms] = true
	}
	return nil
}

// Range returns the latest revision of every minute from from up to to,
// in Unix ms, in minute order.
func (s *Buckets) Range(from, to int64) ([]Bucket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Bucket
	for day := from - floorMod(from, dayMS); day < to; day += dayMS {
		d, err := s.day(DayOf(day))
		if err != nil {
			return nil, err
		}
		for ms, b := range d.latest {
			if ms >= from && ms < to {
				out = append(out, b)
			}
		}
	}
	slices.SortFunc(out, func(a, b Bucket) int { return cmp.Compare(a.At(), b.At()) })
	return out, nil
}

// Pending returns the latest revision of every minute from from on, in
// Unix ms, that is still pending, in minute order.
func (s *Buckets) Pending(from int64) ([]Bucket, error) {
	days, err := s.dayNames(DayOf(from), "9999-12-31")
	if err != nil {
		return nil, err
	}
	var out []Bucket
	for _, day := range days {
		latest, err := s.peek(day)
		if err != nil {
			return nil, err
		}
		for ms, b := range latest {
			if ms >= from && b.State == event.StatePending {
				out = append(out, b)
			}
		}
	}
	slices.SortFunc(out, func(a, b Bucket) int { return cmp.Compare(a.At(), b.At()) })
	return out, nil
}

// Before calls fn with the latest revision of each minute before to, in
// Unix ms, newest first, until fn returns false. fn may call the store.
func (s *Buckets) Before(to int64, fn func(Bucket) bool) error {
	days, err := s.dayNames("0000-01-01", DayOf(to-1))
	if err != nil {
		return err
	}
	slices.Reverse(days)
	for _, day := range days {
		latest, err := s.peek(day)
		if err != nil {
			return err
		}
		ms := slices.Sorted(maps.Keys(latest))
		slices.Reverse(ms)
		for _, m := range ms {
			if m < to && !fn(latest[m]) {
				return nil
			}
		}
	}
	return nil
}

// dayNames returns the days, YYYY-MM-DD, from first to last that have a
// file in the store, in order.
func (s *Buckets) dayNames(first, last string) ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var days []string
	for _, e := range entries {
		day, ok := strings.CutSuffix(e.Name(), ".jsonl")
		if !ok || e.IsDir() || day < first || day > last {
			continue
		}
		if _, err := time.Parse(time.DateOnly, day); err == nil {
			days = append(days, day)
		}
	}
	slices.Sort(days)
	return days, nil
}

// peek returns day's latest revisions: a copy of what the store holds when
// it has read the day, and otherwise what the day's file holds, which it
// doesn't keep, so a walk over many days costs no memory.
func (s *Buckets) peek(day string) (map[int64]Bucket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.days[day]; ok {
		return maps.Clone(d.latest), nil
	}
	latest := map[int64]Bucket{}
	data, err := os.ReadFile(s.path(day))
	if errors.Is(err, fs.ErrNotExist) {
		return latest, nil
	}
	if err != nil {
		return nil, err
	}
	readLines(data, latest)
	return latest, nil
}

// Forget drops from memory the days that end at or before before, after
// flushing and closing their files. A later read of one reads its file
// again. A day whose flush fails stays, to be flushed again at the next
// Sync.
func (s *Buckets) Forget(before int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var first error
	for _, day := range slices.Sorted(maps.Keys(s.days)) {
		d := s.days[day]
		t, err := time.Parse(time.DateOnly, day)
		if err != nil || t.UnixMilli()+dayMS > before {
			continue
		}
		if d.dirty {
			if err := s.flush(day, d); err != nil {
				if first == nil {
					first = err
				}
				continue
			}
		}
		if d.f != nil {
			if err := d.f.Close(); err != nil && first == nil {
				first = err
			}
		}
		delete(s.days, day)
	}
	return first
}

// Close flushes and closes every open file.
func (s *Buckets) Close() error {
	err := s.Sync()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.days {
		if d.f != nil {
			if cerr := d.f.Close(); cerr != nil && err == nil {
				err = cerr
			}
			d.f = nil
		}
	}
	return err
}

// floorMod returns a mod m in [0, m), for m > 0.
func floorMod(a, m int64) int64 {
	r := a % m
	if r < 0 {
		r += m
	}
	return r
}
