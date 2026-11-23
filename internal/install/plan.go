// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/install/udiff"
)

// Plan is everything Apply will do, worked out before anything is written.
type Plan struct {
	// Changes are the targets whose content changes, in the order Apply
	// writes them.
	Changes []Change
	// Problems are targets uninstall has to leave as they are, each with
	// the reason, for the teen to sort out. Install has none: a target it
	// can't edit fails the plan.
	Problems []string

	in        *Installer
	uninstall bool
	oldSum    string    // the digest of manifest.json when planned, "" if absent
	manifest  *manifest // what manifest.json holds after Apply
	backups   []pendingBackup
	rmdirs    []string // directories uninstall removes if empty, innermost first
	// adopted are the targets adopt records without changing them, with
	// what planning read: Apply checks them as it checks the changes.
	adopted []Change

	// unlock drops the install lock the plan holds until Apply or Close;
	// nil once dropped.
	unlock func() error

	// stop, when a test sets it, runs before Apply writes each target, and
	// before install writes the manifest, with File(manifest.json) as the
	// target. An error from it stops Apply there, as a crash or a failed
	// write would.
	stop func(Target) error
}

// Close drops the install lock, which a plan holds from planning on, so
// that no other install or uninstall can start in between (ErrBusy). Apply
// closes the plan itself. Close a plan that won't be applied, such as a dry
// run's or one the teen declined. Closing a plan again does nothing.
func (p *Plan) Close() error {
	if p.unlock == nil {
		return nil
	}
	err := p.unlock()
	p.unlock = nil
	return err
}

// locked runs plan under the install lock, which the plan it returns keeps
// until Apply or Close.
func (in *Installer) locked(plan func() (*Plan, error)) (*Plan, error) {
	unlock, err := in.lock()
	if err != nil {
		return nil, err
	}
	p, err := plan()
	if err != nil {
		unlock()
		return nil, err
	}
	p.unlock = unlock
	return p, nil
}

// Change is one target's planned change.
type Change struct {
	Target Target
	// Items are the IDs of the items whose edits make the change.
	Items  []string
	Before Content
	After  Content
	// Notes say what the teen should know about the change.
	Notes []string

	mkdirs []string // directories to make before writing, outermost first
	kinds  []string // the kinds of the edits that make the change
}

// Empty reports whether the plan changes no target. Apply may still tidy
// pairtime's own files.
func (p *Plan) Empty() bool { return len(p.Changes) == 0 }

// Diff returns the plan as one unified diff per target that changes, which
// is what `pairtime install --dry-run` prints and the consent screen shows.
// A target that isn't there on one side is /dev/null on that side.
//
// It is for people to read, not to apply. In a settings file every string
// value and comment pairtime didn't write shows as ***, so that no token
// or webhook URL of the teen's reaches the screen (maskSettings), and a
// character a terminal would act on is written out (escapeControls).
func (p *Plan) Diff() string {
	var b strings.Builder
	for _, c := range p.Changes {
		from, to := c.Target.String(), c.Target.String()
		if !c.Before.Exists {
			from = "/dev/null"
		}
		if !c.After.Exists {
			to = "/dev/null"
		}
		b.WriteString(udiff.Unified(from, to, c.shown(c.Before), c.shown(c.After)))
	}
	return escapeControls(b.String())
}

// FileDiff returns the diff that turns before into after in the file at
// path, written as Diff writes each change: a side that is nil, for no
// file, is /dev/null, and what a terminal acts on is written out. It masks
// nothing, so before and after are already what the screen may show, as
// for the config.toml change of pairtime install --calibration-volunteer,
// which config's own edit makes, outside any Plan.
func FileDiff(path string, before, after []byte) string {
	from, to := path, path
	if before == nil {
		from = "/dev/null"
	}
	if after == nil {
		to = "/dev/null"
	}
	return escapeControls(udiff.Unified(from, to, before, after))
}

