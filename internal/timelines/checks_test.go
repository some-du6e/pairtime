// SPDX-License-Identifier: AGPL-3.0-or-later

package timelines

// Tests for the consistency-check fixtures in testdata/checks (WP-FX6):
// each scenario's events against pkg/event's rules and the JSON Schema,
// the shape of its expected results, its ledger's roots and heads against
// the records it reveals, and gen.py writing every file byte for byte.
// They test the fixtures, not the checks: pkg/check's own tests (WP-TR1,
// WP-TR2) run the checks against them. testdata/checks/README.md is the
// format.

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// logReaders are the harnesses with a v1 log reader, DESIGN 5.1's "Second
// record (lookback and CC1)" column.
var logReaders = []string{"claude_code", "codex", "copilot", "gemini", "devin", "cline"}

// checkNames are DESIGN 7.3's checks, in order.
var checkNames = []string{"CC1", "CC2", "CC3", "CC4", "CC5", "CC6", "CC7", "CC8", "CC9", "CC10", "CC11"}

// ledgerKinds are ledger-v1.md 4.2's record types 0x01 to 0x12, a table
// fixed for good whatever order kindTable takes later.
var ledgerKinds = []event.Kind{
	"session.start", "session.end", "prompt.submit", "agent.output", "agent.tool", "agent.permission",
	"agent.interrupt", "agent.queue", "agent.stop", "agent.subagent", "editor.activity", "shell.command",
	"git.change", "os.presence", "os.focus", "os.power", "otel.active_time", "cli.track",
}

// checkScenario is a scenario's expected.json.
type checkScenario struct {
	Scenario string   `json:"scenario"`
	Title    string   `json:"title"`
	Story    string   `json:"story"`
	Base     string   `json:"base"`
	Edits    []string `json:"edits"`
	Devices  []struct {
		DeviceID     string `json:"device_id"`
		Base         string `json:"base"`
		OS           string `json:"os"`
		InputKeys    bool   `json:"input_keys"`
		TypingModule bool   `json:"typing_module"`
		VM           bool   `json:"vm"`
		Reveals      bool   `json:"reveals"`
	} `json:"devices"`
	Submission struct {
		Projects []string `json:"projects"`
		Window   struct {
			Start time.Time `json:"start"`
			End   time.Time `json:"end"`
		} `json:"window"`
	} `json:"submission"`
	Live struct {
		From      string `json:"from"`
		Mutations []struct {
			DeviceID string    `json:"device_id"`
			Minute   time.Time `json:"minute"`
			Op       string    `json:"op"`
			From     string    `json:"from"`
			To       string    `json:"to"`
			HeldS    int       `json:"held_s"`
		} `json:"mutations"`
	} `json:"live"`
	Fails        []string               `json:"fails"`
	Checks       map[string]checkResult `json:"checks"`
	NotExercised map[string]string      `json:"not_exercised"`
	KnownLimit   *string                `json:"known_limit"`
	Errata       []string               `json:"errata"`
	Notes        []string               `json:"notes"`
}

// checkResult is one check's expected result.
type checkResult struct {
	Result string            `json:"result"`
	Counts map[string]int    `json:"counts"`
	Items  []json.RawMessage `json:"items"`
	// Reason says why a check is not computable.
	Reason string `json:"reason"`
	// Basis says what a result rests on when the scenario can't state its
	// counts without a scorer.
	Basis string `json:"basis"`
}

// checkLedger is a scenario's ledger.json: per device, the re-score range,
// the batches the client reveals and the rows the server stored.
type checkLedger struct {
	Note    string `json:"note"`
	Devices []struct {
		DeviceID     string `json:"device_id"`
		RescoreRange struct {
			Start  int64  `json:"start"`
			End    int64  `json:"end"`
			FirstB uint64 `json:"first_b"`
			LastB  uint64 `json:"last_b"`
		} `json:"rescore_range"`
		Batches []struct {
			B       uint64 `json:"b"`
			Records []struct {
				Seq        uint64 `json:"seq"`
				RType      int    `json:"rtype"`
				ProjectKey string `json:"project_key"`
				SessionKey string `json:"session_key"`
				Body       string `json:"body"`
			} `json:"records"`
		} `json:"batches"`
		Witnessed []struct {
			B          uint64 `json:"b"`
			N          uint64 `json:"n"`
			Prev       string `json:"prev"`
			Root       string `json:"root"`
			Head       string `json:"head"`
			ReceivedAt int64  `json:"received_at"`
		} `json:"witnessed"`
	} `json:"devices"`
}

