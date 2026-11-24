// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import (
	"cmp"
	"slices"
)

// State is what the reader keeps between runs of the agent, under a key of
// its own in state.json (DESIGN 3.5): its cursor, the rows and runs it still
// watches, the prompts it was asked about and the rows it holds for their
// folders. It holds T3's ids by key alone (adapters.Keys.App), and no path
// or text, so nothing in it is personal. A state saved under another
// K_session matches no row, so the reader starts afresh from it, as it does
// from the zero State.
type State struct {
	Cursor  *Cursor       `json:"cursor,omitempty"`
	Watched []WatchState  `json:"watched,omitempty"`
	Prompts []PromptState `json:"prompts,omitempty"`
	Held    []HeldState   `json:"held,omitempty"`
}

// Cursor is where the reader has read T3's message table to (DESIGN 3.5,
// 5.1): a rowid, the key of the message id read there and that message's
// stamp, Unix ms. Message is empty when no row was there, as at 0 on an
// empty table. When the row at RowID no longer holds that message, or the
// newest rowid is below RowID, T3's VACUUM renumbered the rows or another
// store took the place of the one read, and the reader resyncs by stamp.
type Cursor struct {
	RowID     int64  `json:"rowid"`
	Message   string `json:"message,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
}

// WatchState is a watched row: a message the reader recorded, until
// RELAY_MATCH_S has passed and its run's end can bound no prompt the reader
// remembers, a day at most while no read has seen the run end
// (Reader.prune), or a guest's message, for a day whatever T3 does to its
// row, so that every run of its own T3 gives it delivers a guest's prompt
// across a restart of the agent too. The reader finds the row by RowID, and
// by Message once a resync reads it again. The reader keeps at most
// maxWatched of them.
type WatchState struct {
	RowID int64 `json:"rowid"`
	// Message and Run are the keys of the row's message and run ids. A
	// guest's Run is the run of its own the row last named, empty while it
	// named none, as when the owner steered it into a running turn.
	Message string `json:"message"`
	Run     string `json:"run"`
	// CreatedAt and UpdatedAt are the row's stamps as the reader first read
	// them, Unix ms.
	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
	Guest     bool  `json:"guest,omitempty"`
	// Record is the event id of the message's relay.message.
	Record string `json:"record,omitempty"`
	// Sessions, Start and Done are the run's sessions, as the hook keys
	// them, and its start and end, Unix ms.
	Sessions []string `json:"sessions,omitempty"`
	Start    int64    `json:"start,omitempty"`
	Done     int64    `json:"done,omitempty"`
	// Claimed is the event id of the prompt the run delivered.
	Claimed string `json:"claimed,omitempty"`
	// Steers are the running turns of other messages that the owner
	// steered a guest's message into (Steer).
	Steers []Steer `json:"steers,omitempty"`
}

// PromptState is a prompt the agent asked about.
type PromptState struct {
	Session string `json:"session"`
	T       int64  `json:"t"`
	EventID string `json:"event_id"`
	Claimed bool   `json:"claimed,omitempty"`
}

// HeldState is a row the reader holds for its folder, which pairtime's
// resolver hasn't answered for (Reader.window): its rowid, the key of its
// message id and its stamp as first read, Unix ms. It never holds the
// folder: after a restart the reader reads the row's folder again by its
// rowid and asks the resolver afresh, while the age test still lets it
// record the row (RELAY_SEEN_LAG_S). state.json keeps at most maxHeld of
// them, the newest.
type HeldState struct {
	RowID     int64  `json:"rowid"`
	Message   string `json:"message"`
	CreatedAt int64  `json:"created_at"`
}

// State returns what the reader keeps between runs of the agent, for
// state.json.
func (r *Reader) State() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	var s State
	if r.cursor != nil {
		c := *r.cursor
		s.Cursor = &c
	}
	for _, w := range r.watched {
		if w.changed || w.msg == "" {
			continue
		}
		s.Watched = append(s.Watched, WatchState{
			RowID: w.rowid, Message: w.msg, Run: w.run, CreatedAt: w.created, UpdatedAt: w.updated,
			Guest: w.guest, Record: w.record, Sessions: append([]string(nil), w.sessions...),
			Start: w.start, Done: w.done, Claimed: w.claimed, Steers: cloneSteers(w.steers),
		})
	}
	for _, p := range r.prompts {
		s.Prompts = append(s.Prompts, PromptState{Session: p.session, T: p.t, EventID: p.id, Claimed: p.claimed})
	}
	var held []HeldState
	for id, h := range r.held {
		if h.msg != "" {
			held = append(held, HeldState{RowID: id, Message: h.msg, CreatedAt: h.created})
		}
	}
	s.Held = newestHeld(held)
	return s
}

// newestHeld returns the newest maxHeld rows of held, by stamp and then
// rowid, sorted by rowid: r.held is a map, and the apps component skips a
// save whose bytes haven't changed, so an unchanged reader must give the
// same list. It sorts held in place.
func newestHeld(held []HeldState) []HeldState {
	slices.SortFunc(held, func(a, b HeldState) int {
		return cmp.Or(cmp.Compare(b.CreatedAt, a.CreatedAt), cmp.Compare(b.RowID, a.RowID))
	})
	held = held[:min(len(held), maxHeld)]
	slices.SortFunc(held, func(a, b HeldState) int { return cmp.Compare(a.RowID, b.RowID) })
	return held
}

// load takes up a saved State. A watched message's record went out before
// the save, so a prompt that pairs with it names it, once a read has found
// its row as the reader first read it. A held row comes back with no
// folder, which the next read reads again by its rowid (Reader.window).
func (r *Reader) load(s State) {
	if s.Cursor != nil {
		c := *s.Cursor
		r.cursor = &c
	}
	for _, w := range s.Watched {
		if w.Message == "" || w.Run == "" && !w.Guest {
			continue
		}
		r.watched = append(r.watched, &watch{
			rowid: w.RowID, msg: w.Message, run: w.Run, created: w.CreatedAt, updated: w.UpdatedAt,
			guest: w.Guest, record: w.Record, sessions: append([]string(nil), w.Sessions...),
			start: w.Start, done: w.Done, claimed: w.Claimed, steers: cloneSteers(w.Steers),
		})
	}
	for _, p := range s.Prompts {
		if p.EventID == "" {
			continue
		}
		r.prompts = append(r.prompts, &asked{session: p.Session, t: p.T, id: p.EventID, claimed: p.Claimed})
	}
	var held []HeldState
	for _, h := range s.Held {
		if h.RowID > 0 && h.Message != "" {
			held = append(held, h)
		}
	}
	for _, h := range newestHeld(held) {
		r.held[h.RowID] = heldRow{created: h.CreatedAt, msg: h.Message}
	}
}
