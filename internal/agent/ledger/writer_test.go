// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/sched"
	osclock "github.com/some-du6e/pairtime/internal/platform/clock"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger"
	"github.com/some-du6e/pairtime/pkg/ledger/features"
)

// TestNumbers checks decision E-sm-1.1-seq-gaps' numbering (decided; spec
// wording pending): one sequence over every record the device writes, from
// 1, with no gap or repeat.
func TestNumbers(t *testing.T) {
	cases := []struct {
		name  string
		steps []string // kinds to write; "bad" is a prompt Write refuses
		want  []uint64 // the sequence number each step gets, 0 for none
	}{
		{"records take the numbers from 1 in order", []string{"os.presence", "prompt.submit", "agent.final"},
			[]uint64{1, 2, 3}},
		{"checkpoints and upload-failure notes take numbers from the same sequence",
			[]string{"prompt.submit", "checkpoint", "agent.tool", "note", "os.power"}, []uint64{1, 2, 3, 4, 5}},
		{"a record Write refuses takes no number", []string{"os.presence", "bad", "os.presence"}, []uint64{1, 0, 2}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			w := open(t, dir, newClock(testT))
			rng := rand.New(rand.NewPCG(1, 2))
			for i, kind := range c.steps {
				var s uint64
				var err error
				if kind == "bad" {
					// A hook prompt says it has characters and comes
					// without them.
					in := sampleInput(rng, "prompt.submit", testT, "s_1", "p_web")
					in.Event.Data.(*event.PromptSubmit).LenChars, in.Text = 30, nil
					s, err = w.Write(in)
				} else {
					s, _, err = write(w, rng, kind, testT+int64(i)*1000)
				}
				if (err != nil) != (c.want[i] == 0) || s != c.want[i] {
					t.Fatalf("step %d, %s: got s = %d and %v, want s = %d", i, kind, s, err, c.want[i])
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			for _, r := range records(verify(t, dir, keysOf(t, testKDev))) {
				if _, ok := r.Header.RType().EventKind(); !ok {
					continue
				}
				if ev := eventOf(t, r); ev.Seq != r.Header.Seq() {
					t.Errorf("record %d holds an event whose seq is %d", r.Header.Seq(), ev.Seq)
				}
			}
		})
	}
}

// TestContent checks what each record commits and the features it
// carries (ledger-v1 10.1, 10.7), and the prompt classes from pkg/score.
func TestContent(t *testing.T) {
	keys := keysOf(t, testKDev)
	long := strings.Repeat("abc€ ", 4000) // 28,000 bytes, whose 16 KiB cut falls inside a euro sign
	cases := []struct {
		name   string
		before []Input // written first, in the same session
		in     Input
		// text is what the record commits, nil for no "c".
		text  []byte
		class features.Class // "" for a kind with no class
		check func(t *testing.T, f features.F, ev event.Event)
	}{
		{name: "a hook prompt commits its text, and an error pasted with few typed characters is error_paste",
			in:   prompt("TypeError: x is undefined", 0),
			text: []byte("TypeError: x is undefined"), class: features.ErrorPaste,
			check: func(t *testing.T, f features.F, ev event.Event) {
				if f.MH == nil || f.TH == nil || f.ES == nil {
					t.Errorf("f %+v lacks the sketch, the template or the error signature", f)
				}
			}},
		{name: "a go-ahead is low_effort, from the text test the writer applies to the text",
			in: prompt("ok", 0), text: []byte("ok"), class: features.LowEffort,
			check: func(t *testing.T, f features.F, ev event.Event) {
				if p := ev.Data.(*event.PromptSubmit); !p.LowEffort {
					t.Error("the stored prompt's low_effort is false")
				}
			}},
		{name: "a prompt of SUBSTANTIVE_CHARS typed characters is substantive",
			in: prompt("fix the login redirect loop on mobile", 0), text: []byte("fix the login redirect loop on mobile"),
			class: features.Substantive},
		{name: "a prompt after an output that asked a question is an answer",
			before: []Input{harness(&event.AgentOutput{TurnKey: "t_0", Words: 7, IsFinal: true, AsksQuestion: true},
				[]byte("Which one, the modal or the drawer?"))},
			in: prompt("the drawer", 0), text: []byte("the drawer"), class: features.Answer},
		{name: "a prompt after a turn that stopped on a question is an answer",
			before: []Input{harness(&event.AgentStop{TurnKey: "t_0", StopReason: event.StopQuestion}, nil)},
			in:     prompt("ok", 0), text: []byte("ok"), class: features.Answer},
		{name: "a plugin's output asking a question doesn't make a hooked session's go-ahead an answer",
			before: []Input{
				harness(&event.SessionStart{StartKind: event.StartStartup, Entrypoint: event.EntrypointCLI,
					ParentClass: event.ParentTerminalApp, HasTTY: true}, nil),
				viaPlugin(harness(&event.AgentOutput{TurnKey: "t_0", Words: 1, IsFinal: true, AsksQuestion: true}, []byte("which?"))),
			},
			in: prompt("ok", 0), text: []byte("ok"), class: features.LowEffort},
		{name: "a bridge prompt has no text, commits nothing, and its f is v and pc alone",
			in: bridgePrompt(64), class: features.Substantive,
			check: func(t *testing.T, f features.F, ev event.Event) {
				if f.MH != nil || f.TH != nil || f.ES != nil {
					t.Errorf("f %+v has features of a text the device never had", f)
				}
			}},
		{name: "a final agent message commits its first 16 KiB, cut at a character boundary",
			in:   harness(&event.AgentOutput{TurnKey: "t_1", Words: 8000, IsFinal: true}, []byte(long)),
			text: ledger.CommittedMessage([]byte(long))},
		{name: "a plugin's final message that came without its text commits nothing",
			in: viaPlugin(harness(&event.AgentOutput{TurnKey: "t_1", Words: 9, IsFinal: true}, nil))},
		{name: "a message that isn't the turn's final one commits nothing",
			in: harness(&event.AgentOutput{TurnKey: "t_1", Words: 9}, nil)},
		{name: "a track label commits like a prompt and is classed by its length",
			in:   track("add tests for the parser please"),
			text: []byte("add tests for the parser please"), class: features.Substantive},
		{name: "an agent.tool carries its keyed hunks and command",
			in: Input{Event: harness(&event.AgentTool{ToolClass: event.ToolEdit, LinesAdded: 5, LinesRemoved: 3,
				TStart: testT, TEnd: testT}, nil).Event,
				Edits:   []features.Edit{{Path: "src/nav.js", OldStart: 10, OldLines: 3, NewStart: 10, NewLines: 5}},
				Command: "npm  test"},
			check: func(t *testing.T, f features.F, ev event.Event) {
				ch, _ := keys.Features().Command("npm test")
				p, _ := keys.Features().Path("src/nav.js")
				want := []features.Hunk{{P: p, OS: 10, OL: 3, NS: 10, NL: 5}}
				if f.CH == nil || *f.CH != ch || !slices.Equal(f.H, want) {
					t.Errorf("f %+v, want hunks %+v and command %s", f, want, ch)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			w := open(t, dir, newClock(testT))
			for _, in := range c.before {
				if _, err := w.Write(in); err != nil {
					t.Fatal(err)
				}
			}
			s, err := w.Write(c.in)
			if err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			recs := records(verify(t, dir, keys))
			r := recs[s-1]
			b := bodyOf(t, r)
			switch {
			case c.text == nil && b.C != nil:
				t.Errorf("the record commits %+v, want no c", *b.C)
			case c.text != nil:
				want, err := keys.Commit(s, c.text)
				if err != nil {
					t.Fatal(err)
				}
				if b.C == nil || *b.C != want {
					t.Errorf("the record commits %+v, want %+v", b.C, want)
				}
			}
			f, _ := featuresOf(t, r)
			if f.PC != c.class {
				t.Errorf("f.pc is %q, want %q", f.PC, c.class)
			}
			if c.check != nil {
				c.check(t, f, eventOf(t, r))
			}
		})
	}
}

// TestQuestionsAfterRestart checks that a restart changes no prompt's
// f.pc: a prompt that answers a question asked before the agent restarted
// is an answer however many batches came between, as it is in a writer that
// never stopped (SM 2.2), and rows don't carry the questions the seal
// frames keep.
func TestQuestionsAfterRestart(t *testing.T) {
	cases := []struct {
		name    string
		asks    bool // the session's last output asked a question
		batches int  // batches sealed after it, before the restart
		want    features.Class
	}{
		{"an answer to a question asked just before a restart is an answer", true, 0, features.Answer},
		{"an answer to a question asked more than questionRows batches before a restart is an answer", true, questionRows + 5, features.Answer},
		{"an answer to a question asked two seal frames of questions before a restart is an answer", true, 2*questionRows + 1, features.Answer},
		{"a go-ahead after an output that asked nothing is low_effort after a restart too", false, questionRows + 5, features.LowEffort},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			w := open(t, dir, newClock(testT))
			if _, err := w.Write(harness(&event.AgentOutput{TurnKey: "t_0", Words: 7, IsFinal: true, AsksQuestion: c.asks},
				[]byte("Which one, the modal or the drawer?"))); err != nil {
				t.Fatal(err)
			}
			rng := rand.New(rand.NewPCG(29, 30))
			for range c.batches {
				if _, _, err := write(w, rng, "os.presence", testT); err != nil {
					t.Fatal(err)
				}
				if _, _, err := w.Seal(); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			w = open(t, dir, newClock(testT))
			s, err := w.Write(prompt("ok", 0))
			if err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if f, _ := featuresOf(t, records(verify(t, dir, keysOf(t, testKDev)))[s-1]); f.PC != c.want {
				t.Errorf("f.pc is %q, want %q", f.PC, c.want)
			}
			bf, err := readBatchesFile(filepath.Join(dir, batchesName))
			if err != nil {
				t.Fatal(err)
			}
			for _, rp := range bf.rows {
				if rp.Q != nil {
					t.Fatalf("the row of batch %d carries the questions", rp.B)
				}
			}
		})
	}
}

// harness is a hook record of session s_1 in project p_web.
func harness(p event.Payload, text []byte) Input {
	rng := rand.New(rand.NewPCG(3, 4))
	return Input{Event: event.Event{Envelope: env(rng, testT, "s_1", "p_web"), Data: p}, Text: text}
}

func viaPlugin(in Input) Input {
	in.Event.Via = event.ViaPlugin
	return in
}

// prompt is a hook prompt of text with pasted of its characters pasted.
func prompt(text string, pasted int) Input {
	n := len([]rune(text))
	return harness(&event.PromptSubmit{TurnKey: "t_1", LenChars: n, PastedChars: pasted, TypedLenEst: n - pasted,
		OriginDeclared: event.DeclaredNone}, []byte(text))
}

func bridgePrompt(n int) Input {
	in := harness(&event.PromptSubmit{TurnKey: "t_1", LenChars: n, TypedLenEst: n, OriginDeclared: event.DeclaredNone}, nil)
	in.Event.Source, in.Event.Via = event.SourceLog, event.ViaBridge
	return in
}

func track(label string) Input {
	in := harness(&event.CLITrack{Label: label, TextLen: len([]rune(label)), LogMatch: event.LogNoReader}, []byte(label))
	in.Event.Source, in.Event.Via = event.SourceCLI, event.ViaTrack
	return in
}

// TestRefusals checks records Write refuses, and that a refused record
// changes nothing on disk.
func TestRefusals(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	withProject := sampleInput(rng, "os.presence", testT, "", "")
	withProject.Event.ProjectKey = "p_web"
	invalid := sampleInput(rng, "os.power", testT, "", "")
	invalid.Event.Data = &event.OSPower{Op: "hibernate"}
	textOnTool := sampleInput(rng, "agent.tool", testT, "s_1", "p_web")
	textOnTool.Text = []byte("a tool commits no text")
	cases := []struct {
		name string
		in   Input
	}{
		{"an os.* event with a project_key is refused (ledger-v1 4.3)", withProject},
		{"an event that fails Validate is refused", invalid},
		{"text on a kind that commits none is refused", textOnTool},
		{"a hook prompt whose len_chars says it has text, sent without it, is refused", func() Input {
			in := prompt("fix it", 0)
			in.Text = nil
			return in
		}()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			w := open(t, dir, newClock(testT))
			if _, _, err := write(w, rng, "os.presence", testT); err != nil {
				t.Fatal(err)
			}
			size := fileSize(t, segPath(dir, dayOf(time.UnixMilli(testT))))
			if s, err := w.Write(c.in); err == nil {
				t.Fatalf("Write took it as record %d", s)
			}
			if got := fileSize(t, segPath(dir, dayOf(time.UnixMilli(testT)))); got != size {
				t.Errorf("the segment grew from %d to %d bytes", size, got)
			}
			if s, _, err := write(w, rng, "os.presence", testT); err != nil || s != 2 {
				t.Errorf("the next record got s = %d and %v, want 2", s, err)
			}
		})
	}
}

