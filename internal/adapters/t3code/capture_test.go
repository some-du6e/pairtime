// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/pkg/event"
)

// captureDir is WP-ADE1's capture of T3 Code 0.1.10 driving Claude Code
// 2.1.330 from its web client (DESIGN 5.1's real fixture; its PROVENANCE.md
// says how it was made and scrubbed): store.sql, the rows of the tables
// testdata/schema.sql creates as T3 left them, with T3's own rowids;
// hooks.jsonl, the hook calls Claude Code made, without their payloads;
// and probes.json, what each prompt a hook saw was, which is the oracle.
const captureDir = "../../../testdata/fixtures/t3_code/0.1.10-nightly.20261112.4606/linux/web"

// captureHook is one of the capture's hook calls, as its logger wrote it:
// for a UserPromptSubmit, whether the prompt opens with T3's guest wrapper
// too, as the agent's device test reads it (OpensAsGuest).
type captureHook struct {
	Event       string `json:"event"`
	SessionID   string `json:"session_id"`
	PromptID    string `json:"prompt_id"`
	TNS         int64  `json:"t_ns"`
	GuestPrefix bool   `json:"guest_prefix"`
}

// captureProbe is what probes.json says a prompt a hook saw was: the
// message row that carried it, whether the run that delivered it was the
// message's own, and whether its text opens as T3 opens a guest's message.
type captureProbe struct {
	HookPrompt     int    `json:"hook_prompt"`
	PromptID       string `json:"prompt_id"`
	SessionID      string `json:"session_id"`
	Kind           string `json:"kind"`
	MessageRowID   int64  `json:"message_rowid"`
	MessageID      string `json:"message_id"`
	CreatedBy      string `json:"created_by"`
	CreationSource string `json:"creation_source"`
	OwnRun         bool   `json:"run_is_messages_own"`
	HookSession    bool   `json:"run_attempt_native_thread_id_is_hook_session"`
	GuestPrefix    bool   `json:"guest_prefix"`
}

// recorded says whether the rules give the probe's row a record: a
// message a person sent from a client, with a run of its own, that doesn't
// open as a guest's (SM 2.2 rule 9).
func (p captureProbe) recorded() bool {
	return p.CreatedBy == "user" && (p.CreationSource == "web" || p.CreationSource == "mobile") && p.OwnRun && !p.GuestPrefix
}

// steered says whether the probe's row is a guest's message the owner
// steered into another message's running turn, whose prompt a Steer must
// cover (decision 4 of docs/errata/2026-11-23-t3-reader.md).
func (p captureProbe) steered() bool { return p.GuestPrefix && !p.OwnRun }

// replayed is what the reader did with the capture.
type replayed struct {
	probes []captureProbe
	// prompts are the hook's UserPromptSubmit calls, in order, and
	// promptAt and promptEvent each one's t in Unix ms and the event_id the
	// replay gives its prompt.submit, by prompt_id.
	prompts     []captureHook
	promptAt    map[string]int64
	promptEvent map[string]string
	// stamps are each message row's created_at, as T3 wrote it and in Unix
	// ms, by rowid.
	stamps map[int64]string
	ms     map[int64]int64
	// records are every record a read wrote, by event id; answers Pair's
	// answer for each prompt, and steersAt what the reader said of steered
	// turns right after that answer, which the agent marks the prompt from
	// (internal/agent/apps.go), by prompt_id; guests the guests' messages
	// the reads reported; steers and counts what the reader said after the
	// last read; folders the folders its resolver was asked about.
	records  map[string]Message
	answers  map[string]Pairing
	steersAt map[string][]Steer
	guests   []GuestRun
	steers   []Steer
	counts   Counts
	folders  []string
	// lag is the reader's RELAY_SEEN_LAG_S, in ms.
	lag int64
	// threadFolders are the folders the capture's threads ran in, as
	// PROVENANCE.md says T3 runs them: a thread's worktree, else its
	// project's root.
	threadFolders []string
}

// promptEventID is the event_id the replay gives the n-th prompt the hook
// saw, at t, Unix ms: a UUIDv7 at t, as the agent gives every
// prompt.submit, since a record names its prompt by that id.
func promptEventID(t int64, n int) string {
	return fmt.Sprintf("%08x-%04x-7000-8000-%012x", uint64(t)>>16, uint64(t)&0xffff, n)
}

// readJSONL reads the file at path as one JSON value of type T per line,
// skipping blank lines.
func readJSONL[T any](t *testing.T, path string) []T {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []T
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			t.Fatalf("%s: %v", filepath.Base(path), err)
		}
		out = append(out, v)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// replayCapture builds a store from testdata/schema.sql and the capture's
