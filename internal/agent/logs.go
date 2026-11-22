// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"slices"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/tailers"
	"github.com/some-du6e/pairtime/internal/install"
	"github.com/some-du6e/pairtime/pkg/event"
)

// The tailers read the harnesses' log files, such as Claude Code's
// transcripts, and hand each new line to the adapter host, whose worker
// has the line's adapter parse it and writes its records to the ledger, as
// it does a hook call's (DESIGN 3.3, 3.4; WP-AD3). The worker takes lines
// after the hook calls it has, so a session's hooks reach its adapter
// before its transcript, which writes less for a session a hook reported
// (claudecode's logparse.go):
//
//   - The tailers start once the inbox's first drain handed its calls to
//     the host, and the worker gave them the adapters' log roots, which it
//     does once it can parse: after the keystore and the ledger opened.
//   - A line whose folder's project is still being worked out, or whose
//     session has hook calls that wait for theirs, waits too, with every
//     line after it in its file (D-park-unresolved): the tailers offer it
//     again once a Track ends or a parked call goes.
//   - Each line's records go to the ledger before the tailers' cursor of
//     the file moves past it, and state.json gets the cursors with the
//     adapters' memory in one Save, so the two agree after a restart.
//
// A log record's event_id is the same each time its line is read
// (logEventID), so a line read again after a crash, before state.json said
// it was, makes records pkg/score takes for copies of the ones already in
// the ledger.

// logReq is a batch of log lines the tailers hand the worker, and where the
// worker answers how many it took.
type logReq struct {
	batch tailers.Batch
	took  chan int
}

// forgetReq is a log file the tailers found gone, whose adapter forgets it.
type forgetReq struct {
	harness event.Harness
	file    string
}

// startTailers makes the tailers and starts them, and returns the step
// that stops them, which runs before the adapter host stops: it answers
// the lines that wait for the worker with none taken, so the tailers' own
// stop never waits for a worker that can't parse.
func (a *agent) startTailers(opts Options) func() {
	h := a.adaptersHost()
	env := opts.Env
	if env == nil {
		e, err := install.DefaultEnv("")
		if err != nil {
			a.setProblem("tailers", fmt.Sprintf("no harness's logs are read, because the home folder isn't known: %v", err))
			return func() {}
		}
		env = &e
	}
	var st tailers.Store
	if a.state != nil {
		st = a.state
	}
	t := tailers.New(tailers.Options{Store: st, Sched: a.sched, Deliver: h.deliverLogs, Forget: h.forgetLog,
		Ready: h.logReady, Log: a.log.With("component", "tailers"), Lookback: opts.Lookback, Progress: h.setLookback,
		LookbackUntil: h.lookbackUntil})
	h.mu.Lock()
	h.logEnv = *env
	h.mu.Unlock()
	h.tail.Store(t)
	stop := t.Start()
	h.poke() // the worker gives the tailers their roots once it can parse
	return func() {
		h.dropLogs()
		stop()
	}
}

// deliverLogs hands a batch of log lines to the worker and waits for its
// answer: how many it took (tailers.Options.Deliver). It runs on the
// tailers' goroutine.
func (h *adapterHost) deliverLogs(b tailers.Batch) int {
	req := &logReq{batch: b, took: make(chan int, 1)}
	h.mu.Lock()
	if h.logsDone || h.closed {
		h.mu.Unlock()
		return 0
	}
	h.logReqs = append(h.logReqs, req)
	h.mu.Unlock()
	h.poke()
	return <-req.took
}

// dropLogs answers every batch that waits for the worker with none taken,
// and every later one at once: the tailers are stopping.
func (h *adapterHost) dropLogs() {
	h.mu.Lock()
	h.logsDone = true
	reqs := h.logReqs
	h.logReqs = nil
	h.mu.Unlock()
	for _, r := range reqs {
		r.took <- 0
	}
}

// forgetLog has the worker tell a gone file's adapter to forget it
// (tailers.Options.Forget). It runs on the tailers' goroutine.
func (h *adapterHost) forgetLog(harness event.Harness, file string) {
	h.mu.Lock()
	h.forgets = append(h.forgets, forgetReq{harness: harness, file: file})
	h.mu.Unlock()
	h.poke()
}

// setLookback keeps how far lookback has read, for the status
// (tailers.Options.Progress).
func (h *adapterHost) setLookback(p tailers.Progress) {
	h.mu.Lock()
	h.lookback = p
	h.mu.Unlock()
}

// lookbackUntil is when lookback's records end, the ledger's start, and the
// zero time before the ledger is open (tailers.Options.LookbackUntil).
func (h *adapterHost) lookbackUntil() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ledgerBegan == 0 {
		return time.Time{}
	}
	return time.UnixMilli(h.ledgerBegan)
}

