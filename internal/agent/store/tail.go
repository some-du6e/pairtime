// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The bucket store's files are append-only logs of revisions, one JSON
// line each, so a reader can follow them the way a tail does: it keeps,
// for each day's file, a Mark of how far it has read, and reads only the
// lines after it. The uploader follows them so that every revision the
// scorer writes reaches the server, whichever minute it is for (WP-AG5),
// and `pairtime privacy show` follows them from the uploader's marks to
// print the next upload. Neither may write to a store an agent has open:
// Buckets cuts a torn last line off when it first reads a day, which would
// cut a line the agent is still writing. So these functions only read.

// maxLine bounds a revision line when a Mark is checked. A bucket's line
// is about a kilobyte.
const maxLine = 64 << 10

// Mark is how far a reader has read one day's file: Off is where the last
// line it took ends, just past its newline, and Line is a digest of that
// line. A power cut can take a day's last lines, which are written without
// a flush; when the scorer writes the file past Off again, Line no longer
// matches what ends at Off, so the reader starts the day again from its
// first line rather than in the middle of one. The zero Mark reads a day
// from its start.
type Mark struct {
	Off  int64  `json:"off"`
	Line string `json:"line,omitempty"`
}

// Revision is one revision line of a day's file: the bucket it holds and
// the Mark just past it.
type Revision struct {
	Bucket Bucket
	Mark   Mark
}

// BucketDay is one day's file in a bucket store, and its size.
type BucketDay struct {
	Day  string // YYYY-MM-DD, the UTC day of the minutes it holds
	Size int64
}

// BucketDays lists the day files in the bucket store in dir, oldest first, from
// one read of the directory. A store that doesn't exist yet has none.
func BucketDays(dir string) ([]BucketDay, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var days []BucketDay
	for _, e := range entries {
		day, ok := strings.CutSuffix(e.Name(), ".jsonl")
		if !ok || !e.Type().IsRegular() {
			continue
		}
		if t, err := time.Parse(time.DateOnly, day); err != nil || t.Format(time.DateOnly) != day {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // deleted since the directory was read
			}
			return nil, err
		}
		days = append(days, BucketDay{Day: day, Size: fi.Size()})
	}
	slices.SortFunc(days, func(a, b BucketDay) int { return strings.Compare(a.Day, b.Day) })
	return days, nil
}

// TailDay calls fn with each revision line of day's file in the bucket
// store in dir after the Mark m, in the order they were written, until fn
// returns false. A revision a later line of the file supersedes is passed
// too, in its place: the caller keeps what it needs. Lines that don't hold
// a bucket are skipped, as Buckets skips them.
//
// It only reads: it stops before a last line that isn't whole yet, and
// leaves it for a later read. When m isn't at the end of the line it names,
// because the file was cut and written again since, it reads the day from
// its first line and restart is true. A day without a file has no lines.
func TailDay(dir, day string, m Mark, fn func(Revision) bool) (restart bool, err error) {
	f, err := os.Open(filepath.Join(dir, day+".jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return m.Off > 0, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return false, err
	}
	size := fi.Size()
	start := int64(0)
	if m.Off > 0 {
		ok, err := markHolds(f, size, m)
		if err != nil {
			return false, err
		}
		if ok {
			start = m.Off
		} else {
			restart = true
		}
	}
	if size <= start {
		return restart, nil
	}
	data := make([]byte, size-start)
	n, err := f.ReadAt(data, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return restart, err
	}
	data = data[:n]
	off := start
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			return restart, nil // the rest isn't a whole line yet
		}
		line := data[:i]
		data = data[i+1:]
		off += int64(i + 1)
		var b Bucket
		if err := json.Unmarshal(line, &b); err != nil || b.Minute.IsZero() {
			continue
		}
		if !fn(Revision{Bucket: b, Mark: Mark{Off: off, Line: lineDigest(line)}}) {
			return restart, nil
		}
	}
}

// markHolds reports whether the line that ends at m.Off in f, of size
// bytes, is the one m names.
func markHolds(f *os.File, size int64, m Mark) (bool, error) {
	if m.Off > size || m.Line == "" {
		return false, nil
	}
	n := min(m.Off, maxLine+1)
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, m.Off-n); err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	if buf[n-1] != '\n' {
		return false, nil
	}
	line := buf[:n-1]
	if j := bytes.LastIndexByte(line, '\n'); j >= 0 {
		line = line[j+1:]
	} else if m.Off > n {
		return false, nil // longer than any line the store writes
	}
	return lineDigest(line) == m.Line, nil
}

// lineDigest is a Mark's digest of a line, without its newline: 16 hex
// digits of its SHA-256. It tells one line from another; nothing secret
// rests on it.
func lineDigest(line []byte) string {
	sum := sha256.Sum256(line)
	return hex.EncodeToString(sum[:8])
}
