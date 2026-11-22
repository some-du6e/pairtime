// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/procs"
	"github.com/some-du6e/pairtime/internal/agent/tailers"
	"github.com/some-du6e/pairtime/internal/install"
	"github.com/some-du6e/pairtime/internal/platform/clock"
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
		Ready: h.logReady, Log: a.log.With("component", "tailers")})
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

// tieFresh is how recently a log line must have been written for the
// session it is from to be tied to a process that runs now (tieLive): a
// line read later, as one the tailers found at a rescan or after the agent
// was down, may be from a session whose process has ended. It is twice
// the hot files' poll. tieRetry is how often a session no hook reports is
// tied again at most, each try a read of the process table.
const (
	tieFresh = 10 * time.Second
	tieRetry = 30 * time.Second
)

// maxTieTried bounds the sessions tieLive remembers it tried.
const maxTieTried = 256

// tieLive ties the session of harness h, id sid, that a line written at at
// is from, in the log folder folder, to the process running it, when no
// hook reports the session, by the memory of lr, the harness's log reader
// (LogHooked), or this run's hook ties, none of its lines is out of
// pairtime since its last that counts (hookTies.ignoring, untieIgnored),
// and the line was written in the last tieFresh (procs.Scanner.Tie): focus
// then finds the session in the window that hosts it, which SM 2.2 rule 6
// reads, and the session.start its log reader writes, once its hooks had
// their grace, is the scan's (tiedStart). Each line keeps the session's
// touch fresh (procs.Scanner.Touch); a session is weighed again at most
// every tieRetry, so a session whose process ended gets the one it resumed
// in, though a try that only waited for a read of the table weighs nothing
// and holds the next line back from none.
//
// For a session it ties that its log reader wrote no session.start for,
// nor holds one (LogStarted), as with a transcript that states no
// entrypoint, it returns the scan's session.start, for the line's records
// recs to follow (scanStart), so rule 4 reads the walk.
func (h *adapterHost) tieLive(lr adapters.LogReader, harness event.Harness, sid, folder string, at int64,
	recs []adapters.Record) (adapters.Record, bool) {
	p := h.a.procs
	now := wallNow()
	if p == nil || at == 0 || now.UnixMilli()-at > tieFresh.Milliseconds() {
		return adapters.Record{}, false
	}
	key := h.sessionKey(sid)
	if key == "" || h.a.hooked.has(key) || h.a.hooked.ignoring(key) || lr.LogHooked(sid) {
		return adapters.Record{}, false
	}
	p.Touch(key, time.UnixMilli(at), folder)
	h.mu.Lock()
	recent := now.Sub(h.tieTried[key]) < tieRetry
	h.mu.Unlock()
	if recent {
		return adapters.Record{}, false
	}
	if _, tied := p.TiedTo(key); tied {
		return adapters.Record{}, false
	}
	sess, tied, judged := p.Tie(harness, key, time.UnixMilli(at), lr.LogCleared(sid))
	if judged {
		h.mu.Lock()
		if h.tieTried == nil || len(h.tieTried) >= maxTieTried {
			h.tieTried = make(map[string]time.Time)
		}
		h.tieTried[key] = now
		h.mu.Unlock()
	}
	if !tied {
		return adapters.Record{}, false
	}
	h.a.refreshFocus()
	if lr.LogStarted(sid) {
		return adapters.Record{}, false
	}
	return h.scanStart(sess, harness, key, recs)
}

