// SPDX-License-Identifier: AGPL-3.0-or-later

// Package timelines loads the timeline fixtures in testdata/timelines: the
// struggle-metric spec's worked examples (docs/design/struggle-metric.md
// section 6) as the events a real client would record, with the minute
// rows, totals, anchors and prompt texts SM states for them. They are the
// scorer's oracle: pkg/score's tests load a fixture, feed its events to the
// scorer and compare the output with its expected rows.
// testdata/timelines/README.md describes the format.
package timelines

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// Files in a fixture directory.
const (
	EventsFile   = "events.jsonl"
	ExpectedFile = "expected.json"
	AnchorsFile  = "anchors.json"
	PromptsFile  = "prompts.json"
)

// Fixture is one worked example.
type Fixture struct {
	// Name is the directory name, such as "6.2".
	Name string
	// Dir is the fixture's directory.
	Dir string
	// Lines holds events.jsonl line by line, without the newlines, so tests
	// can check the raw JSON as well as the decoded events.
	Lines [][]byte
	// Events are the events in arrival order.
	Events   []event.Event
	Expected Expected
	Anchors  Anchors
	Prompts  Prompts
}

// Expected is expected.json: what SM says the scorer outputs.
type Expected struct {
	Fixture   string `json:"fixture"`
	Title     string `json:"title"`
	SM        string `json:"sm"`
	Date      string `json:"date"`
	Timezone  string `json:"timezone"`
	UTCOffset string `json:"utc_offset"`
	OS        string `json:"os"`
	// InputKeys says whether the machine reports keyboard input apart from
	// other input (idle_keys_s set).
	InputKeys bool `json:"input_keys"`
	// Evidence and Witness apply to every credited second of the fixture.
	Evidence event.Evidence `json:"evidence"`
	Witness  event.Witness  `json:"witness"`
	Device   string         `json:"device"`
	// Projects maps each project key to the project's name in SM.
	Projects map[string]string `json:"projects"`
	Window   Window            `json:"window"`
	// Rows tile Window in order. A row is one minute or a run of minutes.
	Rows   []Row  `json:"rows"`
	Totals Totals `json:"totals"`
	// Stated lists the totals SM states in its text. The others are sums
	// of the rows or follow from times SM gives.
	Stated []string `json:"stated"`
	// Approx holds the totals SM gives only roughly ("about 23,500 s").
	Approx map[string]Approx `json:"approx"`
	// Errata name the docs/errata files, without ".md", that apply.
	Errata []string `json:"errata"`
	Notes  []string `json:"notes"`
}

// Window is the span the rows cover. Records outside it set up state and
// aren't checked.
type Window struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Local string    `json:"local"`
}

// Row is SM's account of one minute, or of a run of minutes that SM
// describes only as a range.
type Row struct {
	Start   time.Time `json:"start"`
	Minutes int       `json:"minutes"`
	Local   string    `json:"local"`
	// Bucket says whether the scorer writes a minute bucket for any of the
	// row's minutes. It writes one only when something was credited or an
	// agent was running (SM 5.1). The fields below are set only when Bucket
	// is true.
	Bucket bool `json:"bucket"`
	// BucketMinutes is how many of the row's minutes have a bucket: all of
	// them for a single minute, and for a run as many as had credit or a
	// running agent. Credited and uncredited seconds cover those minutes.
	BucketMinutes int     `json:"bucket_minutes"`
	CreditedS     float64 `json:"credited_s"`
	// Credit holds credited seconds by project, then by reason code.
	Credit map[string]map[event.Reason]float64 `json:"credit"`
	// Uncredited holds uncredited seconds by reason code. It is nil when
	// SM lists the codes without their seconds; Codes then holds them.
	Uncredited map[event.Reason]float64 `json:"uncredited"`
	Codes      []event.Reason           `json:"codes"`
	// AgentRunningS is the seconds an agent turn was running, summed over
	// the row's minutes. SM rarely states it; it follows from the times SM
	// gives for each run.
	AgentRunningS float64 `json:"agent_running_s"`
	// SM names the row of SM's table this row transcribes.
	SM   string `json:"sm"`
	Note string `json:"note"`
	// Derived lists the fields worked out from times SM gives rather than
	// stated in its row.
	Derived []string `json:"derived"`
	// Erratum names the docs/errata file, without ".md", that questions
	// this row.
	Erratum string `json:"erratum"`
}

