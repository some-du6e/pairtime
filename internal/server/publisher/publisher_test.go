// SPDX-License-Identifier: AGPL-3.0-or-later

package publisher_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/keys"
	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/publisher"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// newKey returns the Ed25519 key whose seed is 32 bytes of b.
func newKey(b byte) ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{b}, ed25519.SeedSize))
}

// The server keys the tests sign with.
var (
	keyA = newKey(1)
	keyB = newKey(2)
)

func public(k ed25519.PrivateKey) ed25519.PublicKey { return k.Public().(ed25519.PublicKey) }

func keyID(k ed25519.PrivateKey) string { return backup.KeyID(public(k)) }

// at returns the Unix ms of an RFC 3339 time.
func at(s string) int64 {
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return v.UnixMilli()
}

// clock returns the time of an RFC 3339 string.
func clock(s string) time.Time { return time.UnixMilli(at(s)).UTC() }

// day returns the day number of a YYYY-MM-DD date.
func day(date string) uint64 { return uint64(at(date+"T00:00:00Z")) / ledger.DayMS }

// fixture is a migrated database, its writer, and the directory the day
// files go to.
type fixture struct {
	db  *store.DB
	w   *writer.Writer
	dir string
}

func newFixture(t *testing.T, engine store.Engine) *fixture {
	t.Helper()
	ctx := context.Background()
	db := storetest.Open(t, engine)
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, db, ms, time.Now); err != nil {
		t.Fatal(err)
	}
	w := writer.Start(db.Write, writer.Options{Window: time.Millisecond})
	t.Cleanup(w.Close)
	return &fixture{db: db, w: w, dir: filepath.Join(t.TempDir(), "witness")}
}

func (f *fixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if err := f.w.Do(context.Background(), 1, func(ctx context.Context, tx writer.Tx) error {
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// standIn is the head the tests give batch b of device: the SHA-256 of a
// sentence that names it, as ledger-v1 12.5's stand-in does.
func standIn(device string, b uint64) ledger.Hash {
	return ledger.Hash(sha256.Sum256(fmt.Appendf(nil, "stand-in head of batch %d of %s", b, device)))
}

// store stores r as a witness row, as ingest does.
func (f *fixture) store(t *testing.T, r ledger.DayRow) {
	t.Helper()
	f.exec(t, `INSERT INTO witness_rows (device_id, b, account_id, n, root, head, prev, received_at_ms, receipt_sig, receipt_key_id) VALUES ($1, $2, 'u1', 1, $3, $4, $5, $6, $7, $8)`,
		r.DeviceID, int64(r.B), strings.Repeat("ab", 32), r.Head.String(), strings.Repeat("cd", 32), int64(r.ReceivedAt), strings.Repeat("01", 64), keyID(keyA))
}

// row stores batch b of device with its stand-in head, received at the
// RFC 3339 time when, and returns it.
func (f *fixture) row(t *testing.T, device string, b uint64, when string) ledger.DayRow {
	t.Helper()
	r := ledger.DayRow{DeviceID: device, B: b, Head: standIn(device, b), ReceivedAt: uint64(at(when))}
	f.store(t, r)
	return r
}

// history holds keyA alone, valid from 1970 with no end.
func history() *keys.History {
	return &keys.History{Server: []keys.ServerKey{{ID: keyID(keyA), Public: public(keyA), ValidFrom: 0, ValidTo: keys.Current}}}
}

// options are a run's options: keyA, valid throughout, and the
// fixture's directory.
func (f *fixture) options() publisher.Options {
	return publisher.Options{Writer: f.w, Read: f.db.Read, History: history(),
		Keys: map[string]ed25519.PrivateKey{keyID(keyA): keyA}, Dir: f.dir}
}

func (f *fixture) heads(t *testing.T) map[string]publisher.Day {
	t.Helper()
	hs, err := publisher.Heads(context.Background(), f.db.Read)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]publisher.Day, len(hs))
	for _, h := range hs {
		out[publisher.Date(h.D)] = h
	}
	return out
}

func (f *fixture) head(t *testing.T, date string) publisher.Day {
	t.Helper()
	h, ok := f.heads(t)[date]
	if !ok {
		t.Fatalf("day %s has no stored head", date)
	}
	return h
}

// file returns day date's file under the fixture's directory, or nil when
// it isn't there.
func (f *fixture) file(t *testing.T, date string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dir, filepath.FromSlash(publisher.FileName(day(date)))))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// verifyChain checks every stored head with pkg/ledger's verifier: no gap,
// each prev the head before it, each head from its values, and each
// signature under the key that h says was valid at the day's end.
func (f *fixture) verifyChain(t *testing.T, h *keys.History) {
	t.Helper()
	hs, err := publisher.Heads(context.Background(), f.db.Read)
	if err != nil {
		t.Fatal(err)
	}
	days := make([]ledger.PublishedDay, len(hs))
	for i, d := range hs {
		days[i] = d.PublishedDay
	}
	keyAt := func(t uint64) ed25519.PublicKey {
		k, ok := h.At(int64(t))
		if !ok {
			return nil
		}
		return k.Public
	}
	if err := ledger.VerifyDays(keyAt, days); err != nil {
		t.Errorf("the stored chain doesn't verify: %v", err)
	}
}

