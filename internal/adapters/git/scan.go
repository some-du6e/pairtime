// SPDX-License-Identifier: AGPL-3.0-or-later

package git

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// Change is what one git operation did in a worktree: a git.change event
// and the records of the commits it made or brought (DESIGN 5.8).
//
// The event's envelope has T, Source, Harness, SessionKey and ProjectKey.
// The agent fills in the rest when it writes the event: its id, the
// device, the sequence number, the monotonic clock, the boot id, and Via,
// which is ViaGit for everything the watcher delivers (DESIGN 3.4, 4.1
// E-2).
type Change struct {
	Event   event.Event
	Commits []Commit
	// GitStart and GitEnd are when git wrote the reflog entries that
	// began and finished the operation, in Unix seconds of git's clock. A
	// rebase spans several entries; anything else has one.
	GitStart, GitEnd int64
}

// Op returns the change's git operation.
func (c Change) Op() event.GitOp {
	if g, ok := c.Event.Data.(*event.GitChange); ok {
		return g.Op
	}
	return ""
}

// SetActor sets who ran the operation on the event and on the commits it
// made here.
func (c *Change) SetActor(a event.Actor) {
	if g, ok := c.Event.Data.(*event.GitChange); ok {
		g.Actor = a
	}
	for i := range c.Commits {
		if c.Commits[i].Local.Origin == MadeHere {
			c.Commits[i].Actor = a
		}
	}
}

// Window sizes for reading the reflog: most scans find a few new entries,
// so the first read is small, and it grows when the cursor isn't in it.
var reflogWindows = []int{64, 1024, 16384}

// maxArrived caps the commits a pull or merge records as made elsewhere.
// Pulling a busy upstream can bring thousands; the newest ones are what a
// project story shows.
const maxArrived = 200

// maxRebase caps the commits a rebase records and maps.
const maxRebase = 2000

// recentMax is how many recorded commits a Scanner remembers, so that a
// merge of a local branch doesn't record its commits again as arrivals.
const recentMax = 256

// ScannerState is what a Scanner resumes from after a restart. It is
// local state (state.json).
type ScannerState struct {
	Cursor Cursor   `json:"cursor"`
	Recent []string `json:"recent,omitempty"`
}

// Scanner reads one worktree's HEAD reflog and turns the entries written
// since its last scan into Changes. It is not safe for concurrent use;
// the Watcher runs one scan at a time per worktree.
type Scanner struct {
	git    *Runner
	loc    Location
	st     ScannerState
	now    func() time.Time
	rekey  func(context.Context, Location) (Location, error)
	failed int // consecutive scans that couldn't read commits
}

// NewScanner returns a scanner for the worktree at loc, resuming from st.
// rekey, when set, is called when the repo's identity may have changed:
// its first commit, or a shallow clone unshallowed.
func NewScanner(g *Runner, loc Location, st ScannerState, now func() time.Time, rekey func(context.Context, Location) (Location, error)) *Scanner {
	if now == nil {
		now = time.Now
	}
	st.Recent = slices.Clone(st.Recent)
	return &Scanner{git: g, loc: loc, st: st, now: now, rekey: rekey}
}

// State returns what to keep for the next start.
func (s *Scanner) State() ScannerState {
	st := s.st
	st.Recent = slices.Clone(s.st.Recent)
	return st
}

// Location returns the worktree's location, with its current key.
func (s *Scanner) Location() Location { return cloneLoc(s.loc) }

// maxFailures is how many scans in a row may fail to read the commits of
// the same entries before the scanner skips them, so that one damaged
// object can't stop a repo's watch for good.
const maxFailures = 3