// lookbackWindow keeps the records of a line of a file's history that fall
// in lookback's window: from its start, from, which is LookbackWindow
// before the tailers took the file up, to when the ledger began. From then
// on the agent was running, and its hooks wrote the session's records, if
// any came; a session no hook reported then is the one lookback can't fill
// in, and only an agent upgraded from a release without lookback has one.
func (h *adapterHost) lookbackWindow(recs []adapters.Record, from int64) []adapters.Record {
	h.mu.Lock()
	until := h.ledgerBegan
	h.mu.Unlock()
	return slices.DeleteFunc(recs, func(r adapters.Record) bool { return r.Event.T < from || r.Event.T >= until })
}

// drained reports whether the inbox's first drain has handed its calls to
// the host.
func (h *adapterHost) drained() bool {
	select {
	case <-h.a.drained:
		return true
	default:
		return false
	}
}

// takeLogs has the adapters parse the log lines the tailers handed over,
// in order, once the hook calls before them are parsed or wait in park,
// and answers each batch. The first time, it gives the tailers the log
// roots of the adapters that read logs and lets them start. The worker
// runs it in a step that can write to the ledger; a step that can't, while
// the agent has no keys or no ledger writer, or the held calls replay,
// answers the batches with none taken instead (refuseLogs).
func (h *adapterHost) takeLogs() {
	t := h.tail.Load()
	if t == nil {
		return
	}
	if h.logRefused {
		// The tailers offer what they were refused again.
		h.logRefused = false
		t.Poke()
	}
	if !h.rooted {
		if !h.drained() {
			return // the drain pokes the worker when it's done
		}
		var roots []tailers.Root
		h.mu.Lock()
		env := h.logEnv
		h.mu.Unlock()
		for _, name := range adapters.Default.Harnesses() {
			if lr, ok := h.adapter(name).(adapters.LogReader); ok {
				for _, r := range lr.LogRoots(env) {
					roots = append(roots, tailers.Root{Harness: name, LogRoot: r})
				}
			}
		}
		t.AddRoots(roots)
		h.rooted = true
		close(h.logReady)
	}
	h.mu.Lock()
	reqs, forgets := h.logReqs, h.forgets
	h.logReqs, h.forgets = nil, nil
	parked := h.parkedN
	h.mu.Unlock()
	for _, f := range forgets {
		if lr, ok := h.adapter(f.harness).(adapters.LogReader); ok {
			lr.ForgetLog(f.file)
		}
	}
	for _, r := range reqs {
		r.took <- h.takeBatch(t, r.batch)
	}
	// Lines that waited go again once something they waited for moved: a
	// Track ended, or parked calls went.
	if h.logWait && (h.tracksEnded() != h.logTracks || parked < h.logParked) {
		h.logWait = false
		t.Poke()
	}
}

// refuseLogs answers every batch that waits with none taken: in a step
// that can't write to the ledger, so the tailers don't wait for it, and at
// the last step. The next step that can has the tailers offer them again.
func (h *adapterHost) refuseLogs() {
	h.mu.Lock()
	reqs := h.logReqs
	h.logReqs = nil
	h.mu.Unlock()
	for _, r := range reqs {
		r.took <- 0
	}
	if len(reqs) > 0 {
		h.logRefused = true
	}
}

// takeBatch parses the lines of one file's batch in order, and returns how
// many it took: up to the first that has to wait.
func (h *adapterHost) takeBatch(t *tailers.Tailer, b tailers.Batch) int {
	for i, line := range b {
		// The Tracks that ended before takeLine looked, so one that ends
		// after it looked counts as a change, which pokes the tailers.
		tracks := h.tracksEnded()
		if !h.takeLine(t, line) {
			if !h.logWait {
				h.mu.Lock()
				h.logParked = h.parkedN
				h.mu.Unlock()
				h.logWait, h.logTracks = true, tracks
			}
			return i
		}
	}
	return len(b)
}