// kindsOf returns the kinds of recs, each once.
func kindsOf(recs []editRecord) []string {
	var kinds []string
	for _, r := range recs {
		if !slices.Contains(kinds, r.Kind) {
			kinds = append(kinds, r.Kind)
		}
	}
	return kinds
}

// PlanInstall plans items' edits. Each target is read once and every edit
// for it applied in order, so two items can share a file. A target that
// already holds what the items want isn't in the plan: planning again
// right after Apply gives an empty plan. If the manifest doesn't know such
// a target, Apply still records it (see adopt).
//
// The plan holds the install lock until Apply or Close, and planning
// returns ErrBusy while another plan holds it.
func (in *Installer) PlanInstall(items []Item) (*Plan, error) {
	groups, err := groupEdits(items)
	if err != nil {
		return nil, err
	}
	return in.locked(func() (*Plan, error) { return in.planInstall(groups) })
}

func (in *Installer) planInstall(groups []*group) (*Plan, error) {
	m, oldSum, err := in.loadManifest()
	if err != nil {
		return nil, err
	}

	p := &Plan{in: in, oldSum: oldSum, manifest: m.clone()}
	for _, g := range groups {
		cur, err := in.read(g.target)
		if err != nil {
			return nil, err
		}
		next := cur
		for _, e := range g.edits {
			if next, err = e.Apply(next); err != nil {
				return nil, fmt.Errorf("%s %w", g.target, err)
			}
		}
		if next.Same(cur) {
			if err := p.adopt(g.target, cur, g.recs); err != nil {
				return nil, err
			}
			continue
		}
		if cur.Exists && !g.target.isRegistry() && cur.Mode&0o200 == 0 {
			return nil, fmt.Errorf("%s is read-only, so pairtime leaves it alone; make it writable and run install again", g.target)
		}
		if !g.target.isRegistry() && len(next.Data) > maxFileSize {
			// Uninstall would refuse to read the file it has to take
			// pairtime's entries out of.
			return nil, fmt.Errorf("%s would grow past the %d MiB pairtime reads back, so uninstall couldn't take pairtime's entries out of it; pairtime leaves it alone", g.target, maxFileSize>>20)
		}
		ch := Change{Target: g.target, Items: itemIDs(g.recs), Before: cur, After: next, kinds: kindsOf(g.recs)}
		if !g.target.isRegistry() && !cur.Exists {
			// A file that isn't there may be missing its folders too: on a
			// first install, and on a rerun after an Apply that stopped
			// before it made them, after an uninstall that stopped once it
			// had removed them, or after the teen deleted ~/.claude.
			if ch.mkdirs, err = missingDirs(g.target.Path); err != nil {
				return nil, err
			}
		}
		rec := p.manifest.find(g.target)
		switch {
		case rec == nil:
			// The manifest doesn't know the target: what it holds now is
			// what uninstall puts back, without any pairtime hooks an
			// earlier install left there, which would run a binary that
			// uninstall deletes. An entry pairtime owns whole that an
			// earlier pairtime wrote, with its binary somewhere else, had
			// nothing before it, as adopt counts it.
			before := cur
			if claimed(g.edits, cur) {
				before = Content{}
			}
			base, err := undoAll(cur, before, g.recs)
			if err != nil {
				return nil, fmt.Errorf("%s %w", g.target, err)
			}
			p.manifest.Targets = append(p.manifest.Targets, record{Target: g.target})
			rec = &p.manifest.Targets[len(p.manifest.Targets)-1]
			p.rebase(rec, base)
		case rec.Written != cur.digest():
			// Something else changed the target since pairtime wrote it,
			// such as git-ai rewriting its hooks. From now on uninstall puts
			// back what it holds now, without pairtime's edits.
			old, err := in.loadSaved(rec.Base)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", g.target, err)
			}
			base, err := undoAll(cur, old, rec.Edits)
			if err != nil {
				return nil, fmt.Errorf("%s %w", g.target, err)
			}
			p.rebase(rec, base)
		}
		// Uninstall removes the folders this Apply makes, as it removes
		// the ones an earlier one made.
		rec.Dirs = addDirs(rec.Dirs, ch.mkdirs)
		rec.Written = next.digest()
		for _, r := range g.recs {
			if i := slices.IndexFunc(rec.Edits, r.same); i >= 0 {
				rec.Edits[i].Data = r.Data // what this install's edit recorded
			} else {
				rec.Edits = append(rec.Edits, r)
			}
		}
		p.Changes = append(p.Changes, ch)
	}
	return p, nil
}

