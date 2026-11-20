// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
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
	drops     []string // backup files Apply deletes once it is done
	rmdirs    []string // directories uninstall removes if empty, innermost first
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
}

// Empty reports whether the plan changes no target. Apply may still tidy
// pairtime's own files.
func (p *Plan) Empty() bool { return len(p.Changes) == 0 }

// Diff returns the plan as one unified diff per target that changes, which
// is what `pairtime install --dry-run` prints. A target that isn't there
// on one side is /dev/null on that side.
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
		b.WriteString(udiff.Unified(from, to, c.Before.text(), c.After.text()))
	}
	return b.String()
}

// PlanInstall plans items' edits. Each target is read once and every edit
// for it applied in order, so two items can share a file. A target that
// already holds what the items want isn't in the plan: planning again
// right after Apply gives an empty plan. If the manifest doesn't know such
// a target, Apply still records it (see adopt).
func (in *Installer) PlanInstall(items []Item) (*Plan, error) {
	m, oldSum, err := in.loadManifest()
	if err != nil {
		return nil, err
	}
	type group struct {
		target Target
		edits  []Edit
		recs   []editRecord
	}
	var groups []*group
	byTarget := map[Target]*group{}
	ids := map[string]bool{}
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
			g := byTarget[t]
			if g == nil {
				g = &group{target: t}
				byTarget[t] = g
				groups = append(groups, g)
			}
			g.edits = append(g.edits, e)
			g.recs = append(g.recs, editRecord{Item: it.ID, Kind: e.Kind()})
		}
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
		ch := Change{Target: g.target, Items: itemIDs(g.recs), Before: cur, After: next}
		rec := p.manifest.find(g.target)
		switch {
		case rec == nil:
			// The manifest doesn't know the target: what it holds now is
			// what uninstall puts back, without any pairtime hooks an
			// earlier install left there, which would run a binary that
			// uninstall deletes.
			if !g.target.isRegistry() {
				if ch.mkdirs, err = missingDirs(g.target.Path); err != nil {
					return nil, err
				}
			}
			base, err := undoAll(cur, cur, g.recs)
			if err != nil {
				return nil, fmt.Errorf("%s %w", g.target, err)
			}
			p.manifest.Targets = append(p.manifest.Targets, record{Target: g.target, Dirs: ch.mkdirs})
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
		rec.Written = next.digest()
		for _, r := range g.recs {
			if !slices.Contains(rec.Edits, r) {
				rec.Edits = append(rec.Edits, r)
			}
		}
		p.Changes = append(p.Changes, ch)
	}
	return p, nil
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
	return nil
}

// rebase makes base what uninstall puts back in rec's target.
func (p *Plan) rebase(rec *record, base Content) {
	if rec.Base.Backup != "" {
		p.drops = append(p.drops, rec.Base.Backup)
	}
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
func (in *Installer) PlanUninstall() (*Plan, error) {
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
		if rec.Base.Backup != "" {
			p.drops = append(p.drops, rec.Base.Backup)
		}
	}
	slices.Reverse(kept)
	p.manifest.Targets = kept
	return p, nil
}

// planUndo works out how uninstall puts rec's target back.
func (in *Installer) planUndo(rec record) (Change, error) {
	cur, err := in.read(rec.Target)
	if err != nil {
		return Change{}, err
	}
	ch := Change{Target: rec.Target, Items: itemIDs(rec.Edits), Before: cur}
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
	if !ch.After.Same(cur) && cur.Exists && ch.After.Exists && !rec.Target.isRegistry() && cur.Mode&0o200 == 0 {
		return Change{}, fmt.Errorf("%s is read-only now, so pairtime leaves it alone; make it writable and run uninstall again", rec.Target)
	}
	return ch, nil
}

// undoAll undoes edits on cur, the last one first.
func undoAll(cur, base Content, edits []editRecord) (Content, error) {
	for i := len(edits) - 1; i >= 0; i-- {
		undo, ok := undoers[edits[i].Kind]
		if !ok {
			return Content{}, fmt.Errorf("pairtime doesn't know how to undo an edit of kind %q", edits[i].Kind)
		}
		var err error
		if cur, err = undo(cur, base); err != nil {
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
// Apply. Nothing was written; plan again to see what changed.
var ErrStale = errors.New("changed since the plan was made; nothing was written, so plan again")

// Apply makes p's changes. It first checks that every target still holds
// what the plan read, and writes nothing if one doesn't (ErrStale).
//
// Install writes the copies of what each target held, then the manifest,
// then the targets, so that a crash at any point leaves a manifest that
// covers everything pairtime may have written. Uninstall writes the
// targets first and the manifest last, so that a crash leaves the targets
// not yet undone in the manifest. Running either again finishes the job.
func (p *Plan) Apply() error {
	in := p.in
	if _, oldSum, err := in.loadManifest(); err != nil {
		return err
	} else if oldSum != p.oldSum {
		return fmt.Errorf("%s %w", in.manifestPath(), ErrStale)
	}
	for _, c := range p.Changes {
		cur, err := in.read(c.Target)
		if err != nil {
			return err
		}
		if !cur.Same(c.Before) {
			return fmt.Errorf("%s %w", c.Target, ErrStale)
		}
	}
	data, err := encodeManifest(p.manifest)
	if err != nil {
		return err
	}
	if !p.uninstall {
		if err := p.saveBackups(); err != nil {
			return err
		}
		if err := in.writeManifest(data, p.oldSum); err != nil {
			return err
		}
	}
	for _, c := range p.Changes {
		if err := makeDirs(c.mkdirs); err != nil {
			return err
		}
		if err := in.write(c.Target, c.After); err != nil {
			return fmt.Errorf("writing %s: %w", c.Target, err)
		}
	}
	if p.uninstall {
		for _, d := range p.rmdirs {
			os.Remove(d) // only an empty directory goes, which is the point
		}
		if err := in.writeManifest(data, p.oldSum); err != nil {
			return err
		}
	}
	for _, name := range p.drops {
		if err := os.Remove(filepath.Join(in.backupsDir(), name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if data == nil {
		// Nothing is recorded any more: take away pairtime's install
		// directory, if nothing else is in it.
		os.Remove(in.backupsDir())
		os.Remove(in.Dir)
	}
	return nil
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
