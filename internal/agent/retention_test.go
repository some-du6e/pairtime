// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/retention"
	"github.com/some-du6e/pairtime/internal/agent/vault"
	"github.com/some-du6e/pairtime/pkg/ledger/features"
)

// TestRetentionPeriods checks the periods of the stores that sweep
// themselves against docs/retention.json's device entries, which
// TestDesignPeriods holds to DESIGN 4.4, so the agent deletes what the
// privacy notice says when it says (WP-LG6).
func TestRetentionPeriods(t *testing.T) {
	p, err := retention.Device()
	if err != nil {
		t.Fatal(err)
	}
	day := 24 * time.Hour
	cases := []struct {
		name string
		got  time.Duration
		want int
	}{
		{"the adapter host keeps a hook call it holds no longer than device.held_calls says", heldKeep, p.Held},
		{"the commit store keeps a day's commit records as long as device.commits says", commitsKeep, p.Commits},
		{"the inbox keeps a spool file that waits to be drained no longer than device.inbox says", inbox.SpoolKeep, p.Inbox},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != time.Duration(c.want)*day {
				t.Errorf("%v, want %d days", c.got, c.want)
			}
		})
	}
}

// TestSweepTextWithoutVault checks that turning "keep prompt text" off
// deletes the text the vault kept while the ledger can't open, which opens
// the vault: the retention sweep deletes it without the key (WP-LG6).
func TestSweepTextWithoutVault(t *testing.T) {
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	ks, err := keys.Open(keys.Options{Dir: dirs.Data, Stores: []keys.Store{}})
	if err != nil {
		t.Fatal(err)
	}
	keyID := ks.Status().KeyID
	// What an earlier run kept: record 1's text and paths, today.
	openVault := func(t *testing.T, next uint64) *vault.Vault {
		t.Helper()
		v, err := vault.Open(vault.Options{Dir: dirs.Vault(), Keys: ks.Keys(), KeyID: keyID, KeepText: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := v.Reconcile(next); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { v.Close() })
		return v
	}
	v := openVault(t, 1)
	v.Keep(time.Now().UTC().Format(time.DateOnly), 1, []byte("fix the nav bar"), []string{"src/nav.go"})
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	// A ledger whose batches.seg is damaged doesn't open, so no vault is
	// published.
	if err := os.MkdirAll(dirs.Ledger(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirs.Ledger(), "batches.seg"), []byte("not a ledger"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: map[string]string{}, table: newestTable(), keyStores: []keys.Store{}}
	a.cfg.Store(&config.Config{Mode: config.Local, Privacy: config.Privacy{TextOff: true}})
	if _, err := a.openLedger(); !errors.Is(err, ledger.ErrDamaged) {
		t.Fatalf("openLedger: %v, want ErrDamaged", err)
	}
	periods, err := retention.Device()
	if err != nil {
		t.Fatal(err)
	}
	a.sweep(&sweeps{periods: periods, swept: map[string]string{}, fails: map[string]int{}, stop: make(chan struct{})}, true)
	v = openVault(t, 2)
	cases := []struct {
		name string
		ok   func() (bool, error)
	}{
		{"the text is deleted", func() (bool, error) {
			c, err := ks.Keys().Commit(1, []byte("fix the nav bar"))
			if err != nil {
				return false, err
			}
			_, err = v.Text(1, c)
			return errors.Is(err, vault.ErrNotKept), err
		}},
		{"the paths, which the switch doesn't cover, stay", func() (bool, error) {
			key, err := ks.Keys().Features().Path("src/nav.go")
			if err != nil {
				return false, err
			}
			paths, err := v.Paths(1, []features.Hunk{{P: key}})
			return err == nil && slices.Equal(paths, []string{"src/nav.go"}), err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if ok, err := c.ok(); !ok {
				t.Errorf("it isn't: %v", err)
			}
		})
	}
}

// TestSweepVaultBusy checks that a look which finds openLedger opening the
// vault, as it holds vaultFiles meanwhile, waits for nothing and leaves
// the vault due for the next look, a forced look's sweep included.
func TestSweepVaultBusy(t *testing.T) {
	dir := t.TempDir()
	a := &agent{dirs: home.Dirs{Data: dir, Config: dir, Runtime: dir}, log: slog.New(slog.DiscardHandler), problems: map[string]string{}}
	periods, err := retention.Device()
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().UTC().Format(time.DateOnly)
	s := &sweeps{periods: periods, swept: map[string]string{retention.StoreVault: today}, fails: map[string]int{}, stop: make(chan struct{})}
	a.vaultFiles.Lock()
	a.sweep(s, true)
	a.vaultFiles.Unlock()
	_, sweptBusy := s.swept[retention.StoreVault]
	a.sweep(s, false)
	cases := []struct {
		name string
		ok   bool
	}{
		{"a forced look that finds the vault being opened leaves it due", !sweptBusy},
		{"the next look, though not forced, sweeps it", s.swept[retention.StoreVault] == today},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Error("it doesn't")
			}
		})
	}
}
