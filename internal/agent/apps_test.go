// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/adapters/t3code"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/presence"
	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/clock"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
	"github.com/some-du6e/pairtime/pkg/ledger/features"
)

// appT is when the apps tests' prompts reach the agent.
var appT = time.Date(2026, 11, 22, 15, 4, 5, 0, time.UTC).UnixMilli()

// The event ids of the apps tests' records: a prompt and another copy of
// its turn, and the relay.message records of two messages.
const (
	appPromptID = "019a0000-0000-7000-8000-00000000a001"
	appCopyID   = "019a0000-0000-7000-8000-00000000a002"
	appEarlier  = "019a0000-0000-7000-8000-00000000b001"
	appLater    = "019a0000-0000-7000-8000-00000000b002"
)

// fakeApp is an app reader whose answers a test sets, and which keeps what
// it was asked about. The apps component calls it holding its lock.
type fakeApp struct {
	pairing t3code.Pairing
	// batch is what the next read finds, and err how every read ends.
	batch t3code.Batch
	err   error
	// steers are the running turns the reader says the owner steered a
	// guest's message into.
	steers []t3code.Steer
	state  t3code.State
	status t3code.Status
	asked  []t3code.Prompt
	polls  int
}

func (f *fakeApp) Poll(context.Context) (t3code.Batch, error) {
	f.polls++
	b := f.batch
	f.batch = t3code.Batch{}
	return b, f.err
}

func (f *fakeApp) Pair(_ context.Context, p t3code.Prompt) (t3code.Pairing, t3code.Batch, error) {
	f.asked = append(f.asked, p)
	b := f.batch
	f.batch = t3code.Batch{}
	return f.pairing, b, f.err
}

func (f *fakeApp) Steers() []t3code.Steer { return f.steers }
func (f *fakeApp) State() t3code.State    { return f.state }
func (f *fakeApp) Status() t3code.Status  { return f.status }

// appsRig is an adapter host with a ledger writer, whose apps component
// reads with a fake reader.
type appsRig struct {
	dirs home.Dirs
	a    *agent
	h    *adapterHost
	p    *appsHost
	w    *ledger.Writer
	app  *fakeApp
}

func newAppsRig(t *testing.T) *appsRig {
	t.Helper()
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	st, _, err := state.Open(dirs.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	a := &agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: map[string]string{}, hooked: newHookTies(),
		table: newestTable(), state: st}
	w := testWriter(t, dirs)
	h := newAdapterHost(a)
	h.writer = w
	app := &fakeApp{}
	p := a.appsHost()
	p.reader, p.writer = app, w
	return &appsRig{dirs: dirs, a: a, h: h, p: p, w: w, app: app}
}

// write has the adapter host write rec, as it writes the records of a hook
// call or a log line.
func (r *appsRig) write(t *testing.T, rec adapters.Record) {
	t.Helper()
	st := &harnessStats{}
	if !r.h.write(st, rec, clock.Stamp{}) {
		t.Fatalf("the ledger refused a %s record: %s", rec.Event.Kind(), st.LastFailure)
	}
}