// group is the edits of one or more items to one target, in order.
type group struct {
	target Target
	edits  []Edit
	recs   []editRecord
}

// claimer is an edit that owns its target whole, such as an autostart
// entry, and can tell an entry an earlier pairtime wrote there, whatever
// binary path it names, from something else at the same name, which is
// the teen's.
type claimer interface {
	Edit
	// claims reports whether cur, which exists, is an earlier pairtime's.
	claims(cur Content) bool
}

// owns reports whether one of edits owns its target whole.
func owns(edits []Edit) bool {
	return slices.ContainsFunc(edits, func(e Edit) bool {
		_, ok := e.(claimer)
		return ok
	})
}

// claimed reports whether one of edits claims cur as an earlier pairtime's.
func claimed(edits []Edit, cur Content) bool {
	return cur.Exists && slices.ContainsFunc(edits, func(e Edit) bool {
		c, ok := e.(claimer)
		return ok && c.claims(cur)
	})
}

// groupEdits groups items' edits by target, in the order the targets first
// appear. It refuses an item without an ID, one listed twice, an edit of a
// kind uninstall can't undo, and two edits of one kind from one item for
// one target: the manifest keeps one record for each, which a reinstall
// finds by item and kind, so uninstall could undo only one of them.
func groupEdits(items []Item) ([]*group, error) {
	type edit struct {
		item, kind string
		target     Target
	}
	var groups []*group
	byTarget := map[Target]*group{}
	ids := map[string]bool{}
	seen := map[edit]bool{}
	for _, it := range items {
		if it.ID == "" || ids[it.ID] {
			return nil, fmt.Errorf("install item %q is missing its ID or listed twice", it.ID)
		}
		ids[it.ID] = true
		for _, e := range it.Edits {
			if _, ok := undoers[e.Kind()]; !ok {
				return nil, fmt.Errorf("install item %s has an edit of kind %q, which uninstall can't undo", it.ID, e.Kind())
			}
			t := e.Target()
			k := edit{it.ID, e.Kind(), t}
			if seen[k] {
				return nil, fmt.Errorf("install item %s has two edits of kind %q for %s, and uninstall could undo only one", it.ID, e.Kind(), t)
			}
			seen[k] = true
			g := byTarget[t]
			if g == nil {
				g = &group{target: t}
				byTarget[t] = g
				groups = append(groups, g)
			}
			r := editRecord{Item: it.ID, Kind: e.Kind()}
			if rd, ok := e.(Recorder); ok {
				r.Data = rd.Record()
				if r.Data != nil && !json.Valid(r.Data) {
					return nil, fmt.Errorf("install item %s has an edit of kind %q that records data that isn't JSON", it.ID, e.Kind())
				}
			}
			g.edits = append(g.edits, e)
			g.recs = append(g.recs, r)
		}
	}
	return groups, nil
}

// adopt records a target that already holds what the items want when the
// manifest doesn't know it, as after a reinstall with pairtime's data
// directory gone. Without a record uninstall would leave pairtime's hooks
// there, running a binary it deletes. What pairtime finds of its own
// counts as pairtime's: uninstall takes it out, and removes an entry
// pairtime owns whole.
func (p *Plan) adopt(t Target, cur Content, recs []editRecord) error {
	if p.manifest.find(t) != nil {
		return nil
	}
	base, err := undoAll(cur, Content{}, recs)
	if err != nil {
		return fmt.Errorf("%s %w", t, err)
	}
	if base.Same(cur) {
		return nil // nothing of pairtime's there
	}
	p.manifest.Targets = append(p.manifest.Targets, record{Target: t, Written: cur.digest(), Edits: append([]editRecord(nil), recs...)})
	p.rebase(&p.manifest.Targets[len(p.manifest.Targets)-1], base)
	p.adopted = append(p.adopted, Change{Target: t, Before: cur})
	return nil
}