// dates returns days as YYYY-MM-DD.
func dates(days []uint64) []string {
	out := []string{}
	for _, d := range days {
		out = append(out, publisher.Date(d))
	}
	return out
}

func mustHash(s string) ledger.Hash {
	var h ledger.Hash
	if err := h.UnmarshalText([]byte(s)); err != nil {
		panic(err)
	}
	return h
}

func TestPublish(t *testing.T) {
	type run struct {
		// before changes what the run finds.
		before func(t *testing.T, f *fixture)
		// at is when the run happens, RFC 3339.
		at string
		// notReady makes Options.Ready fail for this run, as a replica
		// more than MaxLag behind does.
		notReady bool
		// wantStored and wantWritten are the dates whose heads the run
		// stores and whose files it writes; wantErr is part of its error.
		wantStored, wantWritten []string
		wantErr                 string
	}
	tests := []struct {
		name string
		// rows stores the witness rows before the first run.
		rows func(t *testing.T, f *fixture)
		// options changes the runs' options.
		options func(o *publisher.Options)
		runs    []run
		// check looks at what the runs left.
		check func(t *testing.T, f *fixture)
	}{
		{name: "ledger-v1 12.5's day, published first, has the T_d, count_d and G_d the spec gives",
			rows: func(t *testing.T, f *fixture) {
				f.store(t, ledger.DayRow{DeviceID: "d_example", B: 1, Head: mustHash("d0277390f5841d01711ecb7493662d98d18fd1a318f14ce25fefc94edc1b00a7"), ReceivedAt: 1795089660250})
				f.store(t, ledger.DayRow{DeviceID: "d_other", B: 7, Head: mustHash("0f8e07775a3efbc8ca16d900af808e95567b6092c72e22a0a3d18872e06cc242"), ReceivedAt: 1795089630250})
			},
			runs: []run{{at: "2026-11-20T00:10:00Z", wantStored: []string{"2026-11-19"}, wantWritten: []string{"2026-11-19"}}},
			check: func(t *testing.T, f *fixture) {
				h := f.head(t, "2026-11-19")
				if h.D != 20776 || h.Count != 2 || h.Prev != (ledger.Hash{}) ||
					h.Root.String() != "10d0de031bcf121f8db0423641f73dc5105696cece9d56f7df58feeb02b372a2" ||
					h.Head.String() != "b222eaa5b42b58700786a473f68ca59f03f77e4971d49b77e049bf5f9e6650c5" {
					t.Errorf("day 2026-11-19 has d %d, count %d, prev %s, T_d %s and G_d %s, not ledger-v1 12.5's", h.D, h.Count, h.Prev, h.Root, h.Head)
				}
				if err := ledger.VerifyDay(public(keyA), h.D, h.Head, h.Signature); err != nil || h.KeyID != keyID(keyA) {
					t.Errorf("the head's signature, by %s, doesn't verify under the key valid at the day's end: %v", h.KeyID, err)
				}
				leaves, err := publisher.Leaves(context.Background(), f.db.Read, h)
				if err != nil || len(leaves) != 2 ||
					leaves[0].String() != "dbb9530abac5f5e52ac7188e663999f5510b184196d22795ae6df086de8ae3de" ||
					leaves[1].String() != "bf08bde260d74c54b160bf8345364601ae6afe57cfac850ecc3be3f054c6c850" {
					t.Errorf("the stored leaves are %v (%v), not 12.5's LEAF(leaf 1) and LEAF(leaf 2)", leaves, err)
				}
			}},
		{name: "a day is due 10 minutes after it ends and not a millisecond before",
			rows: func(t *testing.T, f *fixture) { f.row(t, "d1", 1, "2026-11-19T12:00:00Z") },
			runs: []run{
				{at: "2026-11-20T00:09:59.999Z"},
				{at: "2026-11-20T00:10:00Z", wantStored: []string{"2026-11-19"}, wantWritten: []string{"2026-11-19"}},
			}},
		{name: "a missed day is published on the next run, with every day after it that is due, chained without a gap",
			rows: func(t *testing.T, f *fixture) {
				f.row(t, "d1", 1, "2026-11-19T12:00:00Z")
				f.row(t, "d1", 2, "2026-11-20T12:00:00Z")
				f.row(t, "d1", 3, "2026-11-21T12:00:00Z")
				f.row(t, "d1", 4, "2026-11-22T12:00:00Z")
			},
			runs: []run{
				{at: "2026-11-20T00:10:00Z", wantStored: []string{"2026-11-19"}, wantWritten: []string{"2026-11-19"}},
				{at: "2026-11-22T09:00:00Z", wantStored: []string{"2026-11-20", "2026-11-21"}, wantWritten: []string{"2026-11-20", "2026-11-21"}},
			},
			check: func(t *testing.T, f *fixture) { f.verifyChain(t, history()) }},
		{name: "a day with no rows gets a head over the SHA-256 of no bytes, so the chain has no gap",
			rows: func(t *testing.T, f *fixture) {
				f.row(t, "d1", 1, "2026-11-19T12:00:00Z")
				f.row(t, "d1", 2, "2026-11-21T12:00:00Z")
			},
			runs: []run{{at: "2026-11-22T00:10:00Z", wantStored: []string{"2026-11-19", "2026-11-20", "2026-11-21"}, wantWritten: []string{"2026-11-19", "2026-11-20", "2026-11-21"}}},
			check: func(t *testing.T, f *fixture) {
				empty := f.head(t, "2026-11-20")
				if empty.Count != 0 || empty.Root.String() != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
					t.Errorf("the day with no rows has count %d and T_d %s", empty.Count, empty.Root)
				}
				if leaves, err := publisher.Leaves(context.Background(), f.db.Read, empty); err != nil || len(leaves) != 0 {
					t.Errorf("the day with no rows has leaves %v (%v)", leaves, err)
				}
				var n int
				if err := f.db.Read.QueryRow(`SELECT COUNT(*) FROM day_leaves`).Scan(&n); err != nil || n != 2 {
					t.Errorf("day_leaves holds %d rows (%v), one for each of the two days with rows", n, err)
				}
				f.verifyChain(t, history())
			}},
		{name: "the first day is the day of the oldest witness row, and nothing is published before it",
			rows: func(t *testing.T, f *fixture) { f.row(t, "d1", 1, "2026-11-21T05:42:00Z") },
			runs: []run{{at: "2026-11-24T00:10:00Z", wantStored: []string{"2026-11-21", "2026-11-22", "2026-11-23"}, wantWritten: []string{"2026-11-21", "2026-11-22", "2026-11-23"}}},
			check: func(t *testing.T, f *fixture) {
				if h := f.head(t, "2026-11-21"); h.Prev != (ledger.Hash{}) {
					t.Errorf("the first day's prev is %s, not zero(32)", h.Prev)
				}
			}},
		{name: "with no witness rows there is nothing to publish",
			runs: []run{{at: "2026-11-20T00:10:00Z"}},
			check: func(t *testing.T, f *fixture) {
				if _, err := os.Stat(f.dir); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("the run made the directory or wrote into it: %v", err)
				}
			}},
		{name: "the chain resumes from the stored heads, not the files: a lost directory gets its files back byte for byte, and the next day links to the stored head",
			rows: func(t *testing.T, f *fixture) {
				f.row(t, "d1", 1, "2026-11-19T12:00:00Z")
				f.row(t, "d1", 2, "2026-11-20T12:00:00Z")
			},
			runs: []run{
				{at: "2026-11-20T00:10:00Z", wantStored: []string{"2026-11-19"}, wantWritten: []string{"2026-11-19"}},
				{before: func(t *testing.T, f *fixture) {
					if err := os.RemoveAll(f.dir); err != nil {
						t.Fatal(err)
					}
				}, at: "2026-11-21T00:10:00Z", wantStored: []string{"2026-11-20"}, wantWritten: []string{"2026-11-19", "2026-11-20"}},
			},
			check: func(t *testing.T, f *fixture) {
				for _, date := range []string{"2026-11-19", "2026-11-20"} {
					want, err := f.head(t, date).File()
					if err != nil {
						t.Fatal(err)
					}
					if got := f.file(t, date); !bytes.Equal(got, want) {
						t.Errorf("%s's file is\n%s\nnot its stored head's\n%s", date, got, want)
					}
				}
				if f.head(t, "2026-11-20").Prev != f.head(t, "2026-11-19").Head {
					t.Error("2026-11-20's prev isn't 2026-11-19's stored head")
				}
			}},
		{name: "a file that is there already is never rewritten, even one that doesn't hold its day's head, and the run says so",
			rows: func(t *testing.T, f *fixture) { f.row(t, "d1", 1, "2026-11-19T12:00:00Z") },
			runs: []run{{before: func(t *testing.T, f *fixture) {
				p := filepath.Join(f.dir, "2026", "2026-11-19.json")
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}, at: "2026-11-20T00:10:00Z", wantStored: []string{"2026-11-19"}, wantErr: "doesn't hold day 2026-11-19's stored head"}},
			check: func(t *testing.T, f *fixture) {
				if got := f.file(t, "2026-11-19"); string(got) != "{}\n" {
					t.Errorf("the file there before was replaced with\n%s", got)
				}
			}},
		{name: "each day is signed with the key valid at its last millisecond, which may be a key retired since",
			rows: func(t *testing.T, f *fixture) {
				f.row(t, "d1", 1, "2026-11-19T12:00:00Z")
				f.row(t, "d1", 2, "2026-11-20T11:00:00Z")
				f.row(t, "d1", 3, "2026-11-21T12:00:00Z")
			},
			options: func(o *publisher.Options) {
				o.History = rotated()
				o.Keys = map[string]ed25519.PrivateKey{keyID(keyA): keyA, keyID(keyB): keyB}
			},
			runs: []run{{at: "2026-11-22T00:10:00Z", wantStored: []string{"2026-11-19", "2026-11-20", "2026-11-21"}, wantWritten: []string{"2026-11-19", "2026-11-20", "2026-11-21"}}},
			check: func(t *testing.T, f *fixture) {
				for date, want := range map[string]string{"2026-11-19": keyID(keyA), "2026-11-20": keyID(keyB), "2026-11-21": keyID(keyB)} {
					if got := f.head(t, date).KeyID; got != want {
						t.Errorf("%s was signed by %s, want %s, the key valid at the day's end", date, got, want)
					}
				}
				f.verifyChain(t, rotated())
			}},
		{name: "a day whose key this server doesn't hold stops the chain there, and the days before it are published",
			rows: func(t *testing.T, f *fixture) {
				f.row(t, "d1", 1, "2026-11-19T12:00:00Z")
				f.row(t, "d1", 2, "2026-11-20T11:00:00Z")
			},
			options: func(o *publisher.Options) { o.History = rotated() },
			runs: []run{
				{at: "2026-11-22T00:10:00Z", wantStored: []string{"2026-11-19"}, wantWritten: []string{"2026-11-19"}, wantErr: "needs server key " + keyID(keyB)},
				{at: "2026-11-23T00:10:00Z", wantErr: "needs server key " + keyID(keyB)},
			}},
		{name: "a day no key was valid at the end of isn't signed",
			rows:    func(t *testing.T, f *fixture) { f.row(t, "d1", 1, "2026-11-19T12:00:00Z") },
			options: func(o *publisher.Options) { o.History = &keys.History{} },
			runs:    []run{{at: "2026-11-20T00:10:00Z", wantErr: "no server key was valid at the end of day 2026-11-19"}}},
		{name: "a due day isn't signed while the readiness check fails, as while the replica lags, and is once it passes",
			rows: func(t *testing.T, f *fixture) { f.row(t, "d1", 1, "2026-11-19T12:00:00Z") },
			runs: []run{
				{at: "2026-11-20T00:10:00Z", notReady: true, wantErr: "day 2026-11-19 is due, and the replica is 2m0s behind"},
				{at: "2026-11-20T00:15:00Z", wantStored: []string{"2026-11-19"}, wantWritten: []string{"2026-11-19"}},
			}},
		{name: "a failing readiness check holds nothing back while no day is due",
			rows: func(t *testing.T, f *fixture) { f.row(t, "d1", 1, "2026-11-19T12:00:00Z") },
			runs: []run{{at: "2026-11-19T23:00:00Z", notReady: true}}},
		{name: "without a directory the heads are stored and no file is written",
			rows:    func(t *testing.T, f *fixture) { f.row(t, "d1", 1, "2026-11-19T12:00:00Z") },
			options: func(o *publisher.Options) { o.Dir = "" },
			runs:    []run{{at: "2026-11-20T00:10:00Z", wantStored: []string{"2026-11-19"}}},
			check: func(t *testing.T, f *fixture) {
				if _, err := os.Stat(f.dir); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("a run with no directory wrote to %s: %v", f.dir, err)
				}
			}},
		{name: "a row that arrives for a day already published, as after a clock set back, leaves that day's head as it was",
			rows: func(t *testing.T, f *fixture) { f.row(t, "d1", 1, "2026-11-19T12:00:00Z") },
			runs: []run{
				{at: "2026-11-20T00:10:00Z", wantStored: []string{"2026-11-19"}, wantWritten: []string{"2026-11-19"}},
				{before: func(t *testing.T, f *fixture) { f.row(t, "d2", 1, "2026-11-19T23:59:59.999Z") },
					at: "2026-11-21T00:10:00Z", wantStored: []string{"2026-11-20"}, wantWritten: []string{"2026-11-20"}},
			},
			check: func(t *testing.T, f *fixture) {
				if h := f.head(t, "2026-11-19"); h.Count != 1 {
					t.Errorf("2026-11-19's head counts %d rows, and it was published with 1", h.Count)
				}
			}},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				f := newFixture(t, engine)
				if tt.rows != nil {
					tt.rows(t, f)
				}
				o := f.options()
				if tt.options != nil {
					tt.options(&o)
				}
				for i, r := range tt.runs {
					if r.before != nil {
						r.before(t, f)
					}
					ro := o
					if r.notReady {
						ro.Ready = func(context.Context) error { return errors.New("the replica is 2m0s behind") }
					}
					res, err := publisher.Publish(context.Background(), ro, clock(r.at))
					switch {
					case r.wantErr == "" && err != nil:
						t.Fatalf("run %d: %v", i+1, err)
					case r.wantErr != "" && (err == nil || !strings.Contains(err.Error(), r.wantErr)):
						t.Fatalf("run %d: error %v, want one saying %q", i+1, err, r.wantErr)
					case r.notReady && r.wantErr != "" && !errors.Is(err, publisher.ErrNotReady):
						t.Fatalf("run %d: error %v doesn't wrap ErrNotReady, which Run retries sooner", i+1, err)
					}
					if got := dates(res.Stored); !slices.Equal(got, nonNil(r.wantStored)) {
						t.Errorf("run %d stored the heads of %v, want %v", i+1, got, r.wantStored)
					}
					if got := dates(res.Written); !slices.Equal(got, nonNil(r.wantWritten)) {
						t.Errorf("run %d wrote the files of %v, want %v", i+1, got, r.wantWritten)
					}
				}
				if tt.check != nil {
					tt.check(t, f)
				}
			})
		}
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// rotated is a history where keyA was retired at 12:00 UTC on 2026-11-20
// and keyB took over.
func rotated() *keys.History {
	r := at("2026-11-20T12:00:00Z")
	return &keys.History{Server: []keys.ServerKey{
		{ID: keyID(keyA), Public: public(keyA), ValidFrom: 0, ValidTo: r - 1},
		{ID: keyID(keyB), Public: public(keyB), ValidFrom: r, ValidTo: keys.Current},
	}}
}

