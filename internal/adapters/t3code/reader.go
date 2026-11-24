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
	// prompt after, on the same clock. A run's window closes at its end,
	// with no slack after it (SM 2.2): T3 starts a queued run as soon as the
	// one before it ends, so a slack there would hand the next run's prompt
	// to a run that delivered none. For the same reason the slack before a
	// start never reaches back past the end of an earlier watched run of
	// the session (ranBefore): in WP-ADE1's capture, a steer during a run's
	// Stop hook came 111 ms before its end and 1,586 ms before the next
	// run's start.
	startSlack = 2000
	// stampAhead is how much later than the read that decides it a row may
	// be stamped (SM 2.2). T3's server and the agent run under one clock, so
	// a later stamp comes from a row written by hand or a clock stepped
	// back, and the reader refuses the row (Counts.Ahead).
	stampAhead = 2000
	// resyncMargin is how much older than RELAY_SEEN_LAG_S a resync looks.
	resyncMargin = 5000
	// watchMargin is how long past RELAY_MATCH_S the reader remembers a
	// message's run, and a prompt it was asked about.
	watchMargin = 60_000
	// guestWatch is how long the reader watches a guest's message, for the
	// runs T3 gives it. A held queue has no bound, so this is long.
	guestWatch = 24 * 60 * 60 * 1000
	// runWatch is the cap on how long after its stamp the reader watches a
	// message whose run no read has seen end, so that the end bounds the
	// session's next run (ranBefore, prune): a day, as for a guest's
	// message. A run that outlasts it, as one T3 never completes when it is
	// killed mid-run, bounds nothing after. A message whose row T3 deletes
	// is watched until the cap too, since no read finds its run again.
	runWatch = guestWatch
	// maxWatched and maxPrompts bound what the reader remembers, and
	// maxSteers the running turns it remembers one guest's message was
	// steered into (Steer). Past them it forgets the oldest.
	maxWatched = 1024
	maxPrompts = 1024
	maxSteers  = 8
	// maxHeld bounds the rows held for their folders that state.json keeps
	// (State). A held row is a message a person sent from a client in the
	// last RELAY_SEEN_LAG_S to a folder pairtime's resolver hasn't answered
	// for, and nobody sends 64 of those in a minute. Past it the oldest go,
	// the ones nearest the age test.
	maxHeld = 64
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

// Pair reads up to pairTries times while its reads fail for a reason that
// passes (passing), pairRetry apart, within the time the agent gives it.
const (
	pairTries = 3
	pairRetry = 250 * time.Millisecond
)

// FolderState is what pairtime's project resolver says of a T3 thread's
// folder.
type FolderState uint8

const (
	// FolderPending says the resolver hasn't answered yet. The reader
	// decides none of the folder's rows meanwhile and reads no text of them,
	// and asks again on its next reads while a row is young enough to record
	// (RELAY_SEEN_LAG_S). A guest's message there is watched all the same
	// (FolderOut).
	FolderPending FolderState = iota
	// FolderTracked is a folder pairtime tracks, whose project is
	// Folder.Project.
	FolderTracked
	// FolderOut is a folder pairtime doesn't track or ignores (DESIGN 8.8).
	// The reader reads no text of its rows and records none of them. A
	// guest's message there is still watched by its key, with no text, as
	// every guest's is, since the guest query has no folder test (DESIGN
	// 5.1); the folder's prompts are dropped anyway.
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
	// stores it: a thread's worktree, else its project's root. The agent's
	// resolver starts a git Track for a folder it hasn't seen, so the reader
	// asks it of no row read more than RELAY_SEEN_LAG_S after its stamp,
	// read again by a resync, holding a message the reader watches, or whose
	// run, its own or the running turn it was steered into, T3 runs on
	// another harness than Claude Code (Reader.window). It still asks of a
	// row that the decision then turns away for T3's marks, for having no
	// run of its own, for an edit or for a stamp ahead of the read.
	// Required.
	Folder func(dir string) Folder
	// Server reports whether the agent's last process scan found T3 Code's
	// server running (ServerIn), which with the -wal and -shm files of the
	// store a read picks is what makes T3 run (Status.Running). A scan that
	// failed found none. Nil means no scan found one. The reader calls it
	// at each read, holding its own lock, so it must not wait.
	Server func() bool
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
	server func() bool
	// stat says what the -wal and -shm files of the store at a path are:
	// walOf, which tests replace.
	stat  func(path string) (walFiles, bool)
	table *params.Table
	lag   int64 // RELAY_SEEN_LAG_S, in ms
	match int64 // RELAY_MATCH_S, in ms
	now   func() time.Time
	zone  *time.Location

	mu      sync.Mutex
	store   string    // the store the last read read
	probed  time.Time // when the probe last passed, zero until it has
	schema  schema
	cursor  *Cursor
	watched []*watch
	prompts []*asked
	held    map[int64]heldRow
	status  Status
	// quiet is what a stat of the store's -wal and -shm files said once the
	// last read that read all there was to read had run (settled), and nil
	// after any other read. While a stat says the same, T3 has written
	// nothing since, and a read that holds no row and asks about no prompt
	// starts no sqlite3 (DESIGN 3.3's cost).
	quiet *walFiles
	// reads counts the reads, and known is the last one whose word on the
	// store still stands: the last that read it all, or one after it that
	// found T3 had written nothing. A watched row's found is the read that
	// last found it.
	reads, known int
	// top is the newest rowid the last window saw, and reread the rows a
	// resync reads again.
	top    int64
	reread rereadZone
}