// records stops the rig's writer and reads back every event record of its
// ledger, in the order they were written.
func (r *appsRig) records(t *testing.T) []ledgerRecord {
	t.Helper()
	if err := r.w.Close(); err != nil {
		t.Fatal(err)
	}
	var out []ledgerRecord
	err := ledger.ReadBatches(r.dirs.Ledger(), 1, func(b ledger.Batch) error {
		for _, rec := range b.Records {
			body, err := pkgledger.ParseBody(rec.Body)
			if err != nil {
				return err
			}
			ev, err := body.Event()
			if err != nil {
				return err
			}
			out = append(out, ledgerRecord{ev: ev, body: body})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading the ledger back: %v", err)
	}
	return out
}

// recordIn returns the record of event id in recs, and where it is.
func recordIn(t *testing.T, recs []ledgerRecord, id string) (ledgerRecord, int) {
	t.Helper()
	for i, r := range recs {
		if r.ev.EventID == id {
			return r, i
		}
	}
	t.Fatalf("the ledger holds no record %s", id)
	return ledgerRecord{}, -1
}

// appEnv is the envelope of a record of session s_app in project p_app at
// appT, as the Claude Code adapter makes one for a call or a line that came
// via. An empty id has the host give the record one.
func appEnv(id string, via event.Via) event.Envelope {
	src := event.SourceHook
	if via == event.ViaLog {
		src = event.SourceLog
	}
	return event.Envelope{EventID: id, T: appT, Mono: 5_000, BootID: "boot-1", Source: src, Via: via,
		Harness: event.HarnessClaudeCode, SessionKey: "s_app", ProjectKey: "p_app"}
}

// appPrompt is the prompt.submit of turn t_1, with its text, that the
// Claude Code adapter makes of a call or a transcript line that came via.
func appPrompt(id string, via event.Via) adapters.Record {
	const text = "fix the flaky login test please"
	return adapters.Record{Event: event.Event{Envelope: appEnv(id, via),
		Data: &event.PromptSubmit{TurnKey: "t_1", LenChars: len(text), TypedLenEst: len(text),
			OriginDeclared: event.DeclaredNone}},
		Text: []byte(text)}
}

// appMessage is T3 Code's reader's record id of a 31-character message
// stamped a second before appT, read two seconds after it, and bound to
// session s_app, which names prompt when that prompt came first.
func appMessage(id, prompt string) t3code.Message {
	return t3code.Message{EventID: id, SeenAt: time.UnixMilli(appT + 2000), Harness: event.HarnessClaudeCode,
		SessionKey: "s_app", ProjectKey: "p_app",
		Data: event.RelayMessage{App: event.RelayAppT3Code, Client: event.RelayClientWeb, SentAt: appT - 1000,
			ThreadKey: "r_1", Declared: event.RelayDeclaredNone, LenChars: 31, Prompt: prompt}}
}

// TestAppsPrompt checks what the agent asks T3 Code's reader before it
// writes a prompt, and what it writes (DESIGN 5.1, 4.1 E-13): a prompt a
// hook or a log reader saw, of a session an agent app's server started, is
// asked about before its text is kept, takes the relay or the guest mark
// the answer gives it, and has the records the read found written after
// it; no other prompt is asked about; and only the reader's word sets
// relay and guest.
func TestAppsPrompt(t *testing.T) {
	cases := []struct {
		name     string
		app      bool // an agent app's server started the session
		noReader bool // the reader hasn't started
		via      event.Via
		late     bool
		preset   bool // an adapter or a sender put a relay and a guest mark on the prompt
		pairing  t3code.Pairing
		batch    []t3code.Message
		err      error
		// What the agent does: whether it asks the reader, the relay and
		// guest mark it writes the prompt with, whether the prompt's
		// record commits its text, and the records written after it.
		asked bool
		relay string
		guest bool
		text  bool
		after []string
	}{
		{name: "a hook prompt of a session an agent app's server started is asked about before its text is kept, and takes as its relay the earlier record it pairs with",
			app: true, via: event.ViaHook, pairing: t3code.Pairing{Known: true, Relay: appEarlier},
			asked: true, relay: appEarlier, text: true},
		{name: "a record the same read pairs with the prompt is written after the prompt and names it",
			app: true, via: event.ViaHook, pairing: t3code.Pairing{Known: true},
			batch: []t3code.Message{appMessage(appLater, appPromptID)},
			asked: true, text: true, after: []string{appLater}},
		{name: "a prompt a guest's run delivered is marked guest, and the ledger keeps none of its text",
			app: true, via: event.ViaHook, pairing: t3code.Pairing{Known: true, Guest: true},
			asked: true, guest: true},
		{name: "a guest's run an earlier read saw marks the prompt guest even when this read fails",
			app: true, via: event.ViaHook, pairing: t3code.Pairing{Guest: true}, err: t3code.ErrBusy,
			asked: true, guest: true},
		{name: "a prompt the reader can't answer for is marked nothing and keeps its text, as any prompt does",
			app: true, via: event.ViaHook, err: t3code.ErrNoSQLite3,
			asked: true, text: true},
		{name: "the records a failed read finished are written after the prompt all the same",
			app: true, via: event.ViaHook, batch: []t3code.Message{appMessage(appLater, "")}, err: t3code.ErrBusy,
			asked: true, text: true, after: []string{appLater}},
		{name: "a log prompt of a session an agent app's server started is asked about too",
			app: true, via: event.ViaLog, pairing: t3code.Pairing{Known: true, Relay: appEarlier},
			asked: true, relay: appEarlier, text: true},
		{name: "a prompt of a session no agent app's server started is asked nothing",
			via: event.ViaHook, text: true},
		{name: "lookback's prompt, of a time long past, is asked nothing",
			app: true, via: event.ViaLog, late: true, text: true},
		{name: "a plugin's prompt is asked nothing, since a relay record witnesses only a hook or log prompt (DESIGN 5.2)",
			app: true, via: event.ViaPlugin, text: true},
		{name: "before the reader has started, a prompt of an agent app's session is written as any prompt is",
			app: true, noReader: true, via: event.ViaHook, text: true},
		{name: "the relay and guest mark an adapter or a sender put on a prompt are dropped, so only the reader's word sets them",
			via: event.ViaHook, preset: true, text: true},
		{name: "the relay and guest mark a sender put on a prompt the reader is asked about give way to the reader's answer",
			app: true, via: event.ViaHook, preset: true, pairing: t3code.Pairing{Known: true},
			asked: true, text: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newAppsRig(t)
			if c.app {
				r.p.noteSession("s_app", event.ParentAgentApp)
			}
			if c.noReader {
				r.p.reader = nil
			}
			r.app.pairing, r.app.err, r.app.batch = c.pairing, c.err, t3code.Batch{Messages: c.batch}
			rec := appPrompt(appPromptID, c.via)
			rec.Late = c.late
			if c.preset {
				p := rec.Event.Data.(*event.PromptSubmit)
				p.Relay, p.Guest = appEarlier, true
			}
			r.write(t, rec)
			var want []t3code.Prompt
			if c.asked {
				want = []t3code.Prompt{{SessionKey: "s_app", T: appT, EventID: appPromptID}}
			}
			if !slices.Equal(r.app.asked, want) {
				t.Errorf("the reader was asked about %+v, want %+v", r.app.asked, want)
			}
			recs := r.records(t)
			got, at := recordIn(t, recs, appPromptID)
			if at != 0 {
				t.Errorf("the prompt is record %d of the ledger, want the first", at+1)
			}
			p := got.ev.Data.(*event.PromptSubmit)
			if p.Relay != c.relay || p.Guest != c.guest {
				t.Errorf("the prompt has relay %q and guest %v, want %q and %v", p.Relay, p.Guest, c.relay, c.guest)
			}
			if kept := got.body.C != nil; kept != c.text {
				t.Errorf("the prompt's record commits its text: %v, want %v", kept, c.text)
			}
			var after []string
			for _, rr := range recs[at+1:] {
				after = append(after, rr.ev.EventID)
			}
			if !slices.Equal(after, c.after) {
				t.Errorf("after the prompt the ledger holds %v, want %v", after, c.after)
			}
			for i, m := range c.batch {
				if i+at+1 >= len(recs) {
					break
				}
				if d, ok := recs[i+at+1].ev.Data.(*event.RelayMessage); !ok || *d != m.Data {
					t.Errorf("record %d after the prompt holds %+v, want the reader's %+v", i+1, recs[i+at+1].ev.Data, m.Data)
				}
			}
		})
	}
}

// TestAppsTurnCopies checks the copies of one turn, its hook's and its
// transcript's, or its spooled call's: the reader is asked once, about the
// first copy a hook or a log reader saw; every copy of a turn a guest's run
// delivered is marked guest and keeps no text; and only the copy asked
// about takes the relay, so one run claims one record of the turn.
func TestAppsTurnCopies(t *testing.T) {
	cases := []struct {
		name          string
		first, second event.Via
		// opening, when set, is the first copy's text; the second keeps the
		// turn's own.
		opening string
		pairing t3code.Pairing
		asked   string // the copy asked about
		guest   bool
		relays  [2]string
		texts   [2]bool // whether each copy's record commits its text
	}{
		{"a guest's turn marks its transcript's copy guest too, and keeps the text of neither, asking once",
			event.ViaHook, event.ViaLog, "", t3code.Pairing{Known: true, Guest: true}, appPromptID, true,
			[2]string{}, [2]bool{}},
		{"a relayed turn's transcript copy is asked nothing and takes no relay",
			event.ViaHook, event.ViaLog, "", t3code.Pairing{Known: true, Relay: appEarlier}, appPromptID, false,
			[2]string{appEarlier, ""}, [2]bool{true, true}},
		{"a transcript copy that came first is the one asked about, and the hook's copy after it takes its guest mark",
			event.ViaLog, event.ViaHook, "", t3code.Pairing{Known: true, Guest: true}, appPromptID, true,
			[2]string{}, [2]bool{}},
		{"a guest's turn marks the spooled copy of its call guest too",
			event.ViaHook, event.ViaPlugin, "", t3code.Pairing{Known: true, Guest: true}, appPromptID, true,
			[2]string{}, [2]bool{}},
		{"a spooled copy that came first is asked nothing, and the transcript copy after it is the one asked about",
			event.ViaPlugin, event.ViaLog, "", t3code.Pairing{Known: true, Relay: appEarlier}, appCopyID, false,
			[2]string{"", appEarlier}, [2]bool{true, true}},
		{"a turn whose asked copy opens as T3 Code opens a guest's message marks its transcript copy guest too, though that copy's text, as its adapter rebuilt it, doesn't open so",
			event.ViaHook, event.ViaLog, guestText, t3code.Pairing{Known: true}, appPromptID, true,
			[2]string{}, [2]bool{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newAppsRig(t)
			r.p.noteSession("s_app", event.ParentAgentApp)
			r.app.pairing = c.pairing
			first := appPrompt(appPromptID, c.first)
			if c.opening != "" {
				p := first.Event.Data.(*event.PromptSubmit)
				p.LenChars, p.TypedLenEst = len(c.opening), len(c.opening)
				first.Text = []byte(c.opening)
			}
			r.write(t, first)
			r.write(t, appPrompt(appCopyID, c.second))
			if len(r.app.asked) != 1 || r.app.asked[0].EventID != c.asked {
				t.Errorf("the reader was asked about %+v, want %s alone", r.app.asked, c.asked)
			}
			recs := r.records(t)
			for i, id := range []string{appPromptID, appCopyID} {
				got, _ := recordIn(t, recs, id)
				p := got.ev.Data.(*event.PromptSubmit)
				if kept := got.body.C != nil; p.Guest != c.guest || p.Relay != c.relays[i] || kept != c.texts[i] {
					t.Errorf("copy %d has guest %v, relay %q and its text kept %v; want %v, %q and %v",
						i+1, p.Guest, p.Relay, kept, c.guest, c.relays[i], c.texts[i])
				}
			}
		})
	}
}

// guestText is an invented prompt as T3 Code sends on a guest's message
// (T3 d8f3feff, GuestShareDecider.ts): its opening, the guest's name, and
// the guest's words.
const guestText = "Message from guest Robin, sent by the owner: could the settings page get a dark mode toggle?"

// TestAppsGuestOpening checks the device test of a guest's words (DESIGN
// 5.1, Q1 of #225): every copy of a prompt whose text opens as T3 Code
// opens a guest's message, past white space and an Ultrathink: line, is
// marked guest and keeps no text, on every path a copy can take, a hook's,
// a spooled call's, a transcript's and lookback's, whatever its harness and
// whether or not the reader knows of the guest's run, and takes no relay.
// A prompt with those words anywhere else keeps its text.
func TestAppsGuestOpening(t *testing.T) {
	cases := []struct {
		name    string
		app     bool // an agent app's server started the session
		via     event.Via
		late    bool
		harness event.Harness
		text    string
		pairing t3code.Pairing
		err     error
		asked   bool
		guest   bool
	}{
		{name: "a hook copy whose run the reader didn't know of is marked guest and keeps no text",
			app: true, via: event.ViaHook, text: guestText, pairing: t3code.Pairing{Known: true}, asked: true, guest: true},
		{name: "it takes no relay the reader gives it, since no message of the owner's witnesses a guest's words",
			app: true, via: event.ViaHook, text: guestText, pairing: t3code.Pairing{Known: true, Relay: appEarlier}, asked: true, guest: true},
		{name: "one the reader can't answer for is marked guest all the same",
			app: true, via: event.ViaHook, text: guestText, err: t3code.ErrNoSQLite3, asked: true, guest: true},
		{name: "a spooled call's copy, which the reader is never asked about, is marked guest",
			app: true, via: event.ViaPlugin, text: guestText, guest: true},
		{name: "a transcript's copy is marked guest",
			app: true, via: event.ViaLog, text: guestText, pairing: t3code.Pairing{Known: true}, asked: true, guest: true},
		{name: "lookback's copy, of a time no read answers for, is marked guest",
			app: true, via: event.ViaLog, late: true, text: guestText, guest: true},
		{name: "a copy of a session no agent app's server started is marked guest, as one sent while the agent was down is",
			via: event.ViaHook, text: guestText, guest: true},
		{name: "a copy from any harness is marked guest",
			via: event.ViaHook, harness: event.HarnessCodex, text: guestText, guest: true},
		{name: "the opening counts past white space and a leading Ultrathink: line, as T3 sends one",
			app: true, via: event.ViaHook, text: "  \n Ultrathink:\n\n" + guestText, pairing: t3code.Pairing{Known: true}, asked: true, guest: true},
		{name: "a prompt that has the words further on keeps its text",
			app: true, via: event.ViaHook, text: "please reply to this: " + guestText, pairing: t3code.Pairing{Known: true}, asked: true},
		{name: "an Ultrathink: line with more words on it isn't one T3 adds, so the words after it aren't an opening",
			via: event.ViaHook, text: "Ultrathink: about this\n" + guestText},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newAppsRig(t)
			if c.app {
				r.p.noteSession("s_app", event.ParentAgentApp)
			}
			r.app.pairing, r.app.err = c.pairing, c.err
			rec := appPrompt(appPromptID, c.via)
			rec.Late = c.late
			if c.harness != "" {
				rec.Event.Harness = c.harness
			}
			p := rec.Event.Data.(*event.PromptSubmit)
			p.LenChars, p.TypedLenEst = len(c.text), len(c.text)
			rec.Text = []byte(c.text)
			r.write(t, rec)
			if asked := len(r.app.asked) > 0; asked != c.asked {
				t.Errorf("the reader was asked about the prompt: %v, want %v", asked, c.asked)
			}
			got, _ := recordIn(t, r.records(t), appPromptID)
			ps := got.ev.Data.(*event.PromptSubmit)
			if ps.Guest != c.guest || ps.Relay != "" {
				t.Errorf("the prompt has guest %v and relay %q, want guest %v and no relay", ps.Guest, ps.Relay, c.guest)
			}
			if kept := got.body.C != nil; kept == c.guest {
				t.Errorf("the prompt's record commits its text: %v, want %v", kept, !c.guest)
			}
		})
	}
}