// TestFrameLimit checks that the writer refuses a record whose frame would
// be more than a frame holds (maxPayload), which every reader takes for
// damage: a refused record takes no number and changes nothing on disk.
func TestFrameLimit(t *testing.T) {
	dir := t.TempDir()
	w := open(t, dir, newClock(testT))
	rng := rand.New(rand.NewPCG(27, 28))
	if _, _, err := write(w, rng, "os.presence", testT); err != nil {
		t.Fatal(err)
	}
	seg := segPath(dir, "2026-11-19")
	before := fileSize(t, seg)
	huge := json.RawMessage(`{"blob":"` + strings.Repeat("a", maxPayload) + `"}`)
	s, err := w.Checkpoint("p_web", uint64(testT)/ledger.CheckpointMS*ledger.CheckpointMS, huge)
	after := fileSize(t, seg)
	next, _, nextErr := write(w, rng, "os.presence", testT)
	closeErr := w.Close()

	cases := []struct {
		name string
		ok   bool
	}{
		{"a checkpoint whose frame would be more than a frame holds is refused", err != nil && s == 0},
		{"the refused checkpoint leaves the segment as it was", after == before},
		{"the next record takes the number the refused one would have", nextErr == nil && next == 2},
		{"the ledger still reads back whole", closeErr == nil && len(records(verify(t, dir, keysOf(t, testKDev)))) == 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("no: Checkpoint gave s = %d and %v; the segment went from %d to %d bytes; the next record got s = %d and %v; Close %v",
					s, err, before, after, next, nextErr, closeErr)
			}
		})
	}
}

// TestWriteKeepsInput checks that Write changes nothing the caller holds,
// though it stores the prompt with the low_effort and error_sig it derives.
func TestWriteKeepsInput(t *testing.T) {
	w := open(t, t.TempDir(), newClock(testT))
	in := prompt("ok", 0)
	p := in.Event.Data.(*event.PromptSubmit)
	before := *p
	if _, err := w.Write(in); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*p, before) || in.Event.Seq != 0 || in.Event.DeviceID != "" {
		t.Errorf("the caller's event changed: %+v, seq %d, device %q", *p, in.Event.Seq, in.Event.DeviceID)
	}
}

// TestSeals checks when the writer seals (DESIGN 3.6): at once on a prompt
// and a stop, every BATCH_S otherwise, never an empty batch, and what is
// left at Close.
func TestSeals(t *testing.T) {
	cases := []struct {
		name string
		kind string
		// atOnce is set when writing kind seals without Seal being called.
		atOnce bool
	}{
		{"a prompt seals at once", "prompt.submit", true},
		{"an agent.stop seals at once", "agent.stop", true},
		{"a presence sample waits for the next seal", "os.presence", false},
		{"a tool call waits for the next seal", "agent.tool", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sealed := make(chan Batch, 4)
			w := open(t, t.TempDir(), newClock(testT), func(o *Options) { o.Sealed = func(b Batch) { sealed <- b } })
			rng := rand.New(rand.NewPCG(7, 8))
			s, _, err := write(w, rng, c.kind, testT)
			if err != nil {
				t.Fatal(err)
			}
			wait := 200 * time.Millisecond
			if c.atOnce {
				wait = 10 * time.Second
			}
			select {
			case b := <-sealed:
				if !c.atOnce {
					t.Fatalf("batch %d was sealed without a seal", b.Row.B)
				}
				if b.Row.B != 1 || b.Row.FirstSeq != s || len(b.Records) != 1 {
					t.Errorf("sealed batch %d of %d records from %d, want batch 1 holding record %d", b.Row.B, len(b.Records), b.Row.FirstSeq, s)
				}
			case <-time.After(wait):
				if c.atOnce {
					t.Fatal("nothing was sealed")
				}
				if _, ok, err := w.Seal(); !ok || err != nil {
					t.Fatalf("Seal sealed %v, %v", ok, err)
				}
			}
		})
	}

	t.Run("a seal with nothing new seals nothing, and an empty batch is never sealed", func(t *testing.T) {
		w := open(t, t.TempDir(), newClock(testT))
		if _, ok, err := w.Seal(); ok || err != nil {
			t.Fatalf("Seal on an empty ledger sealed %v, %v", ok, err)
		}
		if _, _, err := write(w, rand.New(rand.NewPCG(1, 1)), "os.presence", testT); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := w.Seal(); !ok {
			t.Fatal("Seal didn't seal the record")
		}
		if _, ok, err := w.Seal(); ok || err != nil {
			t.Fatalf("a second Seal sealed %v, %v", ok, err)
		}
	})

	t.Run("Close seals what is left, and the writer refuses records after it", func(t *testing.T) {
		dir := t.TempDir()
		w := open(t, dir, newClock(testT))
		rng := rand.New(rand.NewPCG(1, 1))
		for range 3 {
			if _, _, err := write(w, rng, "os.presence", testT); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if _, _, err := write(w, rng, "os.presence", testT); !errors.Is(err, ErrClosed) {
			t.Errorf("Write after Close: %v, want ErrClosed", err)
		}
		if got := len(records(verify(t, dir, keysOf(t, testKDev)))); got != 3 {
			t.Errorf("%d records are sealed, want 3", got)
		}
	})

	t.Run("Open puts a seal every BATCH_S on the scheduler", func(t *testing.T) {
		s := sched.New()
		open(t, t.TempDir(), newClock(testT), func(o *Options) { o.Sched = s })
		jobs := s.Jobs()
		i := slices.IndexFunc(jobs, func(j sched.JobInfo) bool { return j.Name == "ledger.seal" })
		if i < 0 || jobs[i].Every != 60*time.Second {
			t.Errorf("jobs %+v, want ledger.seal every 60 s", jobs)
		}
	})
}

