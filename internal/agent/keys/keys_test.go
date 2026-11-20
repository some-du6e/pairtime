// SPDX-License-Identifier: AGPL-3.0-or-later

package keys

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/ledger"
)

// These tests use fake stores and the key file, so they run the same on
// every OS. os_test.go and keys_desktop_test.go use the real stores.

func TestOpen(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, dir string)
	}{
		{"the first run keeps K_dev in the OS store, with no warning", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{osStore}})
			if !ks.Created() {
				t.Error("the first Open didn't report that it made the key")
			}
			st := ks.Status()
			if st.Store != KindKeychain || st.Fallback || st.Warning() != "" {
				t.Errorf("status %+v, want the Keychain with no warning", st)
			}
			if osStore.size() != 1 {
				t.Errorf("the OS store has %d items, want 1", osStore.size())
			}
			if files := keyFiles(t, dir); len(files) != 0 {
				t.Errorf("key files exist beside an OS store that took the key: %v", files)
			}
		}},
		{"a second Open reads the same key back and makes none", func(t *testing.T, dir string) {
			osStore := newFake(KindSecretService)
			opts := Options{Dir: dir, Stores: []Store{osStore}}
			first := mustOpen(t, opts)
			second := mustOpen(t, opts)
			if second.Created() {
				t.Error("the second Open reported that it made a key")
			}
			if !sameKeys(first.Keys(), second.Keys()) {
				t.Error("the second Open derived other keys")
			}
			if n := osStore.count("set"); n != 1 {
				t.Errorf("the store was written %d times, want once", n)
			}
		}},
		{"an OS store that refuses the key at the first run sends it to the key file, with the reason", func(t *testing.T, dir string) {
			osStore := newFake(KindSecretService)
			osStore.failSet = errors.New("no keyring daemon")
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{osStore}})
			st := ks.Status()
			if st.Store != KindFile || !st.Fallback {
				t.Fatalf("status %+v, want the key file", st)
			}
			if !strings.Contains(st.Reason, "the Secret Service isn't available (no keyring daemon)") {
				t.Errorf("reason %q doesn't say why the Secret Service wasn't used", st.Reason)
			}
			w := st.Warning()
			for _, want := range []string{
				st.File, st.Reason,
				"Other non-admin accounts", "administrators can", "any program you run can",
				"secret store would also keep the key encrypted on disk",
			} {
				if !strings.Contains(w, want) {
					t.Errorf("warning %q doesn't contain %q", w, want)
				}
			}
			if b := readFile(t, st.File); len(b) != keySize {
				t.Errorf("the key file holds %d bytes, want %d", len(b), keySize)
			}
		}},
		{"a store that gives back other bytes than it took isn't used, and keeps nothing", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			osStore.alter = true
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{osStore}})
			st := ks.Status()
			if st.Store != KindFile || !strings.Contains(st.Reason, "other bytes") {
				t.Errorf("status %+v, want the key file because the Keychain gave back other bytes", st)
			}
			if osStore.size() != 0 {
				t.Error("the lying store still holds the key")
			}
		}},
		{"a store that doesn't answer in time at the first run isn't used", func(t *testing.T, dir string) {
			osStore := newFake(KindSecretService)
			osStore.hang = make(chan struct{})
			defer close(osStore.hang)
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{osStore}, Timeout: 50 * time.Millisecond})
			st := ks.Status()
			if st.Store != KindFile || !strings.Contains(st.Reason, "didn't answer within 50ms") {
				t.Errorf("status %+v, want the key file because the Secret Service didn't answer", st)
			}
		}},
		{"the key file isn't held to the store deadline, so a slow disk can't fail a first run", func(t *testing.T, dir string) {
			// A nanosecond is less than any write takes, so a key file
			// under the deadline would fail here.
			opts := Options{Dir: dir, Stores: []Store{}, Timeout: time.Nanosecond}
			first := mustOpen(t, opts)
			if st := first.Status(); st.Store != KindFile {
				t.Fatalf("status %+v, want the key file", st)
			}
			again := mustOpen(t, opts)
			if again.Created() || !sameKeys(again.Keys(), first.Keys()) {
				t.Error("the second Open didn't read the key file back")
			}
		}},
		{"with no OS store the key goes in the key file, and the warning says there was none", func(t *testing.T, dir string) {
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{}})
			st := ks.Status()
			if st.Store != KindFile || st.Reason != "" {
				t.Errorf("status %+v, want the key file with no reason", st)
			}
			if !strings.Contains(st.Warning(), "no secret store pairtime can use") {
				t.Errorf("warning %q doesn't say there was no store", st.Warning())
			}
		}},
		{"when the key's store can't be reached, Open is ErrUnavailable and makes no key", func(t *testing.T, dir string) {
			osStore := newFake(KindSecretService)
			opts := Options{Dir: dir, Stores: []Store{osStore}}
			mustOpen(t, opts)
			before := readFile(t, filepath.Join(keysDir(dir), markerName))
			osStore.update(func(f *fakeStore) { f.failGet = errors.New("the bus is down") })
			_, err := Open(opts)
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("got %v, want ErrUnavailable", err)
			}
			checkNoNewKey(t, dir, osStore, before)
		}},
		{"a store that hangs when the key is read is ErrUnavailable after the timeout", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			opts := Options{Dir: dir, Stores: []Store{osStore}, Timeout: 50 * time.Millisecond}
			mustOpen(t, opts)
			before := readFile(t, filepath.Join(keysDir(dir), markerName))
			hang := make(chan struct{})
			defer close(hang)
			osStore.update(func(f *fakeStore) { f.hang = hang })
			start := time.Now()
			_, err := Open(opts)
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("got %v, want ErrUnavailable", err)
			}
			if d := time.Since(start); d > 10*time.Second {
				t.Errorf("Open took %v with a 50 ms timeout", d)
			}
			osStore.update(func(f *fakeStore) { f.hang = nil })
			checkNoNewKey(t, dir, osStore, before)
		}},
		{"when the key is gone from its store, Open is ErrKeyLost and makes no key", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			opts := Options{Dir: dir, Stores: []Store{osStore}}
			mustOpen(t, opts)
			before := readFile(t, filepath.Join(keysDir(dir), markerName))
			osStore.update(func(f *fakeStore) { clear(f.items) })
			_, err := Open(opts)
			if !errors.Is(err, ErrKeyLost) {
				t.Fatalf("got %v, want ErrKeyLost", err)
			}
			checkNoNewKey(t, dir, osStore, before)
		}},
		{"a store that holds the key but can't decrypt it is ErrKeyLost", func(t *testing.T, dir string) {
			osStore := newFake(KindDPAPI)
			opts := Options{Dir: dir, Stores: []Store{osStore}}
			mustOpen(t, opts)
			osStore.update(func(f *fakeStore) {
				f.failGet = fmt.Errorf("%w: CryptUnprotectData: the data is invalid", ErrUnreadable)
			})
			_, err := Open(opts)
			if !errors.Is(err, ErrKeyLost) {
				t.Fatalf("got %v, want ErrKeyLost", err)
			}
		}},
		{"a deleted key file is ErrKeyLost, and no key file comes back", func(t *testing.T, dir string) {
			opts := Options{Dir: dir, Stores: []Store{}}
			ks := mustOpen(t, opts)
			if err := os.Remove(ks.Status().File); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(opts); !errors.Is(err, ErrKeyLost) {
				t.Fatalf("got %v, want ErrKeyLost", err)
			}
			if files := keyFiles(t, dir); len(files) != 0 {
				t.Errorf("Open made a new key file: %v", files)
			}
		}},
		{"a key of the wrong length is ErrDamaged", func(t *testing.T, dir string) {
			opts := Options{Dir: dir, Stores: []Store{}}
			ks := mustOpen(t, opts)
			if err := os.WriteFile(ks.Status().File, make([]byte, keySize-1), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(opts); !errors.Is(err, ErrDamaged) {
				t.Fatalf("got %v, want ErrDamaged", err)
			}
		}},
		{"a key that isn't the one keystore.json names is ErrDamaged", func(t *testing.T, dir string) {
			opts := Options{Dir: dir, Stores: []Store{}}
			ks := mustOpen(t, opts)
			other := make([]byte, keySize)
			rand.Read(other)
			if err := os.WriteFile(ks.Status().File, other, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(opts); !errors.Is(err, ErrDamaged) {
				t.Fatalf("got %v, want ErrDamaged", err)
			}
		}},
		{"an item its store says isn't in the form it writes is ErrDamaged, not ErrUnavailable", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			opts := Options{Dir: dir, Stores: []Store{osStore}}
			mustOpen(t, opts)
			before := readFile(t, filepath.Join(keysDir(dir), markerName))
			osStore.update(func(f *fakeStore) {
				f.failGet = platformErr(errBadItem, errNoItem, errBadItem)
			})
			_, err := Open(opts)
			if !errors.Is(err, ErrDamaged) || errors.Is(err, ErrUnavailable) {
				t.Fatalf("got %v, want ErrDamaged alone", err)
			}
			checkNoNewKey(t, dir, osStore, before)
		}},
		{"an unreadable keystore.json is ErrDamaged, and no key is made", func(t *testing.T, dir string) {
			writeRaw(t, dir, "{")
			osStore := newFake(KindKeychain)
			if _, err := Open(Options{Dir: dir, Stores: []Store{osStore}}); !errors.Is(err, ErrDamaged) {
				t.Fatalf("got %v, want ErrDamaged", err)
			}
			if osStore.count("set") != 0 || len(keyFiles(t, dir)) != 0 {
				t.Error("Open made a key over a damaged keystore.json")
			}
		}},
		{"a keystore.json from a newer pairtime is ErrUnavailable, not damage", func(t *testing.T, dir string) {
			writeRaw(t, dir, `{"v":2,"install":"00000000000000000000000000000000"}`)
			_, err := Open(Options{Dir: dir, Stores: []Store{}})
			if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrDamaged) {
				t.Fatalf("got %v, want ErrUnavailable alone", err)
			}
		}},
		{"keystore.json naming a store this system lacks is ErrUnavailable", func(t *testing.T, dir string) {
			mustOpen(t, Options{Dir: dir, Stores: []Store{newFake(KindDPAPI)}})
			_, err := Open(Options{Dir: dir, Stores: []Store{newFake(KindKeychain)}})
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("got %v, want ErrUnavailable", err)
			}
		}},
		{"a first run cut off after keystore.json was written finishes under the same installation id", func(t *testing.T, dir string) {
			const install = "0123456789abcdef0123456789abcdef"
			writeRaw(t, dir, `{"v":1,"install":"`+install+`"}`)
			osStore := newFake(KindKeychain)
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{osStore}})
			if !ks.Created() || ks.Status().Store != KindKeychain {
				t.Fatalf("status %+v, want a new key in the Keychain", ks.Status())
			}
			m, err := readMarker(keysDir(dir))
			if err != nil {
				t.Fatal(err)
			}
			if m.Install != install {
				t.Errorf("installation id %q, want the cut-off run's %q", m.Install, install)
			}
			if !osStore.has(install + ".kdev") {
				t.Error("the key isn't under the cut-off run's item name")
			}
		}},
		{"a first run that finishes in the OS store deletes a key file a cut-off run left", func(t *testing.T, dir string) {
			const install = "fedcba9876543210fedcba9876543210"
			writeRaw(t, dir, `{"v":1,"install":"`+install+`"}`)
			stale := filepath.Join(keysDir(dir), install+".kdev.key")
			if err := os.WriteFile(stale, make([]byte, keySize), 0o600); err != nil {
				t.Fatal(err)
			}
			mustOpen(t, Options{Dir: dir, Stores: []Store{newFake(KindSecretService)}})
			if _, err := os.Stat(stale); !os.IsNotExist(err) {
				t.Errorf("the stale key file is still there (%v)", err)
			}
		}},
		{"two installations on one account keep their keys under different names", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			a := mustOpen(t, Options{Dir: filepath.Join(dir, "a"), Stores: []Store{osStore}})
			b := mustOpen(t, Options{Dir: filepath.Join(dir, "b"), Stores: []Store{osStore}})
			if osStore.size() != 2 {
				t.Errorf("the shared store has %d items, want 2", osStore.size())
			}
			if sameKeys(a.Keys(), b.Keys()) {
				t.Error("two installations got the same keys")
			}
		}},
		{"many Opens at once on a new installation make one key", func(t *testing.T, dir string) {
			osStore := newFake(KindSecretService)
			opts := Options{Dir: dir, Stores: []Store{osStore}}
			const n = 8
			ids := make([]string, n)
			errs := make([]error, n)
			var wg sync.WaitGroup
			for i := range n {
				wg.Add(1)
				go func() {
					defer wg.Done()
					ks, err := Open(opts)
					if err == nil {
						ids[i] = ks.Status().KeyID
					}
					errs[i] = err
				}()
			}
			wg.Wait()
			for i := range n {
				if errs[i] != nil {
					t.Fatalf("Open %d: %v", i, errs[i])
				}
				if ids[i] != ids[0] {
					t.Errorf("Open %d got key %s, Open 0 got %s", i, ids[i], ids[0])
				}
			}
			if c := osStore.count("set"); c != 1 {
				t.Errorf("the store was written %d times, want once", c)
			}
		}},
		{"the data directory must be an absolute path", func(t *testing.T, _ string) {
			if _, err := Open(Options{Dir: "relative/dir"}); err == nil {
				t.Error("Open accepted a relative data directory")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.run(t, t.TempDir()) })
	}
}

