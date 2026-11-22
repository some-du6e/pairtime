// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"bytes"
	binenc "encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	agentledger "github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// ledgerZone is the device's zone in the ledger tests: five hours behind
// UTC, so a local day takes records from two of the ledger's UTC segments.
var ledgerZone = time.FixedZone("UTC-5", -5*60*60)

// ledgerRig is a real ledger, written by the agent's writer with test
// keys, in a PAIRTIME_HOME of its own.
type ledgerRig struct {
	h fakeHome
	w *agentledger.Writer
	n int

	mu  sync.Mutex
	now time.Time // the writer's clock, which its goroutine reads too
}

func newLedgerRig(t *testing.T) *ledgerRig {
	t.Helper()
	h := newHome(t)
	kDev := make([]byte, ledger.KeySize)
	for i := range kDev {
		kDev[i] = byte(i)
	}
	keys, err := ledger.DeriveKeys(kDev)
	if err != nil {
		t.Fatal(err)
	}
	r := &ledgerRig{h: h}
	w, err := agentledger.Open(agentledger.Options{Dir: h.dirs().Ledger(), Keys: keys, KeyID: "00112233445566778899aabbccddeeff",
		Now: func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	r.w = w
	return r
}

// at is a time on the test's days, in the device's zone.
func at(day, clock string) time.Time {
	t, err := time.ParseInLocation(time.DateOnly+" 15:04", day+" "+clock, ledgerZone)
	if err != nil {
		panic(err)
	}
	return t
}

// presence writes an os.presence record that happened at when and was
// written then, and returns its seq.
func (r *ledgerRig) presence(t *testing.T, when time.Time) uint64 {
	t.Helper()
	return r.write(t, when, event.Envelope{Source: event.SourceOS, Via: event.ViaOS, Harness: event.HarnessNone,
		SessionKey: event.NoSession}, &event.OSPresence{IdleAnyS: 12.5})
}

// answer writes the final agent.output of a Claude Code session in a
// project, with its text, and returns its seq. Unlike a prompt, it asks
// the writer for no seal, so the test says which batch each record is in.
func (r *ledgerRig) answer(t *testing.T, when time.Time, text string) uint64 {
	t.Helper()
	return r.write(t, when, event.Envelope{Source: event.SourceHook, Via: event.ViaHook, Harness: event.HarnessClaudeCode,
		HarnessVersion: "2.1.327", SessionKey: "s_1", ProjectKey: "p_web"},
		&event.AgentOutput{TurnKey: "t_1", Words: len(strings.Fields(text)), IsFinal: true}, []byte(text)...)
}

func (r *ledgerRig) write(t *testing.T, when time.Time, e event.Envelope, data event.Payload, text ...byte) uint64 {
	t.Helper()
	r.mu.Lock()
	r.now = when
	r.mu.Unlock()
	r.n++
	var id [16]byte
	binenc.BigEndian.PutUint64(id[:8], uint64(when.UnixMilli())<<16)
	id[6] = id[6]&0x0f | 0x70
	id[8] = 0x80
	id[15] = byte(r.n)
	h := hex.EncodeToString(id[:])
	e.EventID = h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
	e.T, e.Mono, e.BootID = when.UnixMilli(), int64(r.n)*1000, "b_test"
	in := agentledger.Input{Event: event.Event{Envelope: e, Data: data}}
	if len(text) > 0 {
		in.Text = text
	}
	s, err := r.w.Write(in)
	if err != nil {
		t.Fatalf("writing a record at %s: %v", when, err)
	}
	return s
}

// seal seals the records written so far into a batch.
func (r *ledgerRig) seal(t *testing.T) {
	t.Helper()
	if _, _, err := r.w.Seal(); err != nil {
		t.Fatal(err)
	}
}

// held is every segment file of the ledger, end to end, and the device
// id of its chain: what each printed record is checked against.
func (r *ledgerRig) held(t *testing.T) ([]byte, string) {
	t.Helper()
	dir := r.h.dirs().Ledger()
	chain, err := agentledger.ReadChain(dir)
	if err != nil {
		t.Fatal(err)
	}
	names, err := filepath.Glob(filepath.Join(dir, "*.seg"))
	if err != nil {
		t.Fatal(err)
	}
	var all []byte
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, b...)
	}
	return all, chain.DeviceID
}

