// SPDX-License-Identifier: AGPL-3.0-or-later

package inbox

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// SpoolName is the file in the inbox directory that senders append to.
const SpoolName = "spool.jsonl"

// takenSuffix ends the name of a spool file that a drain has taken.
const takenSuffix = ".drain"

// MaxSpool is the size past which Append refuses to grow the spool, about
// two days of heavy use with the agent down. Every sender that spools a
// message also starts the agent (DESIGN 3.2), so the spool only fills when
// the agent can't run at all, and then losing new messages beats filling
// the disk.
const MaxSpool = 256 << 20

// ErrSpoolFull means the spool is over MaxSpool.
var ErrSpoolFull = errors.New("inbox: the spool is full")

// appendTries bounds how often Append writes a message again because a
// drain took the spool while it was writing.
const appendTries = 4

// afterWrite, when set, runs between Append's write and its check that the
// spool is still the file it wrote to. Tests use it to take the spool at
// exactly that moment.
var afterWrite func()

// Append adds m to the spool in dir, the inbox directory, as one line
// written with one O_APPEND write. It creates dir when it has to, readable
// by this user only where the OS has file modes. Many processes may append
// at once: the OS keeps each write whole and in one place.
//
// Each record has a newline before it as well as after it. When a sender is
// killed halfway through its write, the next record still starts on a line
// of its own, and only the cut one is lost.
func Append(dir string, m Message) error {
	b, err := m.Encode()
	if err != nil {
		return err
	}
	rec := make([]byte, 0, len(b)+2)
	rec = append(rec, '\n')
	rec = append(rec, b...)
	rec = append(rec, '\n')
	name := filepath.Join(dir, SpoolName)
	for range appendTries {
		kept, err := appendOnce(dir, name, rec)
		if err != nil || kept {
			return err
		}
	}
	return errors.New("inbox: the agent kept taking the spool while this message was written")
}

// appendOnce writes rec to the spool and reports whether the write landed
// in the file that is still the spool. A drain takes the spool by renaming
// it and then reads the renamed file, so a write that lands after the
// rename may be in a file that drain has read already. The caller writes
// such a message again; if the drain did see the first copy, the sink drops
// the second.
func appendOnce(dir, name string, rec []byte) (bool, error) {
	const flags = os.O_WRONLY | os.O_APPEND | os.O_CREATE
	f, err := os.OpenFile(name, flags, 0o600)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return false, err
		}
		f, err = os.OpenFile(name, flags, 0o600)
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return false, err
	}
	if opened.Size()+int64(len(rec)) > MaxSpool {
		return false, ErrSpoolFull
	}
	if _, err := f.Write(rec); err != nil {
		return false, err
	}
	if afterWrite != nil {
		afterWrite()
	}
	// Checked while the file is still open: on Windows a drain can't
	// rename it until it is closed, so a rename after this point can't
	// have moved the write.
	now, err := os.Stat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return os.SameFile(opened, now), nil
}

// DrainResult counts what one Drain did.
type DrainResult struct {
	// Files is how many spool files were read to the end and deleted.
	Files int
	// Delivered is how many messages went to the sink, including any the
	// sink dropped as duplicates.
	Delivered int
	// Bad is how many lines weren't a message the agent takes, such as a
	// record cut short by a killed sender. They are skipped.
	Bad int
}

// renameTries and renameWait bound how long Drain waits for a sender on
// Windows, where a file that a sender has open can't be renamed or deleted.
// A sender holds the spool open for one write.
const (
	renameTries = 10
	renameWait  = 20 * time.Millisecond
)

// Drain hands every message waiting in dir, the inbox directory, to sink,
// and deletes each spool file once all its messages are delivered. now
// stamps each delivery.
//
// It first takes the spool by renaming it, so senders start a new one, and
// then reads every taken file, oldest first. A file that an earlier Drain
// took but didn't delete, because the agent was killed in between, is read
// again from the start, and the sink drops the messages it already has
// (DESIGN 3.5).
//
// When the sink fails, Drain stops and returns the error. The file stays,
// and the next Drain reads it again.
func Drain(dir string, sink Sink, now func() time.Time) (DrainResult, error) {
	var res DrainResult
	takeErr := take(dir)
	names, err := taken(dir)
	if err != nil {
		return res, errors.Join(takeErr, err)
	}
	for _, name := range names {
		delivered, bad, err := drainFile(name, sink, now)
		res.Delivered += delivered
		res.Bad += bad
		if err != nil {
			return res, errors.Join(takeErr, err)
		}
		if err := retry(func() error { return os.Remove(name) }); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return res, errors.Join(takeErr, err)
		}
		res.Files++
	}
	return res, takeErr
}

// take renames the spool to a name that sorts after every file taken
// before, unless the clock went back.
func take(dir string) error {
	src := filepath.Join(dir, SpoolName)
	dst := filepath.Join(dir, fmt.Sprintf("spool-%020d-%s%s", time.Now().UnixNano(), NewID()[:8], takenSuffix))
	err := retry(func() error { return os.Rename(src, dst) })
	if errors.Is(err, fs.ErrNotExist) {
		return nil // nothing spooled since the last drain
	}
	return err
}

// retry runs op until it succeeds, fails with ErrNotExist, or has failed
// renameTries times.
func retry(op func() error) error {
	var err error
	for i := range renameTries {
		if err = op(); err == nil || errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if i < renameTries-1 {
			time.Sleep(renameWait)
		}
	}
	return err
}

// taken lists the taken spool files in dir, oldest first.
func taken(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasPrefix(e.Name(), "spool-") && strings.HasSuffix(e.Name(), takenSuffix) {
			names = append(names, filepath.Join(dir, e.Name()))
		}
	}
	slices.Sort(names)
	return names, nil
}

// drainFile delivers every message in the file name.
func drainFile(name string, sink Sink, now func() time.Time) (delivered, bad int, err error) {
	f, err := os.Open(name)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		line, long, err := readLine(r, MaxMessage)
		if errors.Is(err, io.EOF) {
			return delivered, bad, nil
		}
		if err != nil {
			return delivered, bad, err
		}
		if long {
			bad++
			continue
		}
		if len(line) == 0 {
			continue // the blank line between two records
		}
		m, err := Decode(line)
		if err != nil {
			bad++
			continue
		}
		if err := sink.Deliver(Delivery{Message: m, Via: ViaInbox, Received: now()}); err != nil {
			return delivered, bad, err
		}
		delivered++
	}
}

// readLine returns the next line of r without its line ending. A line
// longer than limit is read to its end and skipped: it comes back as nil
// with long set. A last line with no newline comes back like any other, and
// the call after it returns io.EOF.
func readLine(r *bufio.Reader, limit int) (line []byte, long bool, err error) {
	for {
		chunk, err := r.ReadSlice('\n')
		if !long {
			if len(line)+len(chunk) > limit+2 { // +2 for "\r\n"
				line, long = nil, true
			} else {
				line = append(line, chunk...)
			}
		}
		switch {
		case err == nil:
			return trimEOL(line), long, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(line) == 0 && !long {
				return nil, false, io.EOF
			}
			return trimEOL(line), long, nil
		default:
			return nil, false, err
		}
	}
}

func trimEOL(line []byte) []byte {
	line = bytes.TrimSuffix(line, []byte{'\n'})
	return bytes.TrimSuffix(line, []byte{'\r'})
}
