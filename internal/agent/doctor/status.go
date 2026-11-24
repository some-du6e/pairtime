// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import "time"

// Status is what doctor reads of the running agent's status: its answer
// to ipc.AskStatus, which is GET /v1/status (docs/api/local.openapi.yaml,
// Status). It has only the fields doctor reads, and package agent's tests
// check that each one is the agent's field of the same name and type.
type Status struct {
	Version Version   `json:"version"`
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	UptimeS int64     `json:"uptime_s"`
	// Mode is local or online (DESIGN 4.2).
	Mode string `json:"mode"`
	// Wakeups counts the scheduler's timer firings since the start.
	Wakeups  uint64   `json:"wakeups"`
	IPC      IPC      `json:"ipc"`
	Signals  Signals  `json:"signals"`
	Adapters Adapters `json:"adapters"`
	Apps     Apps     `json:"apps"`
	// Ledger is what the ledger writer says of its chain, nil while the
	// ledger isn't open.
	Ledger    *Ledger   `json:"ledger,omitempty"`
	Footprint Footprint `json:"footprint"`
	// Problems is what isn't working, by where it comes from, in words for
	// the teen.
	Problems map[string]string `json:"problems,omitempty"`
}

// Ledger is what the ledger writer says of its chain.
type Ledger struct {
	// LateLocal counts the records the writer dropped over the chain's
	// life: made while config.toml said local-only, they reached it only
	// after it had sealed a batch online since, so they never upload
	// (D-late-local). HarnessCounts.LateLocal is each harness's part of it
	// since the agent started. A log line read again after a restart is
	// dropped, and counted, again.
	LateLocal uint64 `json:"late_local"`
}

// Footprint is what the agent costs the computer since it started, as the
// OS reports it for the agent's process.
type Footprint struct {
	// RSSBytes and PeakRSSBytes are the resident set now and at most, 0
	// where the OS doesn't say.
	RSSBytes     uint64  `json:"rss_bytes,omitempty"`
	PeakRSSBytes uint64  `json:"peak_rss_bytes,omitempty"`
	CPUS         float64 `json:"cpu_s"`
}