// appSteer is a running turn T3 Code's reader says the owner steered a
// guest's message into, of session s_app, from from to to (0 while it
// runs).
func appSteer(from, to int64) []t3code.Steer {
	return []t3code.Steer{{Run: "r_run", Sessions: []string{"s_app"}, From: from, To: to}}
}

// TestAppsGuestSteer checks the mark of a guest's steer (DESIGN 5.1;
// docs/errata/2026-11-23-t3-reader.md, decision 4): T3 Code's reader
// reports each running turn the owner steered a guest's message into
// (t3code.Steer), and a copy of a prompt of one of the turn's sessions,
// stamped from the turn's From to its To, or after its From while it runs,
// is marked guest and keeps no text, on whatever path it came, unless a
// run claimed it.
func TestAppsGuestSteer(t *testing.T) {
	cases := []struct {
		name    string
		steers  []t3code.Steer
		session string // the copy's session, s_app when ""
		via     event.Via
		late    bool
		pairing t3code.Pairing
		batch   []t3code.Message
		err     error // how the read for the copy ends
		// What the agent writes: the copy's guest mark and relay, and the
		// records after it. A copy marked guest keeps no text.
		guest bool
		relay string
		after []string
	}{
		{name: "a hook copy stamped in a steered turn is marked guest and keeps no text, though no guest's run delivered it",
			steers: appSteer(appT-1000, appT+1000), via: event.ViaHook, pairing: t3code.Pairing{Known: true}, guest: true},
		{name: "a copy stamped at the turn's From is in it",
			steers: appSteer(appT, appT+1000), via: event.ViaHook, pairing: t3code.Pairing{Known: true}, guest: true},
		{name: "a copy stamped at the turn's To is in it",
			steers: appSteer(appT-1000, appT), via: event.ViaHook, pairing: t3code.Pairing{Known: true}, guest: true},
		{name: "a copy stamped before the turn's From isn't, as the turn's own prompt before the steer isn't",
			steers: appSteer(appT+1, appT+1000), via: event.ViaHook, pairing: t3code.Pairing{Known: true}},
		{name: "a copy stamped after the turn's end isn't, as the next turn's isn't",
			steers: appSteer(appT-1000, appT-1), via: event.ViaHook, pairing: t3code.Pairing{Known: true}},
		{name: "while the turn runs, every later copy of its session is in it",
			steers: appSteer(appT-60_000, 0), via: event.ViaHook, pairing: t3code.Pairing{Known: true}, guest: true},
		{name: "a copy of another session isn't",
			steers: appSteer(appT-1000, appT+1000), session: "s_other", via: event.ViaHook, pairing: t3code.Pairing{Known: true}},
		{name: "a copy a message's run claimed keeps its relay and its text",
			steers: appSteer(appT-1000, appT+1000), via: event.ViaHook, pairing: t3code.Pairing{Known: true, Relay: appEarlier},
			relay: appEarlier},
		{name: "a copy a record of the same read names keeps its text, and the record comes after it",
			steers: appSteer(appT-1000, appT+1000), via: event.ViaHook, pairing: t3code.Pairing{Known: true},
			batch: []t3code.Message{appMessage(appLater, appPromptID)}, after: []string{appLater}},
		{name: "a copy a guest's run claimed is a guest's as before",
			steers: appSteer(appT-1000, appT+1000), via: event.ViaHook, pairing: t3code.Pairing{Known: true, Guest: true}, guest: true},
		{name: "a copy the reader can't answer for is marked from what an earlier read said",
			steers: appSteer(appT-1000, appT+1000), via: event.ViaHook, err: t3code.ErrBusy, guest: true},
		{name: "a spooled call's copy, which the reader is never asked about, is marked from what the last read said",
			steers: appSteer(appT-1000, appT+1000), via: event.ViaPlugin, guest: true},
		{name: "lookback's copy, of a time no read answers for, is marked too",
			steers: appSteer(appT-1000, appT+1000), via: event.ViaLog, late: true, guest: true},
		{name: "with no steered turn, a copy is marked nothing",
			via: event.ViaHook, pairing: t3code.Pairing{Known: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newAppsRig(t)
			r.p.noteSession("s_app", event.ParentAgentApp)
			r.p.noteSession("s_other", event.ParentAgentApp)
			// A read the apps job asked for learns of the turn before the
			// copy comes, as T3 stores the steer before Claude Code takes it.
			r.app.steers = c.steers
			r.p.poll()
			r.app.pairing, r.app.batch, r.app.err = c.pairing, t3code.Batch{Messages: c.batch}, c.err
			rec := appPrompt(appPromptID, c.via)
			rec.Late = c.late
			if c.session != "" {
				rec.Event.SessionKey = c.session
			}
			r.write(t, rec)
			recs := r.records(t)
			got, at := recordIn(t, recs, appPromptID)
			p := got.ev.Data.(*event.PromptSubmit)
			if p.Guest != c.guest || p.Relay != c.relay {
				t.Errorf("the copy has guest %v and relay %q, want %v and %q", p.Guest, p.Relay, c.guest, c.relay)
			}
			if kept := got.body.C != nil; kept == c.guest {
				t.Errorf("the copy's record commits its text: %v, want %v", kept, !c.guest)
			}
			var after []string
			for _, rr := range recs[at+1:] {
				after = append(after, rr.ev.EventID)
			}
			if !slices.Equal(after, c.after) {
				t.Errorf("after the copy the ledger holds %v, want %v", after, c.after)
			}
		})
	}
}