// rebase makes base what uninstall puts back in rec's target. The copy of
// the base it replaces goes once the manifest no longer names it
// (dropBackups).
func (p *Plan) rebase(rec *record, base Content) {
	s, b := save(rec.Target, base)
	rec.Base = s
	if b != nil {
		p.backups = append(p.backups, *b)
	}
}

// PlanUninstall plans taking out everything the manifest records, the
// target pairtime touched last first. A target that still holds what
// pairtime wrote gets back what it held before, byte for byte: a file
// pairtime created is deleted, with the directories pairtime made for it.
// A target that changed since loses only pairtime's edits. A target that
// can't be undone stays in the manifest and in Problems.
//
// items are the items install would offer on this machine, the same ones
// PlanInstall takes. Their targets that the manifest doesn't record lose
// whatever pairtime finds of its own there, as adopt would record it:
// pairtime's hooks, and an autostart entry that starts a pairtime agent.
// That covers a manifest that is gone with pairtime's data directory, after
// "Delete everything" (DESIGN 8.8) or by hand, which would otherwise leave
// hooks and a login item that keep starting pairtime.
//
// The plan holds the install lock until Apply or Close, as PlanInstall's
// does.
func (in *Installer) PlanUninstall(items ...Item) (*Plan, error) {
	groups, err := groupEdits(items)
	if err != nil {
		return nil, err
	}
	return in.locked(func() (*Plan, error) { return in.planUninstall(groups) })
}

func (in *Installer) planUninstall(groups []*group) (*Plan, error) {
	m, oldSum, err := in.loadManifest()
	if err != nil {
		return nil, err
	}
	p := &Plan{in: in, uninstall: true, oldSum: oldSum, manifest: &manifest{V: manifestVersion}}
	var kept []record
	for i := len(m.Targets) - 1; i >= 0; i-- {
		rec := m.Targets[i]
		ch, err := in.planUndo(rec)
		if err != nil {
			p.Problems = append(p.Problems, err.Error())
			kept = append(kept, rec)
			continue
		}
		if !ch.Before.Same(ch.After) {
			p.Changes = append(p.Changes, ch)
		}
		for j := len(rec.Dirs) - 1; j >= 0; j-- {
			p.rmdirs = append(p.rmdirs, rec.Dirs[j])
		}
	}
	slices.Reverse(kept)
	p.manifest.Targets = kept
	for i := len(groups) - 1; i >= 0; i-- {
		g := groups[i]
		if m.find(g.target) != nil {
			continue
		}
		ch, err := in.planFound(g)
		if err != nil {
			p.Problems = append(p.Problems, err.Error())
			continue
		}
		if !ch.Before.Same(ch.After) {
			p.Changes = append(p.Changes, ch)
		}
	}
	return p, nil
}

// planUndo works out how uninstall puts rec's target back.
func (in *Installer) planUndo(rec record) (Change, error) {
	cur, err := in.read(rec.Target)
	if err != nil {
		return Change{}, err
	}
	ch := Change{Target: rec.Target, Items: itemIDs(rec.Edits), Before: cur, kinds: kindsOf(rec.Edits)}
	base, err := in.loadSaved(rec.Base)
	if err != nil {
		return Change{}, fmt.Errorf("%s: %w", rec.Target, err)
	}
	if cur.digest() == rec.Written {
		ch.After = base
	} else {
		if ch.After, err = undoAll(cur, base, rec.Edits); err != nil {
			return Change{}, fmt.Errorf("%s changed since pairtime wrote it, and pairtime's entries can't be taken out: %w", rec.Target, err)
		}
		if !ch.After.Same(cur) {
			ch.Notes = append(ch.Notes, "This changed since pairtime wrote it, so only pairtime's own entries come out.")
		}
	}
	if ch.After.Exists && cur.Exists && !rec.Target.isRegistry() {
		// The file keeps the mode it has now, which the digest doesn't
		// cover: a settings.json the teen made private since install
		// stays private.
		ch.After.Mode = cur.Mode
	}
	// checkUndo may change ch, so it runs before ch is returned.
	err = checkUndo(&ch, rec.Edits)
	return ch, err
}