// takeLine has line's adapter parse it and writes its records, and reports
// whether it took the line. It doesn't when the line's folder's project is
// still being worked out, when hook calls of its session wait for theirs,
// or when the ledger writer has stopped.
func (h *adapterHost) takeLine(t *tailers.Tailer, line tailers.Line) bool {
	ll := line.Log
	ad := h.adapter(line.Harness)
	lr, ok := ad.(adapters.LogReader)
	if !ok {
		// No adapter of this build reads the harness's logs.
		t.Advance(ll.File, ll.End, line.Hash, ll.End)
		return true
	}
	if h.writerDown() {
		return false
	}
	st := h.statsOf(line.Harness, true)
	if !ll.Idle && !ll.TooLong && ll.Entry == nil && ll.DecodeErr == nil {
		ll.Entry, ll.DecodeErr = lr.DecodeLog(line.Bytes)
	}
	now := wallNow()
	mono, boot := ledger.RunClock(now)
	raw := adapters.RawRecord{Source: adapters.SourceLog, Payload: line.Bytes, Log: &ll,
		Arrival: adapters.Arrival{Via: event.ViaLog, T: now.UnixMilli(), Mono: mono, BootID: boot}}
	if _, ok := lr.LogResume(ll.File); !ok && ll.Offset > 0 && !ll.Idle && !line.Fresh {
		// The adapter has no memory of a file the tailers resume in its
		// middle, as after a restore made it forget, or it forgot a file
		// it read long ago: reading it from its start again, the lines it
		// had as replays, gives the adapter what it knew of it. A fresh
		// line, after a history lookback gave up on, starts it afresh.
		t.Rewind(ll.File)
		return false
	}
	dir, session := lr.LogPlace(raw)
	if session != "" && h.parkedBy[session] > 0 || h.waits(dir) {
		return false
	}
	h.ignored = false
	recs, err := ad.Parse(raw)
	if err != nil {
		h.count(func() {
			st.LineFailures++
			st.LastFailure = clip(err.Error())
		})
	}
	if h.ignored {
		// A folder out of pairtime (DESIGN 8.8): its lines make no record.
		recs = nil
		h.count(func() { st.Ignored++ })
	}
	if line.Lookback > 0 {
		recs = h.lookbackWindow(recs, line.Lookback)
	}
	if !ll.Idle && !ll.Replay {
		h.count(func() { st.Lines++ })
	}
	due := false
	for i, r := range recs {
		key := r.LogKey
		if key == "" {
			key = fmt.Sprintf("%d.%d", ll.Offset, i)
		}
		r.Event.EventID = h.logEventID(r.Event.T, ll.File, key)
		// A history's records need no seal and no save of their own: the
		// timed seal and the minute's save cover them, and a crash before
		// those reads the lines again, to the same event_ids.
		r.Late = line.Lookback > 0
		if !h.write(st, r) && h.writerDown() {
			return false
		}
		switch r.Event.Kind() {
		case event.KindSessionStart, event.KindPromptSubmit, event.KindAgentStop:
			due = due || !r.Late
		}
	}
	resume, ok := lr.LogResume(ll.File)
	if !ok {
		resume = ll.End
	}
	t.Advance(ll.File, ll.End, line.Hash, resume)
	if due {
		// As for a hook call's: the memory these records go with goes to
		// state.json with them (adaptersSaveEvery).
		h.mu.Lock()
		h.saveDue = true
		h.mu.Unlock()
	}
	return true
}

// hint has the tailers read the log file a via hook call names soon, and
// watch its root, which may be one the adapter's LogRoots didn't know
// (adapters.LogReader.LogHint). Only a call the agent's walk traced to the
// harness says which file its session writes: a spooled one is any
// process's word.
func (h *adapterHost) hint(c hostCall) {
	t := h.tail.Load()
	if t == nil || c.raw.Arrival.Via != event.ViaHook {
		return
	}
	if lr, ok := c.ad.(adapters.LogReader); ok {
		if p, root, ok := lr.LogHint(c.raw); ok {
			t.Hint(c.ad.Name(), p, root)
		}
	}
}

// logEventID is a log record's event_id: a UUIDv7 with the record's time,
// whose other bits are an HMAC, under K_session and a domain of its own,
// of the file and the record's LogKey, which names the line its content
// came from and which of that line's records it is. A line read again
// makes the same ids, so pkg/score, which drops a record whose event_id it
// has, counts it once.
func (h *adapterHost) logEventID(t int64, file, key string) string {
	m := hmac.New(sha256.New, h.a.sessionSecret())
	m.Write([]byte("pairtime/v1/log-event"))
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(file)))
	m.Write(n[:])
	m.Write([]byte(file))
	m.Write([]byte(key))
	return eventIDFrom(t, m.Sum(nil))
}

// eventIDFrom writes a UUIDv7 (RFC 9562) whose timestamp is ms, Unix
// milliseconds, and whose other bits come from bits, as event.NewEventID
// writes one with random bits.
func eventIDFrom(ms int64, bits []byte) string {
	var b [16]byte
	for i := 0; i < 6; i++ {
		b[i] = byte(uint64(ms) >> (40 - 8*i))
	}
	copy(b[6:], bits[:10])
	b[6] = b[6]&0x0f | 0x70 // version 7
	b[8] = b[8]&0x3f | 0x80 // variant 10
	var s [36]byte
	hex.Encode(s[0:8], b[0:4])
	s[8] = '-'
	hex.Encode(s[9:13], b[4:6])
	s[13] = '-'
	hex.Encode(s[14:18], b[6:8])
	s[18] = '-'
	hex.Encode(s[19:23], b[8:10])
	s[23] = '-'
	hex.Encode(s[24:36], b[10:16])
	return string(s[:])
}