// TestAppsGuestSteerCopies checks a steered guest's mark on the copies of
// one turn, its hook's and its transcript's (decision 4 of
// docs/errata/2026-11-23-t3-reader.md): it reaches past the turn cache, so
// a turn asked about before the reader knew of the steer has its later copy
// marked; a turn a message's run claimed keeps its later copy as it is; and
// a turn the steer marked marks its later copy whenever that copy is
// stamped.
func TestAppsGuestSteerCopies(t *testing.T) {
	cases := []struct {
		name string
		// before and after are what the reader says of steered turns when
		// the first copy comes and when the second does.
		before, after []t3code.Steer
		pairing       t3code.Pairing
		// late moves the second copy's stamp past the steered turn's end.
		late   int64
		guests [2]bool
		relays [2]string
	}{
		{"a turn asked about before the reader knew of the steer has its transcript copy marked, past the turn cache",
			nil, appSteer(appT-1000, appT+1000), t3code.Pairing{Known: true}, 0,
			[2]bool{false, true}, [2]string{}},
		{"a turn a message's run claimed keeps its transcript copy as it is, though a steered turn covers it",
			nil, appSteer(appT-1000, appT+1000), t3code.Pairing{Known: true, Relay: appEarlier}, 0,
			[2]bool{false, false}, [2]string{appEarlier, ""}},
		{"a turn the steer marked marks its transcript copy too, though that copy is stamped after the turn's end",
			appSteer(appT-1000, appT), appSteer(appT-1000, appT), t3code.Pairing{Known: true}, 500,
			[2]bool{true, true}, [2]string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newAppsRig(t)
			r.p.noteSession("s_app", event.ParentAgentApp)
			r.app.steers = c.before
			r.p.poll()
			r.app.pairing = c.pairing
			r.write(t, appPrompt(appPromptID, event.ViaHook))
			r.app.steers = c.after
			r.p.poll()
			second := appPrompt(appCopyID, event.ViaLog)
			second.Event.T += c.late
			r.write(t, second)
			if len(r.app.asked) != 1 || r.app.asked[0].EventID != appPromptID {
				t.Errorf("the reader was asked about %+v, want the hook's copy alone", r.app.asked)
			}
			recs := r.records(t)
			for i, id := range []string{appPromptID, appCopyID} {
				got, _ := recordIn(t, recs, id)
				p := got.ev.Data.(*event.PromptSubmit)
				if kept := got.body.C != nil; p.Guest != c.guests[i] || p.Relay != c.relays[i] || kept == c.guests[i] {
					t.Errorf("copy %d has guest %v, relay %q and its text kept %v; want %v, %q and %v",
						i+1, p.Guest, p.Relay, kept, c.guests[i], c.relays[i], !c.guests[i])
				}
			}
		})
	}
}