// parseShown reads ledger show's output back into records, with each
// one's batch, rebuilding each header from its fields.
func parseShown(t *testing.T, out string) ([]ledger.Record, []uint64) {
	t.Helper()
	var recs []ledger.Record
	var batches []uint64
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(nil, 1<<24)
	for sc.Scan() {
		dec := json.NewDecoder(bytes.NewReader(sc.Bytes()))
		dec.DisallowUnknownFields()
		var s shownRecord
		if err := dec.Decode(&s); err != nil {
			t.Fatalf("a line of ledger show isn't a record: %v\n%s", err, sc.Text())
		}
		var h ledger.Header
		binenc.BigEndian.PutUint64(h[:8], s.Seq)
		h[8] = s.RType
		for _, f := range []struct {
			hex  string
			into []byte
		}{{s.Project, h[9 : 9+ledger.ProjectDigestSize]}, {s.Session, h[9+ledger.ProjectDigestSize:]}} {
			if f.hex == "" {
				continue
			}
			b, err := hex.DecodeString(f.hex)
			if err != nil || len(b) != len(f.into) {
				t.Fatalf("record %d has a digest %q that isn't %d bytes of hex", s.Seq, f.hex, len(f.into))
			}
			copy(f.into, b)
		}
		if got := ledger.RType(s.RType).String(); got != s.Type {
			t.Errorf("record %d says type %q for rtype %d, which is %q", s.Seq, s.Type, s.RType, got)
		}
		recs = append(recs, ledger.Record{Header: h, Body: slices.Clone([]byte(s.Body))})
		batches = append(batches, s.Batch)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return recs, batches
}

func TestLedgerShow(t *testing.T) {
	r := newLedgerRig(t)
	setZone(t, ledgerZone)
	before := r.presence(t, at("2026-11-23", "23:30"))  // the day before, written in the 11-24 UTC segment
	morning := r.presence(t, at("2026-11-24", "00:00")) // the first millisecond of the day
	answer := r.answer(t, at("2026-11-24", "11:00"), "Fixed the <nav> & the footer.")
	late := r.presence(t, at("2026-11-24", "23:59"))  // written in the 11-25 UTC segment
	after := r.presence(t, at("2026-11-25", "00:00")) // the next day's first millisecond
	r.seal(t)
	unsealed := r.presence(t, at("2026-11-24", "23:59").Add(30*time.Second)) // late, and in no batch yet
	if _, err := os.Stat(filepath.Join(r.h.dirs().Ledger(), "2026-11-25.seg")); err != nil {
		t.Fatalf("the test's records don't span two UTC segments: %v", err)
	}
	files, device := r.held(t)

	tests := []struct {
		name        string
		day         string
		wantSeqs    []uint64
		wantBatches []uint64
		wantStderr  string
		wantHas     []string
		wantNot     []string
	}{
		{
			name:        "a day's records are those whose time is on the local day, from both UTC segments it spans, sealed or not, and the agent's answer shows as its numbers and commitment, never its text",
			day:         "2026-11-24",
			wantSeqs:    []uint64{morning, answer, late, unsealed},
			wantBatches: []uint64{1, 1, 1, 0},
			wantStderr:  "4 records on Tuesday, November 24.\n",
			wantHas:     []string{`"type":"agent.output"`, `"c":{`, `"type":"os.presence"`},
			wantNot:     []string{"nav", "footer"},
		},
		{
			name:        "the day before holds only its own record, though it was written in the next day's UTC segment",
			day:         "2026-11-23",
			wantSeqs:    []uint64{before},
			wantBatches: []uint64{1},
			wantStderr:  "1 record on Monday, November 23.\n",
		},
		{
			name:        "the next day starts at its local midnight",
			day:         "2026-11-25",
			wantSeqs:    []uint64{after},
			wantBatches: []uint64{1},
			wantStderr:  "1 record on Wednesday, November 25.\n",
		},
		{
			name:       "a day with no records prints nothing and says so",
			day:        "2026-11-20",
			wantStderr: "No records on Friday, November 20.\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runCmd("ledger", "show", "--day", tt.day)
			if code != 0 || stderr != tt.wantStderr {
				t.Fatalf("ledger show = %d, stderr %q; want 0 and %q", code, stderr, tt.wantStderr)
			}
			recs, batches := parseShown(t, stdout)
			var seqs []uint64
			for _, rec := range recs {
				seqs = append(seqs, rec.Header.Seq())
				// Each line parses back into a record the ledger holds,
				// header and body byte for byte, that checks out against
				// its chain's device id.
				if !bytes.Contains(files, append(rec.Header[:], rec.Body...)) {
					t.Errorf("record %d printed as %x %s, which no segment holds", rec.Header.Seq(), rec.Header[:], rec.Body)
				}
				if err := ledger.CheckBody(device, rec.Header, rec.Body); err != nil {
					t.Errorf("record %d's printed header and body don't check out: %v", rec.Header.Seq(), err)
				}
			}
			if !slices.Equal(seqs, tt.wantSeqs) || !slices.Equal(batches, tt.wantBatches) {
				t.Errorf("ledger show printed records %v in batches %v, want %v in %v", seqs, batches, tt.wantSeqs, tt.wantBatches)
			}
			for _, w := range tt.wantHas {
				if !strings.Contains(stdout, w) {
					t.Errorf("ledger show doesn't print %s:\n%s", w, stdout)
				}
			}
			for _, w := range tt.wantNot {
				if strings.Contains(stdout, w) {
					t.Errorf("ledger show prints %q:\n%s", w, stdout)
				}
			}
		})
	}
}