// TestDeterministic checks that a day's head and file follow from its rows
// and the key alone: the same rows on a fresh database, stored in another
// order, give the same files byte for byte. That is what lets a database
// restored without its newest head store the same head again.
func TestDeterministic(t *testing.T) {
	rows := []ledger.DayRow{
		{DeviceID: "d1", B: 1, Head: standIn("d1", 1), ReceivedAt: uint64(at("2026-11-19T08:00:00Z"))},
		{DeviceID: "d2", B: 1, Head: standIn("d2", 1), ReceivedAt: uint64(at("2026-11-19T08:00:00Z"))},
		{DeviceID: "d1", B: 2, Head: standIn("d1", 2), ReceivedAt: uint64(at("2026-11-20T23:59:59.999Z"))},
	}
	tests := []struct {
		name  string
		order []int
	}{
		{name: "rows stored in day order give the files of rows stored in reverse", order: []int{2, 1, 0}},
		{name: "rows stored in an order of their own give the same files too", order: []int{1, 2, 0}},
	}
	publish := func(t *testing.T, order []int) map[string][]byte {
		t.Helper()
		f := newFixture(t, store.SQLite)
		for _, i := range order {
			f.store(t, rows[i])
		}
		if _, err := publisher.Publish(context.Background(), f.options(), clock("2026-11-21T00:10:00Z")); err != nil {
			t.Fatal(err)
		}
		return map[string][]byte{"2026-11-19": f.file(t, "2026-11-19"), "2026-11-20": f.file(t, "2026-11-20")}
	}
	want := publish(t, []int{0, 1, 2})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := publish(t, tt.order); !reflect.DeepEqual(got, want) {
				t.Errorf("the files differ:\n%s\nwant\n%s", got, want)
			}
		})
	}
}

