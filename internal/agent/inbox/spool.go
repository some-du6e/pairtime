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

	"github.com/some-du6e/pairtime/internal/platform/clock"
)

// SpoolName is the file in the inbox directory that senders append to.
const SpoolName = "spool.jsonl"

// takenSuffix ends the name of a spool file that a drain has taken.
const takenSuffix = ".drain"

// badSuffix ends the name of a spool file that a drain set aside: one that
// drain after drain failed to read, or that isn't this user's alone.
const badSuffix = ".bad"

// MaxSpool is the size past which Append refuses to grow the spool, about
// two days of heavy use with the agent down. Every sender that spools a
// message also starts the agent (DESIGN 3.2), so the spool only fills when
// the agent can't run at all, or can't store what it drains, and then
// losing new messages beats filling the disk. Drain doesn't take the spool
// while its sink fails, so taken files don't pile up past it.
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
	// Rejected is how many messages the sink refused for good
	// (ErrRejected). They are skipped too.
	Rejected int
	// Failed is how many spool files couldn't be read this time. Each is
	// read again by the next drain, until maxFileFailures drains in a row
	// have failed it and it is set aside.
	Failed int
	// SetAside is how many spool files were renamed to end in .bad,
	// because drain after drain failed to read them or because they aren't
	// this user's alone. Drain deletes them KeepDays later.
	SetAside int
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

// maxFileFailures is how many drains in a row may fail to read a spool
// file before Drain sets it aside. What this is for, such as a disk error,
// doesn't pass; what does pass, such as running out of file descriptors,
// gets a few minutes to.
const maxFileFailures = 3

// A Drainer drains one inbox directory into one sink, again and again,
// and remembers what earlier drains left of each taken file: where the
// last one stopped reading it, and how many in a row failed to read it.
// The agent keeps one for as long as it runs. A Drainer isn't safe for
// concurrent use.
type Drainer struct {
	dir  string
	sink Sink
	now  func() clock.Stamp
	left map[string]*leftOver
}

// leftOver is what drains left of one taken file.
type leftOver struct {
	offset   int64 // where the next drain starts reading
	failures int   // drains in a row that failed to read the file
}

// NewDrainer returns a Drainer that hands the messages waiting in dir, the
// inbox directory, to sink. now is the agent's clock, clock.Now, which
// stamps each delivery as Delivery.Received, and whose wall time dates the
// files a drain sets aside.
func NewDrainer(dir string, sink Sink, now func() clock.Stamp) *Drainer {
	return &Drainer{dir: dir, sink: sink, now: now, left: make(map[string]*leftOver)}
}

// Drain drains dir into sink once, as a new Drainer's first Drain does.
func Drain(dir string, sink Sink, now func() clock.Stamp) (DrainResult, error) {
	return NewDrainer(dir, sink, now).Drain()
}

// Drain hands every message waiting in the inbox to the sink, and deletes
// each spool file once all its messages are in (DESIGN 3.5).
//
// It first reads the files that earlier drains took, oldest first, each
// from where the last drain of it stopped. A file that a drain took but
// didn't delete, because the agent was killed in between, is read again
// from the start, and the sink drops the messages it has already. Then,
// unless the sink failed, it takes the spool, by renaming it so that
// senders start a new one, and reads that too. While the sink fails, the
// spool stays where it is, so Append's MaxSpool still bounds what waits on
// disk.
//
// A message the sink refuses for good (ErrRejected) is counted and
// skipped. Any other sink error stops the drain: the file waits, and so
// does everything after it, since the same sink would get it. A file that
// can't be read is passed over, and the files after it are drained. It is
// tried again by the next drain, and set aside once maxFileFailures drains
// in a row have failed it, or at once when it isn't this user's alone
// (privateFile).
//
// Drain returns what it did and every problem it met, joined. A spool it
// couldn't take is not an error but res.Busy, and waits for the next
// drain.
func (d *Drainer) Drain() (DrainResult, error) {
	var res DrainResult
	if err := privateDir(d.dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return res, nil // nothing was ever spooled
		}
		return res, err
	}
	names, err := d.taken()
	if err != nil {
		return res, err
	}
	for name := range d.left {
		if !slices.Contains(names, name) {
			delete(d.left, name) // gone since the last drain
		}
	}
	var errs []error
	for _, name := range names {
		stop, err := d.drainFile(name, &res)
		if err != nil {
			errs = append(errs, err)
		}
		if stop {
			return res, errors.Join(errs...)
		}
	}
	name, err := take(d.dir)
	switch {
	case err != nil:
		res.Busy = err
	case name != "":
		if _, err := d.drainFile(name, &res); err != nil {
			errs = append(errs, err)
		}
	}
	return res, errors.Join(errs...)
}