// End is the end of the row's last minute.
func (r Row) End() time.Time { return r.Start.Add(time.Duration(r.Minutes) * time.Minute) }

// CreditedReasons sums the row's credit over its projects.
func (r Row) CreditedReasons() map[event.Reason]float64 {
	m := map[event.Reason]float64{}
	for _, reasons := range r.Credit {
		for k, s := range reasons {
			m[k] += s
		}
	}
	return m
}

// Totals are the fixture's totals: the sums of its rows, and whatever else
// SM states, such as agent_only_s.
type Totals struct {
	CreditedS         float64                  `json:"credited_s"`
	CreditedByProject map[string]float64       `json:"credited_by_project"`
	CreditedByReason  map[event.Reason]float64 `json:"credited_by_reason"`
	Uncredited        map[event.Reason]float64 `json:"uncredited"`
	AgentRunningS     float64                  `json:"agent_running_s"`
	// Other holds the remaining totals by name, such as "agent_only_s".
	Other map[string]float64 `json:"-"`
}

// UnmarshalJSON decodes the named fields and puts every other total in
// Other. Every other total must be a number.
func (t *Totals) UnmarshalJSON(b []byte) error {
	type plain Totals
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return err
	}
	p.Other = map[string]float64{}
	for k, v := range all {
		switch k {
		case "credited_s", "credited_by_project", "credited_by_reason", "uncredited", "agent_running_s":
			continue
		}
		var n float64
		if err := json.Unmarshal(v, &n); err != nil {
			return fmt.Errorf("totals.%s: %w", k, err)
		}
		p.Other[k] = n
	}
	*t = Totals(p)
	return nil
}

// Get returns the total named k, whether a named field or in Other.
func (t Totals) Get(k string) (float64, bool) {
	switch k {
	case "credited_s":
		return t.CreditedS, true
	case "agent_running_s":
		return t.AgentRunningS, true
	}
	v, ok := t.Other[k]
	return v, ok
}

// Approx is a total SM gives only roughly.
type Approx struct {
	About float64 `json:"about"`
	// SM quotes the sentence that gives it.
	SM string `json:"sm"`
}

// Anchors is anchors.json: every human act SM names, and every turn.
type Anchors struct {
	Fixture string   `json:"fixture"`
	Anchors []Anchor `json:"anchors"`
	Turns   []Turn   `json:"turns"`
}

// Anchor is a human act that opens a window (SM 2.3).
//
// A relayed anchor (SM 2.2 rule 9) is the exception to "the event behind
// the act, at its t": its event is the relay.message an agent app's reader
// wrote, and its T and Local are the message's sent_at, the app's stamp,
// where its lead ends, not the record's own t (SM 2.3). Prompt names the
// prompt.submit the message paired with, and Labels carries via_relay. A
// prompt that rules 6 and 7 made typed at the app's stamp keeps its own t,
// with the stamp in SentAt and LocalSentAt and the record in Relay
// (testdata/timelines/relayed/README.md).
type Anchor struct {
	// EventID is the event_id of the event behind the act.
	EventID     string            `json:"event_id"`
	Kind        event.AnchorKind  `json:"kind"`
	T           int64             `json:"t"`
	Local       string            `json:"local"`
	ProjectKey  string            `json:"project_key"`
	Origin      event.Origin      `json:"origin"`
	PromptClass event.PromptClass `json:"prompt_class"`
	LeadS       float64           `json:"lead_s"`
	TrailS      float64           `json:"trail_s"`
	// LeadFrom is "sm" when SM states the lead, or "params" when it comes
	// from SM 2.3's table and the parameters in SM 8.
	LeadFrom string         `json:"lead_from"`
	Via      event.Via      `json:"via"`
	Level    event.Evidence `json:"level"`
	SM       string         `json:"sm"`
	// SentAt and LocalSentAt are the app's stamp of the message that
	// witnessed a prompt typed at it, and Relay that message's
	// relay.message. Prompt is a relayed anchor's prompt.submit.
	SentAt      int64  `json:"sent_at"`
	LocalSentAt string `json:"local_sent_at"`
	Relay       string `json:"relay"`
	Prompt      string `json:"prompt"`
	// Labels are the anchor's labels (DESIGN 6.4) where anchors.json lists
	// them, as the relayed cases do a relayed anchor's via_relay. Nil means
	// the fixture doesn't say.
	Labels []event.Label `json:"labels"`
}