// scenarioFixture is one directory of testdata/checks.
type scenarioFixture struct {
	name   string
	dir    string
	exp    checkScenario
	lines  [][]byte
	events []event.Event
}

func checksRoot(t *testing.T) string {
	t.Helper()
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "..", "checks")
}

// loadScenarios reads every scenario of testdata/checks. Every directory
// there but Python's __pycache__ is a scenario and must hold an
// expected.json (the plan's "one expected file per scenario"), so a
// scenario can't go unchecked by missing one.
func loadScenarios(t *testing.T) []*scenarioFixture {
	t.Helper()
	root := checksRoot(t)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []*scenarioFixture
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		if !e.IsDir() || e.Name() == "__pycache__" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, ExpectedFile)); err != nil {
			t.Fatalf("testdata/checks/%s has no %s; every scenario has one", e.Name(), ExpectedFile)
		}
		s := &scenarioFixture{name: e.Name(), dir: dir}
		if err := readJSON(filepath.Join(dir, ExpectedFile), &s.exp); err != nil {
			t.Fatalf("%s/%s: %v", s.name, ExpectedFile, err)
		}
		b, err := os.ReadFile(filepath.Join(dir, EventsFile))
		if err != nil {
			t.Fatal(err)
		}
		if len(b) == 0 || b[len(b)-1] != '\n' {
			t.Fatalf("%s/%s must end with a newline", s.name, EventsFile)
		}
		for i, line := range bytes.Split(b[:len(b)-1], []byte("\n")) {
			var ev event.Event
			if err := json.Unmarshal(line, &ev); err != nil {
				t.Fatalf("%s/%s line %d: %v", s.name, EventsFile, i+1, err)
			}
			s.lines = append(s.lines, line)
			s.events = append(s.events, ev)
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		t.Fatal("no scenarios under testdata/checks")
	}
	return out
}

// TestCheckScenariosCoverDesign pins what the plan asks of WP-FX6: for each
// of CC1 to CC11 a scenario that fails and one that passes, a scenario where
// CC1 isn't computable, and failing scenarios that each fail one check.
func TestCheckScenariosCoverDesign(t *testing.T) {
	scenarios := loadScenarios(t)
	results := map[string]map[string][]string{}
	for _, cc := range checkNames {
		results[cc] = map[string][]string{}
	}
	for _, s := range scenarios {
		for cc, r := range s.exp.Checks {
			if _, ok := results[cc]; ok {
				results[cc][r.Result] = append(results[cc][r.Result], s.name)
			}
		}
	}
	for _, cc := range checkNames {
		t.Run(cc+" has a passing and a failing scenario", func(t *testing.T) {
			if len(results[cc]["pass"]) == 0 || len(results[cc]["fail"]) == 0 {
				t.Errorf("%s passes in %q and fails in %q", cc, results[cc]["pass"], results[cc]["fail"])
			}
		})
	}
	t.Run("CC1 is not computable on a harness with no log reader", func(t *testing.T) {
		found := false
		for _, s := range scenarios {
			r, ok := s.exp.Checks["CC1"]
			if !ok || r.Result != "not_computable" || r.Counts["not_computable"] == 0 {
				continue
			}
			for _, it := range r.Items {
				var item struct {
					Harness string `json:"harness"`
					Status  string `json:"status"`
				}
				if err := json.Unmarshal(it, &item); err != nil {
					t.Fatal(err)
				}
				if item.Status == "not_computable" && item.Harness != "" && !slices.Contains(logReaders, item.Harness) {
					found = true
				}
			}
		}
		if !found {
			t.Error("no scenario where CC1 isn't computable because a hook prompt's harness has no v1 log reader")
		}
	})
	for _, s := range scenarios {
		t.Run(s.name+" fails at most one check, so each check is proven on its own", func(t *testing.T) {
			if len(s.exp.Fails) > 1 {
				t.Errorf("fails %q", s.exp.Fails)
			}
		})
	}
}

