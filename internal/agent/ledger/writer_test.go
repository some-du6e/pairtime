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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/sched"
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
// frame after a crash does.
func TestSealMode(t *testing.T) {
	type mode struct {
		sealed, row, read []bool
	}
	cases := []struct {
		name string
		// online is what Options.Online says at each of three seals, nil
		// for a writer without it. Each batch's first record comes while it
		// still says what it said at the seal before.
		online []bool
		// crash, when set, leaves the ledger as a crash after the third
		// seal's flush does, with batches.seg's rows of batches 2 and 3
		// lost, which the next Open rebuilds from their seal frames. Online
		// says local-only by then, so a rebuild that asked it would lose
		// batch 3's mark.
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
		})
	}
}

// TestNoteMode checks the mode the agent's config watch notes
// (Writer.NoteMode): a record written while it says local-only makes its
// batch local-only, though Online says online at the seal, as when the teen
// switches back online before the next seal; the stretch's first record
// seals at once, so a crash after it can't take the switch with it; and the
// switch back online seals what the stretch left.
func TestNoteMode(t *testing.T) {
	// A step notes a mode ("local", "online"), writes a record ("write"),
	// seals ("seal"), or waits for the batch a seal of the writer's own
	// sealed ("sealed").
	cases := []struct {
		name  string
		steps []string
		// online is the mode of each batch sealed, and records how many
		// records each holds.
		online  []bool
		records []int
	}{
		{"a record written while the noted mode is local-only seals at once, with the records before it, and its batch is local-only",
			[]string{"write", "local", "write", "sealed"}, []bool{false}, []int{2}},
		{"the switch back online seals the stretch's last records, and the records after it go online",
			[]string{"local", "write", "sealed", "write", "online", "sealed", "write", "seal"}, []bool{false, false, true}, []int{1, 1, 1}},
		{"only the stretch's first record seals at once",
			[]string{"local", "write", "sealed", "write", "write", "seal"}, []bool{false, false}, []int{1, 2}},
		{"a switch with no record written in it leaves the next batch online",
			[]string{"local", "online", "write", "seal"}, []bool{true}, []int{1}},
		{"a second stretch's first record seals at once too",
			[]string{"local", "write", "sealed", "online", "write", "seal", "local", "write", "sealed"}, []bool{false, true, false}, []int{1, 1, 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sealed := make(chan Batch, 8)
			w := open(t, t.TempDir(), newClock(testT), func(o *Options) {
				o.Online = func() bool { return true } // config.toml reads online at every seal
				o.Sealed = func(b Batch) { sealed <- b }
			})
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
			for i, step := range c.steps {
				switch step {
				case "local", "online":
					w.NoteMode(step == "online")
				case "write":
					if _, _, err := write(w, rng, "os.presence", testT+int64(i)*1000); err != nil {
						t.Fatal(err)
					}
				case "seal":
					// A seal the writer still owed may take the records first,
					// and then this one has none.
					if _, _, err := w.Seal(); err != nil {
						t.Fatalf("step %d: %v", i, err)
					}
					take()
				case "sealed":
					take()
				}
			}
			select {
			case b := <-sealed:
				t.Fatalf("batch %d was sealed after the last step", b.Row.B)
			case <-time.After(200 * time.Millisecond):
			}
			var online []bool
			var records []int
			for _, b := range got {
				online, records = append(online, b.Online), append(records, len(b.Records))
			}
			if !slices.Equal(online, c.online) || !slices.Equal(records, c.records) {
				t.Errorf("batches sealed online %v holding %v records, want %v holding %v", online, records, c.online, c.records)
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