// TestAppsSessions checks whose prompts the agent asks T3 Code's reader
// about: those of a session whose nearest non-shell ancestor is an agent
// app's server, parent_class agent_app, as its session.start or a hook
// call's walk says, until a record or a walk names another class.
func TestAppsSessions(t *testing.T) {
	type step func(t *testing.T, r *appsRig)
	start := func(session string, class event.ParentClass) step {
		return func(t *testing.T, r *appsRig) {
			env := appEnv("", event.ViaHook)
			env.SessionKey = session
			r.write(t, adapters.Record{Event: event.Event{Envelope: env, Data: &event.SessionStart{
				StartKind: event.StartStartup, Entrypoint: event.EntrypointUnknown, ParentClass: class}}})
		}
	}
	walk := func(w *platform.Lineage) step {
		return func(t *testing.T, r *appsRig) { r.p.noteWalk("s_app", w) }
	}
	cases := []struct {
		name  string
		steps []step
		asked bool
	}{
		{"a session.start whose parent is an agent app's server makes its session's prompts ones to ask about",
			[]step{start("s_app", event.ParentAgentApp)}, true},
		{"a hook call's walk that found an agent app's server does too, as after a restart, with no start of its own",
			[]step{walk(&platform.Lineage{ParentClass: event.ParentAgentApp})}, true},
		{"a later start of unknown parentage, a spooled call's or a log reader's, leaves the mark",
			[]step{start("s_app", event.ParentAgentApp), start("s_app", event.ParentUnknown)}, true},
		{"a later start from a terminal takes the mark away",
			[]step{start("s_app", event.ParentAgentApp), start("s_app", event.ParentTerminalApp)}, false},
		{"a later walk that found a terminal takes it away too",
			[]step{walk(&platform.Lineage{ParentClass: event.ParentAgentApp}), walk(&platform.Lineage{ParentClass: event.ParentTerminalApp})}, false},
		{"a call whose walk didn't count says nothing",
			[]step{walk(&platform.Lineage{ParentClass: event.ParentAgentApp}), walk(nil)}, true},
		{"another session's start marks nothing", []step{start("s_other", event.ParentAgentApp)}, false},
		{"a session nothing said anything of is asked nothing", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newAppsRig(t)
			for _, s := range c.steps {
				s(t, r)
			}
			r.write(t, appPrompt(appPromptID, event.ViaHook))
			if asked := len(r.app.asked) > 0; asked != c.asked {
				t.Errorf("the reader was asked about the prompt: %v, want %v", asked, c.asked)
			}
		})
	}
}

// TestAppsPoll checks the records a read the apps job asked for writes
// (DESIGN 4.1 E-12, 5.1): one relay.message for each message the reader
// found, source log and via app, at the moment the reader read its row and
// with this run's clock then, bound to its run's session or to none,
// committing no text, with f.pc the class of the app's own text, an answer
// when the session the record is bound to last asked a question.
func TestAppsPoll(t *testing.T) {
	r := newAppsRig(t)
	// The session's last output asked a question.
	r.write(t, adapters.Record{Event: event.Event{Envelope: appEnv("", event.ViaHook),
		Data: &event.AgentOutput{TurnKey: "t_0", Words: 5, IsFinal: true, AsksQuestion: true}},
		Text: []byte("Should the retry wait longer?")})
	bound := appMessage(appEarlier, "")
	unbound := appMessage(appLater, "")
	unbound.SessionKey, unbound.Harness = "", event.HarnessNone
	r.app.batch = t3code.Batch{Messages: []t3code.Message{bound, unbound}}
	r.p.poll()
	if r.app.polls != 1 {
		t.Fatalf("the reader read %d times, want once", r.app.polls)
	}
	recs := r.records(t)
	cases := []struct {
		name    string
		m       t3code.Message
		session string
		class   features.Class
	}{
		{"a message whose run went to a Claude Code session is bound to it, and answers the question the session asked",
			bound, "s_app", features.Answer},
		{"a message whose run hasn't started is bound to no session, and answers nothing",
			unbound, event.NoSession, features.Substantive},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := recordIn(t, recs, c.m.EventID)
			mono, boot := ledger.RunClock(c.m.SeenAt)
			want := event.Envelope{EventID: c.m.EventID, DeviceID: got.ev.DeviceID, Seq: got.ev.Seq,
				T: c.m.SeenAt.UnixMilli(), Mono: mono, BootID: boot, Source: event.SourceLog, Via: event.ViaApp,
				Harness: c.m.Harness, SessionKey: c.session, ProjectKey: "p_app"}
			if got.ev.Envelope != want {
				t.Errorf("envelope %+v, want %+v", got.ev.Envelope, want)
			}
			if d, ok := got.ev.Data.(*event.RelayMessage); !ok || *d != c.m.Data {
				t.Errorf("data %+v, want the reader's %+v", got.ev.Data, c.m.Data)
			}
			if got.body.C != nil {
				t.Error("the record commits a text, which the reader dropped")
			}
			var f features.F
			if err := json.Unmarshal(got.body.F, &f); err != nil {
				t.Fatal(err)
			}
			if f.PC != c.class || f.MH != nil || f.TH != nil || f.ES != nil {
				t.Errorf("f %+v, want v and pc %q alone", f, c.class)
			}
		})
	}
}

// appRead is how one read of a test's fake reader ends: its error, and
// whether its status says T3 ran (t3code.Status.Running: the agent's process
// scan found T3's server, and the store had its -wal and -shm files).
type appRead struct {
	err     error
	running bool
}