// planFound works out how uninstall takes pairtime's entries out of g's
// target, which the manifest doesn't record, the way adopt finds them:
// every pairtime hook goes, with the lists and the file that only held
// pairtime's hooks, and an entry pairtime owns whole goes when one of g's
// edits claims it as pairtime's.
func (in *Installer) planFound(g *group) (Change, error) {
	cur, err := in.read(g.target)
	if err != nil {
		return Change{}, err
	}
	ch := Change{Target: g.target, Items: itemIDs(g.recs), Before: cur, After: cur, kinds: kindsOf(g.recs)}
	switch {
	case !cur.Exists:
		return ch, nil
	case owns(g.edits):
		if !claimed(g.edits, cur) {
			return ch, nil // an entry of the same name that isn't pairtime's
		}
	default:
		// Only a file that holds a pairtime entry changes: the empty lists
		// and the empty file that the undo below takes away are pairtime's
		// only when pairtime's entries were in them.
		kept, err := undoAll(cur, cur, g.recs)
		if err != nil {
			return Change{}, fmt.Errorf("%s %w", g.target, err)
		}
		if kept.Same(cur) {
			return ch, nil
		}
	}
	if ch.After, err = undoAll(cur, Content{}, g.recs); err != nil {
		return Change{}, fmt.Errorf("%s %w", g.target, err)
	}
	if !ch.After.Same(cur) {
		ch.Notes = append(ch.Notes, "pairtime has no record of installing this, so uninstall takes out what it recognizes as pairtime's.")
	}
	err = checkUndo(&ch, g.recs)
	return ch, err
}

// emptyFiles are, for each kind of edit to a file whose format has one, the
// file that holds nothing: what uninstall writes through a symbolic link
// instead of deleting a file pairtime created.
var emptyFiles = map[string]string{
	kindClaudeHooks: "{}",
}

// checkUndo refuses an undo that would rewrite a file the teen made
// read-only, and keeps a symbolic link the teen made. Deleting a file
// pairtime created would delete the link when settings.json was moved into
// a dotfiles repository and linked back, and leave pairtime's entries in
// the copy there, which the next checkout links back. So the file the link
// points to gets the file that holds nothing instead, or, for a kind of
// file without one, the target is a problem the teen sorts out.
func checkUndo(ch *Change, recs []editRecord) error {
	if ch.After.Same(ch.Before) || !ch.Before.Exists || ch.Target.isRegistry() {
		return nil
	}
	if !ch.After.Exists {
		if !ch.Before.link {
			return nil
		}
		empty, ok := emptyFile(recs)
		if !ok {
			return fmt.Errorf("%s is a symbolic link, so pairtime leaves it alone; pairtime created the file it points to, which you can delete", ch.Target)
		}
		newline := "\n"
		if bytes.Contains(ch.Before.Data, []byte("\r\n")) {
			newline = "\r\n"
		}
		ch.After = Content{Exists: true, Data: []byte(empty + newline), Mode: ch.Before.Mode}
		ch.Notes = append(ch.Notes, "This is a link, which stays, so the file it points to is left empty rather than deleted.")
	}
	if ch.Before.Mode&0o200 == 0 {
		return fmt.Errorf("%s is read-only now, so pairtime leaves it alone; make it writable and run uninstall again", ch.Target)
	}
	return nil
}

// emptyFile returns the file that holds nothing for a target that recs
// edited, when every one of them is of a kind that has one.
func emptyFile(recs []editRecord) (string, bool) {
	empty := ""
	for _, r := range recs {
		e, ok := emptyFiles[r.Kind]
		if !ok || (empty != "" && e != empty) {
			return "", false
		}
		empty = e
	}
	return empty, empty != ""
}

