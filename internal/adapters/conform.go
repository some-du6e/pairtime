// SPDX-License-Identifier: AGPL-3.0-or-later

package adapters

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/clock"
	"github.com/some-du6e/pairtime/pkg/event"
)

// Call is one captured hook call: testdata/fixtures/<harness>/<version>/
// <os>/<run>/hooks/NNNNNN, as tools/capture's dumper saved it
// (testdata/fixtures/README.md).
type Call struct {
	// Dir is the call's directory name, such as "000001", which orders the
	// calls as they arrived.
	Dir string
	// Event is the hook's event, the last of the arguments the harness
	// passed the dumper.
	Event string
	// Stdin is what the harness wrote to the hook.
	Stdin []byte
	// Arrived is when the dumper ran.
	Arrived time.Time
	// Parents are the hook's parent processes, nearest first.
	Parents []platform.Proc
}

// ReadCalls reads a run's hook calls in arrival order. run holds the
// run's files: hooks/NNNNNN/{argv,stdin,time,parents}.
func ReadCalls(run fs.FS) ([]Call, error) {
	dirs, err := fs.ReadDir(run, "hooks")
	if err != nil {
		return nil, err
	}
	var calls []Call
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		c, err := readCall(run, d.Name())
		if err != nil {
			return nil, fmt.Errorf("hooks/%s: %w", d.Name(), err)
		}
		calls = append(calls, c)
	}
	slices.SortFunc(calls, func(a, b Call) int { return strings.Compare(a.Dir, b.Dir) })
	return calls, nil
}

func readCall(run fs.FS, dir string) (Call, error) {
	c := Call{Dir: dir}
	p := "hooks/" + dir + "/"
	argv, err := fs.ReadFile(run, p+"argv")
	if err != nil {
		return c, err
	}
	if args := strings.Fields(string(argv)); len(args) > 0 {
		c.Event = args[len(args)-1]
	}
	if c.Stdin, err = fs.ReadFile(run, p+"stdin"); err != nil {
		return c, err
	}
	tf, err := fs.ReadFile(run, p+"time")
	if err != nil {
		return c, err
	}
	for _, line := range strings.Split(string(tf), "\n") {
		if v, ok := strings.CutPrefix(line, "unix_ns="); ok {
			ns, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return c, fmt.Errorf("time: %w", err)
			}
			c.Arrived = time.Unix(0, ns).UTC()
		}
	}
	if c.Arrived.IsZero() {
		return c, errors.New("time has no unix_ns")
	}
	pf, err := fs.ReadFile(run, p+"parents")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return c, err
	}
	c.Parents = parseParents(pf)
	return c, nil
}

// parseParents reads the dumper's parents file: a pid, a controlling
// terminal ("?" or "??" for none) and the process's name per line, which
// is Linux's comm, the name a process goes by. The table reader names a
// process by its executable and keeps comm as its alias, so this does
// too: a name with ": " in it is a process title, such as tmux's "tmux:
// server", whose executable is the part before the colon, and a name
// ending in ".sh" is a shell script, such as the capture kit's
// allow-once.sh, whose executable is sh.
func parseParents(b []byte) []platform.Proc {
	var out []platform.Proc
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.SplitN(strings.TrimSpace(sc.Text()), " ", 3)
		if len(f) < 3 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil || pid <= 0 {
			continue
		}
		p := platform.Proc{PID: pid, Name: f[2], TTY: platform.TTYNone}
		if f[1] != "?" && f[1] != "??" {
			p.TTY = platform.TTYAttached
		}
		if name, _, ok := strings.Cut(f[2], ": "); ok {
			p.Name, p.Alias = name, f[2]
		} else if strings.HasSuffix(f[2], ".sh") {
			p.Name, p.Alias = "sh", f[2]
		}
		out = append(out, p)
	}
	for i := range out {
		if i+1 < len(out) {
			out[i].PPID = out[i+1].PID
		}
	}
	return out
}

// Delivery says how a replay hands each captured call to the agent.
type Delivery int

const (
	// Walked delivers each call over the socket from under its captured
	// parents, as the agent's walk would read them, so a call whose
	// parents reach one of the adapter's hosts is via hook.
	Walked Delivery = iota
	// Spooled delivers each call through the inbox spool, with the
	// captured parents as the chain its line records, which decides
	// nothing (D-spool-via).
	Spooled
)

