// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/some-du6e/pairtime/pkg/params"
	"github.com/some-du6e/pairtime/pkg/score"
)

// Preview is the next upload as the agent would send it, for `pairtime
// privacy show` (DESIGN 8.8).
type Preview struct {
	// JSON is the request's body before gzip, byte for byte, and nil when
	// nothing is waiting to go.
	JSON []byte
	// Retry is set when the request failed and waits to be sent again as
	// it is.
	Retry bool
	// Batches, Buckets and Commits count the batch roots, minute buckets
	// and commit records it carries, and Labels says whether it carries the
	// labels.
	Batches, Buckets, Commits int
	Labels                    bool
	// More is set when more requests wait behind this one.
	More bool
	// Stopped is the first batch sealed in local-only mode after the
	// chain's last link: no batch root from it on goes until the next link,
	// nor the minutes it holds. 0 for none. NoMode is set when an older
	// pairtime version sealed it, which recorded no mode, so it counts as
	// local-only (ledger.Row.NoMode). Cause says why it was sealed
	// local-only, as its seal frame records it (StopCause), "" when it
	// doesn't say, and StoppedAt when it was sealed, zero then.
	Stopped   uint64
	NoMode    bool
	Cause     string
	StoppedAt time.Time
	// Fork is the batch the server's record of this computer's chain parts
	// from its ledger at, after which nothing more goes under its device ID
	// (#150 R5). 0 for none.
	Fork uint64
	// Mismatch is set once the server said the device ID is another
	// account's, after which nothing more goes under it (#150 R5).
	Mismatch bool
	// Names is the project names request that goes before the upload, nil
	// when none waits (project-names-v1; NextNames).
	Names *NamesPreview
}

// NamesPreview is the next project names request, as the uploader would
// send it: the names config.toml turns on that the server's last answer
// doesn't show, and the ones it shows that config.toml has off.
type NamesPreview struct {
	// JSON is the request's body before gzip, byte for byte, labels and
	// all.
	JSON []byte
	// Set are the names it shows, each project key's label, and Delete the
	// keys whose names it stops showing, or which the owner hid on the
	// dashboard and pairtime turned off here too.
	Set    map[string]string
	Delete []string
	// Codes are the short codes the server's last answer gave, by key, for
	// the words around it.
	Codes map[string]string
}

// PreviewOptions say where an installation keeps its files, and what the
// agent makes the labels from.
type PreviewOptions struct {
	// LedgerDir, BucketDir and DataDir are home.Dirs' Ledger(), Buckets()
	// and Data, and StateFile its StateFile().
	LedgerDir, BucketDir, DataDir, StateFile string
	// Params is the table the agent scores with, and Device what it knows
	// of the machine.
	Params *params.Table
	Device score.Device
	// Version is the client build, version.Get().Version when "".
	Version string
	// Credentials are the sign-in the upload would go with.
	Credentials Credentials
	// Now is the time to preview at, time.Now() when zero.
	Now time.Time
	// Names are the project names config.toml turns on, each project key's
	// label, as the agent's uploader reads them, and OwnKeys the keys of the
	// projects this computer has seen (Options.OwnKeys), every key when
	// nil. With no names, no names request shows.
	Names   map[string]string
	OwnKeys map[string]bool

	// maxRoots, maxBuckets and maxCommits are the Uploader's, for tests.
	maxRoots, maxBuckets, maxCommits int
}

// Next returns the next upload: the request waiting for its retry, or the
// plan the agent's uploader makes from the same ledger, bucket store and
// state.json, with the same code. It only reads those files, so it can
// run beside an agent that has them open. What it returns is what goes,
// byte for byte, unless the agent records or scores something new first,
// or learns something new from the server when it starts.
func Next(o PreviewOptions) (Preview, error) {
	if o.Params == nil {
		return Preview{}, errors.New("uploader: Next needs the parameter table")
	}
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	build := o.Version
	if build == "" {
		build = defaultBuild()
	}
	build = clientBuild(build)
	st, started, err := readState(o.StateFile, now)
	if err != nil {
		return Preview{}, err
	}
	rows, err := loadRows(o.LedgerDir, 1)
	if err != nil {
		return Preview{}, err
	}
	c := o.Credentials
	switch {
	case st.Chain != rows.device:
		st = saved{Chain: rows.device, Harnesses: st.Harnesses}
	case st.Server != c.Server || st.Device != c.DeviceID:
		// As the agent's load does, the runs of batches sealed in local-only
		// mode stay: they are the ledger's.
		foreign := st.Server != ""
		st = saved{Chain: rows.device, Harnesses: st.Harnesses, LocalFrom: st.LocalFrom, Gaps: st.Gaps, Modes: st.Modes}
		if foreign {
			rows.forgetReceipts() // they are the old server's
			st.Foreign = rows.last()
		}
	}
	st.prepare(rows)
	// As the agent's load does, the modes of the batches sealed since the
	// place last took one go in from the rows, and a request an older
	// version left waiting is planned again.
	older := st.Modes == 0 && st.Outbox != nil
	st.foldModes(rows)
	if st.Fork != 0 || st.Mismatch {
		// As the agent does, nothing more goes after a fork or a
		// device_mismatch.
		return Preview{Fork: st.Fork, Mismatch: st.Mismatch}, nil
	}
	// As the agent's load does: the link first, then the request waiting
	// for its retry, which a new link drops.
	fresh := relinked(st, c)
	relink(&st, rows, c)
	stop := st.localStop(rows.last())
	noMode := stoppedNoMode(rows, stop)
	cause, stoppedAt := "", time.Time{}
	if r, ok := rows.row(stop); ok && stop > 0 && !noMode && r.Why != nil {
		cause, stoppedAt = StopCause(r.Why), r.SealedAt
	}
	names, err := NextNames(o.DataDir, c, o.Names, o.OwnKeys, st.Linking)
	if err != nil {
		return Preview{}, err
	}
	if p, err := readOutbox(o.DataDir, st); err == nil && p != nil && !fresh && !older {
		pv := previewOf(p.plan, true)
		pv.Stopped, pv.NoMode, pv.Cause, pv.StoppedAt, pv.Names = stop, noMode, cause, stoppedAt, names
		return pv, nil
	}
	if fresh {
		st.Outbox = nil
	}
	rows.trim(max(st.Next, 1))
	in := planInput{
		rows:       rows,
		st:         st,
		bucketDir:  o.BucketDir,
		commitDir:  commitDir(o.DataDir),
		lag:        o.Params.Int(params.WITNESS_LAG_S) * 1000,
		maxRoots:   o.maxRoots,
		maxBuckets: o.maxBuckets,
		maxCommits: o.maxCommits,
		notAfter:   now.Add(time.Duration(st.Offset) * time.Millisecond).Add(futureMargin).UnixMilli(),
	}
	l := currentLabels(build, o.Params, o.Device, st.Harnesses)
	due, err := labelsDue(l, st, started, now)
	if err != nil {
		return Preview{}, err
	}
	if due {
		in.labels = &l
	}
	p, err := makePlan(in)
	if err != nil {
		return Preview{}, err
	}
	pv := previewOf(p, false)
	pv.Stopped, pv.NoMode, pv.Cause, pv.StoppedAt, pv.Names = stop, noMode, cause, stoppedAt, names
	return pv, nil
}

