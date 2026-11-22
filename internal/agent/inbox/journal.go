// SPDX-License-Identifier: AGPL-3.0-or-later

package inbox

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
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
// when, how, from which process and how big its body was, and what the
// agent made of where it came from (Delivery.Walk, EventVia, Harness).
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
	// spooled are the hook calls of the day that only the spool delivered,
	// for doctor's spool line (DESIGN 5.2). They are few: a call goes to
	// the spool only while the agent doesn't take it over the socket.
	spooled []spooledCall
}

// spooledCall is a hook call only the spool delivered: when its sender
// made it, and whether it was a prompt.
type spooledCall struct {
	sent time.Time
	// prompt is nil for a line from before the journal recorded it.
	prompt *bool
}

// KindHook is the inbox message kind of a hook call (hookpath.Kind).
const KindHook = "hook"

// journalLine is one line of a day file.
type journalLine struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Via      Via       `json:"via"`
	PeerPID  int       `json:"peer_pid,omitempty"`
	Sent     time.Time `json:"sent"`
	Received time.Time `json:"received"`
	Bytes    int       `json:"body_bytes"`
	// Walk is how many processes the agent's own walk of the sender's
	// parent chain held, and EventVia and Harness what the agent made of
	// them (Delivery).
	Walk     int           `json:"walk,omitempty"`
	EventVia event.Via     `json:"event_via,omitempty"`
	Harness  event.Harness `json:"harness,omitempty"`
	// Prompt says whether a hook call was a prompt, by the kind of event
	// the agent took it as (Delivery.EventKind). It is absent from other
	// messages, and from the lines of versions before it.
	Prompt *bool `json:"prompt,omitempty"`
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
		ids, spooled, err := readDay(name)
		if err != nil {
			return nil, err
		}
		j.days = append(j.days, journalDay{day: day, ids: ids, spooled: spooled})
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

// readDay reads the ids in one day file, and the hook calls in it that
// only the spool delivered, skipping lines that don't parse, such as one
// cut short when the agent was killed.
func readDay(name string) (map[[16]byte]struct{}, []spooledCall, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	ids := make(map[[16]byte]struct{})
	var spooled []spooledCall
	r := bufio.NewReader(f)
	for {
		line, long, err := readLine(r, maxJournalLine)
		if errors.Is(err, io.EOF) {
			return ids, spooled, nil
		}
		if err != nil {
			return nil, nil, err
		}
		if long || len(line) == 0 {
			continue
		}
		var l struct {
			ID     string    `json:"id"`
			Kind   string    `json:"kind"`
			Via    Via       `json:"via"`
			Sent   time.Time `json:"sent"`
			Prompt *bool     `json:"prompt"`
		}
		if json.Unmarshal(line, &l) != nil {
			continue
		}
		k, ok := idKey(l.ID)
		if !ok {
			continue
		}
		if _, dup := ids[k]; !dup && l.Kind == KindHook && l.Via == ViaInbox {
			spooled = append(spooled, spooledCall{sent: l.Sent, prompt: l.Prompt})
		}
		ids[k] = struct{}{}
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
// A message whose id it can't keep is ErrRejected.
func (j *Journal) Deliver(d Delivery) error {
	_, err := j.Store(d)
	return err
}

// Store is Deliver, and also reports whether it stored d: false with a nil
// error means the journal had d's id already. The agent hands only the
// calls it stored to the adapters, so a call that came twice, over the
// socket and through the spool, makes its records once.
func (j *Journal) Store(d Delivery) (bool, error) {
	k, ok := idKey(d.ID)
	if !ok {
		return false, fmt.Errorf("%w: its id isn't 32 lowercase hex digits", ErrRejected)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return false, errClosed
	}
	if j.has(k) {
		j.dups++
		return false, nil
	}
	day := d.Received.Wall.UTC().Format(time.DateOnly)
	if err := j.openDay(day); err != nil {
		return false, err
	}
	var prompt *bool
	if d.Kind == KindHook {
		p := d.EventKind == event.KindPromptSubmit
		prompt = &p
	}
	b, err := json.Marshal(journalLine{
		ID:       d.ID,
		Kind:     d.Kind,
		Via:      d.Via,
		PeerPID:  d.PeerPID,
		Sent:     d.Sent.UTC(),
		Received: d.Received.Wall.UTC(),
		Bytes:    len(d.Body),
		Walk:     len(d.Walk),
		EventVia: d.EventVia,
		Harness:  d.Harness,
		Prompt:   prompt,
	})
	if err != nil {
		return false, err
	}
	line := make([]byte, 0, len(b)+2)
	if j.torn {
		line = append(line, '\n')
	}
	line = append(line, b...)
	line = append(line, '\n')
	if _, err := j.f.Write(line); err != nil {
		j.torn = true // part of the line may be in the file
		return false, err
	}
	j.torn = false
	j.add(day, k)
	if d.Kind == KindHook && d.Via == ViaInbox {
		j.addSpooled(day, spooledCall{sent: d.Sent.UTC(), prompt: prompt})
	}
	j.stored++
	return true, nil
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

// addSpooled notes a hook call of day that only the spool delivered. The
// day is there: add has just added it, or found it.
func (j *Journal) addSpooled(day string, c spooledCall) {
	for i := range j.days {
		if j.days[i].day == day {
			j.days[i].spooled = append(j.days[i].spooled, c)
			return
		}
	}
}

// Spooled counts the hook calls made at since or after that only the spool
// delivered, of those the journal still keeps, and how many of them were
// prompts (DESIGN 5.2). The events of each such call get via = plugin, so
// it earns side-door credit, and doctor says how many there were. Unknown
// counts the calls whose lines don't say whether they were prompts, which
// a version before this one wrote. A call counts by when its sender made
// it (Message.Sent).
func (j *Journal) Spooled(since time.Time) (calls, prompts, unknown uint64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, d := range j.days {
		for _, c := range d.spooled {
			if c.sent.Before(since) {
				continue
			}
			calls++
			switch {
			case c.prompt == nil:
				unknown++
			case *c.prompt:
				prompts++
			}
		}
	}
	return calls, prompts, unknown
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