// hookPID is the replay's pid for the hook process itself, which the
// capture doesn't list: the dumper ran in its place.
const hookPID = 1 << 30

// replayBoot is the boot id of every replayed call.
const replayBoot = "replay"

// DeliveryOf returns the delivery the agent would have taken call c of
// harness h in as, by way, with c's arrival as both the sender's stamp and
// the agent's. base is the run's first call's arrival, from which the
// replay's monotonic readings count.
func DeliveryOf(h event.Harness, c Call, way Delivery, base time.Time) inbox.Delivery {
	self := platform.Proc{PID: hookPID, Name: "pairtime", TTY: platform.TTYNone, Start: c.Arrived}
	if len(c.Parents) > 0 {
		self.PPID = c.Parents[0].PID
	}
	chain := append([]platform.Proc{self}, c.Parents...)
	body, _ := json.Marshal(hookpath.Body{Harness: string(h), Event: c.Event, Payload: json.RawMessage(compactJSON(c.Stdin)), Bytes: len(c.Stdin)})
	if !json.Valid(c.Stdin) {
		body, _ = json.Marshal(hookpath.Body{Harness: string(h), Event: c.Event, Text: string(c.Stdin), Bytes: len(c.Stdin)})
	}
	stamp := clock.Stamp{Wall: c.Arrived, Mono: c.Arrived.Sub(base).Milliseconds() + 1000, BootID: replayBoot}
	m := inbox.Message{ID: strings.Repeat("0", 26) + c.Dir, Kind: hookpath.Kind, Sent: stamp.Wall, Mono: stamp.Mono,
		BootID: stamp.BootID, Body: body}
	m.Chain = []inbox.Proc{{PID: self.PID, Name: self.Name, Start: self.Start}}
	d := inbox.Delivery{Message: m, Received: stamp}
	switch way {
	case Walked:
		d.Via, d.PeerPID, d.Walk = inbox.ViaSocket, self.PID, chain
	case Spooled:
		d.Via = inbox.ViaInbox
		for _, p := range c.Parents {
			d.Message.Chain = append(d.Message.Chain, inbox.Proc{PID: p.PID, Name: p.Name, Start: p.Start})
		}
	}
	return d
}

// compactJSON returns b without insignificant whitespace, as the hook's
// own read of stdin keeps it whole.
func compactJSON(b []byte) []byte {
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		return b
	}
	return buf.Bytes()
}

// Replayed is one record a replay made, with the call it came from.
type Replayed struct {
	From   string
	Record Record
}

// ReplayOptions configure Replay.
type ReplayOptions struct {
	// GOOS is the OS the capture ran on, as Go names it.
	GOOS string
	// Way is how each call reaches the agent.
	Way Delivery
	// Ways, when set, picks the way for each call instead, by its Dir.
	Ways func(dir string) Delivery
}

// Replay hands every call to the adapter a, in order, the way the agent
// would, and returns the records it made and the parse failures, by call.
func Replay(a Adapter, calls []Call, opts ReplayOptions) ([]Replayed, map[string]error) {
	var out []Replayed
	fails := map[string]error{}
	if len(calls) == 0 {
		return nil, fails
	}
	base := calls[0].Arrived
	for _, c := range calls {
		way := opts.Way
		if opts.Ways != nil {
			way = opts.Ways(c.Dir)
		}
		raw, err := HookRecord(a, opts.GOOS, DeliveryOf(a.Name(), c, way, base))
		if err != nil {
			fails[c.Dir] = err
			continue
		}
		recs, err := a.Parse(raw)
		if err != nil && !errors.Is(err, ErrOtherHarness) {
			fails[c.Dir] = err
		}
		for _, r := range recs {
			out = append(out, Replayed{From: c.Dir, Record: r})
		}
	}
	return out, fails
}

// Line is one record as an expected.jsonl file writes it: the call it came
// from, the event with its keyed values replaced by symbols, and its
// content. Keys are compared by what they stand for, not their bytes: the
// first session key of a replay is "session-1", the next new one
// "session-2", and so on for each kind of key, so an expected file holds
// no secret and needs no key to write (Normalize).
type Line struct {
	From    string          `json:"from"`
	Event   json.RawMessage `json:"event"`
	Text    *string         `json:"text,omitempty"`
	Command string          `json:"command,omitempty"`
	Edits   []LineEdit      `json:"edits,omitempty"`
}