// Scan reads the reflog entries written since the last scan and returns
// what they did, oldest first. The first scan of a worktree only notes
// where the reflog ends: what happened before pairtime saw the repo wasn't
// observed.
func (s *Scanner) Scan(ctx context.Context) ([]Change, error) {
	now := s.now()
	window, err := readReflog(ctx, s.git, s.loc.Dir, reflogWindows[0])
	if err != nil {
		return nil, err
	}
	if !s.st.Cursor.Set {
		if len(window) > 0 {
			s.st.Cursor = cursorAt(window[0])
		} else {
			s.st.Cursor = Cursor{HEAD: s.head(ctx), Set: true}
		}
		return nil, nil
	}

	fresh, err := s.fresh(ctx, window, now)
	if err != nil || len(fresh) == 0 {
		return nil, err
	}
	ops, used := plan(fresh, rebaseInProgress(s.loc.GitDir))
	if used == 0 {
		return nil, nil
	}
	if err := s.maybeRekey(ctx, ops); err != nil {
		return nil, err
	}
	changes, err := s.build(ctx, ops, now)
	if err != nil {
		s.failed++
		if s.failed < maxFailures {
			return nil, err
		}
		changes = nil // give up on these entries
	}
	s.failed = 0
	last := fresh[used-1]
	s.st.Cursor = cursorAt(last)
	if last.Msg == "" { // made up from HEAD, for a repo without a reflog
		s.st.Cursor = Cursor{HEAD: last.New, Set: true}
	}
	for _, c := range changes {
		for _, cm := range c.Commits {
			s.remember(cm.SHA)
		}
	}
	return changes, err
}

// fresh returns the entries after the cursor, oldest first, reading more
// of the reflog when the cursor isn't in the first window.
func (s *Scanner) fresh(ctx context.Context, window []entry, now time.Time) ([]entry, error) {
	cur := s.st.Cursor
	if len(window) == 0 {
		// No reflog, or it was turned off: compare HEAD itself.
		head := s.head(ctx)
		if head == "" || head == cur.HEAD {
			return nil, nil
		}
		return []entry{{Old: cur.HEAD, New: head, Time: now.Unix()}}, nil
	}
	if cur.New == "" {
		// The reflog was empty when the cursor was set, so all of it is new.
		return reversed(window), nil
	}
	for i := 0; ; i++ {
		if got, ok := cur.after(window); ok {
			return got, nil
		}
		if len(window) < reflogWindows[i] || i+1 == len(reflogWindows) {
			// The whole reflog is in the window and the cursor's entry
			// isn't: the reflog expired or was rewritten.
			return cur.since(window), nil
		}
		w, err := readReflog(ctx, s.git, s.loc.Dir, reflogWindows[i+1])
		if err != nil {
			return nil, err
		}
		window = w
	}
}

func (s *Scanner) head(ctx context.Context) string {
	out, err := s.git.Run(ctx, s.loc.Dir, "rev-parse", "--quiet", "--verify", "HEAD^{commit}")
	if err != nil {
		return ""
	}
	ids, err := parseIDs(string(out))
	if err != nil || len(ids) != 1 {
		return ""
	}
	return ids[0]
}

func (s *Scanner) remember(sha string) {
	if slices.Contains(s.st.Recent, sha) {
		return
	}
	s.st.Recent = append(s.st.Recent, sha)
	if len(s.st.Recent) > recentMax {
		s.st.Recent = slices.Delete(s.st.Recent, 0, len(s.st.Recent)-recentMax)
	}
}

// maybeRekey asks the resolver for the repo's identity again when these
// operations may have changed it: a repo's first commit, or a shallow
// clone whose shallow file is gone.
func (s *Scanner) maybeRekey(ctx context.Context, ops []op) error {
	if s.rekey == nil {
		return nil
	}
	switch s.loc.Kind {
	case KindUnborn:
		if !slices.ContainsFunc(ops, func(o op) bool { return o.makes() }) {
			return nil
		}
	case KindShallow:
		if _, err := os.Stat(filepath.Join(s.loc.CommonDir, "shallow")); err == nil {
			return nil
		}
	default:
		return nil
	}
	loc, err := s.rekey(ctx, s.loc)
	if err != nil {
		return err
	}
	s.loc = loc
	return nil
}