// TestCheckScenarioEvents checks every event against pkg/event, the JSON
// Schema and its own encoding, and each device's events as a stream.
func TestCheckScenarioEvents(t *testing.T) {
	sv := loadEventSchema(t)
	for _, s := range loadScenarios(t) {
		t.Run(s.name+" every event is valid and pkg/event's own encoding", func(t *testing.T) {
			for i, e := range s.events {
				where := fmt.Sprintf("%s line %d", EventsFile, i+1)
				if err := e.Validate(); err != nil {
					t.Errorf("%s: %v", where, err)
				}
				v, err := decodeJSON(s.lines[i])
				if err != nil {
					t.Fatalf("%s: %v", where, err)
				}
				if errs := sv.validate(v); len(errs) > 0 {
					t.Errorf("%s: %s", where, strings.Join(errs, "; "))
				}
				b, err := json.Marshal(e)
				if err != nil {
					t.Fatalf("%s: %v", where, err)
				}
				if !bytes.Equal(b, s.lines[i]) {
					t.Errorf("%s re-encodes differently:\n got %s\nwant %s", where, b, s.lines[i])
				}
			}
		})
		t.Run(s.name+" each device's events are a gapless stream in time order", func(t *testing.T) {
			last := map[string]event.Event{}
			mono := map[string]int64{}
			ids := map[string]bool{}
			for i, e := range s.events {
				where := fmt.Sprintf("%s line %d", EventsFile, i+1)
				if i > 0 && e.T < s.events[i-1].T {
					t.Errorf("%s: t %d is before the previous line's %d", where, e.T, s.events[i-1].T)
				}
				if prev, ok := last[e.DeviceID]; ok && e.Seq != prev.Seq+1 {
					t.Errorf("%s: seq %d follows %d on device %s", where, e.Seq, prev.Seq, e.DeviceID)
				}
				last[e.DeviceID] = e
				if ids[e.EventID] {
					t.Errorf("%s: event_id %s repeats", where, e.EventID)
				}
				ids[e.EventID] = true
				if got := uuidTime(e.EventID); got != e.T {
					t.Errorf("%s: event_id carries time %d, but t is %d", where, got, e.T)
				}
				if m, ok := mono[e.BootID]; ok && e.Mono < m {
					t.Errorf("%s: mono goes back within boot %s", where, e.BootID)
				}
				mono[e.BootID] = e.Mono
			}
			var listed []string
			for _, d := range s.exp.Devices {
				listed = append(listed, d.DeviceID)
				if _, ok := last[d.DeviceID]; !ok {
					t.Errorf("device %s has no events", d.DeviceID)
				}
			}
			for d := range last {
				if !slices.Contains(listed, d) {
					t.Errorf("events of device %s, which expected.json doesn't list", d)
				}
			}
		})
	}
}

