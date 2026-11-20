// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
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
	// Batches and Buckets count the batch roots and minute buckets it
	// carries, and Labels says whether it carries the labels.
	Batches, Buckets int
	Labels           bool
	// More is set when more requests wait behind this one.
	More bool
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

	// maxRoots and maxBuckets are the Uploader's, for tests.
	maxRoots, maxBuckets int
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
	if !buildForm(build) {
		build = "unknown"
	}
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
		foreign := st.Server != ""
		st = saved{Chain: rows.device, Harnesses: st.Harnesses}
		if foreign {
			rows.forgetReceipts() // they are the old server's
			st.Foreign = rows.last()
		}
	}
	st.prepare(rows)
	if p, err := readOutbox(o.DataDir, st); err == nil && p != nil {
		return previewOf(p.plan, true), nil
	}
	if st.From == 0 {
		st.From = firstLinked(rows, c.LinkedAt)
	}
	rows.trim(max(st.Next, 1))
	in := planInput{
		rows:       rows,
		st:         st,
		bucketDir:  o.BucketDir,
		lag:        o.Params.Int(params.WITNESS_LAG_S) * 1000,
		maxRoots:   o.maxRoots,
		maxBuckets: o.maxBuckets,
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
	return previewOf(p, false), nil
}

// previewOf describes plan p.
func previewOf(p plan, retry bool) Preview {
	if p.body.Empty() {
		return Preview{}
	}
	return Preview{
		JSON: p.js, Retry: retry,
		Batches: len(p.body.Batches), Buckets: len(p.body.Buckets), Labels: p.body.Labels != nil, More: p.more,
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