// rebaseInProgress reports whether a rebase has stopped partway, for an
// edit, a conflict or a break. Its reflog entries are held back until it
// finishes or is aborted, so an aborted rebase records nothing.
func rebaseInProgress(gitDir string) bool {
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		if fi, err := os.Stat(filepath.Join(gitDir, name)); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

// op is one git operation, from one reflog entry or, for a rebase, from
// the entries between its start and its end.
type op struct {
	kind  event.GitOp
	first entry
	last  entry
	// steps are a rebase's entries, from its start to its end.
	steps   []entry
	unknown bool // a message the adapter doesn't know: decided from the commit graph
}

// makes reports whether the operation creates commits.
func (o op) makes() bool {
	switch o.kind {
	case event.GitCommit, event.GitAmend, event.GitRevert, event.GitMerge, event.GitRebase:
		return true
	}
	return o.unknown
}

// plan groups entries, oldest first, into operations. It returns how many
// entries it used: when inRebase holds and a rebase's start has no end
// yet, the entries from the start on wait for the next scan.
func plan(entries []entry, inRebase bool) ([]op, int) {
	var ops []op
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		a := classify(e.Msg)
		switch a.step {
		case stepRebaseStart:
			end, aborted, closed := rebaseEnd(entries, i)
			if !closed && inRebase {
				return ops, i
			}
			if !aborted {
				ops = append(ops, op{kind: event.GitRebase, first: e, last: entries[end], steps: entries[i : end+1]})
			}
			i = end
			continue
		case stepNone, stepRebaseStep, stepRebaseFinish, stepRebaseAbort:
			// A rebase step outside a start and an end belongs to a rebase
			// whose start came before the cursor; it was handled then.
			continue
		}
		if e.Old == e.New {
			continue // HEAD didn't move: a stash, a reset to HEAD
		}
		o := op{first: e, last: e}
		switch a.step {
		case stepCommit:
			o.kind = event.GitCommit
		case stepAmend:
			o.kind = event.GitAmend
		case stepRevert:
			o.kind = event.GitRevert
		case stepMerge:
			o.kind = event.GitMerge
		case stepCheckout:
			o.kind = event.GitCheckout
		case stepReset:
			o.kind = event.GitReset
		default:
			o.kind, o.unknown = event.GitCheckout, true
		}
		ops = append(ops, o)
	}
	return ops, len(entries)
}

// rebaseEnd finds the end of the rebase that starts at entries[start]. A
// rebase spans its steps and whatever a stop for an edit allows: commits,
// amends and resets. It ends at its finish or abort entry; closed is
// false when neither came. A rebase that was quit, which writes no entry,
// ends before the first entry that can't be part of it, or at the last.
func rebaseEnd(entries []entry, start int) (end int, aborted, closed bool) {
	end = start
	for j := start + 1; j < len(entries); j++ {
		switch classify(entries[j].Msg).step {
		case stepRebaseStep, stepCommit, stepAmend, stepReset:
		case stepRebaseFinish:
			return j, false, true
		case stepRebaseAbort:
			return j, true, true
		default:
			return end, false, false
		}
		end = j
	}
	return end, false, false
}

