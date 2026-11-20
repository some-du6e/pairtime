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
type Buckets struct {
	dir string

	mu   sync.Mutex
	days map[string]*dayFile // the UTC days read so far, by YYYY-MM-DD
}

// dayFile is one day's file, read into memory.
type dayFile struct {
	latest map[int64]Bucket // each minute's latest revision, by its Unix ms
	// size is where the file's whole lines end: where the next line goes.
	size  int64
	f     *os.File // open for appending once the store has written to it
	dirty bool     // appended to since the last Sync
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
	return &Buckets{dir: filepath.Clean(dir), days: make(map[string]*dayFile)}, nil
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
	d := &dayFile{latest: make(map[int64]Bucket)}
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
	if err := s.append(day, d, append(line, '\n')); err != nil {
		return Bucket{}, false, err
	}
	d.latest[ms] = b
	return b, true, nil
}

// append writes line at the end of day's whole lines. s.mu is held.
func (s *Buckets) append(day string, d *dayFile, line []byte) error {
	if d.f == nil {
		name := s.path(day)
		// Cut off what a failed append left, as day does a torn line.
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
	}
	n, err := d.f.Write(line)
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

// Sync flushes every file appended to since the last Sync.
func (s *Buckets) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var first error
	for _, day := range slices.Sorted(maps.Keys(s.days)) {
		d := s.days[day]
		if !d.dirty || d.f == nil {
			continue
		}
		if err := d.f.Sync(); err != nil && first == nil {
			first = err
		}
		d.dirty = false
	}
	return first
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
// Unix ms, that is still pending, in minute order. It reads every day file
// in the directory from from's day on, without keeping the ones it hadn't
// read, so a scan from far back costs no memory.
func (s *Buckets) Pending(from int64) ([]Bucket, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	first := DayOf(from)
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Bucket
	for _, e := range entries {
		day, ok := strings.CutSuffix(e.Name(), ".jsonl")
		if !ok || e.IsDir() || day < first {
			continue
		}
		if _, err := time.Parse(time.DateOnly, day); err != nil {
			continue
		}
		latest := map[int64]Bucket{}
		if d, ok := s.days[day]; ok {
			latest = d.latest
		} else {
			data, err := os.ReadFile(s.path(day))
			if err != nil {
				return nil, err
			}
			readLines(data, latest)
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

// Forget drops from memory the days that end at or before before, closing
// their files. A later read of one reads its file again.
func (s *Buckets) Forget(before int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var first error
	for day, d := range s.days {
		t, err := time.Parse(time.DateOnly, day)
		if err != nil || t.UnixMilli()+dayMS > before {
			continue
		}
		if d.f != nil {
			if d.dirty {
				if err := d.f.Sync(); err != nil && first == nil {
					first = err
				}
			}
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