// TestFile checks a day's file: the members ledger-v1 8 lists and no
// others, hashes and a count and nothing a device sent, in a fixed form.
func TestFile(t *testing.T) {
	f := newFixture(t, store.SQLite)
	devices := []string{"teen-laptop-7f3a", "d_example"}
	for i, d := range devices {
		f.row(t, d, uint64(i+1), "2026-11-19T12:34:56.789Z")
	}
	if _, err := publisher.Publish(context.Background(), f.options(), clock("2026-11-20T00:10:00Z")); err != nil {
		t.Fatal(err)
	}
	data := f.file(t, "2026-11-19")
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		t.Fatalf("the file isn't a JSON object: %v\n%s", err, data)
	}
	h := f.head(t, "2026-11-19")
	hexOf := func(n int) func(s string) bool {
		return func(s string) bool {
			b, err := hex.DecodeString(s)
			return err == nil && len(b) == n && s == strings.ToLower(s)
		}
	}
	str := func(name string) string {
		var s string
		if err := json.Unmarshal(members[name], &s); err != nil {
			return ""
		}
		return s
	}
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "the file has the nine members and no others", ok: func() bool {
			var names []string
			for n := range members {
				names = append(names, n)
			}
			slices.Sort(names)
			return slices.Equal(names, []string{"count", "date", "day", "format", "head", "key_id", "prev", "root", "signature"})
		}()},
		{name: "format names the file's version", ok: str("format") == publisher.FileFormat},
		{name: "day and date name the UTC day", ok: string(members["day"]) == "20776" && str("date") == "2026-11-19"},
		{name: "count is the number of the day's rows", ok: string(members["count"]) == "2"},
		{name: "prev, root and head are 32-byte hashes in lowercase hex, the stored head's", ok: hexOf(32)(str("prev")) && hexOf(32)(str("root")) && hexOf(32)(str("head")) &&
			str("prev") == h.Prev.String() && str("root") == h.Root.String() && str("head") == h.Head.String()},
		{name: "signature is 64 bytes in lowercase hex, and key_id the signing key's id", ok: hexOf(64)(str("signature")) && str("key_id") == keyID(keyA)},
		{name: "nothing a device sent is in the file: no device id, no time within the day", ok: !bytes.Contains(data, []byte(devices[0])) && !bytes.Contains(data, []byte(devices[1])) &&
			!bytes.Contains(data, []byte(fmt.Sprint(at("2026-11-19T12:34:56.789Z")))) && !bytes.Contains(data, []byte("12:34"))},
		{name: "the file is the stored head's, two-space indented, with a newline at the end", ok: func() bool {
			want, err := h.File()
			return err == nil && bytes.Equal(data, want) && bytes.HasSuffix(data, []byte("}\n")) && bytes.HasPrefix(data, []byte("{\n  \"format\": "))
		}()},
		{name: "the file's name is its UTC date under its year", ok: publisher.FileName(h.D) == "2026/2026-11-19.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("the file is\n%s", data)
			}
		})
	}
}