// build turns operations into changes, reading what they need from git.
func (s *Scanner) build(ctx context.Context, ops []op, now time.Time) ([]Change, error) {
	// The commit each operation moved HEAD to, in one read.
	var tips []string
	for _, o := range ops {
		tips = appendNew(tips, o.last.New)
	}
	metas, err := readMeta(ctx, s.git, s.loc.Dir, tips)
	if err != nil {
		return nil, err
	}

	var changes []Change
	var need []*meta // commits whose stats to read
	for _, o := range ops {
		tip := metas[o.last.New]
		if tip == nil {
			continue // the commit is gone, as after an aggressive gc
		}
		c := Change{GitStart: o.first.Time, GitEnd: o.last.Time}
		var made, arrived []*meta
		var rewrites []Rewrite
		kind := o.kind
		if o.unknown {
			kind, made, arrived, err = s.decide(ctx, o, tip)
			if err != nil {
				return nil, err
			}
		} else {
			switch kind {
			case event.GitCommit, event.GitRevert:
				made = []*meta{tip}
			case event.GitAmend:
				made = []*meta{tip}
				if !zeroID(o.first.Old) && o.first.Old != "" {
					rewrites = []Rewrite{{OldSHA: o.first.Old, NewSHA: tip.SHA}}
				}
			case event.GitMerge:
				if len(tip.Parents) >= 2 && tip.Parents[0] == o.first.Old {
					made = []*meta{tip}
				}
				if arrived, err = s.arrivals(ctx, o.first.Old, tip, made != nil); err != nil {
					return nil, err
				}
			case event.GitRebase:
				if made, rewrites, err = s.rebased(ctx, o); err != nil {
					return nil, err
				}
			}
		}
		c.Event = s.event(kind, tip, made, now)
		for _, m := range made {
			c.Commits = append(c.Commits, s.record(m, kind, MadeHere, o.last.Time, rewritesTo(rewrites, m.SHA), now))
			need = append(need, m)
		}
		for _, m := range arrived {
			c.Commits = append(c.Commits, s.record(m, kind, MadeElsewhere, o.last.Time, nil, now))
			need = append(need, m)
		}
		changes = append(changes, c)
	}

	// When the stats can't be read, the counts stay 0 and NoStat says so;
	// the commits are still recorded.
	_ = readStats(ctx, s.git, s.loc.Dir, uniqueMetas(need))
	for i := range changes {
		for j := range changes[i].Commits {
			cm := &changes[i].Commits[j]
			for _, m := range need {
				if m.SHA == cm.SHA {
					cm.FilesChanged, cm.Insertions, cm.Deletions = m.files, m.ins, m.dl
					cm.Local.NoStat = m.noStat
					break
				}
			}
		}
		if g, ok := changes[i].Event.Data.(*event.GitChange); ok && len(changes[i].Commits) > 0 {
			// The event carries the stats of the commit HEAD moved to, when
			// the operation made it.
			for _, cm := range changes[i].Commits {
				if cm.SHA == g.SHA && cm.Local.Origin == MadeHere {
					g.FilesChanged, g.Insertions, g.Deletions = cm.FilesChanged, cm.Insertions, cm.Deletions
				}
			}
		}
	}
	return changes, nil
}

func uniqueMetas(ms []*meta) []*meta {
	var out []*meta
	seen := make(map[string]bool)
	for _, m := range ms {
		if !seen[m.SHA] {
			seen[m.SHA] = true
			out = append(out, m)
		}
	}
	return out
}

func rewritesTo(rs []Rewrite, sha string) []Rewrite {
	var out []Rewrite
	for _, r := range rs {
		if r.NewSHA == sha {
			out = append(out, r)
		}
	}
	return out
}

// event builds the git.change event for an operation that moved HEAD to
// tip. The actor is provisional: the Watcher sets ActorAgent when an
// agent's tool ran the same git command.
func (s *Scanner) event(kind event.GitOp, tip *meta, made []*meta, now time.Time) event.Event {
	g := &event.GitChange{
		Op:      kind,
		SHA:     tip.SHA,
		Parents: slices.Clone(tip.Parents),
		Actor:   provisionalActor(tip, made),
	}
	if slices.ContainsFunc(made, func(m *meta) bool { return m.SHA == tip.SHA }) {
		g.AgentTrailer = tip.agent
	}
	return event.Event{
		Envelope: event.Envelope{
			T:          now.UnixMilli(),
			Source:     event.SourceGit,
			Harness:    event.HarnessNone,
			SessionKey: event.NoSession,
			ProjectKey: s.loc.Project,
		},
		Data: g,
	}
}