// scanStart returns the process scan's session.start for session key,
// which sess tied to its process and its log reader has none of: dated a
// millisecond before the session's first line read live
// (procs.Scanner.FirstTouch), with the harness version and project of the
// session's first record in recs. Its later starts, at a change of token,
// are the scan's too (tiedStart).
func (h *adapterHost) scanStart(sess procs.Session, harness event.Harness, key string, recs []adapters.Record) (adapters.Record, bool) {
	first, ok := h.a.procs.FirstTouch(key)
	if !ok {
		return adapters.Record{}, false
	}
	t := first.UnixMilli() - 1
	now := wallNow()
	mono, boot := ledger.RunClock(now)
	env := event.Envelope{T: t, Mono: max(mono-(now.UnixMilli()-t), 0), BootID: boot, Source: event.SourceOS,
		Via: event.ViaOS, Harness: harness, SessionKey: key}
	for _, r := range recs {
		if r.Event.SessionKey == key {
			env.HarnessVersion, env.ProjectKey = r.Event.HarnessVersion, r.Event.ProjectKey
			break
		}
	}
	h.mu.Lock()
	if _, had := h.firstStarts[key]; had {
		// Its first start is written already, as when it was tied before.
		h.mu.Unlock()
		return adapters.Record{}, false
	}
	if h.firstStarts == nil || len(h.firstStarts) >= maxFirstStarts {
		h.firstStarts = make(map[string]firstStart)
	}
	h.firstStarts[key] = firstStart{tied: true, walk: sess.Event}
	h.mu.Unlock()
	st := sess.Event
	return adapters.Record{Event: event.Event{Envelope: env, Data: &st}, LogKey: "tie-start"}, true
}

// untieIgnored keeps session sid out of focus from a line of it in a
// folder out of pairtime on (DESIGN 8.8), as a hook call there does
// (hookTies.ignore): it undoes the session's procs tie
// (procs.Scanner.Untie), and tieLive ties it again only after a line of it
// that counts.
func (h *adapterHost) untieIgnored(sid string) {
	key := h.sessionKey(sid)
	if key == "" {
		return
	}
	// Its next line that counts may tie it again at once.
	h.mu.Lock()
	delete(h.tieTried, key)
	h.mu.Unlock()
	news := h.a.hooked.ignore(key)
	if p := h.a.procs; p != nil && p.Untie(key) {
		news = true
	}
	if news {
		h.a.refreshFocus()
	}
}

// sessionKey returns the key of session sid under this installation's
// K_session, as the adapters key it (adapters.Keys.Session), or "" while
// the agent has none.
func (h *adapterHost) sessionKey(sid string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessKeys == nil {
		secret := h.a.sessionSecret()
		if len(secret) != hookpath.SessionSecretLen {
			return ""
		}
		h.sessKeys = adapters.NewKeys(secret)
	}
	return h.sessKeys.Session(sid)
}

// tiedStart returns r, a session.start a log reader wrote for a session no
// hook reported, as the process scan's when tieLive tied the session to its
// process (procs.Scanner.TieOf, which keeps a tie a while after its process
// ended, for a short session's start): one record, with the walk's parent
// and TTY. A session's later starts, at a change of token, have the parent
// and TTY its first had, so its parent class never changes mid-session
// (DESIGN 7.3, CC9): a session whose first start was the transcript's,
// untied, keeps the transcript's for the rest.
func (h *adapterHost) tiedStart(r adapters.Record, start *event.SessionStart) adapters.Record {
	p := h.a.procs
	if p == nil || r.Event.Source != event.SourceLog {
		return r
	}
	key := r.Event.SessionKey
	h.mu.Lock()
	first, seen := h.firstStarts[key]
	h.mu.Unlock()
	walk := first.walk
	if !seen {
		sess, ok := p.TieOf(key)
		first = firstStart{tied: ok, walk: sess.Event}
		walk = sess.Event
		h.mu.Lock()
		if h.firstStarts == nil || len(h.firstStarts) >= maxFirstStarts {
			h.firstStarts = make(map[string]firstStart)
		}
		h.firstStarts[key] = first
		h.mu.Unlock()
	}
	if !first.tied {
		return r
	}
	st := tiedStartOf(walk, start, r.Token)
	r.Event.Source, r.Event.Via, r.Event.Data = event.SourceOS, event.ViaOS, &st
	return r
}

// firstStart is how a session's first session.start went: the scan's, with
// its process's walk, or the transcript's.
type firstStart struct {
	tied bool
	walk event.SessionStart
}

