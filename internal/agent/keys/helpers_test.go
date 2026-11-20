// SPDX-License-Identifier: AGPL-3.0-or-later

package keys

import (
	"bytes"
	"crypto/subtle"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/ledger"
)

// fakeStore is a Store in memory that a test can make fail, hang or lie.
type fakeStore struct {
	kind Kind

	mu      sync.Mutex
	items   map[string][]byte
	calls   map[string]int // by method: get, set, delete
	failGet error
	failSet error
	failDel error
	alter   bool          // Get gives the secret back with a bit flipped
	keep    bool          // Delete returns nil and keeps the item, as a dismissed keyring prompt does
	hang    chan struct{} // when set, each call waits until it is closed
	onSet   func(name string)
}

func newFake(kind Kind) *fakeStore {
	return &fakeStore{kind: kind, items: map[string][]byte{}, calls: map[string]int{}}
}

func (f *fakeStore) Kind() Kind { return f.kind }

func (f *fakeStore) enter(method string) {
	f.mu.Lock()
	f.calls[method]++
	hang := f.hang
	f.mu.Unlock()
	if hang != nil {
		<-hang
	}
}

func (f *fakeStore) Get(name string) ([]byte, error) {
	f.enter("get")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failGet != nil {
		return nil, f.failGet
	}
	b, ok := f.items[name]
	if !ok {
		return nil, ErrNotFound
	}
	b = bytes.Clone(b)
	if f.alter {
		b[0] ^= 0x01
	}
	return b, nil
}

func (f *fakeStore) Set(name string, secret []byte) error {
	f.enter("set")
	f.mu.Lock()
	onSet := f.onSet
	f.mu.Unlock()
	if onSet != nil {
		onSet(name)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSet != nil {
		return f.failSet
	}
	f.items[name] = bytes.Clone(secret)
	return nil
}

func (f *fakeStore) Delete(name string) error {
	f.enter("delete")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failDel != nil {
		return f.failDel
	}
	if _, ok := f.items[name]; !ok {
		return ErrNotFound
	}
	if !f.keep {
		delete(f.items, name)
	}
	return nil
}

// update changes the fake's behavior under its lock.
func (f *fakeStore) update(change func(f *fakeStore)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func (f *fakeStore) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

func (f *fakeStore) has(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.items[name]
	return ok
}

func (f *fakeStore) size() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.items)
}

// waitFor fails the test unless the fake has name within 10 s, as it does
// once a call that timed out finishes late.
func (f *fakeStore) waitFor(t *testing.T, name string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !f.has(name); {
		if time.Now().After(deadline) {
			t.Fatalf("the store never got %s", name)
		}
		time.Sleep(time.Millisecond)
	}
}

// useOSStore makes s the store this OS has, for keystores whose
// Options.Stores is nil, until the test ends. Every call that would reach
// this OS's store then reaches s, so a test can count them.
func useOSStore(t *testing.T, s Store) {
	t.Helper()
	saved := thisOSStores
	thisOSStores = func(string) []Store { return []Store{s} }
	t.Cleanup(func() { thisOSStores = saved })
}

// checkCleared checks that the keystore's directory in the data directory
// dir holds nothing but home's lock file, as Destroy leaves it.
func checkCleared(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(keysDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != lockName {
		t.Errorf("the keystore's directory holds %v, want only %s", names, lockName)
	}
}

// markerIn reads keystore.json in the data directory dir.
func markerIn(t *testing.T, dir string) marker {
	t.Helper()
	m, err := readMarker(keysDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// fixedKey returns a source of new device keys that always gives k, for
// tests that need to know K_dev.
func fixedKey(k []byte) func([]byte) {
	return func(b []byte) { copy(b, k) }
}

func mustOpen(t *testing.T, opts Options) *Keystore {
	t.Helper()
	ks, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

// sameKeys compares the four keys of a and b in constant time.
func sameKeys(a, b *ledger.Keys) bool {
	eq := func(x, y [ledger.KeySize]byte) bool { return subtle.ConstantTimeCompare(x[:], y[:]) == 1 }
	return eq(a.LeafKey(), b.LeafKey()) && eq(a.TokenKey(), b.TokenKey()) &&
		eq(a.FeatKey(), b.FeatKey()) && eq(a.VaultKey(), b.VaultKey())
}

// keysDir is the keystore's directory under the data directory dir.
func keysDir(dir string) string { return filepath.Join(dir, dirName) }

// readFile returns a file's bytes, or nil when it doesn't exist.
func readFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return b
}

// keyFiles lists the key files (*.key) in the keystore's directory.
func keyFiles(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(keysDir(dir), "*.key"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}