// errNoItem and errBadItem stand for a keyring store's own errors, which
// platformErr turns into ErrNotFound and ErrDamaged.
var (
	errNoItem  = errors.New("keyring: no such item")
	errBadItem = errors.New("keyring: the item isn't in the form pairtime writes")
)

func TestPlatformErr(t *testing.T) {
	other := errors.New("keyring: the bus is down")
	tests := []struct {
		name string
		err  error
		want error // nil: neither ErrNotFound nor ErrDamaged
	}{
		{"a keyring's not-found error is ErrNotFound", fmt.Errorf("get: %w", errNoItem), ErrNotFound},
		{"a keyring's error for an item in another form is ErrDamaged", fmt.Errorf("get: %w", errBadItem), ErrDamaged},
		{"any other error passes as it is", other, nil},
		{"no error stays no error", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := platformErr(tt.err, errNoItem, errBadItem)
			if tt.err == nil {
				if got != nil {
					t.Fatalf("got %v, want nil", got)
				}
				return
			}
			if tt.want != nil && !errors.Is(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
			if tt.want == nil && (errors.Is(got, ErrNotFound) || errors.Is(got, ErrDamaged) || got != tt.err) {
				t.Errorf("got %v, want %v unchanged", got, tt.err)
			}
		})
	}
}

