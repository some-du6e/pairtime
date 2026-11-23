// SPDX-License-Identifier: AGPL-3.0-or-later

// Package adapters is the interface every harness adapter implements, the
// registry the agent finds adapters in, and the conformance runner that
// replays captured fixtures through an adapter and diffs the records it
// makes against expected JSON (DESIGN 3.4, 5.3; docs/PLAN.md, WP-AD2).
//
// An adapter turns what one tool exposes into SM 1 events and never
// touches the scorer, so a new harness is an adapter-only change. The agent
// owns all I/O: the hook receiver, the tailers and the HTTP routes deliver
// raw records, and the adapter's Parse reads nothing but them. In particular
// the adapter never decides how a record arrived. The agent does, and hands
// it over in [Arrival]: the via its events get, their times, and what the
// agent's own walk of the process tree found (DESIGN 3.4, 5.2; D-spool-via).
// [HookRecord] is that step for a hook call.
//
// # What Parse returns
//
// DESIGN 3.4 sketches Parse as returning events. Here it returns [Record]s,
// each an event with the content the ledger commits about it beside it:
// the prompt's or final message's text, an edit's hunks, a command line
// (ledger-v1 10.1, 10.6). That content stays on the device, in local
// fields of nothing: the event itself holds only what SM 1.2 lists, and the
// ledger writer (internal/agent/ledger) takes the event and the content
// together, as its Input.
//
// Parse is pure in what it reads: no files, no clock, no process table. It
// does keep memory of the sessions it has seen, because SM 2.2 rule 1
// matches a prompt against the scheduled prompts of an earlier stop, and
// D-spool-via records a session.start from the walk for a hooked session
// that has none. So the records it makes depend on the raw records it was
// given and their order, and on nothing else. The project resolver it is
// given may run git; the agent's caches what it finds.
//
// # Keys
//
// Harness ids become opaque keys keyed per device (AGENTS.md): sessions,
// turns and subagents with [Keys], under the hook path's K_session, and
// file paths, error signatures and commands with K_feat, as the ledger
// keys them (pkg/ledger/features; ledger-v1 3, 10.6).
package adapters

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io/fs"
	"strings"

	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/install"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger/features"
)

// Adapter is one harness's adapter (DESIGN 3.4).
//
// Plan returns an error beside its edits, where DESIGN's sketch has none,
// because internal/install refuses an Env it can't plan for, such as one
// with a relative home directory.
type Adapter interface {
	// Name is the harness, as pkg/event names it: "claude_code", ...
	Name() event.Harness
	// Detect says whether the harness looks installed, and where its
	// configuration is.
	Detect(env Env) Detection
	// Plan returns the install edits that add pairtime's hooks. They are
	// idempotent, and uninstall reverts them (internal/install).
	Plan(env Env) ([]ConfigEdit, error)
	// Sources lists what the agent delivers to Parse: hook events, log
	// roots and globs, HTTP routes.
	Sources() []Source
	// Hosts lists the executables that host the harness's sessions. A hook
	// call's events are via hook only when the agent's walk of its parent
	// chain reaches one of them (DESIGN 3.4, 5.2), and focus attribution
	// uses them (SM 3.2).
	Hosts() []ProcessMatch
	// Parse turns one raw record into records, in the order they happened.
	// A raw record it can't fully read gives a *ParseError, with the
	// records it could still make: unknown events and fields are counted
	// as parse failures, never fatal (DESIGN 5.3). It is safe for
	// concurrent use.
	Parse(raw RawRecord) ([]Record, error)
	// Fixtures holds the expected records of each captured fixture the
	// adapter supports, by harness version, OS and run, as the conformance
	// runner reads them (Conform).
	Fixtures() fs.FS
}

// Env is what Detect and Plan read of the machine: the OS, the home
// directory, the environment and the pairtime binary's path. It is
// internal/install's, which plans every edit.
type Env = install.Env

// ConfigEdit is one install item: the edits that add a harness's hooks,
// which `pairtime install` shows as a diff and uninstall reverts.
type ConfigEdit = install.Item

// Detection is what Detect found.
type Detection struct {
	// Installed says the harness's own configuration folder exists.
	Installed bool
	// Version is the harness's version where its configuration says, or "".
	Version string
	// ConfigPaths are the files pairtime's install edits, whether or not
	// they exist yet.
	ConfigPaths []string
}

// SourceKind says how the agent delivers a source's raw records.
type SourceKind string