// undoAll undoes edits on cur, the last one first.
func undoAll(cur, base Content, edits []editRecord) (Content, error) {
	for i := len(edits) - 1; i >= 0; i-- {
		undo, ok := undoers[edits[i].Kind]
		if !ok {
			return Content{}, fmt.Errorf("pairtime doesn't know how to undo an edit of kind %q", edits[i].Kind)
		}
		var err error
		if cur, err = undo(cur, base, edits[i].Data); err != nil {
			return Content{}, err
		}
	}
	return cur, nil
}

func itemIDs(recs []editRecord) []string {
	var ids []string
	for _, r := range recs {
		if !slices.Contains(ids, r.Item) {
			ids = append(ids, r.Item)
		}
	}
	return ids
}

// ErrStale means a target or the manifest changed between planning and
// Apply, so Apply didn't write it. Plan again to see what changed.
var ErrStale = errors.New("changed since the plan was made, so pairtime didn't write it; plan again")

// Apply makes p's changes. It first checks that every target still holds
// what the plan read, and writes nothing if one doesn't (ErrStale).
//
// Install writes the copies of what each target held, then the manifest,
// then the targets, so that a crash at any point leaves a manifest that
// covers everything pairtime may have written. Uninstall writes the
// targets first and the manifest last, so that a crash leaves the targets
// not yet undone in the manifest. Running either again finishes the job.
//
// A target can still change while Apply runs, as when git-ai rewrites
// settings.json during the copies' and the manifest's flushes to disk. So
// Apply reads each target again just before it writes it, and stops with
// ErrStale, writing nothing more, if it changed. The manifest already
// covers what Apply wrote and the target it stopped at, so planning and
// applying again finishes the job and keeps the other program's change.
//
// Apply drops the plan's install lock when it returns, and a plan that was
// applied or closed can't be applied.
func (p *Plan) Apply() error {
	if p.unlock == nil {
		return errors.New("the install plan was applied or closed already; plan again")
	}
	defer p.Close()
	in := p.in
	if _, oldSum, err := in.loadManifest(); err != nil {
		return err
	} else if oldSum != p.oldSum {
		return fmt.Errorf("%s %w", in.manifestPath(), ErrStale)
	}
	// Every target the plan read, those it changes and those it only
	// records, must still hold what it read: a record of hooks that another
	// program has taken out since would say install worked.
	for _, c := range slices.Concat(p.Changes, p.adopted) {
		cur, err := in.read(c.Target)
		if err != nil {
			return err
		}
		if !cur.unchanged(c.Before) {
			return fmt.Errorf("%s %w", c.Target, ErrStale)
		}
	}
	data, err := encodeManifest(p.manifest)
	if err != nil {
		return err
	}
	if !p.uninstall {
		if err := p.saveBackups(); err != nil {
			p.removeBackups()
			return err
		}
		var err error
		if p.stop != nil {
			err = p.stop(File(in.manifestPath()))
		}
		if err == nil {
			err = in.writeManifest(data, p.oldSum)
		}
		if err != nil {
			// No manifest names this plan's backups, and they hold copies
			// of settings files, tokens and all.
			p.removeBackups()
			return err
		}
		// The manifest now names this plan's backups instead of the ones
		// they replace, so those go now, whatever happens to the targets.
		if err := p.dropBackups(); err != nil {
			return err
		}
	}
	for _, c := range p.Changes {
		if p.stop != nil {
			if err := p.stop(c.Target); err != nil {
				return err
			}
		}
		if cur, err := in.read(c.Target); err != nil {
			return err
		} else if !cur.unchanged(c.Before) {
			return fmt.Errorf("%s %w", c.Target, ErrStale)
		}
		existed, err := makeDirs(c.mkdirs)
		if err != nil {
			return err
		}
		if rec := p.manifest.find(c.Target); len(existed) > 0 && rec != nil {
			// The manifest, written before the folders were made, names
			// these as pairtime's. They aren't, so uninstall mustn't take
			// them away: it stops naming them before the target is written.
			rec.Dirs = slices.DeleteFunc(rec.Dirs, func(d string) bool { return slices.Contains(existed, d) })
			if data, err = encodeManifest(p.manifest); err != nil {
				return err
			}
			if err := in.writeManifest(data, ""); err != nil {
				return err
			}
		}
		if err := in.write(c.Target, c.After); err != nil {
			return fmt.Errorf("writing %s: %w", c.Target, err)
		}
	}
	if p.uninstall {
		for _, d := range p.rmdirs {
			// Only an empty directory goes, which is the point, and only a
			// real one: os.Remove would delete a symbolic link or a
			// junction that the teen put where pairtime's folder was,
			// whatever it points to, such as ~/.claude moved into a
			// dotfiles repository.
			if info, err := os.Lstat(d); err == nil && info.Mode().Type() == fs.ModeDir {
				os.Remove(d)
			}
		}
		if err := in.writeManifest(data, p.oldSum); err != nil {
			return err
		}
		if err := p.dropBackups(); err != nil {
			return err
		}
	}
	if data == nil {
		// Nothing is recorded any more: take away pairtime's install
		// directory, with any backup or temporary file an Apply that
		// stopped half way left in it, if nothing else is in it.
		in.removeLeftovers()
	}
	return nil
}

