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
	Wakeups   uint64    `json:"wakeups"`
	IPC       IPC       `json:"ipc"`
	Signals   Signals   `json:"signals"`
	Adapters  Adapters  `json:"adapters"`
	Footprint Footprint `json:"footprint"`
	// Problems is what isn't working, by where it comes from, in words for
	// the teen.
	Problems map[string]string `json:"problems,omitempty"`
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
	Ignored       uint64 `json:"ignored,omitempty"`
	// LastRecord is the time of the newest record, in Unix ms.
	LastRecord int64 `json:"last_record,omitempty"`
	// LastFailure names the events and fields of the last failure, never
	// their values.
	LastFailure string `json:"last_failure,omitempty"`
}
