// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import (
	"cmp"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

// app is the app a T3 Code record names, and the app T3's keys are under.
const app = string(event.RelayAppT3Code)

// claudeDriver is T3's driver for Claude Code, whose session ids the hook
// keys as session_id.
const claudeDriver = "claudeAgent"

const (
	// startSlack is how much earlier than its run's start a prompt may be
	// stamped and still be the one the run delivered: T3 stamps the start
	// just before it hands the turn to Claude Code, and the hook stamps the
	// prompt after, on the same clock.
	startSlack = 2000
	// resyncMargin is how much older than RELAY_SEEN_LAG_S a resync looks.
	resyncMargin = 5000
	// watchMargin is how long past RELAY_MATCH_S the reader remembers a
	// message's run, and a prompt it was asked about.
	watchMargin = 60_000
	// guestWatch is how long the reader watches a guest's run for the
	// prompt it delivers. A held queue has no bound, so this is long.
	guestWatch = 24 * 60 * 60 * 1000
	// maxWatched and maxPrompts bound what the reader remembers. Past them
	// it forgets the oldest.
	maxWatched = 1024
	maxPrompts = 1024
	// maxWindows bounds how many windows one read takes, so a reader far
	// behind catches up over several reads.
	maxWindows = 20
)

// How often the probe runs again on a store it passed: every reprobe, or
// every reprobeUnsampled while a table had no row to sample.
const (
	reprobe          = 10 * time.Minute
	reprobeUnsampled = time.Minute
)

// FolderState is what pairtime's project resolver says of a T3 thread's
// folder.
type FolderState uint8

const (
	// FolderPending says the resolver hasn't answered yet. The reader reads
	// nothing more of the folder's rows meanwhile, and asks again on its next
	// reads while a row is young enough to record (RELAY_SEEN_LAG_S).
	FolderPending FolderState = iota
	// FolderTracked is a folder pairtime tracks, whose project is
	// Folder.Project.
	FolderTracked
	// FolderOut is a folder pairtime doesn't track or ignores (DESIGN 8.8).
	// The reader reads nothing more of its rows.
	FolderOut
)

// Folder is pairtime's own word on a folder a T3 thread runs in.
type Folder struct {
	State FolderState
	// Project is the folder's project_key, when State is FolderTracked.
	Project string
}

// Options configure a Reader.
type Options struct {
	// Stores are where T3's store may be, from Locate. Each read reads the
	// first one T3 holds open. Required.
	Stores []Store
	// SQLite runs sqlite3. Nil means a Runner for "sqlite3" on PATH.
	SQLite *Runner
	// Keys keys T3's own ids (Keys.App) and the Claude Code sessions its
	// runs go to, as the hook keys session_id (Keys.Session). Required.
	Keys *adapters.Keys
	// Folder is pairtime's own project resolver's word on a folder, as T3
	// stores it: a thread's worktree, else its project's root. Required.
	Folder func(dir string) Folder
	// Table holds RELAY_SEEN_LAG_S, RELAY_MATCH_S and SUBSTANTIVE_CHARS. Nil
	// means the newest table this build embeds, which the agent scores with.
	Table *params.Table
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// Zone is the time zone Status counts days in. Nil means time.Local.
	Zone *time.Location
	// State is what State returned before the agent last stopped, from
	// state.json, or the zero State on a first run.
	State State
}

// Reader reads T3 Code's store for the agent's apps component (DESIGN 3.3,
// 5.1). It is safe for concurrent use: reads take turns.
type Reader struct {
	stores []Store
	sqlite *Runner
	keys   *adapters.Keys
	folder func(string) Folder
	table  *params.Table
	lag    int64 // RELAY_SEEN_LAG_S, in ms
	match  int64 // RELAY_MATCH_S, in ms
	now    func() time.Time
	zone   *time.Location

	mu      sync.Mutex
	store   string    // the store the last read read
	probed  time.Time // when the probe last passed, zero until it has
	schema  schema
	cursor  *Cursor
	watched []*watch
	prompts []*asked
	held    map[int64]heldRow
	status  Status
	// reads counts the reads, and checked is the one whose window last read
	// the watched rows again.
	reads, checked int
}

// watch is a row the reader still watches: a message it recorded, until it
// pairs or RELAY_MATCH_S passes, or a guest's message that started a run,
// until the run delivers its prompt. It holds T3's ids by key alone.
type watch struct {
	rowid            int64
	msg, run         string // keys of the message and run ids, "" until first read
	created, updated int64  // the stamps as first read, Unix ms
	guest            bool
	// record is the relay.message's event id, for a message.
	record string
	// out says the record went out in an earlier batch.
	out bool
	// sessions are the session keys of the Claude Code sessions the run
	// went to, start and done its start and end, Unix ms, 0 until known.
	sessions    []string
	start, done int64
	// claimed is the event id of the prompt the run delivered.
	claimed string
	// changed says the row changed after the reader first saw it, or its run
	// no longer names it: it pairs with nothing.
	changed bool
}