func TestSchedule(t *testing.T) {
	tests := []struct {
		name        string
		now         string
		wantDue     string
		wantNothing bool
		wantNextRun string
	}{
		{name: "at 00:10 UTC the day before is due, and the next run is the next day's 00:10", now: "2026-11-20T00:10:00Z", wantDue: "2026-11-19", wantNextRun: "2026-11-21T00:10:00Z"},
		{name: "a millisecond before 00:10 only the day before that is due, and the run comes at 00:10", now: "2026-11-20T00:09:59.999Z", wantDue: "2026-11-18", wantNextRun: "2026-11-20T00:10:00Z"},
		{name: "late in the day the day before is due", now: "2026-11-20T23:59:59.999Z", wantDue: "2026-11-19", wantNextRun: "2026-11-21T00:10:00Z"},
		{name: "a clock in 1970's first day has no day due", now: "1970-01-01T12:00:00Z", wantNothing: true, wantNextRun: "1970-01-02T00:10:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, ok := publisher.LastDue(clock(tt.now))
			switch {
			case tt.wantNothing && ok:
				t.Errorf("LastDue = %s, want none", publisher.Date(d))
			case !tt.wantNothing && (!ok || publisher.Date(d) != tt.wantDue):
				t.Errorf("LastDue = %s, %v; want %s", publisher.Date(d), ok, tt.wantDue)
			}
			if got := publisher.NextRun(clock(tt.now)); !got.Equal(clock(tt.wantNextRun)) {
				t.Errorf("NextRun = %s, want %s", got, tt.wantNextRun)
			}
		})
	}
}