// TestAppsCadence checks how often the apps job reads (DESIGN 3.3): every
// appsEvery while T3 runs, by the process scan and the store's files, and
// the reader reads its store or was kept from it by what passes, and every
// appsIdle while T3 isn't there or isn't running, leftover files included,
// there is no sqlite3, or the probe refused the schema.
func TestAppsCadence(t *testing.T) {
	cases := []struct {
		name   string
		reads  []appRead // in order
		cancel bool      // the agent stops before the last read
		want   time.Duration
	}{
		{"a read of a running T3's store reads again every appsEvery", []appRead{{nil, true}}, false, appsEvery},
		{"a store busy with t3 doctor's compaction is read again every appsEvery", []appRead{{t3code.ErrBusy, true}}, false, appsEvery},
		{"a slow read is tried again every appsEvery", []appRead{{context.DeadlineExceeded, true}}, false, appsEvery},
		{"no store waits appsIdle", []appRead{{t3code.ErrNoStore, false}}, false, appsIdle},
		{"T3 stopping slows the reads back to appsIdle", []appRead{{nil, true}, {t3code.ErrNotRunning, false}}, false, appsIdle},
		{"leftover -wal and -shm files with no T3 server running are read every appsIdle, though the reader reads them",
			[]appRead{{nil, true}, {nil, false}}, false, appsIdle},
		{"no sqlite3 on PATH slows them too", []appRead{{nil, true}, {t3code.ErrNoSQLite3, true}}, false, appsIdle},
		{"a schema the probe refused slows them too, so it isn't probed every appsEvery",
			[]appRead{{nil, true}, {&t3code.SchemaError{Why: "migration 55 isn't recorded"}, true}}, false, appsIdle},
		{"a sqlite3 with no JSON functions slows them too",
			[]appRead{{nil, true}, {fmt.Errorf("the probe: %w", t3code.ErrNoJSON), true}}, false, appsIdle},
		{"T3 starting again speeds them up", []appRead{{t3code.ErrNotRunning, false}, {nil, true}}, false, appsEvery},
		{"a read the agent's stop cut short leaves the cadence as it was", []appRead{{context.Canceled, true}}, true, appsIdle},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newAppsRig(t)
			sch := sched.New()
			r.p.job = sch.Every("apps", appsIdle, func(time.Time) {})
			for i, rd := range c.reads {
				if c.cancel && i == len(c.reads)-1 {
					r.p.cancel()
				}
				r.app.err, r.app.status.Running = rd.err, rd.running
				r.p.poll()
			}
			if jobs := sch.Jobs(); len(jobs) != 1 || jobs[0].Every != c.want {
				t.Errorf("jobs %+v, want the apps job every %v", jobs, c.want)
			}
		})
	}
}

// TestAppsContext checks that T3 Code running keeps a project context live,
// as a running harness process does (D-relayed-context), only while
// relayed credit is possible: a read that finds T3 running, by the process
// scan and the store's files, with a store the reader can read makes
// presence's context live, alone at a sample a minute; one with no sqlite3
// or a schema the probe refused makes none, and neither do leftover files
// with no T3 server, T3 stopped or no store at all.
func TestAppsContext(t *testing.T) {
	cases := []struct {
		name    string
		reads   []appRead // in order
		live    bool
		appOnly bool
	}{
		{"a read that finds T3 running makes a project context live, T3's alone", []appRead{{nil, true}}, true, true},
		{"a store busy for a moment keeps the context", []appRead{{nil, true}, {t3code.ErrBusy, true}}, true, true},
		{"a read that finds T3 running with no sqlite3 to read its store makes none, since nothing can be credited",
			[]appRead{{t3code.ErrNoSQLite3, true}}, false, false},
		{"one that finds it running with a schema the probe refused makes none",
			[]appRead{{&t3code.SchemaError{Why: "migration 55 isn't recorded"}, true}}, false, false},
		{"leftover -wal and -shm files with no T3 server running make none", []appRead{{nil, false}}, false, false},
		{"a read that finds T3 stopped ends the context", []appRead{{nil, true}, {t3code.ErrNotRunning, false}}, false, false},
		{"a read that finds no store makes none", []appRead{{t3code.ErrNoStore, false}}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newAppsRig(t)
			r.a.presence = presence.New(presence.Options{Sched: sched.New(), Params: r.a.table})
			for _, rd := range c.reads {
				r.app.err, r.app.status.Running = rd.err, rd.running
				r.p.poll()
			}
			if got := r.a.presence.Status(); got.Live != c.live || got.AppOnly != c.appOnly {
				t.Errorf("a project context is live: %v, T3's alone: %v; want %v and %v", got.Live, got.AppOnly, c.live, c.appOnly)
			}
		})
	}
}

// TestAppsServer checks what the agent's process scan tells the reader of
// T3 Code's server (DESIGN 3.3): with the store's files, a server of this
// user's makes T3 run, and on Windows one of any session, where T3's
// scheduled service can run outside the user's; another user's doesn't,
// nor a scan that failed. A change of word has the reader read at once.
func TestAppsServer(t *testing.T) {
	me, other := platform.UIDOwner(1000), platform.UIDOwner(1001)
	t3 := func(owner string) []platform.Proc {
		return []platform.Proc{{PID: 1, Name: "systemd", Owner: platform.UIDOwner(0)}, {PID: 4242, PPID: 1, Name: "t3", Owner: owner}}
	}
	cases := []struct {
		name  string
		goos  string
		procs []platform.Proc
		self  string
		want  bool
	}{
		{"T3's server running as this user is T3 running", platform.Linux, t3(me), me, true},
		{"another user's T3 server isn't", platform.Linux, t3(other), me, false},
		{"a scan that failed finds none", platform.Linux, nil, me, false},
		{"on Windows a T3 server in another session, as T3's scheduled service runs, is T3 running", platform.Windows,
			[]platform.Proc{{PID: 4242, Name: "t3.exe", Owner: platform.SessionOwner(0)}}, platform.SessionOwner(1), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newAppsRig(t)
			r.p.reader, r.p.writer = nil, nil
			r.p.stores = []t3code.Store{{Path: filepath.Join(r.dirs.Data, "t3", "userdata", "statev2.sqlite"), From: t3code.FromDefault}}
			var got t3code.Options
			r.p.newReader = func(o t3code.Options) (appReader, error) {
				got = o
				return r.app, nil
			}
			r.p.attach(r.w, make([]byte, 32), func(string) t3code.Folder { return t3code.Folder{} })
			select {
			case <-r.p.kick: // attach's own read
			default:
				t.Fatal("attach didn't ask for a read")
			}
			r.p.noteServer(c.goos, c.procs, c.self)
			if got.Server == nil {
				t.Fatal("the reader has no word of T3's server")
			}
			if s := got.Server(); s != c.want {
				t.Errorf("the reader hears T3's server runs: %v, want %v", s, c.want)
			}
			select {
			case <-r.p.kick:
				if !c.want {
					t.Error("a scan that found no server, as before it, asked for a read")
				}
			default:
				if c.want {
					t.Error("a scan that found T3's server didn't ask for a read")
				}
			}
			r.p.noteServer(c.goos, c.procs, c.self)
			select {
			case <-r.p.kick:
				t.Error("a second scan with the same word asked for another read")
			default:
			}
		})
	}
}