// watch is a row the reader watches, by rowid, and by its message's key
// once a resync finds it again at another rowid (DESIGN 5.1). It holds T3's
// ids by key alone.
//
// A message the reader recorded is watched until RELAY_MATCH_S and a margin
// have passed since its stamp and its run's end can bound no prompt the
// reader remembers, a day after the stamp at most while no read has seen the
// run end (prune), and its row is read again until its run has delivered a
// prompt and a read has seen the run end (readAgain). A row that changes
// after the first read witnesses nothing. A guest's
// message is watched for guestWatch whatever T3 does to the row: promote it
// to a steer, send it again after Claude Code declined the steer, renumber
// it in a VACUUM. Each run of its own T3 gives it, the first and every one
// it sends it again in, delivers a guest's prompt, and each running turn
// the owner steers it into is a Steer.
type watch struct {
	rowid int64
	// msg and run are the keys of the message id and of a run: for a
	// message, the run it was recorded with; for a guest's, the run of its
	// own the row last named, "" while it named none.
	msg, run string
	// created and updated are the row's stamps as first read, Unix ms.
	created, updated int64
	guest            bool
	// record is the relay.message's event id, for a message.
	record string
	// sessions are the session keys of the Claude Code sessions the run
	// went to, start and done its start and end, Unix ms, 0 until known.
	sessions    []string
	start, done int64
	// claimed is the event id of the prompt the run delivered. A read still
	// reads a claimed run's row until it sees the run end (readAgain).
	claimed string
	// steers are the running turns of other messages that the owner
	// steered a guest's message into (Steer).
	steers []Steer
	// changed says a message's row changed after the reader first read it,
	// or its run no longer names it: it pairs with nothing.
	changed bool
	// found is the read that last found the row holding its message: a row
	// a VACUUM renumbered isn't found until a resync finds it again.
	found int
}

// asked is a prompt the agent asked about (Pair).
type asked struct {
	session string
	t       int64
	id      string
	claimed bool
	// guest is Prompt.Guest: the agent marks the prompt guest.
	guest bool
}

// heldRow is a new row whose folder the resolver hadn't answered for: its
// stamp as first read, Unix ms, its folder, and msg, the key of its message
// id. An empty folder means the row came from state.json (load) and no read
// has read its folder since.
type heldRow struct {
	created int64
	folder  string
	msg     string
}

// rereadZone is what a resync reads again only to find the rows the reader
// watches: the rows up to rowid to, the newest when the resync began,
// stamped at or before stamp, which earlier reads decided or which are too
// old to record. It counts nothing of them. The zero rereadZone holds none.
type rereadZone struct{ stamp, to int64 }

// has reports whether the row at rowid, stamped created, is one the resync
// reads again.
func (z rereadZone) has(rowid, created int64) bool {
	return z.to > 0 && rowid <= z.to && created <= z.stamp
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
		server: o.Server,
		stat:   walOf,
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
	// Guests are the guests' messages the read found for the first time,
	// without their text. The reader watches each one, so every prompt a run
	// of its own delivers keeps no text, and says which one the owner
	// steered into a running turn instead (GuestRun.Steered, Reader.Steers).
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
	// RELAY_SEEN_LAG_S after its stamp and at most 2 s before it.
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
	// Steered says that when the reader first read it the owner had steered
	// it into a running turn, another message's run. Claude Code takes a
	// steer as a prompt of its own in that run's session, which no run of
	// the guest's delivers, so the reader reports the turn instead
	// (Reader.Steers); a run of its own that T3 gives it later, as when
	// Claude Code declines the steer, delivers a guest's prompt as any does.
	Steered bool
}

// Steer is a running turn that the owner steered a guest's message into:
// a run another message started. Claude Code takes the guest's words as a
// prompt of its own in the run's session, with a turn of its own
// (WP-ADE1's capture; docs/errata/2026-11-23-t3-reader.md, decision 4), and
// no run of the guest's delivers it. So the agent marks guest every prompt
// of the run's sessions stamped from From to To that no run claimed
// (DESIGN 5.1).
type Steer struct {
	// Run is the key of the run's id.
	Run string `json:"run"`
	// Sessions are the sessions the run went to, keyed as the hook keys
	// session_id, on any provider, since a prompt that carries a guest's
	// words keeps none of them whoever runs it.
	Sessions []string `json:"sessions,omitempty"`
	// From is the later of the guest's row's stamp and the run's start,
	// Unix ms. To is the run's end, 0 while it runs, or, when no read saw
	// the run end, the read that found the row had left it.
	From int64 `json:"from"`
	To   int64 `json:"to,omitempty"`
}

// Steers returns the running turns the reader knows the owner steered a
// guest's message into (Steer), as its last read left them, for the agent
// to mark the prompts that carried the guest's words. The reader keeps them
// while it watches the guest's message, a day, in state.json too.
func (r *Reader) Steers() []Steer {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Steer
	for _, w := range r.watched {
		out = append(out, cloneSteers(w.steers)...)
	}
	return out
}

// cloneSteers copies steers, with their sessions.
func cloneSteers(steers []Steer) []Steer {
	var out []Steer
	for _, s := range steers {
		s.Sessions = slices.Clone(s.Sessions)
		out = append(out, s)
	}
	return out
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
	// Guest says the agent marks the prompt guest whatever the read finds,
	// since its text opens as T3 Code opens a guest's message
	// (OpensAsGuest), and a prompt marked guest takes no relay. So Pair
	// gives it no Relay, and its pairing counts only when a record the
	// reader writes names it (Counts.Paired). A message's run still claims
	// it, so the run takes no later prompt.
	Guest bool
}