// asked is a prompt the agent asked about (Pair).
type asked struct {
	session string
	t       int64
	id      string
	claimed bool
}

// heldRow is a new row whose folder the resolver hadn't answered for.
type heldRow struct {
	created int64
	folder  string
}

// New returns a Reader.
func New(o Options) (*Reader, error) {
	if len(o.Stores) == 0 {
		return nil, errors.New("t3code: no store to read")
	}
	if o.Keys == nil {
		return nil, errors.New("t3code: no keys")
	}
	if o.Folder == nil {
		return nil, errors.New("t3code: no folder resolver")
	}
	t := o.Table
	if t == nil {
		tables := params.Tables()
		t = tables[len(tables)-1]
	}
	for _, name := range []string{"RELAY_SEEN_LAG_S", "RELAY_MATCH_S"} {
		if _, ok := t.Text(name); !ok {
			return nil, fmt.Errorf("t3code: parameter table %s has no %s", t.Version(), name)
		}
	}
	r := &Reader{
		stores: o.Stores,
		sqlite: o.SQLite,
		keys:   o.Keys,
		folder: o.Folder,
		table:  t,
		lag:    t.Int(params.RELAY_SEEN_LAG_S) * 1000,
		match:  t.Int(params.RELAY_MATCH_S) * 1000,
		now:    o.Now,
		zone:   o.Zone,
		held:   map[int64]heldRow{},
	}
	if r.sqlite == nil {
		r.sqlite = &Runner{}
	}
	if r.now == nil {
		r.now = time.Now
	}
	if r.zone == nil {
		r.zone = time.Local
	}
	r.load(o.State)
	return r, nil
}

// Batch is what one read found for the agent to record.
type Batch struct {
	// Messages are the relay.message records to write, in the order T3
	// stored the rows.
	Messages []Message
	// Guests are the guest messages the read found, without their text: each
	// one that started a run of its own, which the reader watches so the
	// prompt the run delivers keeps no text, and each one the owner steered
	// into a running turn (GuestRun.Steered).
	Guests []GuestRun
}

// Message is a relay.message record to write (SM 1.2, DESIGN 4.1 E-12): its
// data, and what its envelope takes from the reader. The agent writes it
// with source log and via app, at SeenAt, through its usual record path.
type Message struct {
	// EventID is the record's event_id: a UUIDv7 at the stamp whose other
	// bits are keyed by the message's id and stamp, so a row read again
	// after a crash makes the same record, which pkg/score counts once.
	EventID string
	// SeenAt is when the reader read the row: the record's t, at most
	// RELAY_SEEN_LAG_S after its stamp.
	SeenAt time.Time
	// Harness is claude_code when T3 runs the message's thread on Claude
	// Code, and none when it didn't say.
	Harness event.Harness
	// SessionKey is the Claude Code session T3 bound the message's run to,
	// keyed as the hook keys session_id, or "" when the run hasn't started:
	// the envelope's session_key, none then.
	SessionKey string
	// ProjectKey is the project of the thread's folder: the envelope's
	// project_key.
	ProjectKey string
	// Data is the record's data. Its Prompt names the prompt.submit the
	// message paired with when that prompt came first, so the agent writes
	// that prompt before this record.
	Data event.RelayMessage
	// Class is SM 2.2's class of the message's own text, as far as the
	// reader can tell it: never an answer, since the reader doesn't follow
	// the session's questions. The ledger writer gives the record its f.pc
	// by the same rule with them (score.RelayClass; ledger-v1 10.7).
	Class event.PromptClass
}

// GuestRun is a guest's message the read found. Its text was never read.
type GuestRun struct {
	// SentAt is the message's stamp, Unix ms.
	SentAt int64
	// Steered says the owner steered it into a running turn, so it started
	// no run of its own. Whether Claude Code reports a steer as a prompt
	// isn't known yet, so the reader marks no prompt for it.
	Steered bool
}

// Prompt is a prompt.submit of a session T3's server started, which the
// agent asks the reader about before it keeps the prompt's text.
type Prompt struct {
	// SessionKey is the prompt's session_key.
	SessionKey string
	// T is the prompt's t, Unix ms.
	T int64
	// EventID is the prompt's event_id, which a record written after it
	// names.
	EventID string
}

// Pairing is the reader's answer about a prompt (Pair).
type Pairing struct {
	// Known says the reader read T3's store for this answer. When it is
	// false, the reader can't tell this prompt from any other, so the agent
	// keeps the prompt's text as it keeps any prompt's (DESIGN 5.1), unless
	// Guest is set: a guest's run an earlier read saw can still claim the
	// prompt, which can only cost it credit. Relay is never set then.
	Known bool
	// Guest says a guest's message started the run that delivered the
	// prompt: the prompt is marked guest and keeps no text.
	Guest bool
	// Relay is the event id of the relay.message, from an earlier batch, the
	// prompt pairs with: the prompt's relay. When the record is in this
	// answer's own batch instead, the record names the prompt.
	Relay string
}