// drainFile drains the taken file name: it hands the sink its messages
// from where the last drain of it stopped, and deletes it once they are
// all in. stop says that the sink failed, and the drain must stop.
func (d *Drainer) drainFile(name string, res *DrainResult) (stop bool, err error) {
	left := d.left[name]
	if left == nil {
		left = &leftOver{}
		d.left[name] = left
	}
	from := left.offset
	sinkErr, err := d.deliver(name, left, res)
	if sinkErr != nil {
		return true, fmt.Errorf("inbox: the sink failed on a message in %s, which waits for the next drain: %w", filepath.Base(name), sinkErr)
	}
	switch {
	case err == nil:
		err = retry(func() error { return os.Remove(name) })
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			delete(d.left, name)
			res.Files++
			return false, nil
		}
	case errors.Is(err, fs.ErrNotExist):
		delete(d.left, name) // gone before it could be read
		return false, nil
	}
	// The file is the trouble, not the sink.
	res.Failed++
	if left.offset > from {
		left.failures = 0 // it got further than the last drain did
	}
	left.failures++
	if left.failures < maxFileFailures && !isNotPrivate(err) {
		return false, fmt.Errorf("inbox: reading %s, which waits for the next drain: %w", filepath.Base(name), err)
	}
	delete(d.left, name)
	if serr := setAside(name, d.now().Wall); serr != nil {
		return false, fmt.Errorf("inbox: reading %s: %w; and setting it aside: %w", filepath.Base(name), err, serr)
	}
	res.SetAside++
	return false, fmt.Errorf("inbox: set %s aside: %w", filepath.Base(name), err)
}