// removeBackups deletes the backups this plan wrote, or began to write.
func (p *Plan) removeBackups() {
	for _, b := range p.backups {
		os.Remove(filepath.Join(p.in.backupsDir(), b.name))
	}
	os.Remove(p.in.backupsDir()) // if it is empty now
}

// dropBackups deletes every backup that the manifest Apply wrote doesn't
// name, and the temporary files of a backup's write. Those are the
// backups this plan replaced, and any an earlier Apply couldn't delete
// once it had written its manifest, because it stopped there or the
// backups folder didn't let it: they hold copies of settings files, tokens
// and all, and this way the next Apply finishes the job. The install lock
// keeps any other Apply from writing a backup meanwhile.
func (p *Plan) dropBackups() error {
	named := map[string]bool{}
	for _, r := range p.manifest.Targets {
		named[r.Base.Backup] = true
	}
	dir := p.in.backupsDir()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var first error
	for _, e := range entries {
		name := e.Name()
		if named[name] || !e.Type().IsRegular() || !(isBackupName(name) || isTempName(name)) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) && first == nil {
			first = err
		}
	}
	return first
}

// removeLeftovers takes away the install directory once the manifest is
// gone: the backups and the temporary files of atomic writes that a failed
// or interrupted Apply left there, then the directories, if they are empty
// then. Only files pairtime names that way go.
func (in *Installer) removeLeftovers() {
	for _, dir := range []string{in.backupsDir(), in.Dir} {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.Type().IsRegular() && (isBackupName(e.Name()) || isTempName(e.Name())) {
				os.Remove(filepath.Join(dir, e.Name()))
			}
		}
		os.Remove(dir)
	}
}

func (p *Plan) saveBackups() error {
	if len(p.backups) == 0 {
		return nil
	}
	if err := os.MkdirAll(p.in.backupsDir(), newDirMode); err != nil {
		return err
	}
	for _, b := range p.backups {
		if err := home.WritePrivate(filepath.Join(p.in.backupsDir(), b.name), b.data); err != nil {
			return err
		}
	}
	return nil
}

// writeManifest replaces manifest.json with data, or deletes it when data
// is nil. It does nothing when the file already holds data.
func (in *Installer) writeManifest(data []byte, oldSum string) error {
	if data == nil {
		if err := os.Remove(in.manifestPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if sum(data) == oldSum {
		return nil
	}
	if err := os.MkdirAll(in.Dir, newDirMode); err != nil {
		return err
	}
	return home.WritePrivate(in.manifestPath(), data)
}