// TestFirstRunRecordsStoresThatMayHoldTheKey checks which stores a first
// run that went on to the key file records in keystore.json's unconfirmed
// list, where Destroy looks for K_dev besides the store that holds it.
func TestFirstRunRecordsStoresThatMayHoldTheKey(t *testing.T) {
	tests := []struct {
		name     string
		set      func(f *fakeStore)
		recorded bool
		items    int // how many items the store has after the first run, or -1 for any
	}{
		{"a store that refuses the key isn't recorded, as it never had it", func(f *fakeStore) {
			f.failSet = errors.New("no keyring daemon")
		}, false, 0},
		{"a save that doesn't answer in time is recorded, as it may yet finish", func(f *fakeStore) {
			f.hang = make(chan struct{})
		}, true, -1},
		{"a store that saves the key but can't give it back is told to delete it, and recorded, as it can't show that it did", func(f *fakeStore) {
			f.failGet = errors.New("read refused")
		}, true, 0},
		{"a store that gives back other bytes is told to delete them, and isn't recorded once it shows that it did", func(f *fakeStore) {
			f.alter = true
		}, false, 0},
		{"a store that gives back other bytes and won't delete them is recorded", func(f *fakeStore) {
			f.alter = true
			f.failDel = errors.New("delete refused")
		}, true, 1},
		{"a store that says it deleted the bytes but kept them is recorded", func(f *fakeStore) {
			f.alter = true
			f.keep = true
		}, true, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			osStore := newFake(KindSecretService)
			tt.set(osStore)
			if osStore.hang != nil {
				defer close(osStore.hang)
			}
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{osStore}, Timeout: 50 * time.Millisecond})
			if st := ks.Status(); st.Store != KindFile {
				t.Fatalf("status %+v, want the key file", st)
			}
			m := markerIn(t, dir)
			if got := slices.Contains(m.Unconfirmed, KindSecretService); got != tt.recorded {
				t.Errorf("keystore.json lists %v as unconfirmed, want the Secret Service there: %v", m.Unconfirmed, tt.recorded)
			}
			if tt.items >= 0 && osStore.size() != tt.items {
				t.Errorf("the store has %d items, want %d", osStore.size(), tt.items)
			}
		})
	}
}