// provisionalActor is who made a change before agent commands are
// matched: unknown when a commit it made names an agent in a trailer,
// since an agent's commit with no tool event behind it, like Aider's, or
// a person committing an agent's message, are both possible; human
// otherwise.
func provisionalActor(tip *meta, made []*meta) event.Actor {
	for _, m := range made {
		if m.agent != "" {
			return event.ActorUnknown
		}
	}
	return event.ActorHuman
}

func (s *Scanner) record(m *meta, kind event.GitOp, origin Origin, reflogTime int64, rewrites []Rewrite, now time.Time) Commit {
	actor := event.ActorHuman
	if m.agent != "" || origin == MadeElsewhere {
		actor = event.ActorUnknown
	}
	return Commit{
		SHA:           m.SHA,
		Parents:       slices.Clone(m.Parents),
		Project:       s.loc.Project,
		ObservedAt:    now.UnixMilli(),
		AuthorTime:    m.AuthorTime,
		CommitterTime: m.CommitterTime,
		Rewrites:      rewrites,
		Actor:         actor,
		AgentTrailer:  m.agent,
		Local: CommitLocal{
			Op:          kind,
			Origin:      origin,
			AuthorTZ:    m.AuthorTZ,
			CommitterTZ: m.CommitterTZ,
			ReflogTime:  reflogTime,
			AI:          m.ai,
		},
	}
}

// arrivals returns the commits a pull or merge brought into HEAD's
// history that this scanner hasn't recorded, newest maxArrived of them,
// oldest first. madeTip says the tip is a merge commit made here, which
// isn't an arrival.
func (s *Scanner) arrivals(ctx context.Context, old string, tip *meta, madeTip bool) ([]*meta, error) {
	if old == "" || zeroID(old) {
		return nil, nil
	}
	out, err := s.git.Run(ctx, s.loc.Dir, "rev-list", "-n", strconv.Itoa(maxArrived+1), tip.SHA, "--not", old, "--")
	if err != nil {
		return nil, err
	}
	ids, err := parseIDs(string(out))
	if err != nil {
		return nil, err
	}
	var want []string
	for _, id := range ids {
		if (madeTip && id == tip.SHA) || slices.Contains(s.st.Recent, id) {
			continue
		}
		want = append(want, id)
	}
	if len(want) > maxArrived {
		want = want[:maxArrived]
	}
	slices.Reverse(want)
	metas, err := readMeta(ctx, s.git, s.loc.Dir, want)
	if err != nil {
		return nil, err
	}
	var ms []*meta
	for _, id := range want {
		if m := metas[id]; m != nil {
			ms = append(ms, m)
		}
	}
	return ms, nil
}

// decide classifies an entry whose message the adapter doesn't know, from
// the commit graph: a new commit on top of the old HEAD, made about when
// the entry was written, is a commit (a merge commit if it has more
// parents); a move to a descendant is a fast-forward; anything else is a
// checkout.
func (s *Scanner) decide(ctx context.Context, o op, tip *meta) (event.GitOp, []*meta, []*meta, error) {
	old := o.first.Old
	if len(tip.Parents) > 0 && tip.Parents[0] == old && abs64(tip.CommitterTime-o.last.Time) <= 300 {
		if len(tip.Parents) >= 2 {
			arrived, err := s.arrivals(ctx, old, tip, true)
			return event.GitMerge, []*meta{tip}, arrived, err
		}
		return event.GitCommit, []*meta{tip}, nil, nil
	}
	if old != "" && !zeroID(old) {
		_, err := s.git.Run(ctx, s.loc.Dir, "merge-base", "--is-ancestor", old, tip.SHA)
		if err == nil {
			arrived, err := s.arrivals(ctx, old, tip, false)
			return event.GitMerge, nil, arrived, err
		}
	}
	return event.GitCheckout, nil, nil, nil
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