func TestPending(t *testing.T) {
	tests := []struct {
		name      string
		rows      []string
		publishAt string
		now       string
		// want is the first and last day pending, or nothing.
		want []string
	}{
		{name: "with no witness rows no day is pending", now: "2026-11-24T12:00:00Z"},
		{name: "rows of a day that isn't due yet leave nothing pending", rows: []string{"2026-11-24T08:00:00Z"}, now: "2026-11-24T12:00:00Z"},
		{name: "every due day from the first row's is pending before the first run", rows: []string{"2026-11-21T08:00:00Z"}, now: "2026-11-24T12:00:00Z", want: []string{"2026-11-21", "2026-11-23"}},
		{name: "a run leaves nothing pending", rows: []string{"2026-11-21T08:00:00Z"}, publishAt: "2026-11-24T00:10:00Z", now: "2026-11-24T12:00:00Z"},
		{name: "the days due since the newest head are pending", rows: []string{"2026-11-21T08:00:00Z"}, publishAt: "2026-11-22T00:10:00Z", now: "2026-11-24T12:00:00Z", want: []string{"2026-11-22", "2026-11-23"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, store.SQLite)
			for i, r := range tt.rows {
				f.row(t, "d1", uint64(i+1), r)
			}
			if tt.publishAt != "" {
				if _, err := publisher.Publish(context.Background(), f.options(), clock(tt.publishAt)); err != nil {
					t.Fatal(err)
				}
			}
			first, last, ok, err := publisher.Pending(context.Background(), f.db.Read, clock(tt.now))
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			if ok {
				got = []string{publisher.Date(first), publisher.Date(last)}
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Pending = %v, want %v", got, tt.want)
			}
		})
	}
}