// Version is the agent binary's version.Info.
type Version struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	Modified  bool   `json:"modified,omitempty"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

// IPC is the socket hooks hand their calls to the agent on.
type IPC struct {
	// Via is socket or pipe, and "" while hooks can't reach the agent and
	// spool their calls.
	Via        string  `json:"via,omitempty"`
	Stored     uint64  `json:"stored"`
	Duplicates uint64  `json:"duplicates"`
	Refused    uint64  `json:"refused"`
	Spooled    Spooled `json:"spooled"`
}

// Spooled counts the hook calls made since Since that reached the agent
// only through the spool (DESIGN 5.2).
type Spooled struct {
	Since   time.Time `json:"since"`
	Calls   uint64    `json:"calls"`
	Prompts uint64    `json:"prompts"`
	// Unknown is how many calls the agent can't tell prompts from other
	// events for, whose journal lines an older version wrote.
	Unknown uint64 `json:"unknown,omitempty"`
}

// Signals says where the agent reads idle time and focus, or why it
// can't (D-design-5.9).
type Signals struct {
	Idle  Signal `json:"idle"`
	Focus Signal `json:"focus"`
	// Session is the kind of Linux desktop session the agent runs in:
	// x11, wayland or none, and "" elsewhere.
	Session string `json:"session,omitempty"`
	// Loop is the macOS notification loop, on macOS once it runs.
	Loop *Loop `json:"loop,omitempty"`
}

// Signal is a signal's source, or why there is none: exactly one is set.
type Signal struct {
	Via     string `json:"via,omitempty"`
	Unknown string `json:"unknown,omitempty"`
}

// Loop is the macOS notification loop's state.
type Loop struct {
	MainThread bool   `json:"main_thread"`
	Dropped    uint64 `json:"dropped"`
}

// Adapters is what the harness adapters made of the hook calls the agent
// stored since it started (DESIGN 5.3).
type Adapters struct {
	// Harnesses has the counts of each harness pairtime has an adapter
	// for, by its name, such as claude_code.
	Harnesses map[string]HarnessCounts `json:"harnesses"`
	// Other counts the calls of harnesses pairtime has no adapter for.
	Other   HarnessCounts `json:"other"`
	Waiting int           `json:"waiting"`
	Dropped uint64        `json:"dropped,omitempty"`
}

// HarnessCounts is one harness's counts.
type HarnessCounts struct {
	Calls         uint64 `json:"calls"`
	Records       uint64 `json:"records"`
	ParseFailures uint64 `json:"parse_failures"`
	WriteFailures uint64 `json:"write_failures,omitempty"`
	// LateLocal counts the records the ledger dropped: made while
	// config.toml said local-only, they reached it only after it sealed a
	// batch online since, so they never upload (D-late-local).
	LateLocal uint64 `json:"late_local,omitempty"`
	Ignored   uint64 `json:"ignored,omitempty"`
	// LastRecord is the time of the newest record, in Unix ms.
	LastRecord int64 `json:"last_record,omitempty"`
	// LastFailure names the events and fields of the last failure, never
	// their values.
	LastFailure string `json:"last_failure,omitempty"`
	// Lines counts the log lines the adapter read, and LineFailures those
	// it couldn't fully read (WP-AD3).
	Lines        uint64 `json:"lines,omitempty"`
	LineFailures uint64 `json:"line_failures,omitempty"`
	// Repeats counts the records of log lines a replay read again that the
	// ledger already held, which the adapter dropped.
	Repeats uint64 `json:"repeats,omitempty"`
}

// Apps is what the agent reads of the agent apps whose own stores it reads
// (DESIGN 3.3, 5.1).
type Apps struct {
	// T3Code is how the agent's reader of T3 Code's store is doing: nil
	// before its first read, and from an agent older than the reader.
	T3Code *T3Code `json:"t3_code,omitempty"`
}

// T3Code is how the agent's reader of T3 Code's message store is doing, as
// its last read left it. It names no path (DESIGN 5.9).
type T3Code struct {
	// Reading says the last read worked, and Off why it didn't: no_store,
	// not_running, no_sqlite3, no_json, schema or failed. Problem says more
	// of a schema the probe refused or a read that failed.
	Reading bool   `json:"reading"`
	Off     string `json:"off,omitempty"`
	Problem string `json:"problem,omitempty"`
	// Newer says T3's schema is newer than the one the reader was tested
	// against, which it reads anyway.
	Newer bool `json:"newer,omitempty"`
	// Running says T3 Code ran at the last read, by DESIGN 3.3's test: the
	// agent's process scan found its server, and its store had the -wal
	// and -shm files T3 holds open. Files a killed T3 left behind don't
	// make it run, though the reader still reads them.
	Running bool `json:"running,omitempty"`
	// NoSQLite3 says the agent found no sqlite3 on its PATH at the last
	// read, whether or not T3 ran then.
	NoSQLite3 bool `json:"no_sqlite3,omitempty"`
	// Witnessed counts today's messages that T3 says a person sent and the
	// reader recorded, WithServer those of them it recorded while the
	// agent's process scan found T3's server, Paired its records that
	// paired with a prompt, Aged the messages its age test turned away, read
	// too long after they were sent, and Ahead the messages it turned away
	// as stamped more than 2 s after the read that decided them, which a
	// clock T3 and the agent share doesn't give unless it was set back.
	Witnessed  int `json:"witnessed"`
	WithServer int `json:"with_server"`
	Paired     int `json:"paired"`
	Aged       int `json:"aged"`
	Ahead      int `json:"ahead"`
	// SessionAt is when a session that an agent app's server started last
	// reached the agent, in Unix ms, 0 for never.
	SessionAt int64 `json:"session_at,omitempty"`
}
