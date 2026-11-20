// SPDX-License-Identifier: AGPL-3.0-or-later

package inbox

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// KeepDays is how many days of ids a Journal remembers, and how long it
// keeps its day files.
const KeepDays = 7

// maxJournalLine bounds a journal line when the journal is read back. Its
// own lines are under 300 bytes; a longer one is damage and is skipped.
const maxJournalLine = 64 << 10

// errClosed means Deliver was called on a closed Journal.
var errClosed = errors.New("inbox: the journal is closed")

// Journal is the Sink the agent stores messages in until the ledger writer
// takes its place (WP-LG5, WP-AG4). It writes one JSON line per message to
// YYYY-MM-DD.jsonl in its directory, by the UTC day the agent took the
// message in, and it keeps no bodies: a body can hold prompt text, which
// only the vault may keep (DESIGN 3.5). A line says which message arrived,
// when, how, from which process and how big its body was.
//
// A line is written before Deliver returns, so it survives the agent being
// killed. It isn't flushed to the disk, so a power cut can lose it, and
// with it messages the agent had acked.
//
// To drop duplicates the journal keeps the ids of its last KeepDays days in
// memory, about 20 bytes each. A duplicate can only come from the spool,
// which the agent drains within a minute of running, so a week is ample.
type Journal struct {
	dir string

	mu     sync.Mutex
	days   []journalDay // the ids of each day file, oldest first
	f      *os.File     // the day file being appended to
	fday   string       // its day
	torn   bool         // f doesn't end in a newline, so the next line starts with one
	closed bool
	stored uint64
	dups   uint64
}

type journalDay struct {
	day string
	ids map[[16]byte]struct{}
}

// journalLine is one line of a day file.
type journalLine struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Via      Via       `json:"via"`
	PeerPID  int       `json:"peer_pid,omitempty"`
	Sent     time.Time `json:"sent"`
	Received time.Time `json:"received"`
	Bytes    int       `json:"body_bytes"`
}

// OpenJournal opens the journal in dir, making dir if it has to, readable
// by this user only where the OS has file modes. It reads back the ids of
// the last KeepDays days before now and deletes older day files.
func OpenJournal(dir string, now time.Time) (*Journal, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	j := &Journal{dir: dir}
	oldest := firstKept(now)
	// ReadDir sorts by name, so the days come oldest first.
	for _, e := range entries {
		day, ok := strings.CutSuffix(e.Name(), ".jsonl")
		if !ok || !e.Type().IsRegular() || !validDay(day) {
			continue
		}
		name := filepath.Join(dir, e.Name())
		if day < oldest {
			if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
			continue
		}
		ids, err := readIDs(name)
		if err != nil {
			return nil, err
		}
		j.days = append(j.days, journalDay{day: day, ids: ids})
	}
	return j, nil
}

// firstKept is the oldest day the journal keeps on the day of now.
func firstKept(now time.Time) string {
	return now.UTC().AddDate(0, 0, -KeepDays).Format(time.DateOnly)
}

func validDay(day string) bool {
	_, err := time.Parse(time.DateOnly, day)
	return err == nil
}

// readIDs reads the ids in one day file, skipping lines that don't parse,
// such as one cut short when the agent was killed.
func readIDs(name string) (map[[16]byte]struct{}, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	ids := make(map[[16]byte]struct{})
	r := bufio.NewReader(f)
	for {
		line, long, err := readLine(r, maxJournalLine)
		if errors.Is(err, io.EOF) {
			return ids, nil
		}
		if err != nil {
			return nil, err
		}
		if long || len(line) == 0 {
			continue
		}
		var l struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(line, &l) != nil {
			continue
		}
		if k, ok := idKey(l.ID); ok {
			ids[k] = struct{}{}
		}
	}
}

// idKey is a message id as 16 bytes, which take less memory than the
// 32-digit string.
func idKey(id string) ([16]byte, bool) {
	var k [16]byte
	if !validID(id) {
		return k, false
	}
	_, err := hex.Decode(k[:], []byte(id))
	return k, err == nil
}

// Deliver stores d, unless the journal has its id already, in which case it
// returns nil and stores nothing. It is safe to call from many goroutines.
func (j *Journal) Deliver(d Delivery) error {
	k, ok := idKey(d.ID)
	if !ok {
		return errors.New("inbox: a message id must be 32 lowercase hex digits")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errClosed
	}
	if j.has(k) {
		j.dups++
		return nil
	}
	day := d.Received.UTC().Format(time.DateOnly)
	if err := j.openDay(day); err != nil {
		return err
	}
	b, err := json.Marshal(journalLine{
		ID:       d.ID,
		Kind:     d.Kind,
		Via:      d.Via,
		PeerPID:  d.PeerPID,
		Sent:     d.Sent.UTC(),
		Received: d.Received.UTC(),
		Bytes:    len(d.Body),
	})
	if err != nil {
		return err
	}
	line := make([]byte, 0, len(b)+2)
	if j.torn {
		line = append(line, '\n')
	}
	line = append(line, b...)
	line = append(line, '\n')
	if _, err := j.f.Write(line); err != nil {
		j.torn = true // part of the line may be in the file
		return err
	}
	j.torn = false
	j.add(day, k)
	j.stored++
	return nil
}

func (j *Journal) has(k [16]byte) bool {
	for _, d := range j.days {
		if _, ok := d.ids[k]; ok {
			return true
		}
	}
	return false
}

func (j *Journal) add(day string, k [16]byte) {
	for i := range j.days {
		if j.days[i].day == day {
			j.days[i].ids[k] = struct{}{}
			return
		}
	}
	j.days = append(j.days, journalDay{day: day, ids: map[[16]byte]struct{}{k: {}}})
}

// openDay makes the file for day the one Deliver appends to. A new day also
// forgets the days that fell out of the window and deletes their files.
func (j *Journal) openDay(day string) error {
	if j.f != nil && j.fday == day {
		return nil
	}
	if j.f != nil {
		j.f.Close()
		j.f, j.fday = nil, ""
	}
	name := filepath.Join(j.dir, day+".jsonl")
	f, err := os.OpenFile(name, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	// An agent killed partway through a line leaves the file without its
	// last newline. The next line must not join that fragment, or the id
	// it carries would be lost when the journal is read back.
	j.torn = false
	if fi, err := f.Stat(); err != nil {
		j.torn = true
	} else if fi.Size() > 0 {
		var last [1]byte
		_, err := f.ReadAt(last[:], fi.Size()-1)
		j.torn = err != nil || last[0] != '\n'
	}
	j.f, j.fday = f, day
	j.prune(day)
	return nil
}

// prune forgets the days before the window that ends on day and deletes
// their files. Deleting is best effort: OpenJournal tries again.
func (j *Journal) prune(day string) {
	t, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return
	}
	oldest := firstKept(t)
	kept := j.days[:0]
	for _, d := range j.days {
		if d.day < oldest {
			os.Remove(filepath.Join(j.dir, d.day+".jsonl"))
			continue
		}
		kept = append(kept, d)
	}
	clear(j.days[len(kept):])
	j.days = kept
}

// Stats returns how many messages the journal stored since it was opened,
// and how many duplicates it dropped.
func (j *Journal) Stats() (stored, duplicates uint64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.stored, j.dups
}

// Close closes the day file. Deliver fails after Close.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.closed = true
	if j.f == nil {
		return nil
	}
	err := j.f.Close()
	j.f, j.fday = nil, ""
	return err
}
