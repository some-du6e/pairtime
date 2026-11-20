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
// turns and subagents with [Keys], under a secret derived from K_dev with
// its own label, and file paths, error signatures and commands with K_feat,
// as the ledger keys them (pkg/ledger/features; ledger-v1 10.6).
package adapters

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io/fs"
	"strings"

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
}

// Arrival is what the agent, not the adapter or the sender, says about how
// a raw record reached it (DESIGN 3.4).
type Arrival struct {
	// Via is the via of the record's events: for a hook call, hook only
	// when it came over the socket or the pipe and the agent's own walk of
	// its parent chain reached one of the adapter's Hosts, and plugin for
	// every other one, spooled calls included (D-spool-via; DESIGN 5.2).
	Via event.Via
	// T is when the call was made, in Unix milliseconds, never later than
	// when the agent took it in; Mono and BootID go with it (SM 1.1).
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
}

// Options are what an adapter needs from the agent to make records.
type Options struct {
	// Keys keys the harness's own ids: sessions, turns and subagents. Nil
	// means a key that only tests should use (TestKeys).
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

// IDSecretLabel is the HKDF label of the harness id secret, from which the
// keys of sessions, turns and subagents are made. ledger-v1.md section 3
// lists it beside the git adapter's project secret.
const IDSecretLabel = "pairtime/v1/harness-ids"

// IDSecret derives the harness id secret from the device key K_dev the way
// ledger-v1.md section 3 derives the ledger's keys: HKDF-SHA256 with RFC
// 5869's default salt, info IDSecretLabel, and 32 bytes of output.
func IDSecret(kDev []byte) ([32]byte, error) {
	var s [32]byte
	if len(kDev) != 32 {
		return s, errors.New("adapters: K_dev must be 32 bytes")
	}
	k, err := hkdf.Key(sha256.New, kDev, nil, IDSecretLabel, len(s))
	if err != nil {
		return s, err
	}
	copy(s[:], k)
	return s, nil
}

// Keys keys a harness's own ids per device, so no one without the device's
// secret can tell which session, turn or subagent a key names, or test a
// guess (AGENTS.md). Each key is a prefix and 32 hex digits of an HMAC
// under the secret, over a domain string and the ids, each with its length,
// so no two kinds or pairs of ids can give the same key.
type Keys struct {
	secret [32]byte
}

// NewKeys returns the Keys for an id secret, from IDSecret.
func NewKeys(secret [32]byte) *Keys { return &Keys{secret: secret} }

// TestKeys returns Keys under a fixed, public secret, for tests and for
// `pairtime adapters test`, whose output must be the same everywhere. The
// agent never uses it.
func TestKeys() *Keys {
	var s [32]byte
	copy(s[:], "pairtime adapters test keys v1..")
	return NewKeys(s)
}

// TestOptions are Options under TestKeys and a fixed, public K_feat, with
// every folder its own project, rooted there. The conformance runner's
// replays and `pairtime adapters test` use them, and the agent never does.
func TestOptions() Options {
	var k [features.KeySize]byte
	copy(k[:], "pairtime adapters test K_feat v1")
	return Options{Keys: TestKeys(), Features: features.NewKey(k), Project: folderProject}
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
	m := hmac.New(sha256.New, k.secret[:])
	writePart(m, "pairtime/v1/harness-ids/"+domain)
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

// Session returns the session_key of harness h's session id.
func (k *Keys) Session(h event.Harness, id string) string {
	return k.key("s", "session", string(h), id)
}

// Turn returns the turn_key of turn id in session id session.
func (k *Keys) Turn(h event.Harness, session, id string) string {
	return k.key("t", "turn", string(h), session, id)
}

// Agent returns the agent_key of subagent id in session id session.
func (k *Keys) Agent(h event.Harness, session, id string) string {
	return k.key("a", "agent", string(h), session, id)
}

// Cron returns the prefix_hash of a scheduled prompt's first 1,000
// characters in session id session (agent.stop's pending_crons, which stay
// on the device).
func (k *Keys) Cron(h event.Harness, session, prefix string) string {
	return k.key("c", "cron", string(h), session, prefix)
}