const (
	// SourceHook is a hook the harness runs: `pairtime hook --harness <h>
	// --event <e>`, delivered over the socket, the pipe or the spool.
	SourceHook SourceKind = "hook"
	// SourceLog is a file the harness writes, read by the tailers.
	SourceLog SourceKind = "log"
	// SourceHTTP is a route on the agent's loopback server.
	SourceHTTP SourceKind = "http"
)

// Source is one thing the agent delivers to an adapter's Parse.
type Source struct {
	Kind SourceKind
	// Event is the hook event, as the harness names it, and Matcher the
	// matcher pairtime installs it under, "" for all. Hooks only.
	Event, Matcher string
	// Root and Glob say which files a log source reads.
	Root, Glob string
	// Route is an HTTP source's path.
	Route string
}

// ProcessMatch names the executables of one kind of process, as the
// process table names them on the OSes it lists (all when empty).
type ProcessMatch struct {
	Names []string
	OS    []string
}

// Matches reports whether p, read on goos, is one of m's processes. Names
// are compared as the platform's app-class table compares them, so a
// Windows ".exe" and a Linux process title are handled as it handles them.
func (m ProcessMatch) Matches(goos string, p platform.Proc) bool {
	if len(m.OS) > 0 && !contains(m.OS, goos) {
		return false
	}
	e, ok := platform.Identify(goos, p)
	if !ok {
		return false
	}
	for _, n := range m.Names {
		if contains(e.Names, n) {
			return true
		}
	}
	return false
}

