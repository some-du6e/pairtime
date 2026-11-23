// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

// State is what the reader keeps between runs of the agent, under a key of
// its own in state.json (DESIGN 3.5): its cursor, the rows and runs it still
// watches and the prompts it was asked about. It holds T3's ids by key
// alone (adapters.Keys.App), and no path or text, so nothing in it is
// personal. A state saved under another K_session matches no row, so the
// reader starts afresh from it, as it does from the zero State.
type State struct {
	Cursor  *Cursor       `json:"cursor,omitempty"`
	Watched []WatchState  `json:"watched,omitempty"`
	Prompts []PromptState `json:"prompts,omitempty"`
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

// WatchState is a watched row: a message the reader recorded, until it
// pairs or RELAY_MATCH_S passes, or a guest's message whose run hasn't
// delivered its prompt yet.
type WatchState struct {
	RowID int64 `json:"rowid"`
	// Message and Run are the keys of the row's message and run ids.
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
}

// PromptState is a prompt the agent asked about.
type PromptState struct {
	Session string `json:"session"`
	T       int64  `json:"t"`
	EventID string `json:"event_id"`
	Claimed bool   `json:"claimed,omitempty"`
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
			Start: w.start, Done: w.done, Claimed: w.claimed,
		})
	}
	for _, p := range r.prompts {
		s.Prompts = append(s.Prompts, PromptState{Session: p.session, T: p.t, EventID: p.id, Claimed: p.claimed})
	}
	return s
}

// load takes up a saved State. A watched message's record went out before
// the save, so a prompt that pairs with it names it.
func (r *Reader) load(s State) {
	if s.Cursor != nil {
		c := *s.Cursor
		r.cursor = &c
	}
	for _, w := range s.Watched {
		if w.Message == "" || w.Run == "" {
			continue
		}
		r.watched = append(r.watched, &watch{
			rowid: w.RowID, msg: w.Message, run: w.Run, created: w.CreatedAt, updated: w.UpdatedAt,
			guest: w.Guest, record: w.Record, out: w.Record != "", sessions: append([]string(nil), w.Sessions...),
			start: w.Start, done: w.Done, claimed: w.Claimed,
		})
	}
	for _, p := range s.Prompts {
		if p.EventID == "" {
			continue
		}
		r.prompts = append(r.prompts, &asked{session: p.Session, t: p.T, id: p.EventID, claimed: p.Claimed})
	}
}