// racingWriter is the writer with one change slipped in before the first
// op it runs: what the writer could take between the publisher's read of
// a day's rows and the op that stores the day's head.
type racingWriter struct {
	publisher.Doer
	slip func()
}

func (r *racingWriter) Do(ctx context.Context, rows int, op writer.Op) error {
	if slip := r.slip; slip != nil {
		r.slip = nil
		slip()
	}
	return r.Doer.Do(ctx, rows, op)
}

// TestStoreHeadRecount checks that a head is stored over exactly the rows
// its tree was built from (ledger-v1 decision 38). When the writer takes an
// upload or a deletion between the read of a day's rows and the op that
// stores its head, the op counts the rows again, stores nothing, and the
// next run publishes the day over the rows it has then.
func TestStoreHeadRecount(t *testing.T) {
	tests := []struct {
		name      string
		slip      func(t *testing.T, f *fixture)
		wantCount uint64
	}{
		{name: "an upload the writer takes in between makes the head wait, and the next run's tree holds it",
			slip: func(t *testing.T, f *fixture) { f.row(t, "d3", 1, "2026-11-19T23:59:59.999Z") }, wantCount: 3},
		{name: "a deletion in between makes the head wait too, and the next run's tree leaves the row out",
			slip: func(t *testing.T, f *fixture) { f.exec(t, `DELETE FROM witness_rows WHERE device_id = $1`, "d2") }, wantCount: 1},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				f := newFixture(t, engine)
				f.row(t, "d1", 1, "2026-11-19T08:00:00Z")
				f.row(t, "d2", 1, "2026-11-19T09:00:00Z")
				o := f.options()
				o.Writer = &racingWriter{Doer: f.w, slip: func() { tt.slip(t, f) }}
				res, err := publisher.Publish(context.Background(), o, clock("2026-11-20T00:10:00Z"))
				if err == nil || !strings.Contains(err.Error(), "so its head waits for the next run") || len(res.Stored) != 0 {
					t.Fatalf("Publish stored %v with error %v, want nothing stored and the head waiting", dates(res.Stored), err)
				}
				if _, ok, err := publisher.Newest(context.Background(), f.db.Read); err != nil || ok {
					t.Fatalf("a head was stored: %v, %v", ok, err)
				}
				if _, err := publisher.Publish(context.Background(), f.options(), clock("2026-11-20T01:10:00Z")); err != nil {
					t.Fatal(err)
				}
				if h := f.head(t, "2026-11-19"); h.Count != tt.wantCount {
					t.Errorf("the next run's head counts %d rows, want %d", h.Count, tt.wantCount)
				}
				f.verifyChain(t, history())
			})
		}
	}
}