// Pairing is the reader's answer about a prompt (Pair).
type Pairing struct {
	// Known says the reader read T3's store for this answer. When it is
	// false, the reader can't tell this prompt from any other, so the agent
	// keeps the prompt's text as it keeps any prompt's (DESIGN 5.1), unless
	// Guest is set: a guest's run an earlier read saw can still claim the
	// prompt, which can only cost it credit. Relay can be set then too: a
	// read can fail after the queries that found a message's row as the
	// reader first read it, and that is word enough for the message's run
	// to claim the prompt (pair).
	Known bool
	// Guest says a guest's message started the run that delivered the
	// prompt: the prompt is marked guest and keeps no text.
	Guest bool
	// Relay is the event id of the relay.message, from an earlier batch, the
	// prompt pairs with: the prompt's relay. When the record is in this
	// answer's own batch instead, the record names the prompt. A prompt the
	// agent marks guest (Prompt.Guest) gets none, since it takes no relay.
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

// SchemaError says the schema probe turned T3's store down (DESIGN 5.1), or
// that this sqlite3 can't read it for want of JSON functions, for which it
// wraps ErrNoJSON.
type SchemaError struct {
	Why string
	err error
}

func (e *SchemaError) Error() string { return "T3 Code's schema isn't one pairtime reads: " + e.Why }

// Unwrap returns ErrNoJSON for a sqlite3 with no JSON functions, and nil
// for a schema the probe refused.
func (e *SchemaError) Unwrap() error { return e.err }

// Why the reader doesn't read (Status.Off).
const (
	OffNoStore    = "no_store"
	OffNotRunning = "not_running"
	OffNoSQLite3  = "no_sqlite3"
	// OffNoJSON is a sqlite3 with no JSON functions (ErrNoJSON), which
	// doctor's line tells how to replace.
	OffNoJSON = "no_json"
	OffSchema = "schema"
	OffFailed = "failed"
)

// Counts are what the reader did on one day, for doctor (DESIGN 5.9). A row
// the reader turns away before its folder test (Reader.window) counts
// whatever its folder: one held for its folder past RELAY_SEEN_LAG_S as
// Aged, and one first read more than RELAY_SEEN_LAG_S after its stamp, or
// of a run T3 runs on another harness, under the first of the decision's
// tests that turns it away (Reader.turnedAway).
type Counts struct {
	// Witnessed counts the records of declared none it wrote: messages T3
	// says a person sent.
	Witnessed int
	// WithServer counts those of them it wrote at a read that found T3
	// running (Status.Running), with a server the app table names. A T3 it
	// doesn't name, as one run from its source under node, gives none: its
	// sessions don't classify as agent_app, so the agent never asks about
	// their prompts, and its messages pair with nothing (doctor's T3 Code
	// line).
	WithServer int
	// Declared counts the records of declared cron or cross_session.
	Declared int
	// Paired counts the records whose pairing with a prompt went on a
	// record: on the relay.message, when the reader found the prompt first,
	// or on the prompt, as Pair's Relay. A pairing found once both records
	// were written goes on neither, and isn't counted, and nor is one with
	// a prompt the agent marks guest that Pair asks about after its record
	// went out, since such a prompt takes no relay (Prompt.Guest).
	Paired int
	// Aged counts the rows the age test turned away: decided more than
	// RELAY_SEEN_LAG_S after their stamp, as a row first read that late is,
	// or one held that long for its folder (SM 2.2).
	Aged int
	// Ahead counts the rows stamped more than 2 s after the read that
	// decided them, which a clock T3 and pairtime share doesn't give.
	Ahead int
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
	// OffNotRunning, OffNoSQLite3, OffNoJSON, OffSchema or OffFailed.
	Off string
	// Problem says more: the schema the probe refused, or the read that
	// failed.
	Problem string
	// Newer says T3's schema is newer than the one this reader was tested
	// against, which it reads anyway.
	Newer bool
	// NoSQLite3 says the last read found no sqlite3 on PATH, or found the
	// one an earlier read had found gone when it ran it. The reader looks
	// for one at every read, whether or not T3 runs, so doctor can say how to
	// install it while T3 is stopped too, whatever Off says.
	NoSQLite3 bool
	// Running says T3 Code ran at the last read (DESIGN 3.3): the agent's
	// last process scan found its server (Options.Server), and the store the
	// read picked had the -wal and -shm files T3 holds open while it runs.
	// The files alone, which a T3 killed while it held them leaves behind,
	// don't make T3 run, though a read still reads what they hold.
	Running bool
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
	c.WithServer += d.WithServer
	c.Declared += d.Declared
	c.Paired += d.Paired
	c.Aged += d.Aged
	c.Ahead += d.Ahead
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
// store has the -wal file T3 holds open, so a closed T3 relays nothing, and
// starts no sqlite3 while T3 has written nothing since the last read. It
// returns the records of the windows it finished even with an error, so the
// agent writes a batch whatever the error says.
func (r *Reader) Poll(ctx context.Context) (Batch, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b Batch
	err := r.poll(ctx, "", &b)
	return b, err
}

// Pair is Poll for a prompt.submit of a session T3's server started, which
// the agent asks about before it keeps the prompt's text (DESIGN 5.1). It
// says whether a guest's run delivered the prompt and which record it pairs
// with: the run T3 started for a message goes to a Claude Code session, and
// the first prompt of that session from just before the run's start to its
// end is the one it delivered. A record in the batch that names the prompt
// is written after the prompt. A read that fails for a reason that passes,
// a store busy for a moment or a run that outlasted its timeout, is tried
// again while ctx lasts, so the prompt can still carry its pairing. The
// reader remembers the prompt for RELAY_MATCH_S even when it can't answer,
// so a later read can still pair a message whose row it hadn't read yet,
// and so no later prompt of the run takes the one it delivered.
func (r *Reader) Pair(ctx context.Context, p Prompt) (Pairing, Batch, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.EventID == "" {
		return Pairing{}, Batch{}, errors.New("t3code: a prompt with no event_id")
	}
	if i := slices.IndexFunc(r.prompts, func(a *asked) bool { return a.id == p.EventID }); i >= 0 {
		// Asked again, as after a restart that state.json kept it across.
		r.prompts[i].guest = r.prompts[i].guest || p.Guest
	} else {
		r.prompts = append(r.prompts, &asked{session: p.SessionKey, t: p.T, id: p.EventID, guest: p.Guest})
	}
	var b Batch
	err := r.poll(ctx, p.EventID, &b)
	for try := 1; try < pairTries && passing(err) && pause(ctx, pairRetry); try++ {
		err = r.poll(ctx, p.EventID, &b)
	}
	out := Pairing{Known: err == nil}
	for _, w := range r.watched {
		if w.claimed != p.EventID {
			continue
		}
		switch {
		case w.guest:
			out.Guest = true
		case p.Guest:
			// A prompt the agent marks guest takes no relay.
		case !slices.ContainsFunc(b.Messages, func(m Message) bool { return m.EventID == w.record }):
			out.Relay = w.record
		}
	}
	return out, b, err
}

// passing reports whether a read that failed with err may work if tried
// again at once: T3 held its store busy for longer than a run waits, as t3
// doctor's compaction does, or a run outlasted its timeout. A context that
// ended stops the tries by itself (pause).
func passing(err error) bool {
	return errors.Is(err, ErrBusy) || errors.Is(err, context.DeadlineExceeded)
}

// pause waits d, or until ctx ends, and reports whether ctx lasted.
func pause(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return ctx.Err() == nil
	}
}

// poll is one read, which adds what it found to b. asking is the event id
// of the prompt Pair asks about, "" for Poll.
func (r *Reader) poll(ctx context.Context, asking string, b *Batch) error {
	now := r.now()
	r.reads++
	r.rollDay(now)
	r.prune(now.UnixMilli())
	err := r.read(ctx, now, b, asking)
	if err != nil {
		r.fail(err)
	} else {
		r.status.Reading, r.status.Off, r.status.Problem, r.status.Last = true, "", "", now
	}
	r.pair(b, asking)
	return err
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
		// A sqlite3 that read() found can be gone by the run (Runner.Query).
		r.status.Off, r.status.NoSQLite3 = OffNoSQLite3, true
	case errors.Is(err, ErrNoJSON):
		r.status.Off, r.status.Problem = OffNoJSON, ErrNoJSON.Error()
	case errors.As(err, &se):
		r.status.Off, r.status.Problem = OffSchema, se.Why
	case errors.As(err, &qe):
		r.status.Off, r.status.Problem = OffFailed, qe.Error()
	default:
		r.status.Off, r.status.Problem = OffFailed, err.Error()
	}
}