// Errors that say why the reader didn't read, naming no path.
var (
	// ErrNoStore means none of the places T3's store may be holds one.
	ErrNoStore = errors.New("T3 Code's store isn't there")
	// ErrNotRunning means T3 isn't running: no store has the -wal and -shm
	// files T3 holds open while it runs.
	ErrNotRunning = errors.New("T3 Code isn't running")
)

// SchemaError says the schema probe turned T3's store down (DESIGN 5.1).
type SchemaError struct{ Why string }

func (e *SchemaError) Error() string { return "T3 Code's schema isn't one pairtime reads: " + e.Why }

// Why the reader doesn't read (Status.Off).
const (
	OffNoStore    = "no_store"
	OffNotRunning = "not_running"
	OffNoSQLite3  = "no_sqlite3"
	OffSchema     = "schema"
	OffFailed     = "failed"
)

// Counts are what the reader did on one day, for doctor (DESIGN 5.9).
type Counts struct {
	// Witnessed counts the records of declared none it wrote: messages T3
	// says a person sent.
	Witnessed int
	// Declared counts the records of declared cron or cross_session.
	Declared int
	// Paired counts the records that paired with a prompt.
	Paired int
	// Aged counts the rows the age test turned away: first read more than
	// RELAY_SEEN_LAG_S after their stamp.
	Aged int
	// Edited counts the rows edited before the reader first read them, or
	// changed after, which witness nothing.
	Edited int
	// NoRun counts the rows that had no run of their own, such as steers.
	NoRun int
	// Marked counts the rows T3 marked as no person's: idle compaction, a
	// workflow's script, a notification, a delegated task's results.
	Marked int
	// OtherHarness counts the rows whose thread runs on a provider other
	// than Claude Code.
	OtherHarness int
	// Guests counts guests' messages.
	Guests int
}

// Status says how the reader is doing, for doctor (DESIGN 5.9). It names
// no path.
type Status struct {
	// Reading says the last read worked.
	Reading bool
	// Off says why the reader doesn't read, when Reading is false: OffNoStore,
	// OffNotRunning, OffNoSQLite3, OffSchema or OffFailed.
	Off string
	// Problem says more: the schema the probe refused, or the read that
	// failed.
	Problem string
	// Newer says T3's schema is newer than the one this reader was tested
	// against, which it reads anyway.
	Newer bool
	// Last is when a read last worked.
	Last time.Time
	// Day is the day Counts are for, as YYYY-MM-DD in Options.Zone.
	Day string
	Counts
}

// Status returns how the reader is doing.
func (r *Reader) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rollDay(r.now())
	return r.status
}

// add adds d to c.
func (c *Counts) add(d Counts) {
	c.Witnessed += d.Witnessed
	c.Declared += d.Declared
	c.Paired += d.Paired
	c.Aged += d.Aged
	c.Edited += d.Edited
	c.NoRun += d.NoRun
	c.Marked += d.Marked
	c.OtherHarness += d.OtherHarness
	c.Guests += d.Guests
}

// rollDay starts the counts afresh on a new day.
func (r *Reader) rollDay(now time.Time) {
	if day := now.In(r.zone).Format("2006-01-02"); day != r.status.Day {
		r.status.Day, r.status.Counts = day, Counts{}
	}
}

// Poll reads what T3 stored since the last read and returns the records to
// write: every 10 s while T3 runs (DESIGN 3.3). It reads nothing while no
// store has the -wal file T3 holds open, so a closed T3 relays nothing.
// It returns the records of the windows it finished even with an error, so
// the agent writes a batch whatever the error says.
func (r *Reader) Poll(ctx context.Context) (Batch, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.poll(ctx)
}

// Pair is Poll for a prompt.submit of a session T3's server started, which
// the agent asks about before it keeps the prompt's text (DESIGN 5.1). It
// says whether a guest's run delivered the prompt and which record it pairs
// with: the run T3 started for a message goes to a Claude Code session, and
// the first prompt of that session at or after the run's start, while the
// run lasts, is the one it delivered. A record in the batch that names the
// prompt is written after the prompt. The reader remembers the prompt for
// RELAY_MATCH_S even when it can't answer, so a later read can still pair
// a message whose row it hadn't read yet, and so no later prompt of the run
// takes the one it delivered.
func (r *Reader) Pair(ctx context.Context, p Prompt) (Pairing, Batch, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.EventID == "" {
		return Pairing{}, Batch{}, errors.New("t3code: a prompt with no event_id")
	}
	if !slices.ContainsFunc(r.prompts, func(a *asked) bool { return a.id == p.EventID }) {
		r.prompts = append(r.prompts, &asked{session: p.SessionKey, t: p.T, id: p.EventID})
	}
	b, err := r.poll(ctx)
	out := Pairing{Known: err == nil}
	for _, w := range r.watched {
		if w.claimed != p.EventID {
			continue
		}
		switch {
		case w.guest:
			out.Guest = true
		case !slices.ContainsFunc(b.Messages, func(m Message) bool { return m.EventID == w.record }):
			out.Relay = w.record
		}
	}
	return out, b, err
}