// NextNames returns the project names request the uploader sends next for
// the sign-in c, before its next upload, from the same project-names.json
// and the same code, and without changing any file: nil when nothing waits
// to be shown or stopped, as while the link's first batch isn't stored
// (linking), or when the file names another sign-in, whose names all go off
// when the agent next loads it (loadNames). wanted are the names
// config.toml turns on. A read, {}, which carries no name, never shows. The
// agent reads first whenever it doesn't know what the server holds, and a
// hide that read finds changes what goes, so this is what goes unless the
// server's next answer changes it.
func NextNames(dataDir string, c Credentials, wanted map[string]string, own map[string]bool, linking bool) (*NamesPreview, error) {
	if linking || (len(wanted) == 0 && !hasNames(dataDir)) {
		return nil, nil
	}
	// A file of another sign-in, or one that doesn't read, which counts as
	// another's, sends nothing: the agent turns its names off when it
	// loads it.
	n, err := ReadNames(dataDir)
	if (err != nil && !errors.Is(err, fs.ErrNotExist)) || (n.Server != "" && (n.Server != c.Server || n.Device != c.DeviceID)) {
		return nil, nil
	}
	if n.Server == "" {
		n = Names{Hidden: n.Hidden}
	}
	wanted = maps.Clone(wanted)
	var acks []string
	for _, key := range n.Hides {
		if !n.Hidden[key].Pending {
			acks = append(acks, key)
		}
	}
	for key, h := range n.Hidden {
		// As hidePending does, without the config.toml edit: a name pairtime
		// couldn't turn off stays out while config.toml has it on, under any
		// label.
		if _, on := wanted[key]; h.Pending && on {
			delete(wanted, key)
		}
	}
	set, del := namesDiff(wanted, n.Confirmed, acks)
	if own != nil {
		set = slices.DeleteFunc(set, func(it nameItem) bool {
			_, coded := n.Codes[it.Project]
			return !coded && !own[it.Project]
		})
	}
	if len(set)+len(del) == 0 {
		return nil, nil
	}
	body := takeNames(set, del)
	js, err := encodeNames(body)
	if err != nil {
		return nil, err
	}
	pv := &NamesPreview{JSON: js, Set: make(map[string]string, len(body.Set)), Delete: body.Delete, Codes: n.Codes}
	for _, it := range body.Set {
		pv.Set[it.Project] = it.Label
	}
	return pv, nil
}

// hasNames reports whether dataDir has a project-names.json, whose
// confirmed names a request may delete with none turned on.
func hasNames(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, NamesFileName))
	return err == nil
}

// previewOf describes plan p.
func previewOf(p plan, retry bool) Preview {
	if p.body.Empty() {
		return Preview{}
	}
	return Preview{
		JSON: p.js, Retry: retry,
		Batches: len(p.body.Batches), Buckets: len(p.body.Buckets), Commits: len(p.body.Commits), Labels: p.body.Labels != nil,
		More: p.more,
	}
}

// readState reads the uploader's place from state.json without opening
// it as the agent does, which would set a damaged file aside under an
// agent that has it open. It also returns when the running agent started:
// its first request carries the labels. With no agent running, the next
// one to start sends them, so that is now.
func readState(name string, now time.Time) (saved, time.Time, error) {
	data, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return saved{}, now, nil
	}
	if err != nil {
		return saved{}, time.Time{}, err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return saved{}, now, nil // the agent starts afresh from such a file
	}
	var st saved
	if raw, ok := keys[StateKey]; ok {
		if err := json.Unmarshal(raw, &st); err != nil {
			st = saved{}
		}
	}
	var ag struct {
		Started time.Time `json:"started"`
		Stopped time.Time `json:"stopped"`
	}
	started := now
	if raw, ok := keys["agent"]; ok && json.Unmarshal(raw, &ag) == nil && !ag.Started.IsZero() && ag.Stopped.Before(ag.Started) {
		started = ag.Started
	}
	return st, started, nil
}