// checkNoNewKey checks that a failed Open left keystore.json as it was,
// wrote nothing to the OS store and made no key file.
func checkNoNewKey(t *testing.T, dir string, osStore *fakeStore, before []byte) {
	t.Helper()
	if after := readFile(t, filepath.Join(keysDir(dir), markerName)); !bytes.Equal(after, before) {
		t.Error("keystore.json changed")
	}
	if n := osStore.count("set"); n != 1 {
		t.Errorf("the store was written %d times, want only the first run's", n)
	}
	if files := keyFiles(t, dir); len(files) != 0 {
		t.Errorf("Open made a key file: %v", files)
	}
}

// writeRaw writes keystore.json as given.
func writeRaw(t *testing.T, dir, json string) {
	t.Helper()
	if err := os.MkdirAll(keysDir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keysDir(dir), markerName), []byte(json), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInspect(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, dir string)
	}{
		{"before the first run there is no keystore", func(t *testing.T, dir string) {
			if _, err := Inspect(dir); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("got %v, want fs.ErrNotExist", err)
			}
		}},
		{"a cut-off first run counts as no keystore yet", func(t *testing.T, dir string) {
			writeRaw(t, dir, `{"v":1,"install":"0123456789abcdef0123456789abcdef"}`)
			if _, err := Inspect(dir); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("got %v, want fs.ErrNotExist", err)
			}
		}},
		{"Inspect says what Status says from keystore.json alone, and asks this OS's store nothing", func(t *testing.T, dir string) {
			// The fake is this OS's store here, so any call Inspect made
			// to it would be counted. It loses the key first, so an
			// Inspect that read it would fail too.
			osStore := newFake(KindKeychain)
			useOSStore(t, osStore)
			ks := mustOpen(t, Options{Dir: dir})
			if st := ks.Status(); st.Store != KindKeychain {
				t.Fatalf("status %+v, want the fake as this OS's store", st)
			}
			osStore.update(func(f *fakeStore) { clear(f.items); clear(f.calls) })
			st, err := Inspect(dir)
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(st) != fmt.Sprint(ks.Status()) {
				t.Errorf("Inspect gave %+v, Status gave %+v", st, ks.Status())
			}
			for _, method := range []string{"get", "set", "delete"} {
				if n := osStore.count(method); n != 0 {
					t.Errorf("Inspect called the store's %s %d times", method, n)
				}
			}
		}},
		{"Inspect repeats the fallback warning without reading the key file", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			osStore.failSet = errors.New("keychain locked")
			useOSStore(t, osStore)
			ks := mustOpen(t, Options{Dir: dir})
			want := ks.Status()
			if !want.Fallback {
				t.Fatalf("status %+v, want the key file", want)
			}
			// With the key file gone, an Inspect that read it would fail.
			if err := os.Remove(want.File); err != nil {
				t.Fatal(err)
			}
			osStore.update(func(f *fakeStore) { clear(f.calls) })
			st, err := Inspect(dir)
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(st) != fmt.Sprint(want) || st.Warning() == "" || st.Warning() != want.Warning() {
				t.Errorf("Inspect gave %+v with warning %q, want %+v with warning %q", st, st.Warning(), want, want.Warning())
			}
			if n := osStore.count("get"); n != 0 {
				t.Errorf("Inspect asked the store for the key %d times", n)
			}
		}},
		{"a damaged keystore.json is ErrDamaged", func(t *testing.T, dir string) {
			writeRaw(t, dir, `{"v":1,"install":"not hex"}`)
			if _, err := Inspect(dir); !errors.Is(err, ErrDamaged) {
				t.Errorf("got %v, want ErrDamaged", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.run(t, t.TempDir()) })
	}
}