// read finds the store, probes it when due, and reads its windows. It looks
// for sqlite3 whether or not T3 runs (Status.NoSQLite3), and notes whether
// T3 runs (Status.Running).
func (r *Reader) read(ctx context.Context, now time.Time, b *Batch, asking string) error {
	_, exeErr := r.sqlite.Exe()
	r.status.NoSQLite3 = exeErr != nil
	store, err := r.pick()
	r.status.Running = err == nil && r.server != nil && r.server()
	if err == nil {
		err = exeErr
	}
	if err != nil {
		// What a stat said before T3 stopped says nothing of the files it
		// makes when it starts again.
		r.quiet = nil
		return err
	}
	if store != r.store {
		r.store, r.probed, r.quiet = store, time.Time{}, nil
	}
	files, stated := r.stat(store)
	if stated && r.quiet != nil && *r.quiet == files && r.cursor != nil && len(r.held) == 0 && asking == "" {
		// T3 has written nothing since a read that read all there was, so
		// this one would find what that one found: it starts no sqlite3, and
		// what that one found of the watched rows still stands.
		for _, w := range r.watched {
			if w.found == r.known {
				w.found = r.reads
			}
		}
		r.known = r.reads
		return nil
	}
	r.quiet = nil
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
			if err := r.resync(ctx); err != nil {
				return err
			}
			continue
		}
		if !more {
			r.known = r.reads
			if stated {
				r.quiet = r.settled(store, files)
			}
			return nil
		}
	}
	return nil
}

// settled is what a read that read all there was leaves in quiet: a stat
// of the store's -wal and -shm files taken now, once its sqlite3 runs are
// done, when the -wal is as before said it was when the read began, and
// before itself otherwise. T3 commits nothing without writing the -wal.
// The read's first run after T3 writes sets a reader's mark in the -shm,
// as any SQLite reader of a store in WAL mode does, and that write stamps
// the -shm's time though nothing new is in the store (TestOwnReads). So
// the read after this one starts no sqlite3 for the mark alone, and does
// for a commit T3 made while this one ran.
func (r *Reader) settled(store string, before walFiles) *walFiles {
	after, ok := r.stat(store)
	if !ok || after.walSize != before.walSize || after.walTime != before.walTime {
		return &before
	}
	return &after
}