// poll is one read.
func (r *Reader) poll(ctx context.Context) (Batch, error) {
	now := r.now()
	r.reads++
	r.rollDay(now)
	r.prune(now.UnixMilli())
	var b Batch
	err := r.read(ctx, now, &b)
	if err != nil {
		r.fail(err)
	} else {
		r.status.Reading, r.status.Off, r.status.Problem, r.status.Last = true, "", "", now
	}
	r.pair(&b, r.checked == r.reads)
	for _, w := range r.watched {
		w.out = w.out || w.record != ""
	}
	return b, err
}

// fail notes why a read failed.
func (r *Reader) fail(err error) {
	r.status.Reading, r.status.Problem = false, ""
	var se *SchemaError
	var qe *QueryError
	switch {
	case errors.Is(err, ErrNoStore):
		r.status.Off = OffNoStore
	case errors.Is(err, ErrNotRunning):
		r.status.Off = OffNotRunning
	case errors.Is(err, ErrNoSQLite3):
		r.status.Off = OffNoSQLite3
	case errors.As(err, &se):
		r.status.Off, r.status.Problem = OffSchema, se.Why
	case errors.As(err, &qe):
		r.status.Off, r.status.Problem = OffFailed, qe.Error()
	default:
		r.status.Off, r.status.Problem = OffFailed, err.Error()
	}
}

// read finds the store, probes it when due, and reads its windows.
func (r *Reader) read(ctx context.Context, now time.Time, b *Batch) error {
	store, err := r.pick()
	if err != nil {
		return err
	}
	if _, err := r.sqlite.Exe(); err != nil {
		return err
	}
	if store != r.store {
		r.store, r.probed = store, time.Time{}
	}
	if err := r.probe(ctx, now); err != nil {
		return err
	}
	if r.cursor == nil {
		c, err := r.at(ctx, startSQL())
		if err != nil {
			return err
		}
		r.cursor = &c
	}
	resynced := false
	for range maxWindows {
		more, anomaly, err := r.window(ctx, b)
		if err != nil {
			return err
		}
		if anomaly {
			if resynced {
				return errors.New("T3 Code's store moved under the cursor twice in one read")
			}
			resynced = true
			floor := max(r.cursor.CreatedAt, r.now().UnixMilli()-r.lag-resyncMargin)
			c, err := r.at(ctx, resyncSQL(stamp(floor)))
			if err != nil {
				return err
			}
			r.cursor = &c
			r.held = map[int64]heldRow{}
			r.probed = time.Time{}
			if err := r.probe(ctx, r.now()); err != nil {
				return err
			}
			continue
		}
		if !more {
			break
		}
	}
	return nil
}

// pick returns the first store T3 holds open.
func (r *Reader) pick() (string, error) {
	found := false
	for _, s := range r.stores {
		if running(s.Path) {
			return s.Path, nil
		}
		found = found || exists(s.Path)
	}
	if found {
		return "", ErrNotRunning
	}
	return "", ErrNoStore
}

// query runs sql on the store the read picked, while T3 still holds it
// open. T3 can stop between two of a read's runs of sqlite3, and a run on a
// store whose -wal and -shm files are gone makes them in T3's folder and
// leaves them there, after which the store looks held open though T3 isn't
// running (DESIGN 5.1).
func (r *Reader) query(ctx context.Context, sql string) ([]row, error) {
	if !running(r.store) {
		return nil, ErrNotRunning
	}
	return r.sqlite.Query(ctx, r.store, sql)
}

// probe runs the schema probe when it is due.
func (r *Reader) probe(ctx context.Context, now time.Time) error {
	if !r.probed.IsZero() {
		every := reprobe
		if len(r.schema.unsampled) > 0 {
			every = reprobeUnsampled
		}
		if now.Sub(r.probed) < every {
			return nil
		}
	}
	rows, err := r.query(ctx, probeSQL())
	var qe *QueryError
	switch {
	case errors.As(err, &qe) && containsAny(qe.Detail, "no such function: json"):
		return &SchemaError{"this sqlite3 has no JSON functions"}
	case errors.As(err, &qe) && containsAny(qe.Detail, "no such table", "not a database"):
		return &SchemaError{"it isn't a store of T3 Code's version 2 schema"}
	case err != nil:
		return err
	}
	s, err := checkProbe(rows)
	var re *refusal
	if errors.As(err, &re) {
		return &SchemaError{re.why}
	}
	if err != nil {
		return err
	}
	r.schema, r.probed, r.status.Newer = s, now, s.newer()
	return nil
}