func TestDestroy(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, dir string)
	}{
		{"Destroy deletes the device key and archived keys from their store, and the keystore's directory", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			opts := Options{Dir: dir, Stores: []Store{osStore}}
			ks := mustOpen(t, opts)
			old := make([]byte, keySize)
			rand.Read(old)
			if _, err := ks.Archive(old); err != nil {
				t.Fatal(err)
			}
			if err := Destroy(opts); err != nil {
				t.Fatal(err)
			}
			if osStore.size() != 0 {
				t.Errorf("the store still has %d items", osStore.size())
			}
			checkCleared(t, dir)
			if _, err := Inspect(dir); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Inspect after Destroy: %v", err)
			}
		}},
		{"Destroy deletes the key file", func(t *testing.T, dir string) {
			opts := Options{Dir: dir, Stores: []Store{}}
			ks := mustOpen(t, opts)
			file := ks.Status().File
			if err := Destroy(opts); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(file); !os.IsNotExist(err) {
				t.Errorf("the key file is still there (%v)", err)
			}
		}},
		{"after Destroy, Open makes a new key", func(t *testing.T, dir string) {
			opts := Options{Dir: dir, Stores: []Store{newFake(KindSecretService)}}
			before := mustOpen(t, opts).Status().KeyID
			if err := Destroy(opts); err != nil {
				t.Fatal(err)
			}
			ks := mustOpen(t, opts)
			if !ks.Created() || ks.Status().KeyID == before {
				t.Error("Open after Destroy didn't make a new key")
			}
		}},
		{"when the key's store can't delete it, Destroy fails and keeps keystore.json for a retry", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			opts := Options{Dir: dir, Stores: []Store{osStore}}
			mustOpen(t, opts)
			osStore.update(func(f *fakeStore) { f.failDel = errors.New("keychain locked") })
			if err := Destroy(opts); err == nil {
				t.Fatal("Destroy succeeded while the key stayed in its store")
			}
			if _, err := Inspect(dir); err != nil {
				t.Errorf("keystore.json didn't survive the failed Destroy: %v", err)
			}
			osStore.update(func(f *fakeStore) { f.failDel = nil })
			if err := Destroy(opts); err != nil {
				t.Fatal(err)
			}
			if osStore.size() != 0 {
				t.Error("the retry didn't delete the key")
			}
		}},
		{"a delete that returns nil but leaves the key doesn't count, so Destroy fails and keeps keystore.json for a retry", func(t *testing.T, dir string) {
			osStore := newFake(KindSecretService)
			opts := Options{Dir: dir, Stores: []Store{osStore}}
			mustOpen(t, opts)
			osStore.update(func(f *fakeStore) { f.keep = true })
			if err := Destroy(opts); err == nil {
				t.Fatal("Destroy succeeded while the key stayed in its store")
			}
			if _, err := Inspect(dir); err != nil {
				t.Errorf("keystore.json didn't survive the failed Destroy: %v", err)
			}
			osStore.update(func(f *fakeStore) { f.keep = false })
			if err := Destroy(opts); err != nil {
				t.Fatal(err)
			}
			if osStore.size() != 0 {
				t.Error("the retry didn't delete the key")
			}
		}},
		{"Destroy deletes the key from a store whose first-run save timed out and finished late", func(t *testing.T, dir string) {
			osStore := newFake(KindSecretService)
			osStore.hang = make(chan struct{})
			opts := Options{Dir: dir, Stores: []Store{osStore}, Timeout: 50 * time.Millisecond}
			ks := mustOpen(t, opts)
			if st := ks.Status(); st.Store != KindFile {
				t.Fatalf("status %+v, want the key file", st)
			}
			item := markerIn(t, dir).deviceItem()
			close(osStore.hang)
			osStore.waitFor(t, item)
			if err := Destroy(opts); err != nil {
				t.Fatal(err)
			}
			if osStore.has(item) {
				t.Error("the late copy of the key is still in the store")
			}
		}},
		{"a store that may hold the key and can't delete it fails Destroy, which keeps keystore.json for a retry", func(t *testing.T, dir string) {
			osStore := newFake(KindSecretService)
			osStore.hang = make(chan struct{})
			opts := Options{Dir: dir, Stores: []Store{osStore}, Timeout: 50 * time.Millisecond}
			mustOpen(t, opts)
			close(osStore.hang)
			osStore.waitFor(t, markerIn(t, dir).deviceItem())
			osStore.update(func(f *fakeStore) { f.failDel = errors.New("keyring locked") })
			if err := Destroy(opts); err == nil {
				t.Fatal("Destroy succeeded while a store that may hold the key couldn't delete it")
			}
			if _, err := Inspect(dir); err != nil {
				t.Errorf("keystore.json didn't survive the failed Destroy: %v", err)
			}
			osStore.update(func(f *fakeStore) { f.failDel = nil })
			if err := Destroy(opts); err != nil {
				t.Fatal(err)
			}
			if osStore.size() != 0 {
				t.Error("the retry didn't delete the key")
			}
		}},
		{"Destroy over a first run cut off after a store timed out deletes the key from that store", func(t *testing.T, dir string) {
			const install = "00112233445566778899aabbccddeeff"
			writeRaw(t, dir, `{"v":1,"install":"`+install+`","unconfirmed":["secret-service"]}`)
			osStore := newFake(KindSecretService)
			if err := osStore.Set(install+".kdev", make([]byte, keySize)); err != nil {
				t.Fatal(err)
			}
			osStore.update(func(f *fakeStore) { f.failDel = errors.New("keyring locked") })
			opts := Options{Dir: dir, Stores: []Store{osStore}}
			if err := Destroy(opts); err == nil {
				t.Fatal("Destroy succeeded while the store it recorded couldn't delete the key")
			}
			osStore.update(func(f *fakeStore) { f.failDel = nil })
			if err := Destroy(opts); err != nil {
				t.Fatal(err)
			}
			if osStore.size() != 0 {
				t.Error("the key is still in the store")
			}
		}},
		{"Destroy names a keyring keystore.json names that this system doesn't have, and still deletes the keystore's files", func(t *testing.T, dir string) {
			mustOpen(t, Options{Dir: dir, Stores: []Store{newFake(KindKeychain)}})
			item := markerIn(t, dir).deviceItem()
			err := Destroy(Options{Dir: dir, Stores: []Store{}})
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("got %v, want ErrUnavailable", err)
			}
			for _, want := range []string{KindKeychain.Description(), item} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q doesn't name %q", err, want)
				}
			}
			checkCleared(t, dir)
		}},
		{"a DPAPI blob keystore.json names goes with the directory on a system without DPAPI", func(t *testing.T, dir string) {
			mustOpen(t, Options{Dir: dir, Stores: []Store{newFake(KindDPAPI)}})
			if err := Destroy(Options{Dir: dir, Stores: []Store{}}); err != nil {
				t.Fatal(err)
			}
			checkCleared(t, dir)
		}},
		{"a store that was never given the key doesn't stop Destroy", func(t *testing.T, dir string) {
			refused := newFake(KindSecretService)
			refused.failSet = errors.New("no keyring daemon")
			refused.failDel = errors.New("no keyring daemon")
			opts := Options{Dir: dir, Stores: []Store{refused}}
			mustOpen(t, opts)
			if err := Destroy(opts); err != nil {
				t.Fatal(err)
			}
		}},
		{"Destroy with no keystore does nothing", func(t *testing.T, dir string) {
			if err := Destroy(Options{Dir: dir, Stores: []Store{}}); err != nil {
				t.Fatal(err)
			}
		}},
		{"Destroy over a damaged keystore.json deletes the files and says items may remain", func(t *testing.T, dir string) {
			writeRaw(t, dir, "not json")
			if err := Destroy(Options{Dir: dir, Stores: []Store{}}); !errors.Is(err, ErrDamaged) {
				t.Errorf("got %v, want ErrDamaged", err)
			}
			checkCleared(t, dir)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.run(t, t.TempDir()) })
	}
}