// TestSealMode checks that the mode a batch was sealed in is on disk with
// its seal (DESIGN 4.2): the seal frame says whether config.toml let the
// computer upload as the batch sealed (Options.Online), asked at the seal
// rather than at the batch's first record, and the batch Sealed gets, its
// row and ReadBatches say the same, as a row Open rebuilds from the seal
// frame after a crash does. Every row records its mode, a local-only
// one's too, so none reads as an older version's (Row.NoMode).
func TestSealMode(t *testing.T) {
	type mode struct {
		sealed, row, read []bool
	}
	cases := []struct {
		name string
		// online is what Options.Online says at each of three seals, nil
		// for a writer without it. Each batch's first record comes while it
		// still says what it said at the seal before, and it says online as
		// the writer opens: a seal that reads it local-only with nothing
		// pending, as Open's own, begins a stretch.
		online []bool
		// crash, when set, leaves the ledger as a crash after the third
		// seal's flush does, with batches.seg's rows of batches 2 and 3
		// lost, which the next Open rebuilds from their seal frames. Online
		// says local-only by then, so a rebuild that asked it would lose
		// batch 3's online mode.
		crash bool
		want  []bool
	}{
		{"a batch sealed online says so, and one sealed in local-only mode doesn't", []bool{true, false, true}, false, []bool{true, false, true}},
		{"a writer without Online seals every batch online", nil, false, []bool{true, true, true}},
		{"rows a crash lost from batches.seg keep their batches' modes when Open rebuilds them from the seal frames", []bool{true, false, true}, true, []bool{true, false, true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			var (
				online    atomic.Bool
				delivered []Batch
			)
			online.Store(true)
			w := open(t, dir, newClock(testT), func(o *Options) {
				o.Sealed = func(b Batch) { delivered = append(delivered, b) }
				if c.online != nil {
					o.Online = online.Load
				}
			})
			rng := rand.New(rand.NewPCG(5, 6))
			for i := range 3 {
				if _, _, err := write(w, rng, "os.presence", testT+int64(i)*1000); err != nil {
					t.Fatal(err)
				}
				if c.online != nil {
					online.Store(c.online[i])
				}
				if _, _, err := write(w, rng, "os.presence", testT+int64(i)*1000+500); err != nil {
					t.Fatal(err)
				}
				if _, ok, err := w.Seal(); !ok || err != nil {
					t.Fatalf("seal %d: %v, %v", i+1, ok, err)
				}
			}
			if c.crash {
				abandon(w)
				batches := filepath.Join(dir, batchesName)
				// Frame 0 is the chain frame, and frames 1 to 3 the rows.
				truncate(t, batches, framesOf(t, batches)[1].end)
				open(t, dir, newClock(testT+60_000), func(o *Options) { o.Online = func() bool { return false } })
			}
			_, rows, err := ReadRows(dir, 1)
			if err != nil {
				t.Fatal(err)
			}
			var got mode
			for _, b := range delivered {
				got.sealed = append(got.sealed, b.Online)
			}
			for _, r := range rows {
				got.row = append(got.row, r.Online)
			}
			for _, b := range readAll(t, dir) {
				got.read = append(got.read, b.Online)
			}
			if !slices.Equal(got.sealed, c.want) || !slices.Equal(got.row, c.want) || !slices.Equal(got.read, c.want) {
				t.Errorf("online: Sealed %v, rows %v, ReadBatches %v; want %v for each", got.sealed, got.row, got.read, c.want)
			}
			for _, r := range rows {
				if r.NoMode {
					t.Errorf("batch %d's row records no mode", r.B)
				}
			}
		})
	}
}

