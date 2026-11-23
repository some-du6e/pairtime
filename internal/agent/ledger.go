// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/pkg/event"
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
	// Every record made from here on counts by what the agent sees of
	// config.toml's mode, whenever the writer opens (modes).
	a.startModes()
	// The ledger opens on a goroutine of its own, and may seal before the
	// adapters have counted the calls held from an earlier run: until they
	// have, a call made before config.toml was last seen local-only may be
	// on its way (waiting).
	a.adaptersHost().expectCount()
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
			a.closeVault()
			return
		}
		w = lw
		// The config watch tells the writer each change of config.toml's
		// mode from here on (ledger.Writer.NoteMode), and publishWriter
		// first gives it what the agent saw since the start, which Open
		// took too (ledger.Seen): every install starts in local mode, so
		// the stretch up to pairtime login counts, as does one the watch
		// noted while the ledger waited to open. The watch notes the login
		// at its first look at config.toml, not after its debounce
		// (noteBack), so the records just after it are online. Once the
		// writer has the stretch, the records that waited for it, presence's
		// queue and the hook calls the adapters held, attach: those made
		// before the stretch ended are sealed local-only, by their own time,
		// and the first batch after them that holds none takes the mark
		// (Batch.Back), once none is still on its way (a.waiting).
		a.publishWriter(lw)
		a.ledgerIn.Attach(lw)
		// The adapters parse the hook calls that waited for the keys and
		// the writer (adapters.go).
		a.adaptersHost().attach(lw)
		if a.scorer != nil {
			a.scorer.Start()
		}
		stopUpload = a.startUploader(lw)
		// The ledger's own sweep was waiting for the writer.
		a.kickSweep(false)
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
		// to the writer. The retention sweeps, which use the writer and the
		// vault too, stopped before this (startRetention).
		stop()
		// Presence and focus have stopped, so this writes the last of
		// their records, and Close seals them.
		a.ledgerIn.Close()
		if lw != nil {
			if err := lw.Close(); err != nil {
				a.log.Warn("closing the ledger", "err", err)
			}
		}
		// After the writer, whose last seal flushes it.
		a.closeVault()
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
// run, and the ledger writer with the keys derived from it, which keeps
// each record's content in the vault (openVault, publishVault). It also
// gives the adapters K_feat and K_project (keyAdapters), the focus
// component K_app (keyFocus), and the hook sink K_session, which keys the
// hooked sessions focus lists and the adapters' records (keySessions).
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
	// Before the writer: the vault flushes what a killed run kept before
	// ledger.Open flushes the batches it restores (ledger.Vault). A
	// retention sweep of the vault's directory, which runs while no vault
	// is open, is done first, and none starts until the vault is published
	// or closed again.
	a.vaultFiles.Lock()
	defer a.vaultFiles.Unlock()
	v := a.openVault(ks, st.KeyID)
	w, err := ledger.Open(ledger.Options{
		Dir:   a.dirs.Ledger(),
		Keys:  ks.Keys(),
		KeyID: st.KeyID,
		// keystore.json keeps the key's chain, so a ledger directory that
		// is deleted can't make the key start another (ledger-v1 3).
		Chains:  ks,
		Params:  a.table,
		Sched:   a.sched,
		Sealed:  a.sealed,
		Vault:   writerVault(v),
		Online:  a.writerOnline,
		Seen:    a.seen,
		Waiting: a.waiting,
		Problem: func(msg string) { a.setProblem("ledger", msg) },
	})
	if err != nil {
		if v != nil {
			v.Close()
		}
		return nil, err
	}
	a.publishVault(v)
	return w, nil
}

// waiting is the ledger writer's Waiting: whether a record that before
// says may have been made before config.toml was last seen local-only is
// still on its way to it: one of presence's still queued, a hook call in
// the inbox's spool, parked behind its folder's first Track or held while
// the adapters' keys or the writer weren't there, or a git change the
// watcher holds for its actor. A git change carries this run's clock from
// when its scan began (gitHost.sink), and is asked about by it. It takes
// presence's, the adapter host's and the git watcher's locks, none of
// which is held while the writer is called.
func (a *agent) waiting(before func(event.Envelope) bool) bool {
	if a.ledgerIn != nil && a.ledgerIn.Waiting(before) || a.spoolHolds(before) {
		return true
	}
	h := a.adaptersHost()
	if h.waiting(before) {
		return true
	}
	g := h.git.Load()
	return g != nil && g.w.Waiting(func(t int64, found time.Time) bool {
		mono, boot := ledger.RunClock(found)
		return before(event.Envelope{T: t, Mono: mono, BootID: boot})
	})
}