// store.sql with the sqlite3 tool, held open as T3 holds its own, and
// replays the capture through a reader: the message rows go in at their
// stamps, in T3's rowid order, which the capture's stamps follow; each
// prompt the hook saw is asked about when the hook saw it, as the agent
// asks about every prompt of a session T3's server started, marked guest
// when it opens with T3's guest wrapper, as the agent marks it; and a read
// runs every 10 s between, as the agent's apps job reads while T3 runs.
// The other tables' rows, runs and attempts among them, are there from the
// start as T3 left them, so each run has its start and end when its
// message is read. Rows T3 rewrote, the promoted guest's message and the
// follow-up, are there only as T3 left them (PROVENANCE.md).
func replayCapture(t *testing.T) *replayed {
	t.Helper()
	tool := sqlite3Tool(t)
	ctx := context.Background()
	out := &replayed{promptAt: map[string]int64{}, promptEvent: map[string]string{},
		stamps: map[int64]string{}, ms: map[int64]int64{}, records: map[string]Message{}, answers: map[string]Pairing{},
		steersAt: map[string][]Steer{}}

	raw, err := os.ReadFile(filepath.Join(captureDir, "probes.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &out.probes); err != nil {
		t.Fatalf("probes.json: %v", err)
	}
	for _, h := range readJSONL[captureHook](t, filepath.Join(captureDir, "hooks.jsonl")) {
		if h.Event == "UserPromptSubmit" {
			at := h.TNS / int64(time.Millisecond)
			out.prompts = append(out.prompts, h)
			out.promptAt[h.PromptID], out.promptEvent[h.PromptID] = at, promptEventID(at, len(out.prompts))
		}
	}
	if len(out.prompts) == 0 || len(out.prompts) != len(out.probes) {
		t.Fatalf("hooks.jsonl has %d prompts and probes.json %d probes, want one probe for each prompt", len(out.prompts), len(out.probes))
	}
	for _, p := range out.probes {
		if _, ok := out.promptAt[p.PromptID]; !ok {
			t.Fatalf("probe %d names a prompt hooks.jsonl doesn't have", p.HookPrompt)
		}
	}

	storeSQL, err := os.ReadFile(filepath.Join(captureDir, "store.sql"))
	if err != nil {
		t.Fatal(err)
	}
	rows := map[int64]string{}
	var rest []string
	for i, line := range strings.Split(string(storeSQL), "\n") {
		// Git for Windows checks text out with CRLF line endings by
		// default, so a line can end in a carriage return there.
		line = strings.TrimSuffix(line, "\r")
		switch {
		case strings.HasPrefix(line, "INSERT INTO "+tMessages+" "):
			_, values, ok := strings.Cut(line, " VALUES (")
			id, _, _ := strings.Cut(values, ",")
			n, err := strconv.ParseInt(id, 10, 64)
			if !ok || err != nil {
				t.Fatalf("store.sql line %d is a message row whose rowid doesn't read", i+1)
			}
			rows[n] = line
		case strings.HasPrefix(line, "INSERT INTO "):
			rest = append(rest, line)
		case line != "" && !strings.HasPrefix(line, "--"):
			t.Fatalf("store.sql line %d is neither an INSERT nor a comment", i+1)
		}
	}

	// The stamps the rows go in at, and the folders the threads run in,
	// from a copy of the whole store.
	src := filepath.Join(t.TempDir(), "capture.sqlite")
	load := exec.Command(tool, "-batch", "-bail", "-init", os.DevNull, src)
	load.Stdin = strings.NewReader(schemaSQL + "\n" + string(storeSQL))
	if b, err := load.CombinedOutput(); err != nil {
		t.Fatalf("loading the capture's store: %v\n%s", err, b)
	}
	got, err := (&Runner{Path: tool}).Query(ctx, src,
		"SELECT "+tag("m")+", "+hx("rowid")+", "+hx("created_at")+" FROM "+tMessages+" ORDER BY rowid;\n"+
			"SELECT "+tag("f")+", "+hx("COALESCE(json_extract(t.payload_json, '$.worktreePath'), p.workspace_root)")+
			" FROM "+tThreads+" AS t JOIN "+tProjects+" AS p ON p.project_id = t.project_id ORDER BY t.rowid;\n")
	if err != nil {
		t.Fatalf("reading the capture's store: %v", err)
	}
	for _, x := range got {
		switch {
		case len(x) == 3 && x[0].s == "m":
			n, ok1 := rowid(x[1])
			at, ok2 := ms(x[2])
			if !ok1 || !ok2 {
				t.Fatalf("the capture's store has a message row whose rowid or stamp doesn't read: %+v", x)
			}
			out.stamps[n], out.ms[n] = x[2].s, at
		case len(x) == 2 && x[0].s == "f" && !x[1].null:
			out.threadFolders = append(out.threadFolders, x[1].s)
		default:
			t.Fatalf("reading the capture's store gave %+v", x)
		}
	}
	if len(out.stamps) != len(rows) {
		t.Fatalf("store.sql has %d message rows, and the store built from it %d", len(rows), len(out.stamps))
	}
	order := slices.Sorted(maps.Keys(out.ms))
	for i := 1; i < len(order); i++ {
		if out.ms[order[i]] < out.ms[order[i-1]] {
			// Written at its stamp, such a row would go in behind the
			// reader's cursor, which T3 never does.
			t.Fatalf("message row %d is stamped before row %d, so writing the rows at their stamps wouldn't keep T3's order", order[i], order[i-1])
		}
	}

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "userdata"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := &store{t: t, sqlite3: tool, home: home, path: filepath.Join(home, "userdata", "statev2.sqlite")}
	s.add("PRAGMA journal_mode=WAL;")
	s.add(schemaSQL)
	s.add(strings.Join(rest, "\n"))
	s.flush()
	s.open()
	c := &clock{}
	r, err := New(Options{
		Stores: []Store{{Path: s.path, From: FromDefault}},
		SQLite: &Runner{Path: tool},
		Keys:   adapters.FixtureKeys(),
		Folder: func(dir string) Folder {
			if !slices.Contains(out.folders, dir) {
				out.folders = append(out.folders, dir)
			}
			return Folder{State: FolderTracked, Project: "p_repo"}
		},
		Now:  c.now,
		Zone: time.UTC,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The timeline: at one millisecond, rows go in first, in rowid order,
	// then a prompt is asked about, then the read the job asked for runs.
	type step struct {
		at    int64
		rank  int   // 0 a row, 1 a prompt, 2 a read
		rowid int64 // the row to write
		hook  *captureHook
	}
	var steps []step
	for _, n := range order {
		steps = append(steps, step{at: out.ms[n], rowid: n})
	}
	last := int64(0)
	for i := range out.prompts {
		h := &out.prompts[i]
		steps = append(steps, step{at: out.promptAt[h.PromptID], rank: 1, hook: h})
		last = max(last, out.promptAt[h.PromptID])
	}
	for at := out.ms[order[0]] - 10_000; at <= last+60_000; at += 10_000 {
		steps = append(steps, step{at: at, rank: 2})
	}
	slices.SortStableFunc(steps, func(a, b step) int { return cmp.Or(cmp.Compare(a.at, b.at), cmp.Compare(a.rank, b.rank)) })
	for _, st := range steps {
		if st.rank == 0 {
			s.add(rows[st.rowid])
			continue
		}
		s.flush()
		c.t = time.UnixMilli(st.at)
		var b Batch
		if st.rank == 2 {
			b, err = r.Poll(ctx)
		} else {
			var p Pairing
			p, b, err = r.Pair(ctx, Prompt{SessionKey: session(st.hook.SessionID), T: st.at, EventID: out.promptEvent[st.hook.PromptID],
				Guest: st.hook.GuestPrefix})
			out.answers[st.hook.PromptID] = p
			out.steersAt[st.hook.PromptID] = r.Steers()
		}
		if err != nil {
			t.Fatalf("the read at %s failed: %v", c.t.UTC().Format(time.RFC3339Nano), err)
		}
		for _, m := range b.Messages {
			out.records[m.EventID] = m
		}
		out.guests = append(out.guests, b.Guests...)
	}
	out.steers = r.Steers()
	out.counts = r.Status().Counts
	out.lag = r.lag
	return out
}

// TestCapture replays WP-ADE1's capture of T3 Code through the reader
// (replayCapture; Q14 of #225) and holds what it does to probes.json:
// which messages it records, which prompt each pairs with, and which
// prompts it reports a guest's words reached as a steer.
func TestCapture(t *testing.T) {
	c := replayCapture(t)
	keys := adapters.FixtureKeys()
	record := func(p captureProbe) string {
		return recordID(keys, p.MessageID, c.stamps[p.MessageRowID], c.ms[p.MessageRowID])
	}
	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"a message a person sent from T3's web client with a run of its own gets a record, and a steer, a guest's message or T3's notification gets none",
			func(t *testing.T) {
				want := 0
				for _, p := range c.probes {
					if p.recorded() {
						want++
					}
					if _, ok := c.records[record(p)]; ok != p.recorded() {
						t.Errorf("probe %d (%s): a record of its row: %v, want %v", p.HookPrompt, p.Kind, ok, p.recorded())
					}
				}
				if len(c.records) != want {
					t.Errorf("the reads wrote %d records, want %d", len(c.records), want)
				}
			}},
		{"a record says what the row says: its stamp, the web client, no declaration, Claude Code's session the hook names, and a read within RELAY_SEEN_LAG_S of the stamp",
			func(t *testing.T) {
				for _, p := range c.probes {
					m, ok := c.records[record(p)]
					if !ok {
						continue
					}
					seen := m.SeenAt.UnixMilli() - m.Data.SentAt
					if m.Data.SentAt != c.ms[p.MessageRowID] || m.Data.Client != event.RelayClientWeb ||
						m.Data.Declared != event.RelayDeclaredNone || m.Harness != event.HarnessClaudeCode ||
						m.SessionKey != session(p.SessionID) || m.ProjectKey != "p_repo" || m.Data.LenChars == 0 ||
						seen < -stampAhead || seen > c.lag {
						t.Errorf("probe %d (%s): the record is %+v, read %d ms after its stamp", p.HookPrompt, p.Kind, m, seen)
					}
					if err := m.Data.Validate(); err != nil {
						t.Errorf("probe %d (%s): the record isn't a valid relay.message: %v", p.HookPrompt, p.Kind, err)
					}
				}
			}},
		{"each record pairs with the prompt its own run delivered, by the session the hook and the run's attempt share, and every other prompt pairs with nothing",
			func(t *testing.T) {
				named := map[string]string{}
				for id, m := range c.records {
					if m.Data.Prompt != "" {
						named[m.Data.Prompt] = id
					}
				}
				for _, p := range c.probes {
					if !p.HookSession {
						t.Fatalf("probe %d (%s): probes.json says its run's attempt names another session than the hook's", p.HookPrompt, p.Kind)
					}
					a := c.answers[p.PromptID]
					got := a.Relay
					if id, ok := named[c.promptEvent[p.PromptID]]; ok {
						if got != "" {
							t.Errorf("probe %d (%s): the prompt has relay %s and a record names it too", p.HookPrompt, p.Kind, got)
						}
						got = id
					}
					want := ""
					if p.recorded() {
						want = record(p)
					}
					if got != want {
						t.Errorf("probe %d (%s): the prompt pairs with %q, want %q", p.HookPrompt, p.Kind, got, want)
					}
				}
			}},
		{"every read knows the prompt it was asked about, and no prompt is a guest's run's, since no guest's message in the capture has a run of its own",
			func(t *testing.T) {
				for _, p := range c.probes {
					if a := c.answers[p.PromptID]; !a.Known || a.Guest {
						t.Errorf("probe %d (%s): the answer is %+v, want a known one that is no guest's", p.HookPrompt, p.Kind, a)
					}
				}
			}},
		{"each guest's message the owner steered into a running turn is reported as steered, and its turn as a Steer that covers the prompt that carried it and no other prompt, right after that prompt's Pair, when the agent marks it, and after the last read, for its later copies",
			func(t *testing.T) {
				var want []GuestRun
				for _, p := range c.probes {
					if p.GuestPrefix {
						want = append(want, GuestRun{SentAt: c.ms[p.MessageRowID], Steered: !p.OwnRun})
					}
					at := c.promptAt[p.PromptID]
					for _, when := range []struct {
						name   string
						steers []Steer
					}{
						{"right after its Pair", c.steersAt[p.PromptID]},
						{"after the last read", c.steers},
					} {
						covered := slices.ContainsFunc(when.steers, func(s Steer) bool {
							return slices.Contains(s.Sessions, session(p.SessionID)) && at >= s.From && (s.To == 0 || at <= s.To)
						})
						if covered != p.steered() {
							t.Errorf("probe %d (%s): %s, a Steer covers its prompt: %v, want %v (steers %+v)",
								p.HookPrompt, p.Kind, when.name, covered, p.steered(), when.steers)
						}
					}
				}
				if !slices.Equal(c.guests, want) {
					t.Errorf("the reads reported the guests %+v, want %+v", c.guests, want)
				}
			}},
		{"the counts are what the capture's rows give: every message recorded paired, the steers had no run of their own, and no row was read late, stamped ahead, edited, marked or of another provider",
			func(t *testing.T) {
				var want Counts
				for _, p := range c.probes {
					switch {
					case p.recorded():
						want.Witnessed++
						want.Paired++
					case p.GuestPrefix:
						want.Guests++
					case p.CreatedBy == "user" && !p.OwnRun:
						want.NoRun++
					}
				}
				if c.counts != want {
					t.Errorf("the counts are %+v, want %+v", c.counts, want)
				}
			}},
		{"a thread with no worktree runs in its project's root and one with a worktree in its worktree, and the resolver hears of those two folders alone",
			func(t *testing.T) {
				got := slices.Clone(c.folders)
				want := slices.Clone(c.threadFolders)
				slices.Sort(got)
				slices.Sort(want)
				if len(want) != 2 || want[0] == want[1] || !slices.Equal(got, want) {
					t.Errorf("the resolver heard of %q, want the threads' folders %q", got, want)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.check)
	}
}