// TestNoModeRows checks the rows of batches whose seal frames record no
// mode, as pairtime 0.1.0-alpha.1's don't: they read as sealed in
// local-only mode by an older version (Row.NoMode), and a writer that
// opens on a ledger whose newest row is one seals at the switch back
// online, as at the pairtime login that follows an upgrade's pairtime
// logout, and marks the batch after it (Batch.Back).
func TestNoModeRows(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewPCG(15, 16))
	w := open(t, dir, newClock(testT))
	for i := range 2 {
		if _, _, err := write(w, rng, "os.presence", testT+int64(i)*1000); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := w.Seal(); !ok || err != nil {
			t.Fatalf("seal %d: %v, %v", i+1, ok, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	dropModes(t, dir)
	_, older, err := ReadRows(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	sealed := make(chan Batch, 4)
	w = open(t, dir, newClock(testT+60_000), func(o *Options) {
		o.Online = func() bool { return true }
		o.Sealed = func(b Batch) { sealed <- b }
	})
	var got []Batch
	take := func() {
		t.Helper()
		select {
		case b := <-sealed:
			got = append(got, b)
		case <-time.After(10 * time.Second):
			t.Fatalf("no batch %d was sealed", len(got)+3)
		}
	}
	// The records come after the new writer's clock, as presence's do: an
	// event made before the switch back would keep its batch unmarked.
	if _, _, err := write(w, rng, "os.presence", testT+61_000); err != nil {
		t.Fatal(err)
	}
	w.NoteMode(true)
	take()
	if _, _, err := write(w, rng, "os.presence", testT+62_000); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.Seal(); err != nil {
		t.Fatal(err)
	}
	take()
	_, rows, err := ReadRows(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name string
		ok   bool
	}{
		{"a row whose seal frame records no mode reads as an older version's, sealed in local-only mode",
			len(older) == 2 && older[0].NoMode && older[1].NoMode && !older[0].Online && !older[1].Online},
		{"the switch back online seals what was pending", len(got) == 2 && got[0].Row.B == 3 && len(got[0].Records) == 1 && got[0].Online && !got[0].Back},
		{"and marks the batch after it", len(got) == 2 && got[1].Online && got[1].Back},
		{"the rows the writer seals since record their modes", len(rows) == 4 && !rows[2].NoMode && !rows[3].NoMode && rows[3].Back},
	}
	for _, ch := range checks {
		t.Run(ch.name, func(t *testing.T) {
			if !ch.ok {
				t.Errorf("no: rows %+v, then %+v; batches sealed %+v", older, rows, got)
			}
		})
	}
}

// dropModes rewrites the rows of dir's batches.seg without the modes their
// seal frames record, as pairtime 0.1.0-alpha.1's writer wrote rows, which
// recorded none. The segments keep their seal frames as they are: Open
// rebuilds no row here, and ReadRows reads batches.seg alone.
func dropModes(t *testing.T, dir string) {
	t.Helper()
	name := filepath.Join(dir, batchesName)
	var out []byte
	for _, fm := range framesOf(t, name) {
		payload := fm.payload
		if fm.kind == kindBatch {
			var row map[string]json.RawMessage
			if err := json.Unmarshal(payload, &row); err != nil {
				t.Fatal(err)
			}
			delete(row, "online")
			delete(row, "back")
			var err error
			if payload, err = json.Marshal(row); err != nil {
				t.Fatal(err)
			}
		}
		out = appendFrame(out, fm.kind, payload)
	}
	if err := os.WriteFile(name, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestNoteMode checks the mode the agent's config watch notes
// (Writer.NoteMode): a record written while it says local-only makes its
// batch local-only, though config.toml says online by the seal; the switch
// seals what it finds pending at once, and with nothing pending the
// stretch's first record does, so a crash after it can't take the switch
// with it; the switch back online seals what the stretch left, or what was
// pending after a seal that read the file local-only, and marks the first
// batch whose records all came after it (Batch.Back); and a switch back the
// watch misses ends the stretch at the next seal that reads the file
// online, marking no batch. Each batch's row and its seal frame say what
// Sealed got, as the rows Open rebuilds after a crash do.
func TestNoteMode(t *testing.T) {
	// A step changes config.toml ("file-local", "file-online"), notes the
	// mode as the config watch does ("local", "online"), writes a record
	// ("write"), or one whose event came a minute before the writer's clock,
	// as a git change the watcher held does ("write-old"), seals ("seal"),
	// seals with nothing pending, which seals no batch ("seal-none"),
	// waits for the batch a seal of the writer's own sealed ("sealed"),
	// gives one that shouldn't come time to ("pause"), holds every seal
	// back until a step lets them go ("hold", "release"), opens the writer
	// again after it closed ("reopen"), or
	// after a crash that lost every row of batches.seg but the first, which
	// Open rebuilds from the seal frames while config.toml says local
	// ("crash"). A first step "no-online" opens the writer without
	// Options.Online.
	//
	// It also sets the writer's clock to N ms after the start ("at:N"),
	// opens the writer again after it closed with its clock at N
	// ("reopen-at:N"), writes an event made then ("write-at:N"), or one
	// made before the last note by the OS's monotonic clock that a step of
	// the wall clock put a minute after the start ("write-stepped"), has
	// Options.Waiting say an event made before the moment it is asked about
	// is still on its way until it has come ("waiting", "arrived"), or
	// opens the writer again after a kill that left the ledger's files as
	// they were ("kill"), with its clock at N ("kill-at:N"), or after a
	// crash that lost every row of batches.seg but the first while
	// config.toml says online ("crash-online").
	//
	// And it writes an event of this run, by this run's clock, made just
	// after the writer's clock ("write-run"); one whose own times are after
	// the last note by every clock but whose sender's stamp is before it by
	// the OS's monotonic clock, as a hook call the agent stamped after a step
	// back of the wall clock ("write-resent"); or a hook call of the boot the
	// writer runs in, half a minute before the start by its t ("write-boot");
	// or opens the writer again in another boot ("reboot").
	cases := []struct {
		name  string
		steps []string
		// online is the mode of each batch sealed, back whether it is
		// marked as the first after a switch back online, and records how
		// many records each holds.
		online, back []bool
		records      []int
	}{
		{"records pending at a switch to local-only mode seal at once, local-only",
			[]string{"write", "file-local", "local", "sealed"}, []bool{false}, []bool{false}, []int{1}},
		{"with nothing pending, the stretch's first record seals at once, local-only",
			[]string{"file-local", "local", "write", "sealed"}, []bool{false}, []bool{false}, []int{1}},
		{"only the stretch's first seal comes at once",
			[]string{"file-local", "local", "write", "sealed", "write", "write", "pause", "seal"}, []bool{false, false}, []bool{false, false}, []int{1, 2}},
		{"a record written in the stretch makes its batch local-only though config.toml says online by the seal the switch back makes, and the batch after it is marked",
			[]string{"file-local", "local", "write", "sealed", "write", "file-online", "online", "sealed", "write", "seal"}, []bool{false, false, true}, []bool{false, false, true}, []int{1, 1, 1}},
		{"a switch back the config watch misses ends the stretch at the next seal that reads config.toml online, and marks no batch",
			[]string{"file-local", "local", "write", "sealed", "write", "file-online", "seal", "write", "seal"}, []bool{false, false, true}, []bool{false, false, false}, []int{1, 1, 1}},
		{"a switch with no record written in it leaves the next batch online, marked as the first after it",
			[]string{"local", "online", "write", "seal"}, []bool{true}, []bool{true}, []int{1}},
		{"a switch to local-only and straight back still seals what was pending as local-only",
			[]string{"write", "file-local", "local", "file-online", "online", "sealed"}, []bool{false}, []bool{false}, []int{1}},
		{"without Online, only the switch back ends a stretch, since no seal reads config.toml",
			[]string{"no-online", "local", "write", "sealed", "write", "seal", "online", "write", "seal"}, []bool{false, false, true}, []bool{false, false, true}, []int{1, 1, 1}},
		{"a second stretch's first record seals at once too",
			[]string{"file-local", "local", "write", "sealed", "file-online", "online", "write", "seal", "file-local", "local", "write", "sealed"}, []bool{false, true, false}, []bool{false, true, false}, []int{1, 1, 1}},
		{"a switch back online after a seal that read config.toml local-only, with nothing pending, marks the next batch",
			[]string{"file-local", "write", "seal", "file-online", "online", "write", "seal"}, []bool{false, true}, []bool{false, true}, []int{1, 1}},
		{"a switch back online after a seal that read config.toml local-only, as at the first pairtime login, seals what is pending, and marks the batch after it",
			[]string{"file-local", "write", "seal", "write", "file-online", "online", "sealed", "write", "seal"}, []bool{false, true, true}, []bool{false, false, true}, []int{1, 1, 1}},
		{"a seal that reads config.toml local-only after a switch back marks no batch after it",
			[]string{"file-local", "local", "write", "sealed", "file-online", "online", "pause", "file-local", "write", "seal", "file-online", "write", "seal"}, []bool{false, false, true}, []bool{false, false, false}, []int{1, 1, 1}},
		{"an event made before the switch back that the writer gets after it, as a git change the watcher held, seals its batch local-only, and the next batch takes the mark",
			[]string{"file-local", "local", "write", "sealed", "file-online", "online", "write-old", "seal", "write", "seal"}, []bool{false, false, true}, []bool{false, false, true}, []int{1, 1, 1}},
		{"an event made before the switch back, pending with records that came after it, makes their batch local-only too",
			[]string{"file-local", "local", "write", "sealed", "file-online", "online", "pause", "write", "write-old", "seal", "write", "seal"}, []bool{false, false, true}, []bool{false, false, true}, []int{1, 2, 1}},
		{"each batch holding an event made before the switch back is local-only, and the mark waits for the batch after them",
			[]string{"file-local", "local", "write", "sealed", "file-online", "online", "pause", "write-old", "seal", "write-old", "seal", "write", "seal"}, []bool{false, false, false, true}, []bool{false, false, false, true}, []int{1, 1, 1, 1}},
		{"a switch-back seal that also takes a record written after the note leaves its batch unmarked, since not all its records came after it",
			[]string{"file-local", "write", "seal", "write", "hold", "file-online", "online", "write", "release", "sealed", "write", "seal"}, []bool{false, true, true}, []bool{false, false, true}, []int{1, 2, 1}},
		{"a writer that opens on a ledger whose newest batch is local-only, while config.toml says local-only, takes the stretch as going on: its first record seals at once, local-only, and the batch after the switch back online is marked",
			[]string{"file-local", "write", "seal", "reopen", "write", "file-online", "online", "sealed", "write", "seal"}, []bool{false, false, true}, []bool{false, false, true}, []int{1, 1, 1}},
		{"rows a crash lost keep their batches' modes and marks when Open rebuilds them from the seal frames",
			[]string{"file-local", "local", "write", "sealed", "write", "file-online", "online", "sealed", "write", "seal", "crash"}, []bool{false, false, true}, []bool{false, false, true}, []int{1, 1, 1}},
		{"an event made before a noted switch back that the writer gets after the mark is sealed local-only too, and the batch after it online",
			[]string{"file-local", "local", "write", "sealed", "file-online", "online", "write", "seal", "write-old", "seal", "write", "seal"},
			[]bool{false, true, false, true}, []bool{false, true, false, false}, []int{1, 1, 1, 1}},
		{"an event made before a seal that read config.toml online after a noted stretch, which the writer gets later, is sealed local-only",
			[]string{"file-local", "local", "write", "sealed", "write", "file-online", "seal", "write-old", "seal", "write", "seal"},
			[]bool{false, false, false, true}, []bool{false, false, false, false}, []int{1, 1, 1, 1}},
		{"the watch's reload repeating the note of its first look leaves an event made between the two online",
			[]string{"file-local", "local", "write", "sealed", "file-online", "online", "at:300", "online", "write-at:100", "seal"},
			[]bool{false, true}, []bool{false, true}, []int{1, 1}},
		{"an event made before the moment the newest seal frame recorded, which a reopened writer gets, is sealed local-only",
			[]string{"file-local", "local", "write", "sealed", "file-online", "online", "write", "seal", "reopen", "write-old", "seal", "write", "seal"},
			[]bool{false, true, false, true}, []bool{false, true, false, false}, []int{1, 1, 1, 1}},
		{"an event made before the moment a seal frame recorded, whose row a crash lost, is sealed local-only once Open rebuilds the row",
			[]string{"file-local", "local", "write", "sealed", "file-online", "online", "write", "seal", "crash-online", "write-old", "seal"},
			[]bool{false, true, false}, []bool{false, true, false}, []int{1, 1, 1}},
		{"an event a kill left without a batch, made before the newest seal frame's moment, is sealed local-only by the next Open",
			[]string{"file-local", "local", "write", "sealed", "file-online", "online", "write", "seal", "write-old", "kill", "sealed"},
			[]bool{false, true, false}, []bool{false, true, false}, []int{1, 1, 1}},
		{"while an event made before the switch back is still on its way, a batch is sealed local-only, and the mark waits for the batch after it",
			[]string{"file-local", "local", "write", "sealed", "file-online", "online", "waiting", "write", "seal", "arrived", "write-old", "seal", "write", "seal"},
			[]bool{false, false, false, true}, []bool{false, false, false, true}, []int{1, 1, 1, 1}},
		{"an event made before the switch back by the OS's monotonic clock, whose t a step of the wall clock put after it, is sealed local-only",
			[]string{"file-local", "local", "write", "sealed", "file-online", "online", "write-stepped", "seal", "write", "seal"},
			[]bool{false, false, true}, []bool{false, false, true}, []int{1, 1, 1}},
		{"an event made before the switch back by the OS's monotonic clock, whose t a step of the wall clock put after it, is sealed local-only after a reopen too, since the seal frame keeps the moment by that clock",
			[]string{"file-local", "local", "write", "sealed", "file-online", "online", "write", "seal", "reopen", "write-stepped", "seal", "write", "seal"},
			[]bool{false, true, false, true}, []bool{false, true, false, false}, []int{1, 1, 1, 1}},
		{"a moment the newest seal frame keeps ahead of the clock, as after a step back of the wall clock while the writer was shut, still holds an event of an earlier run by its t, and none of this run's, which came after it",
			[]string{"file-local", "local", "write", "sealed", "file-online", "online", "write", "seal", "reopen-at:-30000", "write-at:-20000", "seal", "write-run", "seal"},
			[]bool{false, true, false, true}, []bool{false, true, false, false}, []int{1, 1, 1, 1}},
		{"an event a kill left, made before the newest seal frame's moment by its t, is sealed local-only by the next Open though the clock reads earlier by then",
			[]string{"file-local", "local", "write", "sealed", "file-online", "online", "pause", "write", "seal", "write-at:-1000", "kill-at:-30000", "sealed"},
			[]bool{false, true, false}, []bool{false, true, false}, []int{1, 1, 1}},
		{"an event made before the switch back by the OS's monotonic clock, whose t a step of the wall clock put after it, is sealed local-only after a crash too, by the moment Open takes from the seal frame it rebuilds the lost row from",
			[]string{"file-local", "local", "write", "sealed", "file-online", "online", "write", "seal", "crash-online", "write-stepped", "seal"},
			[]bool{false, true, false}, []bool{false, true, false}, []int{1, 1, 1}},
		{"an event whose own times are after the switch back is sealed local-only when its sender's stamp is before it, and the batch after it takes the mark",
			[]string{"file-local", "local", "write", "sealed", "file-online", "online", "write-resent", "seal", "write", "seal"},
			[]bool{false, false, true}, []bool{false, false, true}, []int{1, 1, 1}},
		{"a restart before the batch after a switch back took its mark arms the mark again, so it still waits for an event of the stretch on its way",
			[]string{"file-local", "local", "write", "sealed", "file-online", "online", "waiting", "write", "seal", "reopen", "write", "seal", "arrived", "write", "seal"},
			[]bool{false, false, false, true}, []bool{false, false, false, true}, []int{1, 1, 1, 1}},
		{"a moment the newest seal frame recorded in another boot holds a hook call of the boot the writer runs in as made after it, though its t is before",
			[]string{"file-local", "local", "write", "sealed", "file-online", "online", "write", "seal", "reboot", "write-boot", "seal"},
			[]bool{false, true, true}, []bool{false, true, false}, []int{1, 1, 1}},
		{"a stretch only a seal's read saw, with records pending, ends at the switch back's note, so an event made before the note that the writer gets after it is sealed local-only",
			[]string{"file-local", "write", "seal", "file-online", "online", "write-old", "seal", "write", "seal"},
			[]bool{false, false, true}, []bool{false, false, true}, []int{1, 1, 1}},
		{"a stretch only a seal's read saw, with records pending, ends at the next seal that reads config.toml online, so an event made before that read is sealed local-only",
			[]string{"file-local", "write", "seal", "file-online", "seal-none", "write-old", "seal", "write", "seal"},
			[]bool{false, false, true}, []bool{false, false, false}, []int{1, 1, 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			var file atomic.Bool // config.toml says online
			file.Store(true)
			sealed := make(chan Batch, 8)
			withOnline := len(c.steps) == 0 || c.steps[0] != "no-online"
			clk := newClock(testT)
			// waits is whether an event made before the moment Waiting is
			// asked about is on its way, and asked whether the moment it was
			// last asked about held an event made a moment before the switch
			// back's note, and not one made at it, and held the event made
			// before it by its t though not by the OS's mono, since Waiting
			// is asked by either clock (moment.madeBefore).
			var (
				waits, asked atomic.Bool
			)
			const osBoot, osMono = "b_os", 50_000
			// The OS's clock as the writer reads it, in the boot "reboot"
			// moves to.
			var bootNow atomic.Value
			bootNow.Store(osBoot)
			var monoNow atomic.Int64
			monoNow.Store(osMono)
			openWriter := func(online func() bool) *Writer {
				return open(t, dir, clk, func(o *Options) {
					if withOnline {
						o.Online = online
					}
					o.Sealed = func(b Batch) { sealed <- b }
					o.Waiting = func(before func(event.Envelope) bool) bool {
						asked.Store(before(event.Envelope{T: testT - 1}) && !before(event.Envelope{T: testT}) &&
							before(event.Envelope{T: testT - 1, Mono: osMono + 1000, BootID: osBoot}))
						return waits.Load()
					}
					o.Clock = func() osclock.Stamp {
						return osclock.Stamp{Wall: clk.now(), Mono: monoNow.Load(), BootID: bootNow.Load().(string)}
					}
				})
			}
			w := openWriter(file.Load)
			rng := rand.New(rand.NewPCG(9, 10))
			var got []Batch
			take := func() {
				t.Helper()
				select {
				case b := <-sealed:
					got = append(got, b)
				case <-time.After(10 * time.Second):
					t.Fatalf("no batch %d was sealed", len(got)+1)
				}
			}
			writeAt := func(i int, ms int64, edit func(*Input)) {
				t.Helper()
				in := sampleInput(rng, "os.presence", ms, "", "")
				if edit != nil {
					edit(&in)
				}
				if _, err := w.Write(in); err != nil {
					t.Fatalf("step %d: %v", i, err)
				}
			}
			for i, step := range c.steps {
				if ms, ok := strings.CutPrefix(step, "at:"); ok {
					clk.set(testT + atoi(t, ms))
					continue
				}
				if ms, ok := strings.CutPrefix(step, "reopen-at:"); ok {
					if err := w.Close(); err != nil {
						t.Fatalf("step %d: %v", i, err)
					}
					clk.set(testT + atoi(t, ms))
					w = openWriter(file.Load)
					continue
				}
				if ms, ok := strings.CutPrefix(step, "kill-at:"); ok {
					abandon(w)
					clk.set(testT + atoi(t, ms))
					w = openWriter(file.Load)
					continue
				}
				if ms, ok := strings.CutPrefix(step, "write-at:"); ok {
					writeAt(i, testT+atoi(t, ms), nil)
					continue
				}
				switch step {
				case "write-run":
					writeAt(i, clk.now().UnixMilli()+1000, func(in *Input) {
						// An hour on, so the reading is well past this run's start.
						in.Event.Mono, in.Event.BootID = RunClock(time.Now().Add(time.Hour))
					})
				case "write-resent":
					writeAt(i, testT+60_000, func(in *Input) {
						in.Event.BootID, in.Event.Mono = osBoot, osMono+5000
						in.Sent = osclock.Stamp{Wall: time.UnixMilli(testT + 90_000), Mono: osMono - 1000, BootID: osBoot}
					})
				case "write-boot":
					writeAt(i, testT-30_000, func(in *Input) { in.Event.BootID, in.Event.Mono = bootNow.Load().(string), 5000 })
				case "reboot":
					if err := w.Close(); err != nil {
						t.Fatalf("step %d: %v", i, err)
					}
					bootNow.Store("b_os2")
					monoNow.Store(10_000)
					w = openWriter(file.Load)
				case "write-stepped":
					writeAt(i, testT+60_000, func(in *Input) { in.Event.BootID, in.Event.Mono = osBoot, osMono-1000 })
				case "waiting", "arrived":
					waits.Store(step == "waiting")
				case "kill":
					abandon(w)
					w = openWriter(file.Load)
				case "crash-online":
					abandon(w)
					batches := filepath.Join(dir, batchesName)
					truncate(t, batches, framesOf(t, batches)[1].end)
					w = openWriter(file.Load)
				case "file-local", "file-online":
					file.Store(step == "file-online")
				case "local", "online":
					w.NoteMode(step == "online")
				case "write":
					if _, _, err := write(w, rng, "os.presence", testT+int64(i)*1000); err != nil {
						t.Fatal(err)
					}
				case "write-old":
					if _, _, err := write(w, rng, "os.presence", testT-60_000); err != nil {
						t.Fatal(err)
					}
				case "seal":
					// A seal the writer still owed may take the records first,
					// and then this one has none.
					if _, _, err := w.Seal(); err != nil {
						t.Fatalf("step %d: %v", i, err)
					}
					take()
				case "seal-none":
					if _, ok, err := w.Seal(); ok || err != nil {
						t.Fatalf("step %d: Seal sealed a batch %v, %v; want nothing to seal", i, ok, err)
					}
				case "sealed":
					take()
				case "pause":
					time.Sleep(200 * time.Millisecond)
					select {
					case b := <-sealed:
						t.Fatalf("step %d: batch %d was sealed meanwhile", i, b.Row.B)
					default:
					}
				case "hold":
					w.sealMu.Lock()
				case "release":
					w.sealMu.Unlock()
				case "reopen":
					if err := w.Close(); err != nil {
						t.Fatalf("step %d: %v", i, err)
					}
					w = openWriter(file.Load)
				case "crash":
					abandon(w)
					batches := filepath.Join(dir, batchesName)
					// Frame 0 is the chain frame, and frame 1 batch 1's row.
					truncate(t, batches, framesOf(t, batches)[1].end)
					w = openWriter(func() bool { return false })
				}
			}
			select {
			case b := <-sealed:
				t.Fatalf("batch %d was sealed after the last step", b.Row.B)
			case <-time.After(200 * time.Millisecond):
			}
			var online, back []bool
			var records []int
			for _, b := range got {
				online, back, records = append(online, b.Online), append(back, b.Back), append(records, len(b.Records))
			}
			if !slices.Equal(online, c.online) || !slices.Equal(back, c.back) || !slices.Equal(records, c.records) {
				t.Errorf("batches sealed online %v, marked %v, holding %v records; want %v, %v and %v", online, back, records, c.online, c.back, c.records)
			}
			if slices.Contains(c.steps, "waiting") && !asked.Load() {
				t.Error("Waiting was last asked about another moment than the switch back's note, or not by either clock")
			}
			_, rows, err := ReadRows(dir, 1)
			if err != nil {
				t.Fatal(err)
			}
			read := readAll(t, dir)
			if len(rows) != len(got) || len(read) != len(got) {
				t.Fatalf("%d rows and %d batches read back, for %d sealed", len(rows), len(read), len(got))
			}
			for i, b := range got {
				if r := rows[i]; r.Online != b.Online || r.Back != b.Back || r.NoMode || !r.LocalUntil.Equal(b.LocalUntil) {
					t.Errorf("batch %d's row says online %v, marked %v, with no mode %v, local-only until %v", b.Row.B, r.Online, r.Back, r.NoMode, r.LocalUntil)
				}
				if rb := read[i]; rb.Online != b.Online || rb.Back != b.Back || !rb.LocalUntil.Equal(b.LocalUntil) {
					t.Errorf("batch %d's seal frame says online %v, marked %v, local-only until %v", b.Row.B, rb.Online, rb.Back, rb.LocalUntil)
				}
			}
			if slices.Contains(c.steps, "waiting") {
				if last := got[len(got)-1]; !last.LocalUntil.Equal(time.UnixMilli(testT)) {
					t.Errorf("the marked batch records config.toml as local-only until %v, want the switch back's note at %v", last.LocalUntil, time.UnixMilli(testT))
				}
			}
		})
	}
}

// atoi is a step's number.
func atoi(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestRunClock checks an event of this run whose t a step of the wall
// clock put a minute after a noted switch back online: by this run's
// monotonic clock, which the agent's own events carry (RunClock), it was
// made before the note or after it, and its batch is sealed local-only or
// online accordingly.
func TestRunClock(t *testing.T) {
	// With Go's monotonic reading, as the writer's clock has one, and an
	// hour on, so the event's mono a second before it isn't before this
	// run's start, which a test run first could be within a second of.
	at := time.Now().Add(time.Hour)
	cases := []struct {
		name string
		// by is how much later than the note, by this run's clock, the
		// event was made.
		by     int64
		online bool
	}{
		{"an event made before the note by this run's clock is sealed local-only, though its t is after it", -1000, false},
		{"an event made after the note by this run's clock is sealed online", 1000, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sealed := make(chan Batch, 4)
			w := open(t, t.TempDir(), newClock(at.UnixMilli()), func(o *Options) {
				o.Now = func() time.Time { return at }
				o.Online = func() bool { return true }
				o.Sealed = func(b Batch) { sealed <- b }
			})
			w.NoteMode(false)
			w.NoteMode(true)
			mono, boot := RunClock(at)
			in := sampleInput(rand.New(rand.NewPCG(17, 18)), "os.presence", at.UnixMilli()+60_000, "", "")
			in.Event.Mono, in.Event.BootID = mono+c.by, boot
			if _, err := w.Write(in); err != nil {
				t.Fatal(err)
			}
			if _, _, err := w.Seal(); err != nil {
				t.Fatal(err)
			}
			select {
			case b := <-sealed:
				if b.Online != c.online || len(b.Records) != 1 {
					t.Errorf("the batch of %d records was sealed online %v, want %v", len(b.Records), b.Online, c.online)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("no batch was sealed")
			}
		})
	}
}

// TestNoteSeen checks what the agent saw of config.toml's mode before the
// writer could hear the agent's config watch: Open takes it from
// Options.Seen, and NoteSeen as the agent publishes the writer. A step
// writes an event made a minute before the clock ("write-old") or after it
// ("write"), or an event of this run, by this run's clock ("write-run"),
// seals ("seal"), waits for a seal the writer made of its own ("sealed"),
// has config.toml say online and gives NoteSeen a stretch that ended at
// the clock's time ("seen-ended"), or closes the writer and opens it again
// with the same Seen, as the agent's next start does ("reopen").
func TestNoteSeen(t *testing.T) {
	ended := Seen{Ended: time.UnixMilli(testT)}
	cases := []struct {
		name string
		open Seen
		// local is whether config.toml says local-only as the writer
		// opens, and noOnline opens it without Options.Online, so that no
		// seal reads the file.
		local, noOnline bool
		steps           []string
		online, back    []bool
	}{
		{"a stretch the agent saw end before the writer opened makes an event made before its end local-only, and marks the first batch after it",
			ended, false, false, []string{"write-old", "seal", "write", "seal"}, []bool{false, true}, []bool{false, true}},
		{"a stretch end the newest seal frame already holds, which the agent sees again at its next start, marks no batch then",
			ended, false, false, []string{"write", "seal", "reopen", "write", "seal"}, []bool{true, true}, []bool{true, false}},
		{"a stretch the agent sees going on as the writer opens seals its first record at once, local-only",
			Seen{Local: true}, true, false, []string{"write", "sealed"}, []bool{false}, []bool{false}},
		{"a stretch the agent sees going on as the writer opens, which config.toml no longer says by Open's seal, ends at that seal, so an event made before it is sealed local-only",
			Seen{Local: true}, false, false, []string{"write-old", "seal", "write", "seal"}, []bool{false, true}, []bool{false, false}},
		{"a stretch the writer took as going on, which the agent saw end before it published the writer, ends there, and the first batch after the events made before its end is marked",
			Seen{Local: true}, true, false, []string{"write", "sealed", "seen-ended", "write-old", "seal", "write", "seal"}, []bool{false, false, true}, []bool{false, false, true}},
		{"without Online, a stretch the writer took as going on ends where the agent saw it end, and the batch after it is marked",
			Seen{Local: true}, true, true, []string{"write", "sealed", "seen-ended", "write", "seal"}, []bool{false, true}, []bool{false, true}},
		{"a stretch end ahead of the clock, as account.json's link time after a step back of the wall clock, still holds an event judged by its t",
			Seen{Ended: time.UnixMilli(testT + 60_000)}, false, false, []string{"write", "seal"}, []bool{false}, []bool{false}},
		{"a stretch end an earlier run saw, as state.json kept it, holds none of this run's events, though it is ahead of the clock",
			Seen{Ended: time.UnixMilli(testT + 60_000), Earlier: true}, false, false, []string{"write-run", "seal"}, []bool{true}, []bool{true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			sealed := make(chan Batch, 8)
			var file atomic.Bool // config.toml says online
			file.Store(!c.local)
			openWriter := func() *Writer {
				return open(t, dir, newClock(testT), func(o *Options) {
					if !c.noOnline {
						o.Online = file.Load
					}
					o.Seen = func() Seen { return c.open }
					o.Sealed = func(b Batch) { sealed <- b }
				})
			}
			w := openWriter()
			rng := rand.New(rand.NewPCG(19, 20))
			var got []Batch
			take := func() {
				t.Helper()
				select {
				case b := <-sealed:
					got = append(got, b)
				case <-time.After(10 * time.Second):
					t.Fatalf("no batch %d was sealed", len(got)+1)
				}
			}
			for i, step := range c.steps {
				switch step {
				case "write", "write-old":
					ms := testT + int64(i+1)*1000
					if step == "write-old" {
						ms = testT - 60_000
					}
					if _, _, err := write(w, rng, "os.presence", ms); err != nil {
						t.Fatal(err)
					}
				case "write-run":
					in := sampleInput(rng, "os.presence", testT+int64(i+1)*1000, "", "")
					// An hour on, so the reading is well past this run's start.
					in.Event.Mono, in.Event.BootID = RunClock(time.Now().Add(time.Hour))
					if _, err := w.Write(in); err != nil {
						t.Fatal(err)
					}
				case "seal":
					if _, _, err := w.Seal(); err != nil {
						t.Fatalf("step %d: %v", i, err)
					}
					take()
				case "sealed":
					take()
				case "seen-ended":
					file.Store(true)
					w.NoteSeen(ended)
				case "reopen":
					if err := w.Close(); err != nil {
						t.Fatalf("step %d: %v", i, err)
					}
					w = openWriter()
				}
			}
			var online, back []bool
			for _, b := range got {
				online, back = append(online, b.Online), append(back, b.Back)
			}
			if !slices.Equal(online, c.online) || !slices.Equal(back, c.back) {
				t.Errorf("batches sealed online %v, marked %v; want %v and %v", online, back, c.online, c.back)
			}
		})
	}
}

// TestSealReadsModeAgain checks a note, or a record, that comes while a
// seal reads config.toml (Options.Online), which it does without the
// Writer's lock: the seal reads the file again, so a record written after
// a switch isn't sealed in the mode the file had before it. The test's
// Online notes the mode and writes the record itself, which a real one
// never does, to land them in that moment.
func TestSealReadsModeAgain(t *testing.T) {
	cases := []struct {
		name string
		// noted is the mode noted before the seal, note the one noted while
		// it reads, and before and after what the file says at its first
		// read and at the next. unnoted has the record come mid-read with no
		// note, as before the watch notes a switch.
		noted, note, before, after, want, unnoted bool
	}{
		{"a switch back online noted mid-read, with a record after it, seals the batch online", false, true, false, true, true, false},
		{"a switch to local-only noted mid-read, with a record after it, seals the batch local-only", true, false, true, false, false, false},
		{"a record written mid-read, after config.toml went local-only, which the watch hasn't noted yet, seals the batch local-only", true, true, true, false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var (
				w     *Writer
				armed atomic.Bool
				reads atomic.Int32
			)
			rng := rand.New(rand.NewPCG(13, 14))
			sealed := make(chan Batch, 4)
			w = open(t, t.TempDir(), newClock(testT), func(o *Options) {
				o.Sealed = func(b Batch) { sealed <- b }
				o.Online = func() bool {
					if !armed.Load() {
						return c.noted
					}
					if reads.Add(1) > 1 {
						return c.after
					}
					if !c.unnoted {
						w.NoteMode(c.note)
					}
					if _, _, err := write(w, rng, "os.presence", testT); err != nil {
						t.Error(err)
					}
					return c.before
				}
			})
			w.NoteMode(c.noted)
			armed.Store(true)
			if _, ok, err := w.Seal(); !ok || err != nil {
				t.Fatalf("Seal sealed %v, %v", ok, err)
			}
			select {
			case b := <-sealed:
				if b.Online != c.want || len(b.Records) != 1 {
					t.Errorf("the batch of %d records was sealed online %v, want %v", len(b.Records), b.Online, c.want)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("no batch was sealed")
			}
		})
	}
}

// TestSealReadsUnderLock checks a seal through whose reads of config.toml
// records keep coming, as in a drain: after sealReads reads without the
// record lock, each with a record coming during it, the seal reads the
// file once more under the lock, so no record comes during that read, and
// the batch is sealed in the mode it reads. Waiting can't be asked under
// the lock, so a batch due its mark whose last read without the lock found
// config.toml local-only, so that Waiting wasn't asked, is sealed as while
// an event of the stretch is on its way. The test's Online writes the
// records itself, which a real one never does, to land them in each read.
func TestSealReadsUnderLock(t *testing.T) {
	cases := []struct {
		name string
		// reads are what config.toml says at each read without the lock,
		// locked what it says at the read under it, and mark arms the mark
		// before the seal.
		reads        []bool
		locked, mark bool
		// online and back are the first batch's mode and mark, and next
		// the second batch's mark.
		online, back, next bool
	}{
		{"a switch to local-only during the last read without the lock seals the records that came in it local-only, by the read under the lock",
			[]bool{true, true, true}, false, false, false, false, false},
		{"records that keep coming while config.toml stays online are sealed online, and none comes during the read under the lock",
			[]bool{true, true, true}, true, false, true, false, false},
		{"a batch due its mark whose last read without the lock found config.toml local-only is sealed local-only after an online read under the lock, and the mark waits for the next",
			[]bool{true, true, false}, true, true, false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var (
				w     *Writer
				armed atomic.Bool
				reads atomic.Int32
				late  error
			)
			rng := rand.New(rand.NewPCG(23, 24))
			in := sampleInput(rng, "os.presence", testT, "", "")
			// done closes once the record written as the read under the lock
			// begins is written, which waits for the lock.
			done := make(chan struct{})
			sealed := make(chan Batch, 4)
			w = open(t, t.TempDir(), newClock(testT), func(o *Options) {
				o.Sealed = func(b Batch) { sealed <- b }
				o.Waiting = func(func(event.Envelope) bool) bool { return false }
				o.Online = func() bool {
					if !armed.Load() {
						return true
					}
					n := int(reads.Add(1))
					switch {
					case n <= sealReads:
						if _, _, err := write(w, rng, "os.presence", testT); err != nil {
							t.Error(err)
						}
						return c.reads[n-1]
					case n == sealReads+1:
						go func() {
							_, late = w.Write(in)
							close(done)
						}()
						select {
						case <-done:
							t.Error("a record was written during the read under the record lock")
						case <-time.After(100 * time.Millisecond):
						}
						return c.locked
					}
					return true
				}
			})
			if c.mark {
				w.NoteMode(false)
				w.NoteMode(true)
				// The seal the note asks for, with nothing pending, goes first.
				time.Sleep(200 * time.Millisecond)
			}
			armed.Store(true)
			if _, ok, err := w.Seal(); !ok || err != nil {
				t.Fatalf("Seal sealed %v, %v", ok, err)
			}
			first := <-sealed
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("the seal never read config.toml under the record lock")
			}
			if late != nil {
				t.Fatal(late)
			}
			if _, ok, err := w.Seal(); !ok || err != nil {
				t.Fatalf("the second Seal sealed %v, %v", ok, err)
			}
			second := <-sealed
			if first.Online != c.online || first.Back != c.back || second.Back != c.next ||
				len(first.Records) != sealReads || len(second.Records) != 1 {
				t.Errorf("the first batch of %d records sealed online %v, marked %v, and the next of %d marked %v; want %d, %v, %v, then 1, %v",
					len(first.Records), first.Online, first.Back, len(second.Records), second.Back, sealReads, c.online, c.back, c.next)
			}
		})
	}
}

// TestSealReadsSeenAgain checks a NoteSeen that comes while a seal reads
// config.toml: the seal reads the file again, as after a note, so a mark
// NoteSeen arms meanwhile makes it ask Waiting about the batch. The test's
// Online calls NoteSeen itself, which a real one never does.
func TestSealReadsSeenAgain(t *testing.T) {
	cases := []struct {
		name string
		// seen has NoteSeen give a stretch end during the first read.
		seen   bool
		online bool
	}{
		{"a stretch end NoteSeen gives while a seal reads config.toml has the seal read it again and ask Waiting, which holds the batch local-only", true, false},
		{"with no NoteSeen meanwhile, Waiting isn't asked, and the batch is sealed online", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var (
				w     *Writer
				armed atomic.Bool
				reads atomic.Int32
			)
			sealed := make(chan Batch, 4)
			w = open(t, t.TempDir(), newClock(testT), func(o *Options) {
				o.Sealed = func(b Batch) { sealed <- b }
				// An event of the stretch is on its way.
				o.Waiting = func(func(event.Envelope) bool) bool { return true }
				o.Online = func() bool {
					if armed.Load() && reads.Add(1) == 1 && c.seen {
						w.NoteSeen(Seen{Ended: time.UnixMilli(testT)})
					}
					return true
				}
			})
			if _, _, err := write(w, rand.New(rand.NewPCG(25, 26)), "os.presence", testT+1000); err != nil {
				t.Fatal(err)
			}
			armed.Store(true)
			if _, ok, err := w.Seal(); !ok || err != nil {
				t.Fatalf("Seal sealed %v, %v", ok, err)
			}
			if b := <-sealed; b.Online != c.online {
				t.Errorf("the batch was sealed online %v, want %v", b.Online, c.online)
			}
		})
	}
}

// TestSegments checks that records go to the segment of the UTC day they
// are written on, and that batches read back across days.
func TestSegments(t *testing.T) {
	const day = 86_400_000
	cases := []struct {
		name  string
		times []int64 // the clock at each write
		// days are the segments that should exist after.
		days []string
	}{
		{"records of one day share a segment", []int64{testT, testT + 1000, testT + 60_000}, []string{"2026-11-19"}},
		{"a batch whose records span midnight is sealed in the next day's segment and verifies",
			[]int64{testT + 11*3_600_000 + 59*60_000, testT + 12*3_600_000 + 1000}, []string{"2026-11-19", "2026-11-20"}},
		{"a clock that goes back a day keeps writing to the newest segment",
			[]int64{testT, testT + day, testT}, []string{"2026-11-19", "2026-11-20"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			clk := newClock(c.times[0])
			w := open(t, dir, clk)
			rng := rand.New(rand.NewPCG(9, 10))
			for _, ms := range c.times {
				clk.set(ms)
				if _, _, err := write(w, rng, "os.presence", ms); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			days, err := segments(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(days, c.days) {
				t.Errorf("segments %v, want %v", days, c.days)
			}
			if got := len(records(verify(t, dir, keysOf(t, testKDev)))); got != len(c.times) {
				t.Errorf("%d records read back, want %d", got, len(c.times))
			}
		})
	}
}

// TestReceipts checks that batches.seg keeps a receipt with its batch.
func TestReceipts(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, "pairtime ledger writer test server")
	server := ed25519.NewKeyFromSeed(seed)
	dir := t.TempDir()
	w := open(t, dir, newClock(testT))
	if _, _, err := write(w, rand.New(rand.NewPCG(1, 1)), "os.presence", testT); err != nil {
		t.Fatal(err)
	}
	b, _, err := w.Seal()
	if err != nil {
		t.Fatal(err)
	}
	rc, err := ledger.SignReceipt(server, w.DeviceID(), 1, b.Row.Head, uint64(testT)+250)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		b    uint64
		ok   bool
	}{
		{"a receipt for a sealed batch is kept", 1, true},
		{"a receipt for a batch not sealed yet is refused", 2, false},
		{"batch 0 doesn't exist", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := w.StoreReceipt(c.b, rc); (err == nil) != c.ok {
				t.Fatalf("StoreReceipt(%d): %v", c.b, err)
			}
		})
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	t.Run("the receipt reads back with its batch and verifies under the server key", func(t *testing.T) {
		batches := verify(t, dir, keysOf(t, testKDev))
		got := batches[0].Row.Receipt
		if got == nil || *got != rc {
			t.Fatalf("batch 1's receipt is %+v, want %+v", got, rc)
		}
		info, _ := ReadChain(dir)
		if err := ledger.VerifyReceipt(server.Public().(ed25519.PublicKey), info.DeviceID, 1, batches[0].Row.Head, *got); err != nil {
			t.Error(err)
		}
	})
}

// TestReceiptRegion checks that a receipt marks every frame of batches.seg
// before it as flushed. A frame there that isn't whole is damage, which
// Open refuses rather than cut the receipts after it; a torn frame after
// the last receipt is still a crash's torn tail.
func TestReceiptRegion(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, "pairtime ledger writer test server")
	server := ed25519.NewKeyFromSeed(seed)
	// build gives batches.seg the frames chain, row 1, row 2, receipt 1,
	// receipt 2, row 3.
	build := func(t *testing.T) (dir string, frames []frame) {
		t.Helper()
		dir = t.TempDir()
		w := open(t, dir, newClock(testT))
		rng := rand.New(rand.NewPCG(25, 26))
		var rows []Batch
		receipt := func(b Batch) {
			rc, err := ledger.SignReceipt(server, w.DeviceID(), b.Row.B, b.Row.Head, uint64(testT)+250)
			if err != nil {
				t.Fatal(err)
			}
			if err := w.StoreReceipt(b.Row.B, rc); err != nil {
				t.Fatal(err)
			}
		}
		for i := range 3 {
			if i == 2 {
				receipt(rows[0])
				receipt(rows[1])
			}
			if _, _, err := write(w, rng, "os.presence", testT); err != nil {
				t.Fatal(err)
			}
			b, _, err := w.Seal()
			if err != nil {
				t.Fatal(err)
			}
			rows = append(rows, b)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		frames = framesOf(t, filepath.Join(dir, batchesName))
		if got := len(frames); got != 6 || frames[3].kind != kindReceipt || frames[5].kind != kindBatch {
			t.Fatalf("batches.seg has %d frames, want the six this test builds", got)
		}
		return dir, frames
	}
	cases := []struct {
		name string
		edit func(t *testing.T, name string, frames []frame)
		// want is Open's error, or nil when it recovers with both
		// receipts.
		want error
	}{
		{"a changed byte in a row before a receipt is damage, not a torn tail",
			func(t *testing.T, name string, frames []frame) { flipByte(t, name, frames[1].off+frameHeadSize+1) }, ErrDamaged},
		{"a changed byte in a receipt before another receipt is damage",
			func(t *testing.T, name string, frames []frame) { flipByte(t, name, frames[3].off+frameHeadSize+1) }, ErrDamaged},
		{"a torn row after the last receipt is cut off and rebuilt, and the receipts stay",
			func(t *testing.T, name string, frames []frame) { truncate(t, name, frames[5].end-3) }, nil},
		{"zeros after the last receipt are a torn tail too",
			func(t *testing.T, name string, frames []frame) { appendBytes(t, name, make([]byte, 64)) }, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, frames := build(t)
			c.edit(t, filepath.Join(dir, batchesName), frames)
			w, err := openErr(dir, newClock(testT+60_000))
			if err == nil {
				err = w.Close()
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("Open: %v, want %v", err, c.want)
			}
			if c.want != nil {
				return
			}
			got := verify(t, dir, keysOf(t, testKDev))
			var with []uint64
			for _, b := range got {
				if b.Row.Receipt != nil {
					with = append(with, b.Row.B)
				}
			}
			if len(got) != 3 || !slices.Equal(with, []uint64{1, 2}) {
				t.Errorf("after Open, %d batches, with receipts for %v; want 3, with receipts for batches 1 and 2", len(got), with)
			}
		})
	}
}

// TestFiles checks the layout of the files: the chain frame, a record
// frame holding the header and body as pkg/ledger built them, and the row.
func TestFiles(t *testing.T) {
	dir := t.TempDir()
	w := open(t, dir, newClock(testT))
	in := sampleInput(rand.New(rand.NewPCG(1, 1)), "os.presence", testT, "", "")
	if _, err := w.Write(in); err != nil {
		t.Fatal(err)
	}
	device := w.DeviceID()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	ev := in.Event
	ev.Seq, ev.DeviceID = 1, device
	want, err := ledger.EventRecord(keysOf(t, testKDev), ev, ledger.Content{})
	if err != nil {
		t.Fatal(err)
	}
	seg, err := os.ReadFile(segPath(dir, "2026-11-19"))
	if err != nil {
		t.Fatal(err)
	}
	batches, err := os.ReadFile(filepath.Join(dir, batchesName))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("a record frame holds the 33-byte header and the body, byte for byte", func(t *testing.T) {
		frame := appendFrame(nil, kindRecord, append(want.Header[:], want.Body...))
		if !bytes.HasPrefix(seg, frame) {
			t.Errorf("the segment doesn't start with the record's frame")
		}
	})
	t.Run("the seal frame follows the batch's last record", func(t *testing.T) {
		fr := newFrameReader(bytes.NewReader(seg), 0)
		var kinds []byte
		for {
			fm, err := fr.next()
			if err != nil {
				break
			}
			kinds = append(kinds, fm.kind)
		}
		if string(kinds) != "rs" {
			t.Errorf("the segment's frames are %q, want a record and a seal", kinds)
		}
	})
	t.Run("batches.seg starts with the chain, then the row", func(t *testing.T) {
		fr := newFrameReader(bytes.NewReader(batches), 0)
		chain, err := fr.next()
		if err != nil || chain.kind != kindChain {
			t.Fatalf("first frame %q, %v", chain.kind, err)
		}
		var cp chainPayload
		if err := json.Unmarshal(chain.payload, &cp); err != nil || cp.DeviceID != device || cp.KeyID != testKeyID {
			t.Errorf("chain %+v, %v; want device %s and key %s", cp, err, device, testKeyID)
		}
		row, err := fr.next()
		if err != nil || row.kind != kindBatch {
			t.Fatalf("second frame %q, %v", row.kind, err)
		}
		var rp rowPayload
		if err := json.Unmarshal(row.payload, &rp); err != nil || rp.B != 1 || rp.FirstSeq != 1 || rp.LastSeq != 1 ||
			rp.N != 1 || rp.Start != (position{Seg: "2026-11-19"}) || rp.End.Off != int64(len(seg)) {
			t.Errorf("row %+v, %v", rp, err)
		}
	})
}