// LineEdit is an edit hunk in a Line.
type LineEdit struct {
	Path     string `json:"path"`
	OldStart int    `json:"old_start"`
	OldLines int    `json:"old_lines"`
	NewStart int    `json:"new_start"`
	NewLines int    `json:"new_lines"`
}

// keyFamilies names the symbol each keyed field gets. Fields of one
// family share their numbering: a turn is "turn-3" in its prompt, its
// output and its stop alike.
var keyFamilies = map[string]string{
	"session_key": "session", "project_key": "project",
	"turn_key": "turn", "agent_key": "agent", "files": "file",
	"error_sig": "error", "cmd_hash": "command", "prefix_hash": "cron",
}

// dropped are the envelope fields a replay leaves out: the agent assigns
// event_id, seq and device_id when it writes a record, and mono and
// boot_id are the replay's own.
var dropped = []string{"event_id", "seq", "device_id", "mono", "boot_id"}

// Normalize turns replayed records into Lines.
func Normalize(recs []Replayed) ([]Line, error) {
	sym := symbols{}
	out := make([]Line, 0, len(recs))
	for _, r := range recs {
		ev := r.Record.Event
		if ev.Data == nil {
			return nil, fmt.Errorf("hooks/%s: a record with no event", r.From)
		}
		b, err := json.Marshal(ev)
		if err != nil {
			return nil, fmt.Errorf("hooks/%s: %w", r.From, err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		for _, k := range dropped {
			delete(m, k)
		}
		sym.walk(m)
		nb, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		l := Line{From: r.From, Event: nb, Command: r.Record.Command}
		if r.Record.Text != nil {
			s := string(r.Record.Text)
			l.Text = &s
		}
		for _, e := range r.Record.Edits {
			l.Edits = append(l.Edits, LineEdit{Path: e.Path, OldStart: e.OldStart, OldLines: e.OldLines,
				NewStart: e.NewStart, NewLines: e.NewLines})
		}
		out = append(out, l)
	}
	return out, nil
}

// symbols numbers keyed values by family, in order of first appearance.
type symbols map[string]map[string]string

func (s symbols) of(family, v string) string {
	m := s[family]
	if m == nil {
		m = map[string]string{}
		s[family] = m
	}
	if sym, ok := m[v]; ok {
		return sym
	}
	sym := family + "-" + strconv.Itoa(len(m)+1)
	m[v] = sym
	return sym
}

// walk replaces keyed values in a decoded event, at any depth, with their
// symbols.
func (s symbols) walk(v any) {
	switch x := v.(type) {
	case map[string]any:
		for _, k := range sortedKeys(x) {
			fam, keyed := keyFamilies[k]
			switch val := x[k].(type) {
			case string:
				if keyed && val != "" && val != event.NoSession {
					x[k] = s.of(fam, val)
				}
			case []any:
				for i, e := range val {
					if str, ok := e.(string); ok && keyed {
						val[i] = s.of(fam, str)
					} else {
						s.walk(e)
					}
				}
			default:
				s.walk(val)
			}
		}
	case []any:
		for _, e := range x {
			s.walk(e)
		}
	}
}

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

// ReadLines reads an expected.jsonl file.
func ReadLines(b []byte) ([]Line, error) {
	var out []Line
	dec := json.NewDecoder(bytes.NewReader(b))
	for {
		var l Line
		if err := dec.Decode(&l); err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return nil, fmt.Errorf("line %d: %w", len(out)+1, err)
		}
		out = append(out, l)
	}
}

// Diff compares replayed Lines with expected ones and returns a
// description of each difference, at most limit of them.
func Diff(got, want []Line, limit int) []string {
	var diffs []string
	n := max(len(got), len(want))
	for i := 0; i < n && len(diffs) < limit; i++ {
		var g, w string
		if i < len(got) {
			g = canonical(got[i])
		}
		if i < len(want) {
			w = canonical(want[i])
		}
		if g != w {
			diffs = append(diffs, fmt.Sprintf("record %d:\n  got  %s\n  want %s", i+1, orNone(g), orNone(w)))
		}
	}
	return diffs
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// canonical writes a Line with its event's keys sorted, so two Lines
// compare equal exactly when they say the same.
func canonical(l Line) string {
	var ev any
	if err := json.Unmarshal(l.Event, &ev); err == nil {
		l.Event, _ = json.Marshal(ev) // encoding/json sorts map keys
	}
	b, _ := json.Marshal(l)
	return string(b)
}

// Result is what Conform found for one captured run.
type Result struct {
	// Run is the run's path under the adapter's captures: version/os/run.
	Run string
	// Synthetic says the capture's PROVENANCE.md says synthetic: true.
	Synthetic bool
	// Records is how many records the replay made.
	Records int
	// Failures are the replay's parse failures, by call.
	Failures map[string]error
	// Diffs are the differences from the expected records.
	Diffs []string
	// Err is what kept the run from being replayed.
	Err error
}

// OK reports whether the run replayed to its expected records with no
// parse failure.
func (r Result) OK() bool { return r.Err == nil && len(r.Failures) == 0 && len(r.Diffs) == 0 }

// CheckRun replays the calls of one captured run through a, walked, and
// diffs the records it makes with want. run holds the run's files, and
// goos is the OS the capture ran on.
func CheckRun(a Adapter, run fs.FS, goos string, want []Line) Result {
	var r Result
	calls, err := ReadCalls(run)
	if err != nil {
		r.Err = err
		return r
	}
	recs, fails := Replay(a, calls, ReplayOptions{GOOS: goos, Way: Walked})
	r.Records, r.Failures = len(recs), fails
	got, err := Normalize(recs)
	if err != nil {
		r.Err = err
		return r
	}
	r.Diffs = Diff(got, want, 20)
	return r
}

// GOOS returns Go's name for a fixture's OS directory: windows, macos or
// linux (testdata/fixtures/README.md).
func GOOS(dir string) string {
	if dir == "macos" {
		return "darwin"
	}
	return dir
}

// Conform replays each captured run that the adapter's Fixtures has
// expected records for against captures, which holds
// testdata/fixtures/<harness>: <version>/<os>/PROVENANCE.md and
// <version>/<os>/<run>/hooks. mk makes a fresh adapter for each run, so
// what one run's sessions leave in its memory can't change another's.
//
// It returns an error when the adapter has no capture at all, or only
// synthetic ones: an adapter can't ship with synthetic fixtures alone
// (DESIGN 5.1).
func Conform(mk func() Adapter, captures fs.FS) ([]Result, error) {
	fixtures := mk().Fixtures()
	expected, err := fs.Glob(fixtures, "*/*/*/expected.jsonl")
	if err != nil {
		return nil, err
	}
	var results []Result
	real := 0
	for _, e := range expected {
		run := path.Dir(e)
		osDir := path.Dir(run)
		var r Result
		prov, err := fs.ReadFile(captures, osDir+"/PROVENANCE.md")
		var want []Line
		var sub fs.FS
		if err == nil {
			var b []byte
			if b, err = fs.ReadFile(fixtures, e); err == nil {
				if want, err = ReadLines(b); err != nil {
					err = fmt.Errorf("%s: %w", e, err)
				}
			}
		} else {
			err = fmt.Errorf("no PROVENANCE.md beside the capture: %w", err)
		}
		if err == nil {
			sub, err = fs.Sub(captures, run)
		}
		if err == nil {
			r = CheckRun(mk(), sub, GOOS(path.Base(osDir)), want)
			r.Synthetic = Synthetic(prov)
			if !r.Synthetic {
				real++
			}
		} else {
			r.Err = err
		}
		r.Run = run
		results = append(results, r)
	}
	if real == 0 {
		return results, errors.New("adapters: no capture that isn't synthetic: an adapter can't ship with synthetic fixtures alone (DESIGN 5.1)")
	}
	return results, nil
}

// Synthetic reports whether a PROVENANCE.md's front matter says
// synthetic: true. One that doesn't say counts as synthetic.
func Synthetic(prov []byte) bool {
	lines := strings.Split(string(prov), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return true
	}
	for _, l := range lines[1:] {
		l = strings.TrimSpace(l)
		if l == "---" {
			break
		}
		if v, ok := strings.CutPrefix(l, "synthetic:"); ok {
			return strings.TrimSpace(v) != "false"
		}
	}
	return true
}