// TestCheckScenarioResults checks each expected.json on its own terms.
func TestCheckScenarioResults(t *testing.T) {
	root := checksRoot(t)
	errata := filepath.Join(root, "..", "..", "docs", "errata")
	for _, s := range loadScenarios(t) {
		exp := s.exp
		t.Run(s.name+" states a result for every check, or why it isn't exercised", func(t *testing.T) {
			if exp.Scenario != s.name {
				t.Errorf("scenario %q in directory %s", exp.Scenario, s.name)
			}
			var fails []string
			for _, cc := range checkNames {
				r, checked := exp.Checks[cc]
				why, skipped := exp.NotExercised[cc]
				if checked == skipped {
					t.Errorf("%s: in checks %v, in not_exercised %v; exactly one of them", cc, checked, skipped)
					continue
				}
				if skipped {
					if why == "" {
						t.Errorf("%s: not_exercised says nothing", cc)
					}
					continue
				}
				switch r.Result {
				case "pass", "fail":
					if r.Reason != "" {
						t.Errorf("%s: a %s has no reason; only not computable does", cc, r.Result)
					}
				case "not_computable":
					if r.Reason == "" {
						t.Errorf("%s: not computable without a reason", cc)
					}
				default:
					t.Errorf("%s: result %q", cc, r.Result)
				}
				if len(r.Counts) == 0 {
					t.Errorf("%s: no counts", cc)
				}
				if r.Result == "fail" {
					fails = append(fails, cc)
					if len(r.Items) == 0 {
						t.Errorf("%s: a failure names the items that failed", cc)
					}
				}
			}
			for cc := range exp.Checks {
				if !slices.Contains(checkNames, cc) {
					t.Errorf("unknown check %q", cc)
				}
			}
			if !slices.Equal(fails, exp.Fails) {
				t.Errorf("fails lists %q, the results fail %q", exp.Fails, fails)
			}
			if exp.KnownLimit != nil && len(exp.Fails) > 0 {
				t.Error("a known-limit scenario is one the checks pass")
			}
		})
		t.Run(s.name+" CC5 and CC6, which no code here recomputes, say what they rest on", func(t *testing.T) {
			if r, ok := exp.Checks["CC6"]; ok && r.Basis == "" {
				t.Error("CC6 has no basis")
			}
			if r, ok := exp.Checks["CC5"]; ok && r.Result != "not_computable" && r.Basis == "" {
				if _, counted := r.Counts["slots_credited"]; !counted {
					t.Error("CC5 states neither its slot counts nor a basis")
				}
			}
		})
		t.Run(s.name+" names a submission, its live buckets and existing errata", func(t *testing.T) {
			w := exp.Submission.Window
			if !w.Start.Before(w.End) || !w.Start.Truncate(time.Minute).Equal(w.Start) || !w.End.Truncate(time.Minute).Equal(w.End) {
				t.Errorf("window %s to %s", w.Start, w.End)
			}
			if len(exp.Submission.Projects) == 0 {
				t.Error("a submission has projects")
			}
			if exp.Live.From != "rescore" {
				t.Errorf("live.from %q", exp.Live.From)
			}
			r, ok := exp.Checks["CC6"]
			if ok && (len(exp.Live.Mutations) > 0) != (r.Result == "fail") {
				t.Errorf("CC6 %s with %d mutated minutes", r.Result, len(exp.Live.Mutations))
			}
			reveal := 0
			for _, d := range exp.Devices {
				if d.Reveals {
					reveal++
				}
			}
			if reveal != 1 {
				t.Errorf("%d devices reveal; every scenario has one", reveal)
			}
			for _, name := range exp.Errata {
				if _, err := os.Stat(filepath.Join(errata, name+".md")); err != nil {
					t.Errorf("erratum %q: %v", name, err)
				}
			}
		})
	}
}

