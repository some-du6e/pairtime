// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"fmt"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
)

// ledgerOpenWait bounds how long a stop waits for the ledger to finish
// opening. The keystore can wait up to keys.DefaultTimeout for the user to
// unlock it, and a stop shouldn't.
const ledgerOpenWait = 5 * time.Second

// startLedger opens the device keystore and the ledger writer (WP-OS7,
// WP-LG5) and returns the step that stops them. They open on a goroutine
// of their own, because the keystore can wait for the user to unlock it.
// Until the keystore is open, focus's records go without app_hash
// (keyFocus), and until the writer is open, presence's and focus's
// records wait in a.ledgerIn. Every seal goes through the scheduler's seal
// every BATCH_S, presence's seals on lock and suspend, and the writer's
// own on a prompt or a stop.
func (a *agent) startLedger() func() {
	a.ledgerIn = ledger.NewPresence(func(msg string) { a.setProblem("ledger", msg) })
	var (
		mu      sync.Mutex
		w       *ledger.Writer
		stopped bool
	)
	opened := make(chan struct{})
	go func() {
		defer close(opened)
		lw, err := a.openLedger()
		if err != nil {
			a.setProblem("ledger", fmt.Sprintf("the agent records nothing in the ledger: %v", err))
			a.log.Warn("the ledger didn't open", "err", err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if stopped {
			lw.Close()
			return
		}
		w = lw
		a.ledgerIn.Attach(lw)
	}()
	return func() {
		select {
		case <-opened:
		case <-time.After(ledgerOpenWait):
			a.log.Warn("the agent stops before its ledger opened")
		}
		mu.Lock()
		stopped = true
		lw := w
		mu.Unlock()
		// Presence and focus have stopped, so this writes the last of
		// their records, and Close seals them.
		a.ledgerIn.Close()
		if lw != nil {
			if err := lw.Close(); err != nil {
				a.log.Warn("closing the ledger", "err", err)
			}
		}
	}
}

// openLedger opens the keystore, making K_dev at an installation's first
// run, and the ledger writer with the keys derived from it. It also gives
// the focus component K_app (keyFocus).
func (a *agent) openLedger() (*ledger.Writer, error) {
	ks, err := keys.Open(keys.Options{Dir: a.dirs.Data, Stores: a.keyStores})
	if err != nil {
		return nil, fmt.Errorf("the device key can't be opened: %w", err)
	}
	st := ks.Status()
	// doctor and the dashboard repeat it (DESIGN 3.6; CP 5.2).
	a.setProblem("keystore", st.Warning())
	a.keyFocus(ks)
	return ledger.Open(ledger.Options{
		Dir:     a.dirs.Ledger(),
		Keys:    ks.Keys(),
		KeyID:   st.KeyID,
		Sched:   a.sched,
		Problem: func(msg string) { a.setProblem("ledger", msg) },
	})
}