// TestAppsState checks what state.json keeps of T3 Code's reader: attach
// hands the reader what an earlier run kept, with the stores Locate found,
// K_session's keys and the folder resolver, and has it read at once; a
// read that wrote records saves the reader's memory at once, one that
// wrote none only appsSaveEvery after the last save, and the stop saves it
// whatever.
func TestAppsState(t *testing.T) {
	kept := t3code.State{Cursor: &t3code.Cursor{RowID: 41, Message: "r_msg", CreatedAt: appT},
		Prompts: []t3code.PromptState{{Session: "s_app", T: appT, EventID: appPromptID}}}
	saved := func(t *testing.T, r *appsRig) (t3code.State, bool) {
		t.Helper()
		st, _, err := state.Open(r.dirs.StateFile())
		if err != nil {
			t.Fatal(err)
		}
		var as appsState
		ok, err := st.Get(appsKey, &as)
		if err != nil {
			t.Fatal(err)
		}
		return as.T3Code, ok
	}
	t.Run("attach hands the reader what an earlier run kept, and has it read at once", func(t *testing.T) {
		r := newAppsRig(t)
		r.p.reader, r.p.writer = nil, nil
		if err := r.a.state.Put(appsKey, appsState{T3Code: kept}); err != nil {
			t.Fatal(err)
		}
		r.p.stores = []t3code.Store{{Path: filepath.Join(r.dirs.Data, "t3", "userdata", "statev2.sqlite"), From: t3code.FromDefault}}
		var got t3code.Options
		r.p.newReader = func(o t3code.Options) (appReader, error) {
			got = o
			return r.app, nil
		}
		r.p.attach(r.w, make([]byte, 32), func(string) t3code.Folder { return t3code.Folder{} })
		if r.p.reader != appReader(r.app) || r.p.writer != r.w {
			t.Fatal("attach didn't start the reader with the writer")
		}
		if !reflect.DeepEqual(got.State, kept) || !slices.Equal(got.Stores, r.p.stores) || got.Keys == nil ||
			got.Folder == nil || got.Table != r.a.table {
			t.Errorf("the reader's options are %+v", got)
		}
		select {
		case <-r.p.kick:
		default:
			t.Error("attach didn't ask for a read")
		}
	})
	t.Run("attach after the stop starts no reader", func(t *testing.T) {
		r := newAppsRig(t)
		r.p.reader, r.p.writer = nil, nil
		r.p.stores = []t3code.Store{{Path: filepath.Join(r.dirs.Data, "statev2.sqlite"), From: t3code.FromDefault}}
		r.p.newReader = func(t3code.Options) (appReader, error) { return r.app, nil }
		r.p.close()
		r.p.attach(r.w, make([]byte, 32), func(string) t3code.Folder { return t3code.Folder{} })
		if r.p.reader != nil {
			t.Error("a reader started after the stop")
		}
	})
	t.Run("a read that wrote records saves the reader's memory at once", func(t *testing.T) {
		r := newAppsRig(t)
		r.app.state = kept
		r.p.savedAt = r.p.now()
		r.app.batch = t3code.Batch{Messages: []t3code.Message{appMessage(appEarlier, "")}}
		r.p.poll()
		if got, ok := saved(t, r); !ok || !reflect.DeepEqual(got, kept) {
			t.Errorf("state.json keeps %+v (%v), want %+v", got, ok, kept)
		}
	})
	t.Run("a read that wrote nothing saves the reader's memory only appsSaveEvery after the last save", func(t *testing.T) {
		r := newAppsRig(t)
		r.app.state = kept
		now := time.Now()
		r.p.now = func() time.Time { return now }
		r.p.savedAt = now
		r.p.poll()
		if got, ok := saved(t, r); ok {
			t.Errorf("state.json keeps %+v right after a save, want nothing yet", got)
		}
		now = now.Add(appsSaveEvery)
		r.p.poll()
		if got, ok := saved(t, r); !ok || !reflect.DeepEqual(got, kept) {
			t.Errorf("state.json keeps %+v (%v), want %+v", got, ok, kept)
		}
	})
	t.Run("the stop saves the reader's memory whatever", func(t *testing.T) {
		r := newAppsRig(t)
		r.app.state = kept
		r.p.savedAt = r.p.now()
		r.p.close()
		if got, ok := saved(t, r); !ok || !reflect.DeepEqual(got, kept) {
			t.Errorf("state.json keeps %+v (%v), want %+v", got, ok, kept)
		}
	})
}

// TestAppsStatus checks the apps component's part of GET /v1/status, which
// doctor's T3 Code line reads (DESIGN 5.9): nothing before the reader's
// first read; then the reader's status as its last read left it, with
// today's counts alone, read without waiting for a read; and when a session
// an agent app's server started last reached the agent, which state.json
// keeps across a restart.
func TestAppsStatus(t *testing.T) {
	now := time.Date(2026, 11, 23, 15, 4, 5, 0, time.UTC)
	today := now.Format(time.DateOnly)
	read := func(st t3code.Status) func(*appsRig) {
		return func(r *appsRig) {
			r.app.status = st
			r.p.poll()
		}
	}
	cases := []struct {
		name  string
		steps []func(*appsRig)
		want  *t3Reply
	}{
		{"before the reader's first read the status has no T3 Code part", nil, nil},
		{"after a read the status is the reader's, with today's counts and whether T3 ran",
			[]func(*appsRig){read(t3code.Status{Reading: true, Newer: true, Running: true, Day: today,
				Counts: t3code.Counts{Witnessed: 3, WithServer: 2, Declared: 1, Paired: 2, Aged: 1, Ahead: 1}})},
			&t3Reply{Reading: true, Newer: true, Running: true, Day: today, Witnessed: 3, WithServer: 2, Paired: 2, Aged: 1, Ahead: 1}},
		{"a reader that doesn't read says why, in its own words",
			[]func(*appsRig){read(t3code.Status{Off: t3code.OffSchema, Problem: "T3 Code's table x has no column y", Day: today})},
			&t3Reply{Off: t3code.OffSchema, Problem: "T3 Code's table x has no column y", Day: today}},
		{"a missing sqlite3 is in the status while T3 isn't running too, so doctor can say how to install it",
			[]func(*appsRig){read(t3code.Status{Off: t3code.OffNotRunning, NoSQLite3: true, Day: today})},
			&t3Reply{Off: t3code.OffNotRunning, NoSQLite3: true, Day: today}},
		{"counts the last read left on another day count nothing today, as just after midnight",
			[]func(*appsRig){read(t3code.Status{Reading: true, Day: "2026-11-22", Counts: t3code.Counts{Witnessed: 4, WithServer: 4, Paired: 4, Aged: 2, Ahead: 1}})},
			&t3Reply{Reading: true, Day: today}},
		{"the status says when a session an agent app's server started last reached the agent",
			[]func(*appsRig){
				func(r *appsRig) { r.p.noteSession("s_app", event.ParentAgentApp) },
				read(t3code.Status{Reading: true, Day: today}),
			},
			&t3Reply{Reading: true, Day: today, SessionAt: now.UnixMilli()}},
		{"a session of another class says nothing of when T3 Code's last reached the agent",
			[]func(*appsRig){
				func(r *appsRig) { r.p.noteSession("s_term", event.ParentTerminalApp) },
				read(t3code.Status{Reading: true, Day: today}),
			},
			&t3Reply{Reading: true, Day: today}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newAppsRig(t)
			r.a.loc = time.UTC
			r.p.now = func() time.Time { return now }
			for _, s := range c.steps {
				s(r)
			}
			if got := r.p.reply().T3Code; !reflect.DeepEqual(got, c.want) {
				t.Errorf("t3_code is %+v, want %+v", got, c.want)
			}
		})
	}
	t.Run("the status doesn't wait for a read under way, which holds the component's lock", func(t *testing.T) {
		r := newAppsRig(t)
		r.p.mu.Lock()
		defer r.p.mu.Unlock()
		done := make(chan appsReply, 1)
		go func() { done <- r.p.reply() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the status waited for the read")
		}
	})
	t.Run("state.json keeps when a session an agent app's server started last reached the agent, so a restart still knows", func(t *testing.T) {
		r := newAppsRig(t)
		r.p.now = func() time.Time { return now }
		r.p.noteSession("s_app", event.ParentAgentApp)
		r.p.poll()
		st, _, err := state.Open(r.dirs.StateFile())
		if err != nil {
			t.Fatal(err)
		}
		var as appsState
		if ok, err := st.Get(appsKey, &as); err != nil || !ok || as.SessionAt != now.UnixMilli() {
			t.Fatalf("state.json keeps %+v (%v, %v), want session_at %d", as, ok, err, now.UnixMilli())
		}
		next := newAppsRig(t)
		next.p.reader, next.p.writer = nil, nil
		if err := next.a.state.Put(appsKey, as); err != nil {
			t.Fatal(err)
		}
		next.p.stores = []t3code.Store{{Path: filepath.Join(next.dirs.Data, "t3", "userdata", "statev2.sqlite"), From: t3code.FromDefault}}
		next.p.newReader = func(t3code.Options) (appReader, error) { return next.app, nil }
		next.p.attach(next.w, make([]byte, 32), func(string) t3code.Folder { return t3code.Folder{} })
		if _, heard, _ := next.p.t3Status(); !heard.Equal(now) {
			t.Errorf("after the restart the agent last heard of such a session at %v, want %v", heard, now)
		}
	})
}

