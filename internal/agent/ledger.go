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
// of their own, because the keystore can wait for the user to unlock it.
// Until the keystore is open, focus's records go without app_hash
// (keyFocus), and until the writer is open, presence's and focus's
// records wait in a.ledgerIn. When they don't open for a reason that can
// pass, the goroutine tries again until they do or the agent stops. Every
// seal goes through the scheduler's seal every BATCH_S, presence's seals
// on lock and suspend, and the writer's own on a prompt or a stop. Each
// sealed batch goes to the scorer loop, which startLedger starts once the
// writer is open (WP-AG4), so the scorer reads only sealed records
// (decision E-sm-1.1-seq-gaps), and to the uploader, which startLedger
// starts then too (WP-AG5).
//
// The ledger's problems go under three sources, so none overwrites
// another: "ledger" says why it isn't open, and later why a seal failed;
// "ledger.presence" what the queue of presence's and focus's records had
// to drop; "keystore" where the device key is kept.
func (a *agent) startLedger() func() {
	a.ledgerIn = ledger.NewPresence(func(msg string) { a.setProblem("ledger.presence", msg) })
	var (
		mu         sync.Mutex
		w          *ledger.Writer
		stopped    bool
		stopUpload = func() {}
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
		// The adapters parse the hook calls that waited for the keys and
		// the writer (adapters.go).
		a.adaptersHost().attach(lw)
		w = lw
		// The config watch tells the writer each change of config.toml's
		// mode from here on (ledger.Writer.NoteMode). The mode the agent
		// started in isn't noted: every install starts in local mode, and
		// the watch notes pairtime login only after its debounce, so the
		// records just after the login would count as local-only. A
		// stretch from the start that no seal falls in, as from pairtime
		// install to pairtime login, takes the mode of its seal. Once a
		// seal has read config.toml as local-only, the login's note seals
		// what is pending, and the root of the first batch after it goes,
		// and the minutes after the switch (Batch.Back).
		// The records that waited for the writer, presence's queue and the
		// hook calls the adapters held, attach on goroutines of their own
		// and take the mode of their seal too: those made in a local-only
		// stretch while the ledger was shut can be sealed online, a gap
		// #131 had too, which a WP-AG5 follow-up closes before the teen
		// pilot (docs/PLAN.md).
		a.writer.Store(lw)
		a.ledgerIn.Attach(lw)
		if a.scorer != nil {
			a.scorer.Start()
		}
		stopUpload = a.startUploader(lw)
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
		stop := stopUpload
		mu.Unlock()
		// The uploader stops first: it writes receipts and failure notes
		// to the writer.
		stop()
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
			// Its hook calls would wait for it forever (adapters.go).
			a.adaptersHost().giveUp()
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
// run, and the ledger writer with the keys derived from it. It also gives
// the adapters K_feat and K_project (keyAdapters), the focus component
// K_app (keyFocus), and the hook sink K_session, which keys the hooked
// sessions focus lists and the adapters' records (keySessions).
func (a *agent) openLedger() (*ledger.Writer, error) {
	ks, err := keys.Open(keys.Options{Dir: a.dirs.Data, Stores: a.keyStores})
	if err != nil {
		return nil, fmt.Errorf("the device key can't be opened: %w", err)
	}
	st := ks.Status()
	// doctor and the dashboard repeat it (DESIGN 3.6; CP 5.2).
	a.setProblem("keystore", st.Warning())
	a.keyAdapters(ks)
	a.keyFocus(ks)
	a.keySessions(ks)
	return ledger.Open(ledger.Options{
		Dir:   a.dirs.Ledger(),
		Keys:  ks.Keys(),
		KeyID: st.KeyID,
		// keystore.json keeps the key's chain, so a ledger directory that
		// is deleted can't make the key start another (ledger-v1 3).
		Chains:  ks,
		Params:  a.table,
		Sched:   a.sched,
		Sealed:  a.sealed,
		Online:  a.online,
		Problem: func(msg string) { a.setProblem("ledger", msg) },
	})
}