// TestCheckScenarioLedgers recomputes each revealed batch's root and head
// over the records it reveals and the server's stored prev. A batch that
// doesn't reproduce, or that the server stored for the re-score range and
// the reveal leaves out, must be one CC7's expected items name, and every
// revealed body must hold the event as Shareable writes it.
func TestCheckScenarioLedgers(t *testing.T) {
	for _, s := range loadScenarios(t) {
		t.Run(s.name+" the witnessed rows chain and arrive in order, and the re-score range is DESIGN 6.5's", func(t *testing.T) {
			var led checkLedger
			if err := readJSON(filepath.Join(s.dir, "ledger.json"), &led); err != nil {
				t.Fatal(err)
			}
			w := s.exp.Submission.Window
			for _, dev := range led.Devices {
				rng := dev.RescoreRange
				if rng.Start != w.Start.UnixMilli()-900_000 || rng.End != w.End.UnixMilli()+900_000 {
					t.Errorf("%s: re-score range %d to %d; DESIGN 6.5 with no checkpoint gives W's start - 900 s to its end + 900 s", dev.DeviceID, rng.Start, rng.End)
				}
				if n := len(dev.Witnessed); n == 0 || rng.FirstB != dev.Witnessed[0].B || rng.LastB != dev.Witnessed[n-1].B {
					t.Errorf("%s: re-score range has batches %d to %d; in every scenario it covers every witnessed row", dev.DeviceID, rng.FirstB, rng.LastB)
				}
				for i := 1; i < len(dev.Witnessed); i++ {
					a, b := dev.Witnessed[i-1], dev.Witnessed[i]
					if b.B != a.B+1 || b.Prev != a.Head {
						t.Errorf("%s: witnessed batch %d follows %d without linking to its head", dev.DeviceID, b.B, a.B)
					}
					if b.ReceivedAt <= a.ReceivedAt {
						t.Errorf("%s: batch %d received at %d, not after batch %d at %d", dev.DeviceID, b.B, b.ReceivedAt, a.B, a.ReceivedAt)
					}
				}
			}
		})
		t.Run(s.name+" the reveal covers the re-score range and reproduces the witnessed batches, except where CC7 fails", func(t *testing.T) {
			var led checkLedger
			if err := readJSON(filepath.Join(s.dir, "ledger.json"), &led); err != nil {
				t.Fatal(err)
			}
			want := map[uint64]bool{}
			if r, ok := s.exp.Checks["CC7"]; ok {
				for _, it := range r.Items {
					var item struct {
						B uint64 `json:"b"`
					}
					if err := json.Unmarshal(it, &item); err != nil {
						t.Fatal(err)
					}
					want[item.B] = true
				}
			}
			bySeq := map[string]event.Event{}
			for _, e := range s.events {
				bySeq[fmt.Sprintf("%s/%d", e.DeviceID, e.Seq)] = e
			}
			got := map[uint64]bool{}
			for _, dev := range led.Devices {
				rows := map[uint64]int{}
				for i, w := range dev.Witnessed {
					rows[w.B] = i
				}
				revealed := map[uint64]bool{}
				for _, bt := range dev.Batches {
					revealed[bt.B] = true
				}
				for _, w := range dev.Witnessed {
					if w.B >= dev.RescoreRange.FirstB && w.B <= dev.RescoreRange.LastB && !revealed[w.B] {
						got[w.B] = true
					}
				}
				for _, bt := range dev.Batches {
					i, ok := rows[bt.B]
					if !ok {
						t.Fatalf("batch %d has no witnessed row", bt.B)
					}
					w := dev.Witnessed[i]
					var leaves [][]byte
					for _, r := range bt.Records {
						if r.RType < 1 || r.RType > len(ledgerKinds) {
							t.Fatalf("seq %d: rtype %d", r.Seq, r.RType)
						}
						hdr := binary.BigEndian.AppendUint64(nil, r.Seq)
						hdr = append(hdr, byte(r.RType))
						hdr = append(hdr, digest(r.ProjectKey, 16, "")...)
						hdr = append(hdr, digest(r.SessionKey, 8, event.NoSession)...)
						bh := sha256.Sum256([]byte(r.Body))
						leaves = append(leaves, merkleLeaf(append(hdr, bh[:]...)))
						checkBody(t, r.Seq, r.RType, r.ProjectKey, r.SessionKey, r.Body, bySeq[fmt.Sprintf("%s/%d", dev.DeviceID, r.Seq)])
					}
					rootB := merkleRoot(leaves)
					prev, _ := hex.DecodeString(w.Prev)
					h := sha256.New()
					h.Write([]byte{0x03})
					h.Write(prev)
					h.Write(rootB)
					h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(leaves))))
					h.Write(binary.BigEndian.AppendUint64(nil, bt.B))
					if hex.EncodeToString(rootB) != w.Root || hex.EncodeToString(h.Sum(nil)) != w.Head || uint64(len(leaves)) != w.N {
						got[bt.B] = true
					}
				}
			}
			for b := range got {
				if !want[b] {
					t.Errorf("batch %d doesn't reproduce, and CC7's items don't name it", b)
				}
			}
			for b := range want {
				if !got[b] {
					t.Errorf("CC7's items name batch %d, which reproduces", b)
				}
			}
		})
	}
}

