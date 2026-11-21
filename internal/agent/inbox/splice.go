// SPDX-License-Identifier: AGPL-3.0-or-later

package inbox

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
)

// Span is a part of a file: Len bytes from Off.
type Span struct {
	Off, Len int64
}

// A Splice rewrites a private file that a writer goes on appending to, such
// as the agent's held.jsonl.pending, to hold some parts of what it held
// when the splice began and all that was appended after that, in order. It
// takes two steps, so that the writer waits only for the short one:
// StartSplice copies the parts to a new file beside the old one and
// flushes that to disk while the writer goes on, and Finish, which the
// writer waits for, adds what it appended meanwhile and renames the new
// file over the old.
type Splice struct {
	name     string
	size     int64 // where the parts end and the appended part begins
	written  int64 // the parts' bytes, copied to tmp
	src, tmp *os.File
	srcOpen  bool
	tmpOpen  bool
	tmpGone  bool // renamed over the file, or deleted
}

// StartSplice begins a splice of name, which it opens as OpenPrivate does.
// It copies keep, parts of the file's first size bytes, in the order given,
// to a new file in the same folder, named tmpPrefix and a random suffix, and
// flushes that to disk. copied, when not nil, runs once the parts are
// copied, before the flush: a test can append there while the splice is
// still at work. Until Finish or Abort, nothing may change name but appends
// past size, and a part keep names that the file doesn't hold is an error.
func StartSplice(name, tmpPrefix string, size int64, keep []Span, copied func()) (*Splice, error) {
	src, err := OpenPrivate(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(name), tmpPrefix)
	if err != nil {
		src.Close()
		return nil, err
	}
	s := &Splice{name: name, size: size, src: src, tmp: tmp, srcOpen: true, tmpOpen: true}
	w := bufio.NewWriter(tmp)
	for _, k := range keep {
		if k.Off < 0 || k.Len < 0 || k.Off > size-k.Len {
			s.Abort()
			return nil, fmt.Errorf("inbox: %s has no %d bytes from %d to keep within its first %d", filepath.Base(name), k.Len, k.Off, size)
		}
		n, err := io.Copy(w, io.NewSectionReader(src, k.Off, k.Len))
		if err == nil && n < k.Len {
			err = fmt.Errorf("inbox: %s ends before the %d bytes from %d to keep: %w", filepath.Base(name), k.Len, k.Off, io.ErrUnexpectedEOF)
		}
		if err != nil {
			s.Abort()
			return nil, err
		}
		s.written += n
	}
	if copied != nil {
		copied()
	}
	if err := w.Flush(); err != nil {
		s.Abort()
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		s.Abort()
		return nil, err
	}
	return s, nil
}

// Finish copies what was appended to the file past the size StartSplice
// was given to the new file, renames the new file over the file, and
// returns the size the file has now. The caller must have closed every
// handle of its own to the file first, since Windows renames nothing over
// an open file, and keeps its writer from appending until Finish returns.
// The appended part isn't flushed to disk, as it wasn't in the old file
// either. On an error the file is as it was. Either way the splice is over.
func (s *Splice) Finish() (int64, error) {
	defer s.Abort()
	if !s.tmpOpen {
		return 0, fmt.Errorf("inbox: the splice of %s is over", filepath.Base(s.name))
	}
	n, err := io.Copy(s.tmp, io.NewSectionReader(s.src, s.size, math.MaxInt64-s.size))
	if err != nil {
		return 0, err
	}
	s.tmpOpen = false
	if err := s.tmp.Close(); err != nil {
		return 0, err
	}
	s.srcOpen = false
	s.src.Close() // read only, so nothing is lost if it fails
	if err := os.Rename(s.tmp.Name(), s.name); err != nil {
		return 0, err
	}
	s.tmpGone = true
	return s.written + n, nil
}

// Abort ends a splice that hasn't finished: it deletes the new file and
// leaves the old one as it was. After Finish it does nothing.
func (s *Splice) Abort() {
	if s.srcOpen {
		s.srcOpen = false
		s.src.Close()
	}
	if s.tmpOpen {
		s.tmpOpen = false
		s.tmp.Close()
	}
	if !s.tmpGone {
		s.tmpGone = true
		os.Remove(s.tmp.Name())
	}
}