// TestAppFolder checks pairtime's own word on a folder a T3 Code thread
// runs in, which the reader's folder test reads (DESIGN 5.1): tracked, with
// its project, only when the git adapter found a project and the folder
// isn't out of pairtime.
func TestAppFolder(t *testing.T) {
	cases := []struct {
		name        string
		p           adapters.Project
		ok, ignored bool
		want        t3code.Folder
	}{
		{"a folder with a project is tracked, under the project's key",
			adapters.Project{Key: "p_app", Root: "/w/app"}, true, false, t3code.Folder{State: t3code.FolderTracked, Project: "p_app"}},
		{"a folder out of pairtime is out, whatever its project (DESIGN 8.8)",
			adapters.Project{Key: "p_app", Root: "/w/app"}, true, true, t3code.Folder{State: t3code.FolderOut}},
		{"a folder with no project, as one in no repo, is out",
			adapters.Project{}, false, false, t3code.Folder{State: t3code.FolderOut}},
		{"a project with no key is out",
			adapters.Project{Root: "/w/app"}, true, false, t3code.Folder{State: t3code.FolderOut}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := folderOf(c.p, c.ok, c.ignored); got != c.want {
				t.Errorf("folderOf = %+v, want %+v", got, c.want)
			}
		})
	}
	t.Run("with no git adapter, no folder is one pairtime tracks", func(t *testing.T) {
		h := newAdapterHost(&agent{log: slog.New(slog.DiscardHandler), problems: map[string]string{}})
		if got := h.appFolder("/w/app"); got != (t3code.Folder{State: t3code.FolderOut}) {
			t.Errorf("appFolder = %+v, want out", got)
		}
	})
}

// TestAppFolderSpellings checks the ignore test of a folder a T3 Code thread
// runs in, as T3 spells it and as it really is (DESIGN 5.1, 8.8; Q6 of
// #225): T3 keeps the folder its client sent, which can run through a
// symlink, so a .pairtime-ignore that reaches the folder by either spelling
// keeps it out, at once, and one that reaches neither leaves it to the git
// adapter's answer.
func TestAppFolderSpellings(t *testing.T) {
	base := t.TempDir()
	mkdir := func(t *testing.T, parts ...string) string {
		t.Helper()
		d := filepath.Join(append([]string{base}, parts...)...)
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		return d
	}
	ignore := func(t *testing.T, dir string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, ".pairtime-ignore"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := func(t *testing.T, target string, parts ...string) string {
		t.Helper()
		l := filepath.Join(append([]string{base}, parts...)...)
		if err := os.Symlink(target, l); err != nil {
			t.Skipf("this system makes no symlinks: %v", err)
		}
		return l
	}
	cases := []struct {
		name string
		// dir builds the folders and returns T3's spelling of the thread's.
		dir  func(t *testing.T) string
		want t3code.FolderState
	}{
		{"an ignore that reaches T3's spelling alone, above a symlink to the folder, keeps it out", func(t *testing.T) string {
			target := mkdir(t, "a", "open", "proj")
			ignore(t, mkdir(t, "a", "kept"))
			return link(t, target, "a", "kept", "proj")
		}, t3code.FolderOut},
		{"an ignore that reaches the folder as it really is alone, above the symlink's target, keeps it out", func(t *testing.T) string {
			hidden := mkdir(t, "b", "hidden")
			ignore(t, hidden)
			target := mkdir(t, "b", "hidden", "proj")
			mkdir(t, "b", "open")
			return link(t, target, "b", "open", "proj")
		}, t3code.FolderOut},
		{"with no ignore on either side the folder takes the git adapter's answer", func(t *testing.T) string {
			target := mkdir(t, "c", "real", "proj")
			mkdir(t, "c", "open")
			return link(t, target, "c", "open", "proj")
		}, t3code.FolderTracked},
	}
	dirs := home.Dirs{Data: t.TempDir(), Config: t.TempDir(), Runtime: t.TempDir()}
	a := &agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: map[string]string{}, sched: sched.New()}
	g, err := newGitHost(a, bytes.Repeat([]byte{8}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.close)
	h := newAdapterHost(a)
	h.git.Store(g)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := c.dir(t)
			got := h.appFolder(dir)
			// A folder pairtime may track waits for its first Track, which the
			// git adapter runs in the background.
			for deadline := time.Now().Add(10 * time.Second); got.State == t3code.FolderPending; got = h.appFolder(dir) {
				if time.Now().After(deadline) {
					t.Fatal("the git adapter never answered for the folder")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if got.State != c.want {
				t.Errorf("appFolder = %+v, want state %d", got, c.want)
			}
			if c.want == t3code.FolderTracked && got.Project == "" {
				t.Error("a tracked folder has no project")
			}
		})
	}
}