// checkBody checks a revealed body against its header and against the
// event events.jsonl holds for it: e is that event as Shareable leaves it,
// byte for byte.
func checkBody(t *testing.T, seq uint64, rtype int, project, session, body string, ev event.Event) {
	t.Helper()
	var b struct {
		N string          `json:"n"`
		E json.RawMessage `json:"e"`
		F json.RawMessage `json:"f"`
		C json.RawMessage `json:"c"`
	}
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		t.Fatalf("seq %d: body: %v", seq, err)
	}
	if !strings.HasPrefix(body, `{"n":"`) || len(b.N) != 64 {
		t.Errorf("seq %d: a body starts with its 32-byte nonce in hex", seq)
	}
	if ev.Data == nil {
		t.Fatalf("seq %d: no event with this seq in %s", seq, EventsFile)
	}
	want, err := json.Marshal(ev.Shareable())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b.E, want) {
		t.Errorf("seq %d: body's e isn't the event after Shareable:\n got %s\nwant %s", seq, b.E, want)
	}
	if ledgerKinds[rtype-1] != ev.Kind() {
		t.Errorf("seq %d: rtype %d for a %s", seq, rtype, ev.Kind())
	}
	wantProject := ev.ProjectKey
	if strings.HasPrefix(string(ev.Kind()), "os.") {
		wantProject = ""
	}
	if project != wantProject || session != ev.SessionKey {
		t.Errorf("seq %d: header project %q and session %q, the event has %q and %q", seq, project, session, wantProject, ev.SessionKey)
	}
	if (len(b.F) > 0 || len(b.C) > 0) && ev.Kind() != event.KindPromptSubmit {
		t.Errorf("seq %d: only prompt.submit bodies carry f and c here", seq)
	}
}

// digest is a header's project or session digest (ledger-v1.md 4.3): the
// first n bytes of the key's SHA-256, or zeros for no key.
func digest(key string, n int, none string) []byte {
	if key == "" || key == none {
		return make([]byte, n)
	}
	s := sha256.Sum256([]byte(key))
	return s[:n]
}

func merkleLeaf(data []byte) []byte {
	s := sha256.Sum256(append([]byte{0x00}, data...))
	return s[:]
}

// merkleRoot is RFC 9162's tree over leaf hashes (ledger-v1.md 5.1).
func merkleRoot(hashes [][]byte) []byte {
	switch len(hashes) {
	case 0:
		s := sha256.Sum256(nil)
		return s[:]
	case 1:
		return hashes[0]
	}
	k := 1
	for k*2 < len(hashes) {
		k *= 2
	}
	s := sha256.Sum256(slices.Concat([]byte{0x01}, merkleRoot(hashes[:k]), merkleRoot(hashes[k:])))
	return s[:]
}

// TestCheckGeneratorReproduces runs gen.py into a temporary directory and
// checks that it writes exactly the checked-in scenarios, byte for byte. It
// needs python3; without it the test is skipped, except in CI, where it
// fails.
func TestCheckGeneratorReproduces(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("python3 is needed to check the check-fixture generator")
		}
		t.Skip("python3 not found")
	}
	root := checksRoot(t)
	out := t.TempDir()
	gen := filepath.Join(root, "gen.py")
	cmd := exec.Command(python, "-I", "-B", gen, "--out", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", gen, err, b)
	}
	files := func(dir string) []string {
		var names []string
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, sc := range entries {
			// Every directory but __pycache__ is a scenario, which
			// loadScenarios also requires to hold an expected.json.
			if !sc.IsDir() || sc.Name() == "__pycache__" {
				continue
			}
			inner, err := os.ReadDir(filepath.Join(dir, sc.Name()))
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range inner {
				names = append(names, filepath.Join(sc.Name(), f.Name()))
			}
		}
		return names
	}
	written, checkedIn := files(out), files(root)
	if !slices.Equal(written, checkedIn) {
		t.Fatalf("gen.py writes %q, testdata/checks holds %q", written, checkedIn)
	}
	for _, name := range written {
		t.Run(name+" is what gen.py writes", func(t *testing.T) {
			got, err := os.ReadFile(filepath.Join(out, name))
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s differs from what gen.py writes; run python3 -I testdata/checks/gen.py", name)
			}
		})
	}
}