func TestArchive(t *testing.T) {
	old := make([]byte, keySize)
	rand.Read(old)
	tests := []struct {
		name string
		run  func(t *testing.T, dir string)
	}{
		{"an archived key gives back its own keys, and the device's keys don't change", func(t *testing.T, dir string) {
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{newFake(KindKeychain)}})
			mine := ks.Keys()
			id, err := ks.Archive(old)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ks.ArchivedKeys(id)
			if err != nil {
				t.Fatal(err)
			}
			want, err := ledger.DeriveKeys(old)
			if err != nil {
				t.Fatal(err)
			}
			if !sameKeys(got, want) {
				t.Error("ArchivedKeys derived other keys than the archived K_dev gives")
			}
			if ks.Keys() != mine || sameKeys(got, mine) {
				t.Error("archiving changed the keys the installation writes with")
			}
			if st := ks.Status(); len(st.Archived) != 1 || st.Archived[0] != id {
				t.Errorf("status lists archived keys %v, want [%s]", st.Archived, id)
			}
		}},
		{"an archived key goes in the store the device key is in", func(t *testing.T, dir string) {
			osStore := newFake(KindSecretService)
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{osStore}})
			if _, err := ks.Archive(old); err != nil {
				t.Fatal(err)
			}
			if osStore.size() != 2 {
				t.Errorf("the store has %d items, want the device key and the archived one", osStore.size())
			}
		}},
		{"archiving a key twice gives one id and stores it once", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{osStore}})
			a, err := ks.Archive(old)
			if err != nil {
				t.Fatal(err)
			}
			b, err := ks.Archive(old)
			if err != nil {
				t.Fatal(err)
			}
			if a != b {
				t.Errorf("ids %s and %s for one key", a, b)
			}
			if n := osStore.count("set"); n != 2 {
				t.Errorf("the store was written %d times, want twice: the device key and the archived one", n)
			}
		}},
		{"keystore.json lists an archived key before its store gets it", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{osStore}})
			id, err := keyID(old)
			if err != nil {
				t.Fatal(err)
			}
			listed := false
			osStore.update(func(f *fakeStore) {
				f.onSet = func(string) {
					m, err := readMarker(keysDir(dir))
					_, listed = m.archived(id)
					if err != nil {
						t.Error(err)
					}
				}
			})
			if _, err := ks.Archive(old); err != nil {
				t.Fatal(err)
			}
			if !listed {
				t.Error("the store got the archived key before keystore.json listed it")
			}
		}},
		{"an Archive whose store refused the key leaves keystore.json without it", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{osStore}})
			osStore.update(func(f *fakeStore) { f.failSet = errors.New("keychain locked") })
			if _, err := ks.Archive(old); err == nil {
				t.Fatal("Archive succeeded with a store that refused the key")
			}
			if m := markerIn(t, dir); len(m.Archived) != 0 {
				t.Errorf("keystore.json lists %v", m.Archived)
			}
			if st := ks.Status(); len(st.Archived) != 0 {
				t.Errorf("status lists archived keys %v", st.Archived)
			}
		}},
		{"an Archive whose save timed out stays listed, so Destroy deletes the key once the save lands", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			opts := Options{Dir: dir, Stores: []Store{osStore}, Timeout: 50 * time.Millisecond}
			ks := mustOpen(t, opts)
			hang := make(chan struct{})
			osStore.update(func(f *fakeStore) { f.hang = hang })
			if _, err := ks.Archive(old); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("got %v, want ErrUnavailable", err)
			}
			m := markerIn(t, dir)
			if len(m.Archived) != 1 {
				t.Fatalf("keystore.json lists %v, want the archived key", m.Archived)
			}
			close(hang)
			osStore.waitFor(t, m.archivedItem(m.Archived[0].ID))
			osStore.update(func(f *fakeStore) { f.hang = nil })
			if err := Destroy(opts); err != nil {
				t.Fatal(err)
			}
			if osStore.size() != 0 {
				t.Errorf("the store still has %d items", osStore.size())
			}
		}},
		{"archiving a key whose item is gone from its store saves it again", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{osStore}})
			id, err := ks.Archive(old)
			if err != nil {
				t.Fatal(err)
			}
			if err := osStore.Delete(markerIn(t, dir).archivedItem(id)); err != nil {
				t.Fatal(err)
			}
			if again, err := ks.Archive(old); err != nil || again != id {
				t.Fatalf("got (%s, %v), want (%s, nil)", again, err, id)
			}
			if _, err := ks.ArchivedKeys(id); err != nil {
				t.Errorf("ArchivedKeys after archiving again: %v", err)
			}
		}},
		{"ArchivedKeys won't use a keystore.json of another installation", func(t *testing.T, dir string) {
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{}})
			id, err := ks.Archive(old)
			if err != nil {
				t.Fatal(err)
			}
			want := ks.Status()
			m := markerIn(t, dir)
			m.Install = "ffffffffffffffffffffffffffffffff"
			if err := writeMarker(keysDir(dir), m); err != nil {
				t.Fatal(err)
			}
			if _, err := ks.ArchivedKeys(id); !errors.Is(err, ErrDamaged) {
				t.Errorf("got %v, want ErrDamaged", err)
			}
			if st := ks.Status(); fmt.Sprint(st) != fmt.Sprint(want) {
				t.Errorf("the keystore took the other installation's keystore.json: %+v", st)
			}
		}},
		{"the installation's own key can't be archived", func(t *testing.T, dir string) {
			mine := make([]byte, keySize)
			rand.Read(mine)
			ks, err := open(Options{Dir: dir, Stores: []Store{}}, fixedKey(mine))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ks.Archive(mine); err == nil {
				t.Error("Archive took the key the installation writes with")
			}
		}},
		{"a key of the wrong length can't be archived", func(t *testing.T, dir string) {
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{}})
			if _, err := ks.Archive(old[:keySize-1]); err == nil {
				t.Error("Archive took a 31-byte key")
			}
		}},
		{"Archive leaves the bytes it was given as they were", func(t *testing.T, dir string) {
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{}})
			given := bytes.Clone(old)
			if _, err := ks.Archive(given); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(given, old) {
				t.Error("Archive changed its argument")
			}
		}},
		{"an id that was never archived is ErrNotFound", func(t *testing.T, dir string) {
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{}})
			if _, err := ks.ArchivedKeys("00000000000000000000000000000000"); !errors.Is(err, ErrNotFound) {
				t.Errorf("got %v, want ErrNotFound", err)
			}
		}},
		{"a key archived by another process's keystore can be read", func(t *testing.T, dir string) {
			opts := Options{Dir: dir, Stores: []Store{}}
			agent := mustOpen(t, opts)
			restore := mustOpen(t, opts)
			id, err := restore.Archive(old)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := agent.ArchivedKeys(id); err != nil {
				t.Fatal(err)
			}
			if st := agent.Status(); len(st.Archived) != 1 {
				t.Errorf("the other keystore's status lists %v", st.Archived)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.run(t, t.TempDir()) })
	}
}

