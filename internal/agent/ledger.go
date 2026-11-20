// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"errors"
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

// The waits between tries at opening a ledger that didn't open for a
// reason that can pass, such as a keyring that is still locked after
// login: defaultLedgerRetry first, unless Options.LedgerRetry replaces it,
// and twice the wait before each time after that, up to ledgerRetryMax.
// Each try may ask the user to unlock the keyring again, so the waits
// grow. Presence's queue holds about an hour of records meanwhile.
const (
	defaultLedgerRetry = 15 * time.Second
	ledgerRetryMax     = 10 * time.Minute
)

// startLedger opens the device keystore and the ledger writer (WP-OS7,
// WP-LG5) and returns the step that stops them. They open on a goroutine
// of their own, because the keystore can wait for the user to unlock it;
// until the writer is open, presence's records wait in a.ledgerIn. When
// they don't open for a reason that can pass, the goroutine tries again
// until they do or the agent stops. Every seal goes through the
// scheduler's seal every BATCH_S, presence's seals on lock and suspend,
// and the writer's own on a prompt or a stop.
//
// The ledger's problems go under three sources, so none overwrites
// another: "ledger" says why it isn't open, and later why a seal failed;
// "ledger.presence" what presence's queue had to drop; "keystore" where
// the device key is kept.
func (a *agent) startLedger() func() {
	a.ledgerIn = ledger.NewPresence(func(msg string) { a.setProblem("ledger.presence", msg) })
	var (
		mu      sync.Mutex
		w       *ledger.Writer
		stopped bool
	)
	quit := make(chan struct{})
	opened := make(chan struct{})
	go func() {
		defer close(opened)
		lw := a.openLedgerRetrying(quit)
		if lw == nil {
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
		close(quit)
		select {
		case <-opened:
		case <-time.After(ledgerOpenWait):
			a.log.Warn("the agent stops before its ledger opened")
		}
		mu.Lock()
		stopped = true
		lw := w
		mu.Unlock()
		// Presence has stopped, so this writes the last of its records,
		// and Close seals them.
		a.ledgerIn.Close()
		if lw != nil {
			if err := lw.Close(); err != nil {
				a.log.Warn("closing the ledger", "err", err)
			}
		}
	}
}

// openLedgerRetrying opens the ledger, and tries again while what stops it
// can pass, waiting longer each time (defaultLedgerRetry). It clears the
// "ledger" problem once the ledger opens. It returns nil when the ledger
// can't open (ledgerGivesUp) or quit closes first.
func (a *agent) openLedgerRetrying(quit <-chan struct{}) *ledger.Writer {
	wait := a.ledgerRetry
	for {
		lw, err := a.openLedger()
		if err == nil {
			a.setProblem("ledger", "")
			return lw
		}
		if ledgerGivesUp(err) {
			a.setProblem("ledger", fmt.Sprintf("the agent records nothing in the ledger: %v", err))
			a.log.Warn("the ledger can't open", "err", err)
			return nil
		}
		a.setProblem("ledger", fmt.Sprintf("the agent records nothing in the ledger until it opens, and tries again in %v: %v", wait, err))
		a.log.Warn("the ledger didn't open, and the agent tries again", "in", wait, "err", err)
		t := time.NewTimer(wait)
		select {
		case <-quit:
			t.Stop()
			return nil
		case <-t.C:
		}
		wait = min(2*wait, ledgerRetryMax)
	}
}

// ledgerGivesUp reports whether err means the ledger can't open however
// often the agent tries: the device key is gone from its store or isn't
// what keystore.json names, or the ledger's files are damaged, or the key
// has a chain the ledger can't go on with. Each needs a person: `pairtime
// restore`, or a new device key. Anything else can pass: a keyring that
// is locked or doesn't answer (keys.ErrUnavailable, which the keystore
// also gives for any other error of the OS's store), a full disk, or a
// keystore lock another process holds.
func ledgerGivesUp(err error) bool {
	return errors.Is(err, keys.ErrKeyLost) || errors.Is(err, keys.ErrDamaged) ||
		errors.Is(err, ledger.ErrDamaged) || errors.Is(err, ledger.ErrKeyReuse)
}

// openLedger opens the keystore, making K_dev at an installation's first
// run, and the ledger writer with the keys derived from it.
func (a *agent) openLedger() (*ledger.Writer, error) {
	ks, err := keys.Open(keys.Options{Dir: a.dirs.Data, Stores: a.keyStores})
	if err != nil {
		return nil, fmt.Errorf("the device key can't be opened: %w", err)
	}
	st := ks.Status()
	// doctor and the dashboard repeat it (DESIGN 3.6; CP 5.2).
	a.setProblem("keystore", st.Warning())
	return ledger.Open(ledger.Options{
		Dir:   a.dirs.Ledger(),
		Keys:  ks.Keys(),
		KeyID: st.KeyID,
		// keystore.json keeps the key's chain, so a ledger directory that
		// is deleted can't make the key start another (ledger-v1 3).
		Chains:  ks,
		Sched:   a.sched,
		Problem: func(msg string) { a.setProblem("ledger", msg) },
	})
}
