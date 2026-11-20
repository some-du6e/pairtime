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
	alter   bool              // Get gives the secret back with a bit flipped
	keep    bool              // Delete reports success and keeps the item, as a dismissed prompt does
	hang    chan struct{}     // when set, each call waits until it is closed
	onSet   func(name string) // when set, Set calls it before it saves anything
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

// total returns how many calls of any method the fake has had.
func (f *fakeStore) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		n += c
	}
	return n
}

// resetCounts forgets the calls made so far.
func (f *fakeStore) resetCounts() {
	f.mu.Lock()
	defer f.mu.Unlock()
	clear(f.calls)
}

// waitFor waits up to 10 seconds for the fake to hold name, as when a call
// that missed its deadline finishes late.
func (f *fakeStore) waitFor(t *testing.T, name string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !f.has(name) {
		if time.Now().After(deadline) {
			t.Fatalf("the store never got %s", name)
		}
		time.Sleep(5 * time.Millisecond)
	}
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

// fixedKey returns a source of new device keys that always gives k, for
// tests that need to know K_dev.
func fixedKey(k []byte) func([]byte) {
	return func(b []byte) { copy(b, k) }
}

// useOSStores makes stores this OS's stores until the test ends, so a call
// that reaches the OS store, as through Open with no Stores, reaches a fake
// the test can watch.
func useOSStores(t *testing.T, stores ...Store) {
	t.Helper()
	saved := osStores
	osStores = func(string) []Store { return stores }
	t.Cleanup(func() { osStores = saved })
}

// markerOf reads keystore.json from the data directory dir.
func markerOf(t *testing.T, dir string) marker {
	t.Helper()
	m, err := readMarker(keysDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	return m
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