func TestWithDeviceKey(t *testing.T) {
	kDev := make([]byte, keySize)
	rand.Read(kDev)
	tests := []struct {
		name string
		run  func(t *testing.T, dir string)
	}{
		{"fn gets the K_dev the keys came from, zeroed once fn returns", func(t *testing.T, dir string) {
			ks, err := open(Options{Dir: dir, Stores: []Store{newFake(KindKeychain)}}, fixedKey(kDev))
			if err != nil {
				t.Fatal(err)
			}
			var held []byte
			err = ks.WithDeviceKey(func(k []byte) error {
				if !bytes.Equal(k, kDev) {
					t.Error("fn got another key")
				}
				held = k
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(held, make([]byte, keySize)) {
				t.Error("the slice fn got wasn't zeroed")
			}
		}},
		{"fn's error comes back", func(t *testing.T, dir string) {
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{}})
			boom := errors.New("boom")
			if err := ks.WithDeviceKey(func([]byte) error { return boom }); !errors.Is(err, boom) {
				t.Errorf("got %v, want fn's error", err)
			}
		}},
		{"a store that can't give the key back is ErrUnavailable, and fn isn't called", func(t *testing.T, dir string) {
			osStore := newFake(KindSecretService)
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{osStore}})
			osStore.update(func(f *fakeStore) { f.failGet = errors.New("locked") })
			called := false
			err := ks.WithDeviceKey(func([]byte) error { called = true; return nil })
			if !errors.Is(err, ErrUnavailable) || called {
				t.Errorf("got %v with fn called %v, want ErrUnavailable and no call", err, called)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.run(t, t.TempDir()) })
	}
}