// MatchesAny reports whether p is one of the processes in ms.
func MatchesAny(ms []ProcessMatch, goos string, p platform.Proc) bool {
	for _, m := range ms {
		if m.Matches(goos, p) {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// RawRecord is one thing the agent took in for an adapter.
type RawRecord struct {
	Source SourceKind
	// Event is the harness's name for a hook event, as --event passed it.
	Event string
	// Payload is the raw record itself: what the harness wrote to the
	// hook's stdin. It holds content, so it stays on the device.
	Payload []byte
	// Incomplete says the agent didn't get the payload whole: the hook cut
	// it at 1 MiB, or it wasn't one JSON value, or it didn't fit a message.
	Incomplete bool
	Arrival    Arrival
	// Log says where in a log file a record of SourceLog was read, and is
	// nil for every other source.
	Log *LogLine
}

// Arrival is what the agent, not the adapter or the sender, says about how
// a raw record reached it (DESIGN 3.4).
type Arrival struct {
	// Via is the via of the record's events: for a hook call, hook only
	// when it came over the socket or the pipe and the agent's own walk of
	// its parent chain reached one of the adapter's Hosts, and plugin for
	// every other one, spooled calls included (D-spool-via; DESIGN 5.2).
	// For a line of a log file the tailers read, it is log.
	Via event.Via
	// T is when the call was made, in Unix milliseconds, never later than
	// when the agent took it in; Mono and BootID go with it (SM 1.1). For
	// a log line it is when the agent read the line: the line's own
	// records carry the times the line states, and their mono is the
	// agent's reading at T taken back by how much earlier they are, as
	// git.change records are, and never below 0. A line read more than
	// LATE_READ_S after its time carries none, 0, and goes by its time
	// (internal/adapters/claudecode's env).
	T      int64
	Mono   int64
	BootID string
	// Walk holds what SM 1.2 takes from the agent's walk for the session:
	// parent_class, has_tty and an IDE or desktop entrypoint. It is set
	// only when Via is hook, because only a counted walk says anything
	// about the process that ran the hook (D-spool-via).
	Walk *platform.Lineage
	// Harness is the harness whose process ran the hook: the walk's, or
	// for a call with no counted walk the one its own recorded chain names,
	// which serves only to tell apart the copies of one call that two
	// harnesses make (DESIGN 5.1). It is "" when neither names one.
	Harness event.Harness
}

// Record is one event an adapter made, with the content its ledger record
// commits (ledger-v1 10.1, 10.6). The content never goes in the event: it
// is what the agent hands the ledger writer beside it, and what the vault
// keeps.
type Record struct {
	Event event.Event
	// Text is a prompt.submit's prompt as the harness delivered it, or an
	// agent.output's final message when is_final is set. It is nil for any
	// other record.
	Text []byte
	// Edits are an agent.tool's edit hunks, with paths in the ledger's form
	// (features.CheckPath), in the order the patch lists them.
	Edits []features.Edit
	// Command is the command line an agent.tool ran.
	Command string
	// Dir and Call go with Command: the folder the command ran in, the
	// session's working directory, and what tells the tool call from
	// another, such as Claude Code's tool_use_id. The agent hands them to
	// the git watcher, which matches a git command to the commit it made
	// (git.Watcher.NoteAgentCommand). Both stay on the device, like the
	// command: Dir is a path, and neither goes in the event.
	Dir, Call string
	// LogKey names a log line's record among the records that line, read
	// again, makes again: where in its file the line that holds the
	// record's content starts, and which record of that line it is, such as
	// "1234.prompt". The agent derives the record's event_id from it, so a
	// line read again after a crash makes records pkg/score takes for the
	// ones the ledger has. It is "" for a record of anything but a log.
	LogKey string
	// Late, which the agent sets, says the record tells of a time long
	// past, as lookback's do: the ledger seals it in its own time
	// (ledger.Input.Late).
	Late bool
}

// Stateful is an adapter whose memory of the sessions it parses can
// outlive one run of the agent (DESIGN 3.5, state.json). Without it, a
// restarted agent would take a queued message for a new prompt, miss a
// scheduled prompt that an earlier stop listed (SM 2.2 rule 1) and record
// a second session.start from the walk. The agent keeps what SaveState
// returns on the device, readable by its user alone, and hands it to
// LoadState before the adapter's first Parse.
type Stateful interface {
	// SaveState returns the adapter's memory as JSON. It holds no content:
	// no prompt text, nor anything a reader could turn back into some.
	SaveState() (json.RawMessage, error)
	// LoadState replaces the adapter's memory with what SaveState
	// returned, from this build or an older one. Memory it can't read is
	// an error, and the adapter then starts empty, as on a first run.
	LoadState(json.RawMessage) error
}

// Placer is an adapter that can say, before Parse, which folder a raw
// record's call ran in: the working directory Parse would resolve to a
// project (Options.Project), or "" when it would resolve none. The agent
// holds a call back until its folder's project is known, rather than have
// Parse wait for the resolver or go without the project, and the calls of
// other folders go on meanwhile (D-park-unresolved). Folder reads the raw
// record as Parse does and changes nothing.
type Placer interface {
	Folder(raw RawRecord) string
}

// Options are what an adapter needs from the agent to make records.
type Options struct {
	// Keys keys the harness's own ids: sessions, turns and subagents. An
	// adapter refuses to be made without it.
	Keys *Keys
	// Features is K_feat, which keys file paths, error signatures and
	// commands as the ledger does. Nil leaves out what needs it: files,
	// error_sig and cmd_hash.
	Features *features.Key
	// Project resolves a working directory to its project. Nil, or false,
	// leaves project_key empty and keys file paths absolute.
	Project func(dir string) (Project, bool)
}

// Project is a working directory's project, as the git adapter resolves it
// (internal/adapters/git).
type Project struct {
	// Key is the project_key.
	Key string
	// Root is the project's folder, which file paths are keyed relative
	// to (ledger-v1 10.6).
	Root string
	// Ignored says the folder is out of pairtime (DESIGN 8.8), which the
	// resolver says with false: the host drops the records of the call or
	// line being parsed, and a log reader keeps the file out with it until
	// a line names a folder that counts (LogReader.LogIgnored).
	Ignored bool
}

// ErrOtherHarness is what Parse returns for a hook call that another
// harness's process ran, such as Cursor or Devin running Claude Code's
// hooks (DESIGN 5.1). That harness's adapter records it, so this one makes
// nothing, and the call isn't a parse failure.
var ErrOtherHarness = errors.New("adapters: another harness ran this hook")

// ParseError says what Parse couldn't read in one raw record. It names
// fields and events, never their values, which can be content.
type ParseError struct {
	// Event is the hook event.
	Event string
	// Problems are what went wrong, in order.
	Problems []string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("adapters: %s: %s", e.Event, strings.Join(e.Problems, "; "))
}

// Problem adds a problem to e.
func (e *ParseError) Problem(format string, args ...any) {
	e.Problems = append(e.Problems, fmt.Sprintf(format, args...))
}

// Err returns e, or nil when it has no problem.
func (e *ParseError) Err() error {
	if e == nil || len(e.Problems) == 0 {
		return nil
	}
	return e
}

// Keys keys a harness's own ids per device, so no one without the device's
// key can tell which session, turn or subagent a key names, or test a
// guess (AGENTS.md). They are HMACs under K_session, the hook path's
// session secret (hookpath.SessionSecret), whose label and formulas
// ledger-v1.md section 3 lists: the session key exactly as the hook path
// and the focus component make it, so a session's events and the
// hosted_sessions of the window it runs in name it alike, and the others
// over domain strings of their own. No key depends on the harness, so the
// copies of one call that two harnesses make (DESIGN 5.1) key alike.
type Keys struct {
	secret []byte
}

// NewKeys returns the Keys for K_session, from hookpath.SessionSecret. It
// panics on a secret that isn't hookpath.SessionSecretLen bytes, such as
// the nil one the agent holds before its keystore opens: ids keyed under
// it would be keyed under no secret at all, which anyone could recompute
// from a guess (AGENTS.md).
func NewKeys(kSession []byte) *Keys {
	if len(kSession) != hookpath.SessionSecretLen {
		panic(fmt.Sprintf("adapters: K_session must be %d bytes, not %d", hookpath.SessionSecretLen, len(kSession)))
	}
	return &Keys{secret: bytes.Clone(kSession)}
}

// FixtureKeys returns Keys under a fixed, public secret, for tests and for
// `pairtime adapters test`, whose output must be the same everywhere. The
// agent never uses it.
func FixtureKeys() *Keys { return NewKeys([]byte("pairtime adapters test keys v1..")) }

// FixtureOptions are Options under FixtureKeys and a fixed, public K_feat, with
// every folder its own project, rooted there. The conformance runner's
// replays and `pairtime adapters test` use them, and the agent never does.
func FixtureOptions() Options {
	var k [features.KeySize]byte
	copy(k[:], "pairtime adapters test K_feat v1")
	return Options{Keys: FixtureKeys(), Features: features.NewKey(k), Project: folderProject}
}

// folderProject makes dir a project of its own, keyed by its path under a
// public key: for tests only, where every key becomes a symbol anyway.
func folderProject(dir string) (Project, bool) {
	m := hmac.New(sha256.New, []byte("pairtime adapters test projects"))
	m.Write([]byte(dir))
	return Project{Key: "p" + hex.EncodeToString(m.Sum(nil)[:8]), Root: dir}, true
}

// Format writes a placeholder whatever the verb, so a Keys never prints
// its secret.
func (Keys) Format(f fmt.State, _ rune) { fmt.Fprint(f, "adapters.Keys{redacted}") }

func (k *Keys) key(prefix, domain string, parts ...string) string {
	m := hmac.New(sha256.New, k.secret)
	m.Write([]byte("pairtime/v1/session/" + domain))
	m.Write([]byte{0})
	for _, p := range parts {
		writePart(m, p)
	}
	return prefix + hex.EncodeToString(m.Sum(nil)[:16])
}

func writePart(h hash.Hash, s string) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(s)))
	h.Write(n[:])
	h.Write([]byte(s))
}