// at reads a cursor from startSQL's or resyncSQL's one row.
func (r *Reader) at(ctx context.Context, sql string) (Cursor, error) {
	rows, err := r.query(ctx, sql)
	if err != nil {
		return Cursor{}, err
	}
	if len(rows) != 1 || len(rows[0]) != 4 || rows[0][0].s != "at" {
		return Cursor{}, ErrOutput
	}
	return r.cursorAt(rows[0][1], rows[0][2], rows[0][3])
}

// cursorAt makes a cursor of a rowid, the message id there and its stamp.
// An empty table puts it at 0.
func (r *Reader) cursorAt(rowid, id, created field) (Cursor, error) {
	var c Cursor
	if !rowid.null {
		n, err := strconv.ParseInt(rowid.s, 10, 64)
		if err != nil {
			return Cursor{}, ErrOutput
		}
		c.RowID = n
	}
	if !id.null {
		c.Message = r.key("message", id.s)
		c.CreatedAt, _ = ms(created)
	}
	return c, nil
}

// window reads the rows after the cursor, at most windowRows of them, and the
// rows the reader watches. It reports whether more rows wait, and whether
// the cursor check failed, in which case it read nothing. It changes the
// reader's memory only once every query of the window has answered, so a
// window that fails is read again whole.
func (r *Reader) window(ctx context.Context, b *Batch) (more, anomaly bool, err error) {
	c := r.cursor.RowID
	rowids := make([]int64, 0, len(r.watched))
	for _, w := range r.watched {
		if !w.changed && w.claimed == "" {
			rowids = append(rowids, w.rowid)
		}
	}
	rows, err := r.query(ctx, windowSQL(c, rowids))
	if err != nil {
		return false, false, err
	}
	var cur, end row
	var folds, guests []row
	items := map[int64][]row{}
	for _, x := range rows {
		switch t := x[0].s; {
		case t == "cur" && len(x) == 4:
			cur = x
		case t == "end" && len(x) == 4:
			end = x
		case t == "fold" && len(x) == 5:
			folds = append(folds, x)
		case t == "guest" && len(x) == 4:
			guests = append(guests, x)
		case t == "it" && len(x) == 16:
			n, ok := rowid(x[1])
			if !ok {
				return false, false, ErrOutput
			}
			items[n] = append(items[n], x)
		default:
			return false, false, ErrOutput
		}
	}
	if cur == nil {
		return false, false, ErrOutput
	}
	top, ok := rowid(cur[1])
	if !ok && !cur[1].null {
		return false, false, ErrOutput
	}
	if r.moved(top, cur[2]) {
		return false, true, nil
	}
	hi := min(c+windowRows, top)
	next := Cursor{RowID: max(hi, c)}
	if end != nil {
		if next, err = r.cursorAt(end[1], end[2], end[3]); err != nil {
			return false, false, err
		}
	}
	var n Counts

	// The folder test, by pairtime's own resolver: the rows of this window,
	// and those held for a folder it hadn't answered for.
	seen := r.now()
	verdicts := map[string]Folder{}
	verdict := func(dir string) Folder {
		v, ok := verdicts[dir]
		if !ok {
			v = r.folder(dir)
			verdicts[dir] = v
		}
		return v
	}
	held := maps.Clone(r.held)
	if held == nil {
		held = map[int64]heldRow{}
	}
	for _, f := range folds {
		id, ok := rowid(f[1])
		if !ok {
			return false, false, ErrOutput
		}
		created, ok := ms(f[2])
		if !ok {
			continue
		}
		dir := f[4].s
		if !f[3].null {
			dir = f[3].s
		}
		held[id] = heldRow{created: created, folder: dir}
	}
	var allowed []string
	var decide []int64
	for id, h := range held {
		v := verdict(h.folder)
		if v.State == FolderOut || v.State == FolderTracked && v.Project == "" {
			delete(held, id)
			continue
		}
		// A new row in a tracked folder goes to the decision, which applies
		// the age test with the others. A held one whose folder took too
		// long to answer for is turned away here.
		if (v.State == FolderPending || id <= c) && seen.UnixMilli()-h.created > r.lag {
			n.Aged++
			delete(held, id)
			continue
		}
		if v.State == FolderTracked {
			if !slices.Contains(allowed, h.folder) {
				allowed = append(allowed, h.folder)
			}
			if id <= c {
				decide = append(decide, id)
			}
		}
	}
	slices.Sort(decide)
	slices.Sort(allowed)

	// The decision.
	var picked []*candidate
	if len(allowed) > 0 && (hi > c || len(decide) > 0) {
		rows, err := r.query(ctx, decideSQL(c, hi, decide, allowed))
		if err != nil {
			return false, false, err
		}
		for _, x := range rows {
			if len(x) != 14 || x[0].s != "row" {
				return false, false, ErrOutput
			}
			id, ok := rowid(x[1])
			if !ok {
				return false, false, ErrOutput
			}
			delete(held, id)
			if cand := r.decide(x, seen, verdict, &n); cand != nil {
				cand.rowid = id
				picked = append(picked, cand)
			}
		}
	}
	slices.SortFunc(picked, func(a, b *candidate) int { return cmp.Compare(a.rowid, b.rowid) })
	// Rows of tracked folders that the decision didn't return were turned
	// away by its WHERE clause; only the pending folders' rows stay held.
	for id, h := range held {
		if verdict(h.folder).State != FolderPending {
			delete(held, id)
		}
	}

	// New guests' runs.
	var fresh []*watch
	var steered []GuestRun
	for _, g := range guests {
		id, ok := rowid(g[1])
		if !ok {
			return false, false, ErrOutput
		}
		created, _ := ms(g[2])
		n.Guests++
		if g[3].null || g[3].s != "1" {
			steered = append(steered, GuestRun{SentAt: created, Steered: true})
			continue
		}
		if !r.watching(id) {
			fresh = append(fresh, &watch{rowid: id, guest: true, created: created})
		}
	}

	// The text of the rows left, to measure, and their runs' sessions.
	texts := map[int64]measurement{}
	its := map[int64][]row{}
	if len(picked) > 0 || len(fresh) > 0 {
		var sql strings.Builder
		var ids []int64
		for _, p := range picked {
			sql.WriteString(textSQL(textReq{id: p.id, created: p.createdText, updated: p.updatedText, folder: p.folder}))
			ids = append(ids, p.rowid)
		}
		for _, w := range fresh {
			ids = append(ids, w.rowid)
		}
		sql.WriteString(itemSQL(ids))
		rows, err := r.query(ctx, sql.String())
		if err != nil {
			return false, false, err
		}
		for _, x := range rows {
			if len(x) < 2 {
				return false, false, ErrOutput
			}
			id, ok := rowid(x[1])
			if !ok {
				return false, false, ErrOutput
			}
			switch {
			case x[0].s == "txt" && len(x) == 4:
				// T3's schema gives every message a text; one without
				// counts as an empty one.
				total := 0
				if !x[3].null {
					if total, err = strconv.Atoi(x[3].s); err != nil {
						return false, false, ErrOutput
					}
				}
				// Measured here and dropped: nothing of the text outlives
				// this line.
				texts[id] = measure(x[2].s, total)
			case x[0].s == "it" && len(x) == 16:
				its[id] = append(its[id], x)
			default:
				return false, false, ErrOutput
			}
		}
	}

	// Every query answered: what the window found goes in.
	r.recheck(items)
	b.Guests = append(b.Guests, steered...)
	for _, w := range fresh {
		if r.first(w, its[w.rowid]) {
			r.watched = append(r.watched, w)
			b.Guests = append(b.Guests, GuestRun{SentAt: w.created})
		}
	}
	for _, p := range picked {
		m, ok := texts[p.rowid]
		if !ok {
			// Edited or rewritten since the decision read it.
			n.Edited++
			continue
		}
		w := &watch{rowid: p.rowid, msg: p.msg, run: p.run, created: p.created, updated: p.updated, record: p.eventID}
		r.bind(w, its[p.rowid])
		if w.changed {
			n.Edited++
			continue
		}
		msg := Message{
			EventID:    p.eventID,
			SeenAt:     seen,
			Harness:    event.HarnessNone,
			ProjectKey: p.project,
			Data: event.RelayMessage{
				App:       event.RelayAppT3Code,
				Client:    p.client,
				SentAt:    p.created,
				ThreadKey: p.thread,
				Declared:  p.declared,
				LenChars:  m.lenChars,
				LowEffort: m.lowEffort,
			},
			Class: m.class(r.table),
		}
		if p.driver == claudeDriver || len(w.sessions) > 0 {
			msg.Harness = event.HarnessClaudeCode
		}
		if len(w.sessions) > 0 {
			msg.SessionKey = w.sessions[0]
		}
		if p.declared == event.RelayDeclaredNone {
			n.Witnessed++
		} else {
			n.Declared++
		}
		r.watched = append(r.watched, w)
		b.Messages = append(b.Messages, msg)
	}
	r.status.Counts.add(n)
	r.held = held
	r.cursor = &next
	r.checked = r.reads
	return top > next.RowID, false, nil
}