// Turn is a prompt and the agent's work on it (SM 1.3), with the origin SM
// gives it.
type Turn struct {
	SessionKey  string            `json:"session_key"`
	TurnKey     string            `json:"turn_key"`
	TSubmit     int64             `json:"t_submit"`
	LocalSubmit string            `json:"local_submit"`
	TStop       int64             `json:"t_stop"`
	LocalStop   string            `json:"local_stop"`
	Origin      event.Origin      `json:"origin"`
	PromptClass event.PromptClass `json:"prompt_class"`
	SM          string            `json:"sm"`
}

// Prompts is prompts.json: the prompt texts SM quotes, for features
// computed from content, which events don't carry.
type Prompts struct {
	Fixture string   `json:"fixture"`
	Prompts []Prompt `json:"prompts"`
}

// Prompt is one prompt's text. Text is nil where SM doesn't quote it.
type Prompt struct {
	TurnKey  string  `json:"turn_key"`
	T        int64   `json:"t"`
	Local    string  `json:"local"`
	Text     *string `json:"text"`
	LenChars int     `json:"len_chars"`
	// Source says where the text comes from: an SM quote, or an
	// invention the fixture marks as such.
	Source string `json:"source"`
	// Erratum names the docs/errata file, without ".md", when the quoted
	// text and the length SM states disagree.
	Erratum string `json:"erratum"`
}

// Root returns testdata/timelines in the module that contains the working
// directory. Go runs tests in their package's directory, so this works
// from any package's tests.
func Root() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && bytes.HasPrefix(b, []byte("module github.com/some-du6e/pairtime\n")) {
			return filepath.Join(dir, "testdata", "timelines"), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("timelines: no pairtime go.mod above the working directory")
		}
		dir = parent
	}
}

// List returns the names of the fixtures under root, sorted: every
// directory that holds an events.jsonl.
func List(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, e.Name(), EventsFile)); err == nil {
			names = append(names, e.Name())
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	slices.Sort(names)
	return names, nil
}

// Load reads the fixture in root/name. It decodes every file but doesn't
// validate the events; tests do that.
func Load(root, name string) (*Fixture, error) {
	f := &Fixture{Name: name, Dir: filepath.Join(root, name)}
	if err := f.readEvents(); err != nil {
		return nil, err
	}
	for _, d := range []struct {
		file string
		v    any
	}{
		{ExpectedFile, &f.Expected},
		{AnchorsFile, &f.Anchors},
		{PromptsFile, &f.Prompts},
	} {
		if err := readJSON(filepath.Join(f.Dir, d.file), d.v); err != nil {
			return nil, fmt.Errorf("timelines: %s/%s: %w", name, d.file, err)
		}
	}
	return f, nil
}

func (f *Fixture) readEvents() error {
	path := filepath.Join(f.Dir, EventsFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("timelines: %w", err)
	}
	if len(b) == 0 || b[len(b)-1] != '\n' {
		return fmt.Errorf("timelines: %s/%s must end with a newline", f.Name, EventsFile)
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := slices.Clone(sc.Bytes())
		var e event.Event
		if err := json.Unmarshal(line, &e); err != nil {
			return fmt.Errorf("timelines: %s/%s:%d: %w", f.Name, EventsFile, n, err)
		}
		f.Lines = append(f.Lines, line)
		f.Events = append(f.Events, e)
	}
	return sc.Err()
}

// readJSON decodes the file at path into v, refusing unknown fields, so a
// misspelled field in a hand-checked file fails instead of vanishing.
func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("more than one JSON value")
	}
	return nil
}

// LoadAll loads every fixture under Root.
func LoadAll() ([]*Fixture, error) {
	root, err := Root()
	if err != nil {
		return nil, err
	}
	names, err := List(root)
	if err != nil {
		return nil, err
	}
	var out []*Fixture
	for _, n := range names {
		f, err := Load(root, n)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// EventByID returns the event with the given event_id.
func (f *Fixture) EventByID(id string) (event.Event, bool) {
	for _, e := range f.Events {
		if e.EventID == id {
			return e, true
		}
	}
	return event.Event{}, false
}

// InWindow reports whether t, in Unix milliseconds, falls in the window.
func (f *Fixture) InWindow(t int64) bool {
	return t >= f.Expected.Window.Start.UnixMilli() && t < f.Expected.Window.End.UnixMilli()
}

// String names the fixture.
func (f *Fixture) String() string {
	return strings.TrimSpace(f.Name + " " + f.Expected.Title)
}