func TestLedgerShowUsage(t *testing.T) {
	newHome(t)
	onDay(t, "2026-11-24", ledgerZone)
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{"ledger alone is a usage error", []string{"ledger"}, 2, "Usage: pairtime ledger show"},
		{"a subcommand other than show is a usage error", []string{"ledger", "list"}, 2, "Usage: pairtime ledger show"},
		{"a day that isn't a date is a usage error", []string{"ledger", "show", "--day", "2026-11-31"}, 2, "--day wants a day such as 2026-11-24"},
		{"an argument after the flags is a usage error", []string{"ledger", "show", "today"}, 2, "takes no arguments"},
		{"with no --day it shows today, and an empty ledger has no records", []string{"ledger", "show"}, 0, "No records on Tuesday, November 24."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runCmd(tt.args...)
			if code != tt.wantCode || !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("%q = %d, stderr %q; want %d with %q", tt.args, code, stderr, tt.wantCode, tt.wantStderr)
			}
			if stdout != "" {
				t.Errorf("%q printed %q on stdout, want nothing", tt.args, stdout)
			}
		})
	}
}

// TestLedgerShowWhereMidnightIsSkipped shows the days around a jump of the
// clocks over midnight: on 2026-09-06 Chile's go from 23:59 on the 5th to
// 01:00, so that day has no midnight, and its first instant is 01:00.
func TestLedgerShowWhereMidnightIsSkipped(t *testing.T) {
	zone, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skipf("this system has no America/Santiago: %v", err)
	}
	if m := time.Date(2026, 9, 6, 0, 0, 0, 0, zone); m.Day() == 6 && m.Hour() == 0 {
		t.Skip("this system's America/Santiago has a midnight on 2026-09-06")
	}
	r := newLedgerRig(t)
	setZone(t, zone)
	evening := r.presence(t, time.Date(2026, 9, 5, 22, 30, 0, 0, zone))
	lastHour := r.presence(t, time.Date(2026, 9, 5, 23, 30, 0, 0, zone))
	firstHour := r.presence(t, time.Date(2026, 9, 6, 1, 30, 0, 0, zone))
	r.seal(t)
	tests := []struct {
		name     string
		day      string
		wantSeqs []uint64
	}{
		{"the day before the jump keeps its last hour, up to the jump", "2026-09-05", []uint64{evening, lastHour}},
		{"the day with no midnight is a valid day, and starts at the jump", "2026-09-06", []uint64{firstHour}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runCmd("ledger", "show", "--day", tt.day)
			if code != 0 {
				t.Fatalf("ledger show --day %s = %d, stderr %q", tt.day, code, stderr)
			}
			recs, _ := parseShown(t, stdout)
			var seqs []uint64
			for _, rec := range recs {
				seqs = append(seqs, rec.Header.Seq())
			}
			if !slices.Equal(seqs, tt.wantSeqs) {
				t.Errorf("ledger show --day %s printed records %v, want %v", tt.day, seqs, tt.wantSeqs)
			}
		})
	}
}