// moved reports whether the cursor no longer stands where it was set: the
// newest rowid is below it, or the row at it holds another message, or one
// where there was none (DESIGN 5.1).
func (r *Reader) moved(top int64, at field) bool {
	c := r.cursor
	switch {
	case top < c.RowID:
		return true
	case c.Message == "":
		return !at.null
	default:
		return at.null || r.key("message", at.s) != c.Message
	}
}

// candidate is a row the decision left, before its text is measured.
type candidate struct {
	rowid                    int64
	id                       string
	msg, run, thread         string // keys
	created, updated         int64
	createdText, updatedText string
	client                   event.RelayClient
	declared                 event.RelayDeclared
	driver                   string
	folder, project          string
	eventID                  string
}

// decide applies the rules the WHERE clause leaves to Go (SM 2.2 rule 9):
// the marks, a run of its own, the age test and the updated_at test. It
// returns nil for a row that gets no record.
func (r *Reader) decide(x row, seen time.Time, verdict func(string) Folder, n *Counts) *candidate {
	id, thread, runID := x[2], x[3], x[4]
	if id.null || thread.null {
		return nil
	}
	msg := r.key("message", id.s)
	for _, w := range r.watched {
		if w.msg == msg {
			// Read again after a resync.
			return nil
		}
	}
	mk, err := strconv.Atoi(x[8].s)
	if err != nil {
		return nil
	}
	if mk&markNobody != 0 {
		n.Marked++
		return nil
	}
	if runID.null || x[9].s != "1" || x[10].s != "1" || x[11].s != "0" {
		// No run of its own: a steer, a message whose queued run T3 turns
		// into a steer, or a run stamped at another time.
		n.NoRun++
		return nil
	}
	created, ok1 := ms(x[5])
	updated, ok2 := ms(x[6])
	if !ok1 || !ok2 {
		return nil
	}
	if seen.UnixMilli()-created > r.lag {
		n.Aged++
		return nil
	}
	if updated > created {
		n.Edited++
		return nil
	}
	driver := ""
	if !x[12].null {
		driver = x[12].s
		if driver != claudeDriver {
			n.OtherHarness++
			return nil
		}
	}
	v := verdict(x[13].s)
	if v.State != FolderTracked || v.Project == "" {
		return nil
	}
	c := &candidate{
		id:          id.s,
		msg:         msg,
		run:         r.key("run", runID.s),
		thread:      r.key("thread", thread.s),
		created:     created,
		updated:     updated,
		createdText: x[5].s,
		updatedText: x[6].s,
		client:      event.RelayClient(x[7].s),
		declared:    event.RelayDeclaredNone,
		driver:      driver,
		folder:      x[13].s,
		project:     v.Project,
	}
	switch {
	case mk&markScheduled != 0:
		c.declared = event.RelayDeclaredCron
	case mk&(markSender|markLinkedSender) != 0:
		c.declared = event.RelayDeclaredCrossSession
	}
	if c.client != event.RelayClientWeb && c.client != event.RelayClientMobile {
		return nil
	}
	c.eventID = recordID(r.keys, id.s, c.createdText, created)
	return c
}