// TestRecordChain checks that keystore.json keeps the device id of the one
// ledger chain K_dev writes (ledger-v1 3), so the ledger writer can refuse
// to start a second chain under the key when the ledger directory is gone.
func TestRecordChain(t *testing.T) {
	file := func(dir string) Options { return Options{Dir: dir, Stores: []Store{}} }
	tests := []struct {
		name string
		run  func(t *testing.T, dir string)
	}{
		{"a new key has no chain on record", func(t *testing.T, dir string) {
			if c := mustOpen(t, file(dir)).Chain(); c != "" {
				t.Errorf("Chain() = %q, want none", c)
			}
		}},
		{"a recorded chain is in keystore.json, and the next Open reads it back", func(t *testing.T, dir string) {
			ks := mustOpen(t, file(dir))
			if err := ks.RecordChain("d_first"); err != nil {
				t.Fatal(err)
			}
			if got := markerIn(t, dir).Chain; ks.Chain() != "d_first" || got != "d_first" {
				t.Errorf("Chain() = %q and keystore.json has %q, want d_first", ks.Chain(), got)
			}
			if c := mustOpen(t, file(dir)).Chain(); c != "d_first" {
				t.Errorf("after Open again, Chain() = %q, want d_first", c)
			}
		}},
		{"recording the same chain again does nothing, and a second chain is refused", func(t *testing.T, dir string) {
			ks := mustOpen(t, file(dir))
			if err := ks.RecordChain("d_first"); err != nil {
				t.Fatal(err)
			}
			if err := ks.RecordChain("d_first"); err != nil {
				t.Errorf("recording it again: %v", err)
			}
			if err := ks.RecordChain("d_second"); err == nil || ks.Chain() != "d_first" || markerIn(t, dir).Chain != "d_first" {
				t.Errorf("recording a second chain: %v, and the chain is now %q; want an error and d_first", err, ks.Chain())
			}
		}},
		{"a chain another process recorded stops this one recording a second", func(t *testing.T, dir string) {
			one := mustOpen(t, file(dir))
			two := mustOpen(t, file(dir))
			if err := one.RecordChain("d_first"); err != nil {
				t.Fatal(err)
			}
			if err := two.RecordChain("d_second"); err == nil || markerIn(t, dir).Chain != "d_first" {
				t.Errorf("the second process recorded its chain: %v; keystore.json has %q", err, markerIn(t, dir).Chain)
			}
		}},
		{"a chain that isn't a device id isn't recorded", func(t *testing.T, dir string) {
			ks := mustOpen(t, file(dir))
			if err := ks.RecordChain("../d_first"); err == nil || markerIn(t, dir).Chain != "" {
				t.Errorf("RecordChain took a path: %v", err)
			}
		}},
		{"Archive keeps the recorded chain", func(t *testing.T, dir string) {
			ks := mustOpen(t, file(dir))
			if err := ks.RecordChain("d_first"); err != nil {
				t.Fatal(err)
			}
			restored := make([]byte, keySize)
			rand.Read(restored)
			if _, err := ks.Archive(restored); err != nil {
				t.Fatal(err)
			}
			if got := markerIn(t, dir).Chain; got != "d_first" {
				t.Errorf("after Archive, keystore.json's chain is %q, want d_first", got)
			}
		}},
		{"a keystore.json whose chain isn't a device id is damaged", func(t *testing.T, dir string) {
			mustOpen(t, file(dir))
			m := markerIn(t, dir)
			m.Chain = "not a device id"
			if err := writeMarker(keysDir(dir), m); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(file(dir)); !errors.Is(err, ErrDamaged) {
				t.Errorf("Open: %v, want ErrDamaged", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.run(t, t.TempDir()) })
	}
}