// Session returns the session_key of a harness's session id: the hook
// path's (hookpath.SessionKey).
func (k *Keys) Session(id string) string { return hookpath.SessionKey(k.secret, id) }

// Turn returns the turn_key of turn id in the harness session session.
func (k *Keys) Turn(session, id string) string { return k.key("t", "turn", session, id) }

// Agent returns the agent_key of subagent id in the harness session
// session.
func (k *Keys) Agent(session, id string) string { return k.key("a", "agent", session, id) }

// Cron returns the prefix_hash of a scheduled prompt's first 1,000
// characters in the harness session session (agent.stop's pending_crons,
// which stay on the device).
func (k *Keys) Cron(session, prefix string) string { return k.key("c", "cron", session, prefix) }

// Memory returns the key the agent names this K_session by beside the
// adapters' saved memory in state.json, so memory saved under another one,
// as after a restore, is left behind. It is over a domain of its own, so no
// session, turn, subagent or scheduled prompt key equals it, and it never
// leaves the device.
func (k *Keys) Memory() string { return k.key("m", "memory") }

// App returns the key of one of an agent app's own ids: r and the HMAC over
// its domain, the app (as pkg/event names it, such as "t3_code"), the kind
// of id and the id's parts (DESIGN 4.1 E-12). An app's thread, run and
// message ids enter pairtime only so: a relay.message's thread_key is the
// app's thread key, and the app's reader keeps its cursor and the rows and
// runs it watches by key alone (internal/adapters/t3code). The kind keeps
// a thread and a message with the same id apart.
func (k *Keys) App(app, kind string, id ...string) string {
	return k.key("r", "app", append([]string{app, kind}, id...)...)
}