// watching reports whether the reader watches the row at rowid.
func (r *Reader) watching(rowid int64) bool {
	return slices.ContainsFunc(r.watched, func(w *watch) bool { return w.rowid == rowid })
}

// first fills in a new guest watch from its first read, and reports
// whether it is one to keep: a row not watched already, whose run still
// names it.
func (r *Reader) first(w *watch, its []row) bool {
	if len(its) == 0 || its[0][2].null || its[0][3].null {
		return false
	}
	w.msg, w.run = r.key("message", its[0][2].s), r.key("run", its[0][3].s)
	w.updated, _ = ms(its[0][5])
	for _, o := range r.watched {
		if o.msg == w.msg {
			return false
		}
	}
	r.bind(w, its)
	return !w.changed
}

// recheck reads each watched row again (DESIGN 5.1): a row whose message
// id, run, stamp or updated_at changed since the reader first read it
// witnesses nothing, which catches an edit, a promotion to steer and a
// steer's follow-up T3 dispatched again. A watched run's sessions, start
// and end are read again too.
func (r *Reader) recheck(items map[int64][]row) {
	for _, w := range r.watched {
		if w.changed || w.claimed != "" {
			continue
		}
		switch its := items[w.rowid]; {
		case len(its) == 0:
			w.changed = true
		case w.msg == "":
			// A guest's run whose first read didn't finish.
			w.changed = !r.first(w, its)
		default:
			r.bind(w, its)
		}
		if w.changed && !w.guest {
			r.status.Edited++
		}
	}
}