// resync puts the cursor back among the rows once the row at it no longer
// holds the message it held, or the newest rowid is below it: T3's VACUUM
// renumbered the rows, or another store took the place of the one read
// (DESIGN 5.1). It goes before the oldest row stamped after the later of
// the cursor's stamp and RELAY_SEEN_LAG_S (and a margin) ago, which a read
// may still record, or before the oldest row the reader still reads again
// (watch.readAgain), as one that waits for its run, when that is older:
// such a row is read again there, and its watch moves to its new rowid.
// The older rows the resync reads again only to find those (rereadZone).
func (r *Reader) resync(ctx context.Context) error {
	floor := max(r.cursor.CreatedAt, r.now().UnixMilli()-r.lag-resyncMargin)
	from := floor
	for _, w := range r.watched {
		if w.readAgain() {
			from = min(from, w.created-1)
		}
	}
	top := r.top
	c, err := r.at(ctx, resyncSQL(stamp(from)))
	if err != nil {
		return err
	}
	r.cursor = &c
	r.reread = rereadZone{stamp: floor, to: top}
	r.held = map[int64]heldRow{}
	r.probed = time.Time{}
	return r.probe(ctx, r.now())
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
		return &SchemaError{Why: ErrNoJSON.Error(), err: ErrNoJSON}
	case errors.As(err, &qe) && containsAny(qe.Detail, "no such table", "not a database"):
		return &SchemaError{Why: "it isn't a store of T3 Code's version 2 schema"}
	case err != nil:
		return err
	}
	s, err := checkProbe(rows)
	var re *refusal
	switch {
	case errors.Is(err, ErrNoJSON):
		return &SchemaError{Why: ErrNoJSON.Error(), err: ErrNoJSON}
	case errors.As(err, &re):
		return &SchemaError{Why: re.why}
	case err != nil:
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
		if w.readAgain() {
			rowids = append(rowids, w.rowid)
		}
	}
	// The rows a restart left held, with no folder yet, which the fold reads
	// again by rowid. Like every held row, they are at or below the cursor:
	// the read that held them moved it past them.
	var restored []int64
	for id, h := range r.held {
		if h.folder == "" && id <= c {
			restored = append(restored, id)
		}
	}
	slices.Sort(restored)
	rows, err := r.query(ctx, windowSQL(c, rowids, restored))
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
		case t == "fold" && len(x) == 10:
			folds = append(folds, x)
		case t == "guest" && len(x) == 5:
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
	r.top = top
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
	seen := r.now()
	// moves are the watched rows this window found at another rowid, by
	// their messages' keys: rows a resync reads again after T3's VACUUM
	// renumbered them.
	moves := map[*watch]int64{}

	// The folder test, by pairtime's own resolver: the rows of this window,
	// and those held for a folder it hadn't answered for.
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
	// The resolver hears only of the folders of rows the decision may still
	// record, since for a folder it hasn't seen it starts a git Track, which
	// watches the repo and records its commits (DESIGN 5.8). So these rows
	// are turned away before the folder test: a row held for its folder
	// once the age test turns it away; of the rows a restart left held, one
	// whose rowid holds another message now, or that the fold didn't read
	// again as a person's message from a client, which counts nothing, and
	// one whose run T3 runs on another harness; and of this window's rows,
	// one that holds a message the reader watches, which moves its watch as
	// in the decision, one a resync reads again, which counts nothing, and
	// one stamped more than RELAY_SEEN_LAG_S before this read or whose run,
	// its own or the running turn it was steered into, T3 runs on another
	// harness. A row of another harness, or stamped too long ago, counts as
	// the decision would count it, whatever its folder (Counts).
	for id, h := range held {
		if seen.UnixMilli()-h.created > r.lag {
			n.Aged++
			delete(held, id)
		}
	}
	away := func(f row, created int64, other bool) {
		updated, ok := ms(f[7])
		mk, err := strconv.Atoi(f[8].s)
		if ok && err == nil {
			r.turnedAway(rowTests{marks: mk, own: f[9].s == "1", created: created, updated: updated, other: other},
				seen.UnixMilli(), &n)
		}
	}
	// gone are this window's rows the tests before the folder test turned
	// away, which the decision skips though their folders are ones it reads.
	gone := map[int64]bool{}
	for _, f := range folds {
		id, ok := rowid(f[1])
		if !ok {
			return false, false, ErrOutput
		}
		msg := ""
		if !f[6].null {
			msg = r.key("message", f[6].s)
		}
		dir := f[4].s
		if !f[3].null {
			dir = f[3].s
		}
		other := !f[5].null && f[5].s != claudeDriver
		if id <= c {
			// A row a restart left held, read again by its rowid. Once it
			// has its folder, the decision makes every other test, as for
			// any held row.
			h, ok := held[id]
			switch {
			case !ok:
				// The age test above turned it away, and counted it.
			case msg != h.msg:
				// Its rowid holds another message now.
				delete(held, id)
			case other:
				delete(held, id)
				if created, ok := ms(f[2]); ok {
					away(f, created, other)
				}
			default:
				h.folder = dir
				held[id] = h
			}
			continue
		}
		if msg != "" {
			if w := r.watchOf(msg); w != nil {
				if w.rowid != id {
					moves[w] = id
				}
				gone[id] = true
				continue
			}
		}
		created, ok := ms(f[2])
		if !ok {
			continue
		}
		switch {
		case r.reread.has(id, created):
		case seen.UnixMilli()-created > r.lag || other:
			away(f, created, other)
		default:
			held[id] = heldRow{created: created, folder: dir, msg: msg}
			continue
		}
		gone[id] = true
	}
	var allowed []string
	var decide []int64
	for id, h := range held {
		// A row with no folder is one a restart left held that the fold
		// didn't read again, or one whose thread T3 gives an empty folder,
		// which names none pairtime tracks.
		if h.folder == "" {
			delete(held, id)
			continue
		}
		v := verdict(h.folder)
		if v.State == FolderOut || v.State == FolderTracked && v.Project == "" {
			delete(held, id)
			continue
		}
		// A row of a tracked folder goes to the decision, a held one by its
		// rowid. One whose folder is pending stays held.
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
			if gone[id] {
				continue
			}
			if cand := r.decide(x, id, seen, verdict, moves, &n); cand != nil {
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

	// The guests' messages, by mark or by their opening: new ones, and
	// watched ones a resync reads again.
	var fresh []*watch
	var reports []GuestRun
	for _, g := range guests {
		id, ok := rowid(g[1])
		if !ok || g[4].null {
			return false, false, ErrOutput
		}
		created, _ := ms(g[2])
		msg := r.key("message", g[4].s)
		if w := r.watchOf(msg); w != nil {
			if w.rowid != id {
				moves[w] = id
			}
			continue
		}
		fresh = append(fresh, &watch{rowid: id, msg: msg, guest: true, created: created, updated: created})
		if !r.reread.has(id, created) {
			n.Guests++
			reports = append(reports, GuestRun{SentAt: created, Steered: !g[3].null && g[3].s == "0"})
		}
	}

	// The text of the rows left that declare no sender, to measure, and the
	// runs and sessions of every row this window starts or moves a watch of.
	texts := map[int64]measurement{}
	its := map[int64][]row{}
	var ids []int64
	var sql strings.Builder
	for _, p := range picked {
		// A cron or cross_session record's prompt is taken by rules 1 and 3
		// before its length or class matter, and webhooks put other people's
		// words in those rows, so their text isn't read (SM 2.2).
		if p.declared == event.RelayDeclaredNone {
			sql.WriteString(textSQL(textReq{id: p.id, created: p.createdText, updated: p.updatedText, folder: p.folder}))
		}
		ids = append(ids, p.rowid)
	}
	for _, w := range fresh {
		ids = append(ids, w.rowid)
	}
	for _, id := range moves {
		ids = append(ids, id)
	}
	if len(ids) > 0 {
		slices.Sort(ids)
		sql.WriteString(itemSQL(slices.Compact(ids)))
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
	seenMS := seen.UnixMilli()
	for w, id := range moves {
		w.rowid = id
	}
	maps.Copy(items, its)
	for _, w := range r.watched {
		if !w.readAgain() {
			continue
		}
		// A message that already paired lost nothing to a change.
		claimed := !w.guest && w.claimed != ""
		if r.track(w, items[w.rowid], seenMS) && w.changed && !claimed {
			n.Edited++
		}
	}
	for _, w := range fresh {
		r.track(w, its[w.rowid], seenMS)
		r.watched = append(r.watched, w)
	}
	b.Guests = append(b.Guests, reports...)
	for _, p := range picked {
		m, ok := texts[p.rowid]
		if p.declared == event.RelayDeclaredNone && !ok {
			// Edited or rewritten since the decision read it.
			n.Edited++
			continue
		}
		w := &watch{rowid: p.rowid, msg: p.msg, run: p.run, created: p.created, updated: p.updated, record: p.eventID}
		if !r.track(w, its[p.rowid], seenMS) || w.changed {
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
			if r.status.Running {
				n.WithServer++
			}
		} else {
			n.Declared++
		}
		r.watched = append(r.watched, w)
		b.Messages = append(b.Messages, msg)
	}
	r.status.Counts.add(n)
	r.held = held
	r.cursor = &next
	if r.reread.to > 0 && next.RowID >= r.reread.to {
		r.reread = rereadZone{}
	}
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

// decide applies the rules the WHERE clause leaves to Go (SM 2.2 rule 9) to
// the row at rowid id, which the read reads at seen: the marks, a run of its
// own, the age test, the stamp no later than 2 s after seen, the updated_at
// test and Claude Code as the provider (turnedAway). It returns nil for a
// row that gets no record. A row whose message the reader watches, which a
// resync reads again, goes to moves when it has a new rowid; a guest's is
// never recorded, whatever marks T3 left on it. The window turns away such
// a row before its folder test, and one a resync reads again, one read too
// late and one of a run on another harness, and the decision skips them
// (window).
func (r *Reader) decide(x row, id int64, seen time.Time, verdict func(string) Folder, moves map[*watch]int64, n *Counts) *candidate {
	msgID, thread, runID := x[2], x[3], x[4]
	if msgID.null || thread.null {
		return nil
	}
	msg := r.key("message", msgID.s)
	if w := r.watchOf(msg); w != nil {
		if w.rowid != id {
			moves[w] = id
		}
		return nil
	}
	created, ok1 := ms(x[5])
	updated, ok2 := ms(x[6])
	if !ok1 || !ok2 {
		return nil
	}
	if r.reread.has(id, created) {
		// Decided by a read before the resync, or too old to record.
		return nil
	}
	mk, err := strconv.Atoi(x[8].s)
	if err != nil {
		return nil
	}
	// No run of its own: a steer, a message whose queued run T3 turns into a
	// steer, or a run stamped at another time.
	own := !runID.null && x[9].s == "1" && x[10].s == "1" && x[11].s == "0"
	t := rowTests{marks: mk, own: own, created: created, updated: updated, other: !x[12].null && x[12].s != claudeDriver}
	if r.turnedAway(t, seen.UnixMilli(), n) {
		return nil
	}
	driver := ""
	if !x[12].null {
		driver = x[12].s
	}
	v := verdict(x[13].s)
	if v.State != FolderTracked || v.Project == "" {
		return nil
	}
	c := &candidate{
		id:          msgID.s,
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
	c.eventID = recordID(r.keys, msgID.s, c.createdText, created)
	return c
}

// rowTests are what the decision's tests in Go read of a row, none of it
// text: T3's marks, whether the row has a run of its own, its stamp and
// updated_at, Unix ms, and whether the run it names is on another harness
// than Claude Code, by its provider thread's driver.
type rowTests struct {
	marks            int
	own              bool
	created, updated int64
	other            bool
}

// turnedAway counts in n the first of the decision's tests in Go that turns
// away a row read at seen, Unix ms, in the decision's order: none of T3's
// marks of a message nobody typed (Marked), a run of its own (NoRun), a
// read at most RELAY_SEEN_LAG_S after the stamp (Aged) and at most 2 s
// before it (Ahead), an updated_at no later than the stamp (Edited), and
// Claude Code as the provider (OtherHarness). It reports whether one did.
// The window counts the rows it turns away before their folder test by it
// too, so each counts where the decision counts it.
func (r *Reader) turnedAway(t rowTests, seen int64, n *Counts) bool {
	switch {
	case t.marks&markNobody != 0:
		n.Marked++
	case !t.own:
		n.NoRun++
	case seen-t.created > r.lag:
		n.Aged++
	case t.created-seen > stampAhead:
		n.Ahead++
	case t.updated > t.created:
		n.Edited++
	case t.other:
		n.OtherHarness++
	default:
		return false
	}
	return true
}

// watchOf returns the watch of the message whose key is msg, or nil.
func (r *Reader) watchOf(msg string) *watch {
	for _, w := range r.watched {
		if w.msg == msg {
			return w
		}
	}
	return nil
}

// readAgain reports whether a read reads watched row w again: a guest's
// always (follow), and a message's while its row is as the reader first
// read it and its run hasn't claimed a prompt or hadn't ended at the last
// read, since that end bounds the prompts the session's next run may take
// (ranBefore).
func (w *watch) readAgain() bool {
	return w.guest || !w.changed && (w.claimed == "" || w.done == 0)
}

// track reads a watched row's item rows its, itemSQL's for its rowid, at
// seen, Unix ms, and reports whether they hold its message: after T3's
// VACUUM another message, or none, can hold its rowid until a resync finds
// it again, and only a row found holding its message tells whether it
// changed. A message's row is compared with what the reader first read of
// it (bind); a guest's follows its row to the runs of its own T3 gives it
// (follow).
func (r *Reader) track(w *watch, its []row, seen int64) bool {
	if len(its) == 0 || its[0][2].null || r.key("message", its[0][2].s) != w.msg {
		return false
	}
	w.found = r.reads
	if w.guest {
		r.follow(w, its, seen)
	} else {
		r.bind(w, its, seen)
	}
	return true
}

// bind compares a message's row with what the reader first read of it
// (DESIGN 5.1): a row whose run, stamp or updated_at changed, or whose run
// no longer names it, witnesses nothing, which catches an edit, a promotion
// to steer and a steer's follow-up T3 sent again. Then it reads the run, at
// seen, Unix ms.
func (r *Reader) bind(w *watch, its []row, seen int64) {
	x := its[0]
	created, _ := ms(x[4])
	updated, _ := ms(x[5])
	if x[3].null || r.key("run", x[3].s) != w.run || created != w.created || updated != w.updated || x[7].s != "1" {
		w.changed = true
		return
	}
	r.runOf(w, its, false, seen)
}

// follow follows a guest's row (DESIGN 5.1), whatever T3 does to it. A run
// of its own the row names that the watch didn't have, as when T3 sends the
// message again after Claude Code declined it as a steer, claims afresh,
// and every prompt such a run delivers is a guest's. While the row names no
// run of its own, steered into a running turn or with its run not stored
// yet, the run it had claims nothing stamped after seen, Unix ms. The
// running turn of another message that the row names is a Steer (steer),
// and one it no longer names ends at seen, unless a read saw it end first.
func (r *Reader) follow(w *watch, its []row, seen int64) {
	x := its[0]
	named := !x[3].null && !x[7].null
	into := ""
	if named && x[7].s == "0" {
		into = r.key("run", x[3].s)
	}
	for i := range w.steers {
		if s := &w.steers[i]; s.Run != into && s.To == 0 {
			s.To = seen
		}
	}
	if !named || x[7].s != "1" {
		if w.start > 0 && w.done == 0 {
			w.done = seen
		}
		if into != "" {
			r.steer(w, its, into)
		}
		return
	}
	if run := r.key("run", x[3].s); run != w.run {
		w.run, w.start, w.done, w.sessions, w.claimed = run, 0, 0, nil, ""
	}
	r.runOf(w, its, true, seen)
}

// steer notes run, the running turn of another message that a guest's row
// names (follow), as a Steer: from the later of the row's stamp and the
// run's start to the run's end, in the sessions the run went to on any
// provider. A run that hasn't started, or has no session yet, has delivered
// nothing, and the next read looks again.
func (r *Reader) steer(w *watch, its []row, run string) {
	x := its[0]
	start, started := ms(x[8])
	if !started {
		return
	}
	created, _ := ms(x[4])
	done, _ := ms(x[9])
	s := Steer{Run: run, Sessions: r.sessionsOf(its, true), From: max(created, start), To: done}
	if len(s.Sessions) == 0 {
		return
	}
	for i := range w.steers {
		if w.steers[i].Run == run {
			// A row stamped anew while it names the same turn takes no
			// prompt of the turn back from the mark.
			s.From = min(s.From, w.steers[i].From)
			w.steers[i] = s
			return
		}
	}
	w.steers = append(w.steers, s)
	if n := len(w.steers) - maxSteers; n > 0 {
		w.steers = w.steers[n:]
	}
}

// runOf reads a watched row's run from its item rows, at seen, Unix ms: its
// start, its end and the sessions it went to (sessionsOf). A guest's run
// takes the session of any provider's thread (anyDriver), since its prompt
// keeps no text whoever runs it; a message's run only Claude Code's, since
// only Claude Code's sessions earn relayed credit (SM 2.2). A run that ended
// before it started, as a queued one T3 cancelled, gets its completed_at as
// its end, or the first read that found it ended when T3 gave it none, and
// no start, so it delivers and bounds nothing (pair, ranBefore), and prune
// forgets it RELAY_MATCH_S and a margin after that end.
func (r *Reader) runOf(w *watch, its []row, anyDriver bool, seen int64) {
	x := its[0]
	start, started := ms(x[8])
	if !started {
		// Not started: queued, or ended before it started.
		if done, ok := ms(x[9]); ok {
			w.done = done
		} else if w.done == 0 && finished(x[6]) {
			w.done = seen
		}
		return
	}
	w.start = start
	w.done, _ = ms(x[9])
	w.sessions = r.sessionsOf(its, anyDriver)
}

// finished reports whether status, a run's as T3 stores it, is one T3
// leaves a run in once it has ended: completed, interrupted, failed,
// cancelled or rolled_back, and not preparing, queued, starting, running or
// waiting.
func finished(status field) bool {
	switch status.s {
	case "completed", "interrupted", "failed", "cancelled", "rolled_back":
		return true
	}
	return false
}

// sessionsOf returns the sessions that the run in a watched row's item
// rows, its, went to: each attempt's nativeThreadId, or the run's provider
// thread's nativeThreadRef.nativeId while no attempt names one, on any
// provider's thread (anyDriver) or on Claude Code's alone.
func (r *Reader) sessionsOf(its []row, anyDriver bool) []string {
	x := its[0]
	takes := func(driver field) bool { return anyDriver || driver.null || driver.s == claudeDriver }
	var sessions []string
	for _, a := range its {
		if !a[14].null && takes(a[15]) {
			if k := r.keys.Session(a[14].s); !slices.Contains(sessions, k) {
				sessions = append(sessions, k)
			}
		}
	}
	if len(sessions) == 0 && !x[11].null && takes(x[10]) {
		sessions = append(sessions, r.keys.Session(x[11].s))
	}
	return sessions
}

// pair gives each watched run that started the first prompt it may have
// delivered (SM 2.2 rule 9): one the agent asked about, of a session the
// run went to, stamped from startSlack before the run's start to its end,
// but not at or before the end of an earlier watched run of that session
// (ranBefore), and, for a message, within RELAY_MATCH_S of its stamp.
// Guests' runs claim first, since a guest's prompt taken for the owner's
// keeps the guest's words, then messages' runs, each kind in order of
// start, so a run never takes a later run's prompt, and an earlier run's
// end keeps a later run from that earlier run's prompts no run claimed,
// such as a steer into it. A message's run pairs only on fresh word that
// its row is as the reader first read it: this read found it, or its record
// is in b. The pairing goes on whichever record comes second (Paired): a
// record in b names the prompt, and the prompt asking, Pair's, names a
// record of an earlier batch as its Relay, unless the agent marks it guest
// (Prompt.Guest). A pairing found once both were written goes on neither,
// and nor does one with such a guest's prompt, but the run and the prompt
// pair with nothing else.
func (r *Reader) pair(b *Batch, asking string) {
	runs := slices.Clone(r.watched)
	slices.SortStableFunc(runs, func(a, c *watch) int {
		if a.guest != c.guest {
			if a.guest {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.start, c.start)
	})
	for _, w := range runs {
		if w.claimed != "" || w.changed || w.start == 0 || len(w.sessions) == 0 {
			continue
		}
		batched := !w.guest && slices.ContainsFunc(b.Messages, func(m Message) bool { return m.EventID == w.record })
		if !w.guest && !batched && w.found != r.reads {
			continue
		}
		var best *asked
		for _, p := range r.prompts {
			switch {
			case p.claimed, p.id == "", !slices.Contains(w.sessions, p.session),
				p.t < w.start-startSlack, w.done > 0 && p.t > w.done,
				!w.guest && (p.t < w.created || p.t > w.created+r.match),
				r.ranBefore(w, p):
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
		switch {
		case w.guest:
		case batched:
			for i := range b.Messages {
				if b.Messages[i].EventID == w.record {
					b.Messages[i].Data.Prompt = best.id
				}
			}
			r.status.Paired++
		case best.id == asking && !best.guest:
			// Pair puts the pairing on the prompt as its relay, which a
			// prompt the agent marks guest doesn't take (Prompt.Guest).
			r.status.Paired++
		}
	}
}

// ranBefore reports whether prompt p is stamped at or before the end of
// another watched run that went to p's session and started before w. T3
// starts a thread's next run only once the one before it has completed, so
// such a prompt is the earlier run's, or a steer into it, as one during its
// Stop hook can be, though w's start slack reaches it (SM 2.2). A run that
// hasn't ended bounds nothing, and prune keeps a message's run, however
// long it runs, until its end can bound no prompt the reader remembers, up
// to runWatch while no read has seen it end.
func (r *Reader) ranBefore(w *watch, p *asked) bool {
	return slices.ContainsFunc(r.watched, func(o *watch) bool {
		return o != w && o.start > 0 && o.start < w.start && o.done > 0 && p.t <= o.done &&
			slices.Contains(o.sessions, p.session)
	})
}

// prune forgets, at now in Unix ms, the runs that can no longer pair or
// bound a pairing, and the prompts that can no longer pair. A guest's
// message goes once guestWatch has passed since its stamp, whatever its runs
// did. A message's goes once RELAY_MATCH_S and a margin have passed since its
// stamp, when it can no longer pair, and its run's end can bound no prompt
// the reader remembers (ranBefore): RELAY_MATCH_S and the margin after the
// end a read saw, when every prompt stamped at or before it is forgotten
// too; or, while no read has seen the run end, runWatch after the stamp, or
// at once for a row that changed, which no read follows. A prompt goes once
// RELAY_MATCH_S and the margin have passed since its stamp. maxWatched and
// maxPrompts bound what is left, in state.json too.
func (r *Reader) prune(now int64) {
	keep := r.match + watchMargin
	r.watched = slices.DeleteFunc(r.watched, func(w *watch) bool {
		switch {
		case w.guest:
			return now-w.created > guestWatch
		case now-w.created <= keep:
			return false
		case w.done > 0:
			return now-w.done > keep
		}
		return w.changed || now-w.created > runWatch
	})
	if n := len(r.watched) - maxWatched; n > 0 {
		r.watched = r.watched[n:]
	}
	r.prompts = slices.DeleteFunc(r.prompts, func(p *asked) bool { return now-p.t > keep })
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
