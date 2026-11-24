// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/retention"
	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
	"github.com/some-du6e/pairtime/internal/agent/vault"
)

// sweepEvery is how often the agent looks whether a retention sweep is due:
// each store is swept once a UTC day, again at the next look after a sweep
// of it failed, and whatever the day when config.toml's [privacy] changes
// (startRetention). An hour suits a computer that sleeps through midnight.
const sweepEvery = time.Hour

// The ledger's sweep is tried this many times a look, this long apart, as
// a sweep that ran into a reader of its files may on Windows, which
// renames over and deletes no file a handle has open: the scorer's and the
// uploader's reads at the ledger's open take a while on a long ledger.
const (
	ledgerSweepTries = 3
	ledgerSweepWait  = 10 * time.Second
)

// sweepFailing is how many looks in a row a store's sweep fails before
// doctor hears of it: one that fails once, as at a start that ran into a
// reader, is tried again within the hour.
const sweepFailing = 2

// startRetention runs the device's retention sweeps (DESIGN 4.4; WP-LG6)
// on a goroutine of its own, since a sweep reads and writes files for a
// while: from the agent's start, whether or not the ledger opens, then at
// the first look of each UTC day, and again when applyPrivacy puts a
// change of config.toml's [privacy] in force. The vault is swept without
// its key while it isn't open, the ledger once its writer has opened
// (startLedger), and uploads.jsonl while no uploader runs, which prunes it
// itself. The adapter host sweeps its held calls and the commit store
// commits/. It returns the step that stops it, which waits for a sweep
// that is running, so it goes before the writer, the vault and the bucket
// store close.
func (a *agent) startRetention() func() {
	periods, err := retention.Device()
	if err != nil {
		a.setProblem("retention", fmt.Sprintf("pairtime deletes nothing on time in this run, because %v", err))
		return func() {}
	}
	kick := make(chan struct{}, 1)
	a.sweepKick.Store(&kick)
	stop, done := make(chan struct{}), make(chan struct{})
	// An agent a test builds may have no scheduler: it sweeps at the start,
	// when the ledger opens and when [privacy] changes.
	var job *sched.Job
	if a.sched != nil {
		job = a.sched.Every("retention.sweep", sweepEvery, func(time.Time) { a.kickSweep(false) })
	}
	go func() {
		defer close(done)
		s := sweeps{periods: periods, swept: map[string]string{}, fails: map[string]int{}, stop: stop}
		force := true
		for {
			a.sweep(&s, force)
			select {
			case <-stop:
				return
			case <-kick:
				force = a.sweepForce.Swap(false)
			}
		}
	}()
	return func() {
		a.sweepKick.Store(nil)
		if job != nil {
			job.Stop()
		}
		close(stop)
		<-done
	}
}

// kickSweep asks the retention goroutine to look whether a sweep is due,
// and with force set to sweep whatever the day, as after a change of the
// periods. It never waits.
func (a *agent) kickSweep(force bool) {
	k := a.sweepKick.Load()
	if k == nil {
		return
	}
	if force {
		a.sweepForce.Store(true)
	}
	select {
	case *k <- struct{}{}:
	default:
	}
}

// sweeps is what the retention goroutine remembers between its looks.
type sweeps struct {
	periods retention.Periods
	// swept is the UTC day each store was last swept without an error, by
	// its name (retention.StoreVault and the others), and fails how many
	// looks in a row its sweep failed.
	swept map[string]string
	fails map[string]int
	stop  <-chan struct{}
}

// vaultDir is the vault's directory as retention.Stores takes it while no
// vault is open, swept without the key (vault.SweepDir).
type vaultDir string

func (d vaultDir) Sweep(textFrom, pathsFrom string) (int, error) {
	return vault.SweepDir(string(d), textFrom, pathsFrom)
}

