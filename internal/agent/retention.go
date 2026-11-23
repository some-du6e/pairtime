// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"fmt"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/retention"
)

// sweepEvery is how often the agent looks whether a retention sweep is due:
// one runs at most once a UTC day, but when config.toml's [privacy]
// changes (startRetention). An hour suits a computer that sleeps through
// midnight.
const sweepEvery = time.Hour

// startRetention runs the device's retention sweeps (DESIGN 4.4; WP-LG6)
// over the vault, the ledger writer lw and the minute buckets, on a
// goroutine of its own, since a sweep reads and writes files for a while:
// once lw has opened, then at the first look of each UTC day after, and
// again when applyPrivacy puts a change of config.toml's [privacy] in
// force. The other stores sweep themselves: the adapter host its held
// calls, the commit store commits/, the uploader uploads.jsonl. It returns
// the step that stops it, which waits for a sweep that is running, so it
// goes before the writer and the vault close.
func (a *agent) startRetention(lw *ledger.Writer) func() {
	periods, err := retention.Device()
	if err != nil {
		a.setProblem("retention", fmt.Sprintf("pairtime deletes nothing on time in this run, because %v", err))
		return func() {}
	}
	kick := make(chan struct{}, 1)
	a.sweepKick.Store(&kick)
	stop, done := make(chan struct{}), make(chan struct{})
	job := a.sched.Every("retention.sweep", sweepEvery, func(time.Time) { a.kickSweep(false) })
	go func() {
		defer close(done)
		last := "" // the UTC day of the last sweep
		force := true
		for {
			if today := time.Now().UTC().Format(time.DateOnly); force || today != last {
				last = today
				a.sweep(lw, periods)
			}
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
		job.Stop()
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

// sweep runs one retention sweep with periods, shortened as config.toml's
// [privacy] says (retention.Periods.Shorten), and says in the agent's
// problems when a store couldn't delete what is past its period.
func (a *agent) sweep(lw *ledger.Writer, periods retention.Periods) {
	priv := a.privacy()
	s := retention.Stores{Ledger: lw}
	a.vaultMu.Lock()
	if a.vault != nil {
		s.Vault = a.vault
	}
	a.vaultMu.Unlock()
	if a.buckets != nil {
		s.Buckets = a.buckets
	}
	swept, err := retention.Sweep(time.Now(), periods.Shorten(priv.TextDays, priv.PathDays), s)
	if err != nil {
		a.setProblem("retention", fmt.Sprintf("pairtime couldn't delete all it keeps past its time, and tries again tomorrow: %v", err))
		a.log.Warn("the retention sweep", "err", err)
		return
	}
	a.setProblem("retention", "")
	if swept != (retention.Swept{}) {
		a.log.Info("the retention sweep deleted what was past its time", "vault entries", swept.Entries,
			"ledger batches", swept.Batches, "ledger segments", swept.Segments, "bucket days", swept.Buckets)
	}
}
