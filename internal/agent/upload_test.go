// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
	"github.com/some-du6e/pairtime/internal/devtoken"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

// TestUploadMode is config.toml as uploads obey it, read from the file:
// the ledger writer's Online for each seal, which the seal frame records,
// and the uploader's Login before each upload, which agree on every file.
func TestUploadMode(t *testing.T) {
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	// A linked computer: account.json, and a sign-in that reads.
	if err := os.WriteFile(filepath.Join(dir, "account.json"), []byte(`{"v":1,"server":"https://pairtime.example","device_id":"d_testdevice00000000000000a","linked_at":"2026-11-21T09:30:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := accountCredentials
	t.Cleanup(func() { accountCredentials = saved })
	accountCredentials = func(home.Dirs, []keys.Store) (uploader.Credentials, bool, error) {
		return uploader.Credentials{Server: "https://pairtime.example", Token: "ptd_test"}, true, nil
	}
	write := func(t *testing.T, text *string) {
		t.Helper()
		if text == nil {
			if err := os.Remove(dirs.ConfigFile()); err != nil && !errors.Is(err, fs.ErrNotExist) {
				t.Fatal(err)
			}
			return
		}
		if err := os.WriteFile(dirs.ConfigFile(), []byte(*text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	str := func(s string) *string { return &s }
	tests := []struct {
		name string
		// before is the file the config watch last loaded, and text the
		// file as it is now.
		before, text *string
		// local is whether a batch sealed now is local-only, and so whether
		// nothing uploads; broken whether doctor hears config.toml has an
		// error.
		local, broken bool
	}{
		{"mode = \"online\" uploads, and isn't local-only", nil, str("mode = \"online\"\n"), false, false},
		{"mode = \"local\" is local-only, and uploads nothing", nil, str("mode = \"local\"\n"), true, false},
		{"a file with no mode is local-only, as its default is", nil, str("[display]\nshadow = true\n"), true, false},
		{"no config.toml is local-only, as the defaults are", nil, nil, true, false},
		{"a file that isn't TOML is local-only until it's fixed, whatever it said before", str("mode = \"online\"\n"), str("mode = \"onl"), true, true},
		{"a value of the wrong type keeps it local-only too, whatever its mode says", str("mode = \"online\"\n"), str("mode = \"online\"\n[http]\nport = \"8123\"\n"), true, true},
		{"an edit the config watch didn't see takes effect at once", str("mode = \"online\"\n"), str("mode = \"local\"\n"), true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: map[string]string{}}
			if tc.before != nil {
				write(t, tc.before)
				a.loadConfig(false)
			}
			write(t, tc.text)
			if online := a.online(); online == tc.local {
				t.Errorf("online() = %v, want %v", online, !tc.local)
			}
			if _, ok := a.credentials(); ok == tc.local {
				t.Errorf("uploads go: %v, while a batch sealed now is local-only: %v", ok, tc.local)
			}
			if got := strings.Contains(a.currentProblems()["account"], ConfigBroken); got != tc.broken {
				t.Errorf("doctor hears %q", a.currentProblems()["account"])
			}
		})
	}
}

func TestSignedIn(t *testing.T) {
	const (
		server = "https://pairtime.example"
		device = "d_testdevice00000000000000a"
	)
	linkedAt := time.Date(2026, 11, 21, 9, 30, 0, 0, time.UTC)
	seed := bytes.Repeat([]byte{7}, ed25519.SeedSize)
	pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	token := devtoken.New()
	stores := []keys.Store{} // K_dev and the sign-in go in the key file
	writeAccount := func(t *testing.T, dir, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "account.json"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// link does what pairtime login leaves behind: the token and the key's
	// seed in the keystore, and account.json.
	link := func(t *testing.T, dir string) *keys.Keystore {
		t.Helper()
		ks, err := keys.Open(keys.Options{Dir: dir, Stores: stores})
		if err != nil {
			t.Fatal(err)
		}
		if err := ks.SetAccount([]byte(token), seed); err != nil {
			t.Fatal(err)
		}
		writeAccount(t, dir, fmt.Sprintf(`{"v":1,"server":%q,"device_id":%q,"public_key":%q,"linked_at":%q}`,
			server, device, base64.StdEncoding.EncodeToString(pub), linkedAt.Format(time.RFC3339)))
		return ks
	}
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string)
		mode  config.Mode
		// ok is whether uploads go, fails whether doctor hears why they
		// don't, and keystore whether the data directory has a keystore
		// afterwards.
		ok, fails, keystore bool
	}{
		{"a computer that isn't linked uploads nothing and opens no keystore, which would make K_dev", func(*testing.T, string) {}, modeOnline, false, false, false},
		{"a linked computer uploads with account.json's server, device id and linked_at, and the keystore's token and key", func(t *testing.T, dir string) { link(t, dir) }, modeOnline, true, false, true},
		{"config.toml's local mode uploads nothing on a linked computer", func(t *testing.T, dir string) { link(t, dir) }, config.Local, false, false, true},
		{"a keystore without the sign-in, as after part of a logout, uploads nothing", func(t *testing.T, dir string) {
			if err := link(t, dir).ForgetAccount(); err != nil {
				t.Fatal(err)
			}
		}, modeOnline, false, false, true},
		{"an account.json pairtime didn't write uploads nothing, and doctor hears why", func(t *testing.T, dir string) {
			writeAccount(t, dir, `{"v":9}`)
		}, modeOnline, false, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir)
			c, ok, err := SignedIn(home.Dirs{Data: dir}, config.Config{Mode: tc.mode}, stores)
			if ok != tc.ok || (err != nil) != tc.fails {
				t.Fatalf("SignedIn = %v, %v; want %v, an error %v", ok, err, tc.ok, tc.fails)
			}
			if _, err := os.Stat(filepath.Join(dir, "keys")); errors.Is(err, fs.ErrNotExist) == tc.keystore {
				t.Errorf("the keys directory: %v, want a keystore %v", err, tc.keystore)
			}
			if !ok {
				if c.Token != "" || c.Sign != nil {
					t.Errorf("credentials %q, %v without a sign-in", c.Token, c.Sign != nil)
				}
				return
			}
			if c.Server != server || c.DeviceID != device || c.Token != token || !c.LinkedAt.Equal(linkedAt) {
				t.Errorf("credentials: server %q, device %q, token %q, linked_at %v", c.Server, c.DeviceID, c.Token, c.LinkedAt)
			}
			msg := []byte("pairtime-ingest-v1\n")
			sig, err := c.Sign(msg)
			if err != nil || !ed25519.Verify(pub, msg, sig) {
				t.Errorf("the signer doesn't sign with the key pairtime login registered: %v", err)
			}
		})
	}
}

func TestSignInCache(t *testing.T) {
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	if err := os.WriteFile(dirs.ConfigFile(), []byte("mode = \"online\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := func(t *testing.T, at string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "account.json"), []byte(`{"v":1,"server":"https://pairtime.example","device_id":"d_testdevice00000000000000a","linked_at":"`+at+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	saved := accountCredentials
	t.Cleanup(func() { accountCredentials = saved })
	var (
		reads int
		fail  error
	)
	accountCredentials = func(home.Dirs, []keys.Store) (uploader.Credentials, bool, error) {
		reads++
		if fail != nil {
			return uploader.Credentials{}, false, fail
		}
		return uploader.Credentials{Server: "https://pairtime.example", Token: "ptd_test"}, true, nil
	}
	a := &agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: map[string]string{}}
	link(t, "2026-11-21T09:30:00Z")
	a.credentials()
	a.credentials()
	once := reads
	link(t, "2026-11-21T10:00:00Z") // pairtime logout, then pairtime login
	_, relinked := a.credentials()
	again := reads
	fail = errors.New("the keyring is locked")
	link(t, "2026-11-21T10:30:00Z")
	a.credentials()
	a.credentials()
	tests := []struct {
		name string
		ok   bool
	}{
		{"the keystore is read once while account.json and keystore.json stay as they are", once == 1},
		{"a new sign-in is read at the next upload", again == 2 && relinked},
		{"a sign-in that doesn't read isn't asked for again at once", reads == 3},
		{"doctor hears why", strings.Contains(a.currentProblems()["account"], "the keyring is locked")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("no: %d reads (%d, then %d)", reads, once, again)
			}
		})
	}
}