// sweep sweeps each store that is due, with the periods shortened as
// config.toml's [privacy] says (retention.Periods.Shorten): a store not
// swept yet this UTC day, or every one when force is set. A store whose
// sweep fails is due again at the next look, and doctor hears of it once
// it has failed sweepFailing looks in a row.
func (a *agent) sweep(s *sweeps, force bool) {
	// The inbox's spool files go at each look, the day's first or not:
	// they hold whole hook calls, prompts included (D-inbox-age-cap).
	a.expireInbox()
	now := time.Now()
	today := now.UTC().Format(time.DateOnly)
	due := func(store string) bool { return force || s.swept[store] != today }
	priv := a.privacy()
	periods := s.periods.Shorten(priv.TextDays, priv.PathDays)
	var swept retention.Swept
	failed := map[string]error{}
	var ran []string
	run := func(store string, stores retention.Stores) {
		got, f := retention.Sweep(now, periods, stores)
		swept.Entries, swept.Batches = swept.Entries+got.Entries, swept.Batches+got.Batches
		swept.Segments, swept.Buckets = swept.Segments+got.Segments, swept.Buckets+got.Buckets
		if err := f[store]; err != nil {
			failed[store] = err
		} else {
			delete(failed, store)
		}
		if !slices.Contains(ran, store) {
			ran = append(ran, store)
		}
	}
	// stopping reports whether the agent is stopping, which waits for this.
	stopping := func() bool {
		select {
		case <-s.stop:
			return true
		default:
			return false
		}
	}
	// While no vault is open, as while the ledger can't open, its
	// directory is swept without the key, and openLedger waits to open it
	// until the sweep is done. With "keep prompt text" off and no hold, that
	// sweep deletes every text the files hold too, as DropText does once a
	// vault is open (applyPrivacy). While openLedger is opening the vault,
	// this waits for nothing, so an agent that stops meanwhile doesn't wait
	// here for the ledger's open, and the vault stays due whatever the day,
	// as for a forced look after a change of [privacy]: the look startLedger
	// asks for once the ledger is open, or the next hourly one, sweeps it.
	if due(retention.StoreVault) {
		if !a.vaultFiles.TryLock() {
			delete(s.swept, retention.StoreVault)
		} else {
			a.vaultMu.Lock()
			v := a.vault
			a.vaultMu.Unlock()
			if v != nil {
				a.vaultFiles.Unlock()
				run(retention.StoreVault, retention.Stores{Vault: v})
			} else {
				run(retention.StoreVault, retention.Stores{Vault: vaultDir(a.dirs.Vault())})
				if p := a.privacy(); p.TextOff && !p.TextHeld {
					n, err := vault.DropTextDir(a.dirs.Vault())
					swept.Entries += n
					if err != nil {
						failed[retention.StoreVault] = errors.Join(failed[retention.StoreVault], err)
					}
				}
				a.vaultFiles.Unlock()
			}
		}
	}
	if w := a.writer.Load(); w != nil && due(retention.StoreLedger) && !stopping() {
		stores := retention.Stores{Ledger: w, Floor: a.ledgerFloor()}
		for try := 1; ; try++ {
			run(retention.StoreLedger, stores)
			if failed[retention.StoreLedger] == nil || try == ledgerSweepTries {
				break
			}
			t := time.NewTimer(ledgerSweepWait)
			select {
			case <-s.stop:
				t.Stop()
				return
			case <-t.C:
			}
		}
	}
	if a.buckets != nil && due(retention.StoreBuckets) && !stopping() {
		run(retention.StoreBuckets, retention.Stores{Buckets: a.buckets})
	}
	if due(retention.StoreUploads) && !stopping() {
		// The uploader prunes uploads.jsonl while it runs, and appends to
		// it: none starts while this prunes (startUploader).
		a.uploadsMu.Lock()
		if a.upload.Load() == nil {
			run(retention.StoreUploads, retention.Stores{Uploads: func(now time.Time) error { return uploader.PruneUploads(a.dirs.Data, now) }})
		}
		a.uploadsMu.Unlock()
	}
	for _, store := range ran {
		if err := failed[store]; err != nil {
			s.fails[store]++
			a.log.Warn("the retention sweep", "store", store, "err", err)
			continue
		}
		s.swept[store], s.fails[store] = today, 0
	}
	if len(ran) == 0 {
		return
	}
	var failing []string
	for _, store := range slices.Sorted(maps.Keys(s.fails)) {
		if s.fails[store] >= sweepFailing && failed[store] != nil {
			failing = append(failing, fmt.Sprintf("%s (%v)", store, failed[store]))
		}
	}
	if len(failing) > 0 {
		a.setProblem("retention", fmt.Sprintf("pairtime couldn't delete all it keeps past its time, at its last %d tries, and tries again within the hour: %s", sweepFailing, strings.Join(failing, "; ")))
	} else {
		a.setProblem("retention", "")
	}
	if swept != (retention.Swept{}) {
		a.log.Info("the retention sweep deleted what was past its time", "vault entries", swept.Entries,
			"ledger batches", swept.Batches, "ledger segments", swept.Segments, "bucket days", swept.Buckets)
	}
}

// ledgerFloor is the first batch whose row the ledger's sweep keeps
// whatever its age: while account.json links this computer, the first
// online batch the server hasn't stored, by the uploader's place
// (uploader.HeldFrom), and 0 for none while it isn't linked
// (docs/errata/2026-11-23-unsent-batches-retention.md). A place or rows
// that don't read keep every row.
func (a *agent) ledgerFloor() uint64 {
	if !Linked(a.dirs) {
		return 0
	}
	if a.state == nil {
		return 1 // no place to read, as in an agent a test builds
	}
	floor, err := uploader.HeldFrom(a.state, a.dirs.Ledger())
	if err != nil {
		a.log.Warn("the retention sweep keeps every row of the ledger, because the uploads' place doesn't read", "err", err)
		return 1
	}
	return floor
}