// deliver hands the sink the messages in the file name from left.offset
// on, and moves left.offset past each line once it is done with it. The
// sink stored the lines before it durably (Sink), so a drain after a
// failure starts where this one stopped instead of reading the file from
// the start again. It returns the sink's error or the file's.
func (d *Drainer) deliver(name string, left *leftOver, res *DrainResult) (sinkErr, fileErr error) {
	if fi, err := os.Lstat(name); err != nil {
		return nil, err
	} else if !fi.Mode().IsRegular() {
		return nil, notPrivate("%s isn't a regular file", name)
	}
	f, err := os.OpenFile(name, readFlags, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if err := privateFile(name, fi); err != nil {
		return nil, err
	}
	if _, err := f.Seek(left.offset, io.SeekStart); err != nil {
		return nil, err
	}
	base := left.offset
	cr := &countReader{r: f}
	r := bufio.NewReaderSize(cr, 64<<10)
	// pos is where in the file the next line starts.
	pos := func() int64 { return base + cr.n - int64(r.Buffered()) }
	for {
		line, long, err := readLine(r, MaxMessage)
		if errors.Is(err, io.EOF) {
			left.offset = pos()
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		switch {
		case long:
			res.Bad++
		case len(line) == 0:
			// the blank line between two records
		default:
			m, err := Decode(line)
			if err != nil {
				res.Bad++
				break
			}
			// The spool doesn't say who wrote the line or when, so there is
			// no PeerPID, and Received is the agent's stamp from now
			// (ViaInbox, Delivery).
			if err := d.sink.Deliver(Delivery{Message: m, Via: ViaInbox, Received: d.now()}); err != nil {
				if !errors.Is(err, ErrRejected) {
					return err, nil
				}
				res.Rejected++
				break
			}
			res.Delivered++
		}
		left.offset = pos()
	}
}

// countReader counts the bytes read through it.
type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// take renames the spool to a name that sorts after every file taken
// before, unless the clock went back, and returns that name, or "" when
// nothing was spooled since the last drain.
func take(dir string) (string, error) {
	src := filepath.Join(dir, SpoolName)
	dst := filepath.Join(dir, fmt.Sprintf("spool-%020d-%s%s", time.Now().UnixNano(), NewID()[:8], takenSuffix))
	err := retry(func() error { return os.Rename(src, dst) })
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return dst, nil
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

// Scan hands the messages the inbox in dir holds, which no drain has
// finished with, to each, and reports whether each returned true for one:
// the spool's first, then those of the files drains took, oldest first,
// each file's in the order its senders appended them, up to the first
// limit messages of a file. It reads a message a drain has handed to its
// sink already again, since a drain keeps how far it got to itself. It
// reads the spool before it lists the taken files, so a message the spool
// held as Scan began is read even when a drain takes the spool meanwhile,
// and a file that is gone by the time Scan opens it was deleted by a drain
// that had handed all of it over. Lines that aren't a message are passed
// over, and so are files no drain reads: one set aside (.bad), one that
// isn't this user's alone (privateFile), or one that can't be read, which
// a drain sets aside in time. Scan writes nothing, so a drain can run
// meanwhile.
func Scan(dir string, limit int, each func(Message) bool) bool {
	if scanFile(filepath.Join(dir, SpoolName), limit, each) {
		return true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	var names []string
	for _, e := range entries {
		if n := e.Name(); strings.HasPrefix(n, "spool-") && strings.HasSuffix(n, takenSuffix) {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	for _, n := range names {
		if scanFile(filepath.Join(dir, n), limit, each) {
			return true
		}
	}
	return false
}

// scanFile is Scan of the one file name.
func scanFile(name string, limit int, each func(Message) bool) bool {
	if fi, err := os.Lstat(name); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	f, err := os.OpenFile(name, readFlags, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || privateFile(name, fi) != nil {
		return false
	}
	r := bufio.NewReaderSize(f, 64<<10)
	for n := 0; n < limit; {
		line, long, err := readLine(r, MaxMessage)
		if err != nil {
			return false
		}
		if long || len(line) == 0 {
			continue
		}
		m, err := Decode(line)
		if err != nil {
			continue
		}
		n++
		if each(m) {
			return true
		}
	}
	return false
}

// taken lists the spool files that drains took, oldest first, and deletes
// the ones set aside more than KeepDays ago. A file set aside can hold
// prompts, and the inbox is no place to keep them (DESIGN 3.5).
func (d *Drainer) taken() ([]string, error) {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return nil, err
	}
	cutoff := d.now().Wall.Add(-KeepDays * 24 * time.Hour)
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, "spool-") {
			continue
		}
		switch {
		case strings.HasSuffix(n, takenSuffix):
			names = append(names, filepath.Join(d.dir, n))
		case strings.HasSuffix(n, badSuffix):
			if fi, err := e.Info(); err == nil && fi.ModTime().Before(cutoff) {
				os.Remove(filepath.Join(d.dir, n))
			}
		}
	}
	slices.Sort(names)
	return names, nil
}

// setAside renames the taken file name to end in .bad, so that drains
// leave it alone, and dates it now, which starts the KeepDays after which
// Drain deletes it.
func setAside(name string, now time.Time) error {
	bad := strings.TrimSuffix(name, takenSuffix) + badSuffix
	if err := retry(func() error { return os.Rename(name, bad) }); err != nil {
		return err
	}
	os.Chtimes(bad, now, now) // best effort: Drain then goes by its last write
	return nil
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
