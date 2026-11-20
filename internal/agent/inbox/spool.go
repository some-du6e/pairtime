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

// maxSpool is MaxSpool, as a variable that tests can lower.
var maxSpool int64 = MaxSpool

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
// It writes only into a spool that this user alone can reach (privateDir,
// privateFile), and fails rather than write anywhere else: anyone else who
// could put a file in the spool's place could read what hooks send,
// prompts included.
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
	err := privateDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if err = os.MkdirAll(dir, 0o700); err == nil {
			err = privateDir(dir)
		}
	}
	if err != nil {
		return false, err
	}
	f, err := openSpool(name)
	if err != nil {
		return false, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return false, err
	}
	if err := privateFile(name, opened); err != nil {
		return false, err
	}
	if opened.Size()+int64(len(rec)) > maxSpool {
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
	now, err := os.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return os.SameFile(opened, now), nil
}

// openTries and openWait bound how long a sender waits to open the spool
// while a drain renames it, which on Windows shuts opens out for a moment.
const (
	openTries = 50
	openWait  = time.Millisecond
)

// openSpool opens the spool for appending, and creates it when it is
// missing but its directory isn't.
func openSpool(name string) (*os.File, error) {
	var err error
	for i := range openTries {
		var f *os.File
		if f, err = os.OpenFile(name, spoolFlags, 0o600); err == nil || !busy(err) {
			return f, err
		}
		if i < openTries-1 {
			time.Sleep(openWait)
		}
	}
	return nil, err
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
	// Busy is why the spool couldn't be taken this time, as when senders
	// on Windows kept it open throughout. Its messages wait for the next
	// drain.
	Busy error
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
// and the next Drain reads it again. A spool it couldn't take is not an
// error but res.Busy, and waits for the next Drain.
func Drain(dir string, sink Sink, now func() time.Time) (DrainResult, error) {
	if err := privateDir(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return DrainResult{}, nil // nothing was ever spooled
		}
		return DrainResult{}, err
	}
	res := DrainResult{Busy: take(dir)}
	names, err := taken(dir)
	if err != nil {
		return res, err
	}
	for _, name := range names {
		delivered, bad, err := drainFile(name, sink, now)
		res.Delivered += delivered
		res.Bad += bad
		if err != nil {
			return res, err
		}
		if err := retry(func() error { return os.Remove(name) }); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return res, err
		}
		res.Files++
	}
	return res, nil
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

// drainFile delivers every message in the file name, which must be this
// user's alone: a file that someone else could write may hold messages
// they made up.
func drainFile(name string, sink Sink, now func() time.Time) (delivered, bad int, err error) {
	f, err := os.OpenFile(name, readFlags, 0)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, 0, err
	}
	if err := privateFile(name, fi); err != nil {
		return 0, 0, err
	}
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

// privacyError says why the inbox directory or a spool file isn't this
// user's alone (privateDir, privateFile).
type privacyError struct{ msg string }

func (e *privacyError) Error() string { return e.msg }

func notPrivate(format string, args ...any) error {
	return &privacyError{msg: fmt.Sprintf("inbox: "+format, args...)}
}

// isNotPrivate reports whether err came from privateDir or privateFile.
func isNotPrivate(err error) bool {
	var pe *privacyError
	return errors.As(err, &pe)
}