// appendRecordFrame appends to the ledger's segment of day a record frame
// the writer would never write: header h and body, framed as ledger-v1's
// files are (internal/agent/ledger, doc.go).
func appendRecordFrame(t *testing.T, dir, day string, h ledger.Header, body []byte) {
	t.Helper()
	payload := append(h[:], body...)
	frame := []byte{'r'}
	frame = binenc.BigEndian.AppendUint32(frame, uint32(len(payload)))
	frame = append(frame, payload...)
	frame = binenc.BigEndian.AppendUint32(frame, crc32.Checksum(frame, crc32.MakeTable(crc32.Castagnoli)))
	f, err := os.OpenFile(filepath.Join(dir, day+".seg"), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(frame); err != nil {
		t.Fatal(err)
	}
}

func TestLedgerShowUnknownKinds(t *testing.T) {
	r := newLedgerRig(t)
	setZone(t, ledgerZone)
	known := r.presence(t, at("2026-11-24", "10:00"))
	r.seal(t)
	if err := r.w.Close(); err != nil {
		t.Fatal(err)
	}
	nonce := `"` + strings.Repeat("0", 64) + `"`
	later := func(seq uint64) ledger.Header {
		var h ledger.Header
		binenc.BigEndian.PutUint64(h[:8], seq)
		h[8] = 0x13 // a kind added after this version (ledger-v1 4.2)
		return h
	}
	day := at("2026-11-24", "10:00").UTC().Format(time.DateOnly)
	appendRecordFrame(t, r.h.dirs().Ledger(), day, later(known+1), []byte(`{"n":`+nonce+`,"e":{"t":`+strconv.FormatInt(at("2026-11-24", "11:00").UnixMilli(), 10)+`}}`))
	appendRecordFrame(t, r.h.dirs().Ledger(), day, later(known+2), []byte(`{"n":`+nonce+`,"e":{"what":"no time"}}`))

	code, stdout, stderr := runCmd("ledger", "show", "--day", "2026-11-24")
	recs, batches := parseShown(t, stdout)
	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"a record of a later kind with a time is shown on its day, with its rtype and no batch", func(t *testing.T) {
			if len(recs) != 2 || recs[1].Header.Seq() != known+1 || recs[1].Header.RType() != 0x13 || batches[1] != 0 {
				t.Errorf("ledger show printed %d records, batches %v:\n%s", len(recs), batches, stdout)
			}
			if !strings.Contains(stdout, `"type":"rtype 0x13"`) {
				t.Errorf("the later kind's type isn't rtype 0x13:\n%s", stdout)
			}
		}},
		{"a record of a later kind with no time is on no day, said on stderr, and stops nothing", func(t *testing.T) {
			if code != 0 || !strings.Contains(stderr, "1 record of a kind this pairtime doesn't know says no time") {
				t.Errorf("ledger show = %d, stderr %q", code, stderr)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}