// bind compares a watched row with what the reader first read of it, and
// reads its run's sessions, start and end, from itemSQL's rows for it.
func (r *Reader) bind(w *watch, its []row) {
	if len(its) == 0 {
		w.changed = true
		return
	}
	x := its[0]
	created, _ := ms(x[4])
	updated, _ := ms(x[5])
	if x[2].null || x[3].null || r.key("message", x[2].s) != w.msg || r.key("run", x[3].s) != w.run ||
		created != w.created || updated != w.updated || x[7].s != "1" {
		w.changed = true
		return
	}
	start, started := ms(x[8])
	if !started {
		// Not started: queued, or cancelled before it started.
		return
	}
	w.start = start
	w.done, _ = ms(x[9])
	var sessions []string
	for _, a := range its {
		if !a[14].null && (a[15].null || a[15].s == claudeDriver) {
			if k := r.keys.Session(a[14].s); !slices.Contains(sessions, k) {
				sessions = append(sessions, k)
			}
		}
	}
	if len(sessions) == 0 && !x[11].null && (x[10].null || x[10].s == claudeDriver) {
		sessions = append(sessions, r.keys.Session(x[11].s))
	}
	w.sessions = sessions
}

// pair gives each watched run that started the first prompt it may have
// delivered: one the agent asked about, of a session the run went to,
// stamped at or after the run's start and before its end, and, for a
// message, within RELAY_MATCH_S of its stamp (SM 2.2 rule 9). Runs go in
// order of start, so a run never takes a later run's prompt. A message whose
// record is in this batch names its prompt in the record. checked says this
// read read the watched rows again: without that, only a guest's run, which
// can only cost the prompt credit, and the messages in this batch pair.
func (r *Reader) pair(b *Batch, checked bool) {
	runs := slices.Clone(r.watched)
	slices.SortStableFunc(runs, func(a, c *watch) int { return cmp.Compare(a.start, c.start) })
	for _, w := range runs {
		if w.claimed != "" || w.changed || w.start == 0 || len(w.sessions) == 0 || !checked && !w.guest && w.out {
			continue
		}
		var best *asked
		for _, p := range r.prompts {
			switch {
			case p.claimed, p.id == "", !slices.Contains(w.sessions, p.session),
				p.t < w.start-startSlack, w.done > 0 && p.t > w.done+startSlack,
				!w.guest && (p.t < w.created || p.t > w.created+r.match):
				continue
			}
			if best == nil || p.t < best.t {
				best = p
			}
		}
		if best == nil {
			continue
		}
		w.claimed, best.claimed = best.id, true
		if w.guest {
			continue
		}
		r.status.Paired++
		for i := range b.Messages {
			if b.Messages[i].EventID == w.record {
				b.Messages[i].Data.Prompt = best.id
			}
		}
	}
}

// prune forgets the runs and prompts that can no longer pair, at now in
// Unix ms.
func (r *Reader) prune(now int64) {
	r.watched = slices.DeleteFunc(r.watched, func(w *watch) bool {
		if w.guest {
			return w.claimed != "" || w.changed || now-w.created > guestWatch
		}
		return now-w.created > r.match+watchMargin
	})
	if n := len(r.watched) - maxWatched; n > 0 {
		r.watched = r.watched[n:]
	}
	r.prompts = slices.DeleteFunc(r.prompts, func(p *asked) bool { return now-p.t > r.match+watchMargin })
	if n := len(r.prompts) - maxPrompts; n > 0 {
		r.prompts = r.prompts[n:]
	}
}

// key is the key of one of T3's ids, of a kind.
func (r *Reader) key(kind, id string) string { return r.keys.App(app, kind, id) }

// recordID is a record's event_id: a UUIDv7 at sentAt whose other bits are
// the key of the message id and its stamp.
func recordID(keys *adapters.Keys, id, created string, sentAt int64) string {
	bits, _ := hex.DecodeString(keys.App(app, "record", id, created)[1:])
	var b [16]byte
	for i := 0; i < 6; i++ {
		b[i] = byte(uint64(sentAt) >> (40 - 8*i))
	}
	copy(b[6:], bits)
	b[6] = b[6]&0x0f | 0x70 // version 7
	b[8] = b[8]&0x3f | 0x80 // variant 10
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// rowid reads a rowid field.
func rowid(f field) (int64, bool) {
	if f.null {
		return 0, false
	}
	n, err := strconv.ParseInt(f.s, 10, 64)
	return n, err == nil
}

// ms reads one of T3's stamps, ISO 8601 UTC with milliseconds, as Unix ms.
func ms(f field) (int64, bool) {
	if f.null {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339Nano, f.s)
	if err != nil {
		return 0, false
	}
	return t.UnixMilli(), true
}

// stamp writes Unix ms as T3 writes its stamps.
func stamp(t int64) string {
	return time.UnixMilli(t).UTC().Format("2006-01-02T15:04:05.000Z")
}

// containsAny reports whether s holds any of subs.
func containsAny(s string, subs ...string) bool {
	return slices.ContainsFunc(subs, func(sub string) bool { return strings.Contains(s, sub) })
}
