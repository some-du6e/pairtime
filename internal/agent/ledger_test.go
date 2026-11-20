// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
)

// TestLedger checks that a running agent opens the device keystore and its
// ledger (WP-OS7, WP-LG5), and that the ledger reads back once it stops.
func TestLedger(t *testing.T) {
	dirs := home.Dirs{Data: t.TempDir(), Config: t.TempDir()}
	dirs.Runtime = dirs.Data
	a := startAgent(t, Options{Dirs: dirs, Ports: []int{0}, KeyStores: []keys.Store{}})

	// Both open on a goroutine of their own, after the agent is ready.
	var info ledger.ChainInfo
	deadline := time.Now().Add(20 * time.Second)
	for {
		var err error
		if info, err = ledger.ReadChain(dirs.Ledger()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent didn't open its ledger within 20 s: %v; status %+v", err, getStatus(t, a))
		}
		time.Sleep(50 * time.Millisecond)
	}
	ks, err := keys.Inspect(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	a.cancel()
	stopErr := a.waitExit(t)

	cases := []struct {
		name string
		ok   bool
	}{
		{"the ledger's chain is written under the installation's device key", info.KeyID == ks.KeyID},
		{"the chain names a device", info.DeviceID != ""},
		{"the agent stops cleanly with its ledger open", stopErr == nil},
		{"the stopped agent's ledger reads back", ledger.ReadBatches(dirs.Ledger(), 1, func(ledger.Batch) error { return nil }) == nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("no: chain %+v, keystore %+v, stop %v", info, ks, stopErr)
			}
		})
	}
}

// lockedStore is an OS secret store kept in memory. While it is locked it
// answers every call with keys.ErrUnavailable, as a keyring that is still
// locked after login does when nobody answers its prompt.
type lockedStore struct {
	mu      sync.Mutex
	items   map[string][]byte
	locked  bool
	refused int // calls answered while locked
}

func (s *lockedStore) Kind() keys.Kind { return keys.KindSecretService }

func (s *lockedStore) Get(name string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return nil, err
	}
	b, ok := s.items[name]
	if !ok {
		return nil, keys.ErrNotFound
	}
	return bytes.Clone(b), nil
}

func (s *lockedStore) Set(name string, secret []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	s.items[name] = bytes.Clone(secret)
	return nil
}

func (s *lockedStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	if _, ok := s.items[name]; !ok {
		return keys.ErrNotFound
	}
	delete(s.items, name)
	return nil
}

// check refuses a call while the store is locked. s.mu is held.
func (s *lockedStore) check() error {
	if s.locked {
		s.refused++
		return fmt.Errorf("%w: the test keyring is locked", keys.ErrUnavailable)
	}
	return nil
}

func (s *lockedStore) setLocked(locked bool) {
	s.mu.Lock()
	s.locked = locked
	s.mu.Unlock()
}

func (s *lockedStore) refusals() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refused
}

// TestLedgerRetry checks that a keystore that can't be reached when the
// agent starts doesn't leave the agent without a ledger for its whole run:
// the agent says why the ledger isn't open, tries again, and opens it once
// the keyring answers.
func TestLedgerRetry(t *testing.T) {
	dirs := home.Dirs{Data: t.TempDir(), Config: t.TempDir()}
	dirs.Runtime = dirs.Data
	store := &lockedStore{items: map[string][]byte{}}
	opts := Options{Dirs: dirs, Ports: []int{0}, KeyStores: []keys.Store{store}, LedgerRetry: 20 * time.Millisecond}

	// The first run makes K_dev in the store, so keystore.json names it,
	// and starts the chain.
	first := startAgent(t, opts)
	before := waitChain(t, first, dirs)
	first.cancel()
	if err := first.waitExit(t); err != nil {
		t.Fatal(err)
	}

	// The next run starts with the keyring locked.
	store.setLocked(true)
	a := startAgent(t, opts)
	locked := waitStatus(t, a, func(s statusReply) bool { return s.Problems["ledger"] != "" })
	for deadline := time.Now().Add(10 * time.Second); store.refusals() < 2; {
		if time.Now().After(deadline) {
			t.Fatalf("the agent asked the locked keyring %d times in 10 s; status %+v", store.refusals(), getStatus(t, a))
		}
		time.Sleep(10 * time.Millisecond)
	}
	store.setLocked(false)
	unlocked := waitStatus(t, a, func(s statusReply) bool { return s.Problems["ledger"] == "" })
	refused := store.refusals()
	a.cancel()
	stopErr := a.waitExit(t)
	after, chainErr := ledger.ReadChain(dirs.Ledger())

	cases := []struct {
		name string
		ok   bool
	}{
		{"while the keyring is locked, the ledger problem says the agent tries again",
			strings.Contains(locked.Problems["ledger"], "tries again")},
		{"the agent asked the locked keyring more than once", refused >= 2},
		{"once the keyring answers, the ledger opens and its problem clears", unlocked.Problems["ledger"] == ""},
		{"the ledger it opened is the chain the first run started, under the same key",
			chainErr == nil && after == before},
		{"the agent stops cleanly", stopErr == nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("no: problem %q, refusals %d, chain %+v then %+v (%v), stop %v",
					locked.Problems["ledger"], refused, before, after, chainErr, stopErr)
			}
		})
	}
}

// waitChain waits up to 20 s for the agent to have opened the ledger in
// dirs, and returns its chain.
func waitChain(t *testing.T, a *running, dirs home.Dirs) ledger.ChainInfo {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		info, err := ledger.ReadChain(dirs.Ledger())
		if err == nil {
			return info
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent didn't open its ledger within 20 s: %v; status %+v", err, getStatus(t, a))
		}
		time.Sleep(50 * time.Millisecond)
	}
}