// maxFirstStarts bounds the sessions tiedStart remembers.
const maxFirstStarts = 256

// tiedStartOf is the session.start of a session tied to its process: the
// walk's parent and TTY, the log's start kind and permission mode, and the
// entrypoint E-cc-transcript-labels gives a walk joined to a token, whose
// labels only ever withhold credit. The walk's sdk or exec stands. sdk-cli
// is sdk under any host, and sdk-ts and sdk-py are unless an IDE or an
// agent desktop app hosts the session, which then decides. The log's
// cloud stands. Otherwise the walk's entrypoint stands.
func tiedStartOf(walk event.SessionStart, log *event.SessionStart, token string) event.SessionStart {
	st := walk
	st.StartKind, st.PermissionMode = log.StartKind, log.PermissionMode
	switch {
	case walk.Entrypoint == event.EntrypointSDK || walk.Entrypoint == event.EntrypointExec:
	case token == "sdk-cli":
		st.Entrypoint = event.EntrypointSDK
	case token == "sdk-ts" || token == "sdk-py":
		if walk.Entrypoint != event.EntrypointIDE && walk.Entrypoint != event.EntrypointDesktop {
			st.Entrypoint = event.EntrypointSDK
		}
	case log.Entrypoint == event.EntrypointCloud:
		st.Entrypoint = event.EntrypointCloud
	}
	return st
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
	now := time.Now() // with its monotonic reading, which this run's clock needs
	raw := adapters.RawRecord{Source: adapters.SourceLog, Payload: line.Bytes, Log: &ll, Arrival: logArrival(now, now)}
	if _, ok := lr.LogResume(ll.File); !ok && ll.Offset > 0 && !ll.Idle {
		// The adapter has no memory of a file the tailers resume in its
		// middle, as after a restore made it forget, or it forgot a file
		// it read long ago: reading it from its start again, the lines it
		// had as replays, gives the adapter what it knew of it.
		t.Rewind(ll.File)
		return false
	}
	dir, session, at := lr.LogPlace(raw)
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
	if h.ignored || lr.LogIgnored(ll.File) {
		// A folder out of pairtime (DESIGN 8.8): its lines make no record,
		// nor do the file's lines and idle markers that name no folder,
		// after one that named it.
		recs = nil
		if !ll.Idle && !ll.Replay {
			// A replay was counted when it was first read.
			h.count(func() { st.Ignored++ })
			if session != "" {
				h.untieIgnored(session)
			}
		}
	} else if session != "" && !ll.Replay {
		// A line that counts lets its session be tied again, as when it
		// moved to a folder that isn't out of pairtime.
		h.a.hooked.unignore(h.sessionKey(session))
		if start, ok := h.tieLive(lr, line.Harness, session, filepath.Dir(ll.File), at, recs); ok {
			recs = append([]adapters.Record{start}, recs...)
		}
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
		if start, ok := r.Event.Data.(*event.SessionStart); ok && !ll.Replay {
			r = h.tiedStart(r, start)
		}
		// A log line has no sender's stamp of its own (ledger.Input.Sent).
		if !h.write(st, r, clock.Stamp{}) && h.writerDown() {
			return false
		}
		switch r.Event.Kind() {
		case event.KindSessionStart, event.KindPromptSubmit, event.KindAgentStop:
			due = true
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

// logArrival is a log line's arrival, read at wall by the wall clock and at
// run by this run's clock (ledger.RunClock), as time.Now gives both: run
// must keep Go's monotonic reading, which neither a suspend nor a step of
// the wall clock moves, since the ledger holds a record of this run to the
// moment config.toml said local-only by that reading (ledger.moment). A
// record's mono comes from the arrival's, less how long before its read
// its line says it happened (claudecode's env), so a line the agent read
// after a switch back online, made before it, still counts as made before.
func logArrival(wall, run time.Time) adapters.Arrival {
	mono, boot := ledger.RunClock(run)
	return adapters.Arrival{Via: event.ViaLog, T: wall.UnixMilli(), Mono: mono, BootID: boot}
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