// earlyLedger is the ledger writer as the uploader needs it, for an
// uploader that never uploads.
type earlyLedger struct{}

func (earlyLedger) DeviceID() string                             { return "d_testdevice00000000000000a" }
func (earlyLedger) StoreReceipt(uint64, pkgledger.Receipt) error { return nil }
func (earlyLedger) UploadFailure(uint64, string) (uint64, error) { return 0, nil }

// TestEarlyBatches is the batches the ledger writer seals before the
// uploader exists, as ledger.Open seals what a crash left: up to earlyMax
// of them wait for it, for the harnesses their records name, and it takes
// them, in order, and then every batch sealed after it. Their modes are in
// their seal frames, and so in the rows the uploader reads when it first
// uploads (TestLedgerMode, uploader.TestKillKeepsModes).
func TestEarlyBatches(t *testing.T) {
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	var handed []uint64
	saved := handSealed
	t.Cleanup(func() { handSealed = saved })
	handSealed = func(_ *uploader.Uploader, b ledger.Batch) { handed = append(handed, b.Row.B) }
	a := &agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: map[string]string{}}
	batch := func(b uint64) ledger.Batch {
		return ledger.Batch{Row: pkgledger.BatchRow{B: b, FirstSeq: b, N: 1}, SealedAt: time.Now()}
	}
	for b := uint64(1); b <= earlyMax+1; b++ {
		a.sealed(batch(b))
	}
	kept, before := len(a.early), len(handed)
	st, _, err := state.Open(dirs.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	u, err := uploader.New(uploader.Options{
		Ledger: earlyLedger{}, LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, State: st,
		Params: newestTable(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(u.Close)
	a.handEarly(u)
	early := slices.Clone(handed)
	a.sealed(batch(earlyMax + 2))
	want := make([]uint64, 0, earlyMax)
	for b := uint64(1); b <= earlyMax; b++ {
		want = append(want, b)
	}
	tests := []struct {
		name string
		ok   bool
	}{
		{"the batches sealed before the uploader existed wait for it, up to earlyMax", kept == earlyMax && before == 0},
		{"the uploader takes them, in order", slices.Equal(early, want)},
		{"the uploader takes the batches sealed after it from then on", a.upload.Load() == u && len(a.early) == 0 && slices.Equal(handed[len(early):], []uint64{earlyMax + 2})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("no: %d kept, %d handed before, then %v", kept, before, handed)
			}
		})
	}
}

// TestLedgerMode is config.toml's mode as the agent's ledger writer
// records it: openLedger gives the writer the mode uploads obey, which each
// seal frame keeps, and the config watch's reload (reloadConfig) tells the
// writer of a switch to local-only mode, after which the next record seals
// at once in a local-only batch.
func TestLedgerMode(t *testing.T) {
	str := func(s string) *string { return &s }
	// open opens the agent's ledger in a new data directory with
	// config.toml as text says, nil for none.
	open := func(t *testing.T, text *string) (*agent, *ledger.Writer, home.Dirs) {
		t.Helper()
		dir := t.TempDir()
		dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
		if text != nil {
			if err := os.WriteFile(dirs.ConfigFile(), []byte(*text), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		a := &agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: map[string]string{}, table: newestTable(), keyStores: []keys.Store{}}
		w, err := a.openLedger()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { w.Close() })
		return a, w, dirs
	}
	record := func(t *testing.T, w *ledger.Writer) {
		t.Helper()
		at := uint64(time.Now().UnixMilli()) / pkgledger.CheckpointMS * pkgledger.CheckpointMS
		if _, err := w.Checkpoint("p_test", at, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	rows := func(t *testing.T, dirs home.Dirs) []ledger.Row {
		t.Helper()
		_, rows, err := ledger.ReadRows(dirs.Ledger(), 1)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	seals := []struct {
		name   string
		text   *string
		online bool
	}{
		{"a batch sealed while config.toml says online is sealed online", str("mode = \"online\"\n"), true},
		{"a batch sealed while it says local is local-only", str("mode = \"local\"\n"), false},
		{"a batch sealed with no config.toml is local-only, as its default is", nil, false},
		{"a batch sealed while config.toml has an error is local-only", str("mode = \"onl"), false},
	}
	for _, tc := range seals {
		t.Run(tc.name, func(t *testing.T) {
			_, w, dirs := open(t, tc.text)
			record(t, w)
			if _, _, err := w.Seal(); err != nil {
				t.Fatal(err)
			}
			if got := rows(t, dirs); len(got) != 1 || got[0].Online != tc.online {
				t.Errorf("rows %+v, want one sealed online: %v", got, tc.online)
			}
		})
	}
	// sealsAtOnce writes a record every 50 ms, up to 10 s, until a batch
	// is sealed: only NoteMode's seal of a local-only stretch's first
	// record seals one so soon, since these writers have no BATCH_S job.
	sealsAtOnce := func(t *testing.T, w func() *ledger.Writer, dirs home.Dirs) []ledger.Row {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			lw := w()
			if lw == nil {
				continue // the ledger opens on a goroutine of its own
			}
			record(t, lw)
			if got := rows(t, dirs); len(got) > 0 {
				return got
			}
		}
		return nil
	}
	t.Run("the ledger's start notes no mode and hands the writer to the config watch, whose notes begin at its first reload", func(t *testing.T) {
		dir := t.TempDir()
		dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
		if err := os.WriteFile(dirs.ConfigFile(), []byte("mode = \"local\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		a := &agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: map[string]string{}, table: newestTable(), keyStores: []keys.Store{}}
		t.Cleanup(a.startLedger())
		var w *ledger.Writer
		for deadline := time.Now().Add(10 * time.Second); w == nil && time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
			w = a.writer.Load()
		}
		if w == nil {
			t.Fatal("the ledger didn't open")
		}
		// Unnoted, as records just after pairtime login are until the
		// watch notes it, a record doesn't seal at once.
		record(t, w)
		time.Sleep(200 * time.Millisecond)
		if got := rows(t, dirs); len(got) != 0 {
			t.Fatalf("a record sealed at once after the ledger's start: %+v", got)
		}
		a.reloadConfig() // the watch's reload of a change that leaves mode = "local"
		if got := sealsAtOnce(t, a.writer.Load, dirs); len(got) != 1 || got[0].Online {
			t.Errorf("rows %+v, want one local-only batch sealed at once", got)
		}
	})
	t.Run("the config watch tells the writer of a switch to local-only mode, after which the next record seals at once, local-only", func(t *testing.T) {
		a, w, dirs := open(t, str("mode = \"online\"\n"))
		ctx, cancel := context.WithCancel(context.Background())
		a.sched = sched.New()
		go a.sched.Run(ctx)
		t.Cleanup(cancel)
		a.writer.Store(w) // as the ledger's start does
		t.Cleanup(a.watchConfig())
		record(t, w)
		time.Sleep(200 * time.Millisecond)
		if got := rows(t, dirs); len(got) != 0 {
			t.Fatalf("a record written online sealed at once: %+v", got)
		}
		if err := os.WriteFile(dirs.ConfigFile(), []byte("mode = \"local\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := sealsAtOnce(t, func() *ledger.Writer { return w }, dirs); len(got) != 1 || got[0].Online {
			t.Errorf("rows %+v, want one local-only batch sealed at once", got)
		}
	})
}
