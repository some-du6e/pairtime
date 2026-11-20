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
			for _, want := range []string{st.File, st.Reason, "any program you run can"} {
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
		{"Inspect says what Status says, and asks no store for the key", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			osStore.failSet = errors.New("keychain locked")
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{osStore}})
			gets := osStore.count("get")
			st, err := Inspect(dir)
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(st) != fmt.Sprint(ks.Status()) {
				t.Errorf("Inspect gave %+v, Status gave %+v", st, ks.Status())
			}
			if st.Warning() == "" {
				t.Error("a key in the key file has no warning")
			}
			if osStore.count("get") != gets {
				t.Error("Inspect asked the store for the key")
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
			if _, err := os.Stat(keysDir(dir)); !os.IsNotExist(err) {
				t.Errorf("the keystore's directory is still there (%v)", err)
			}
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
			if _, err := os.Stat(keysDir(dir)); !os.IsNotExist(err) {
				t.Errorf("the keystore's directory is still there (%v)", err)
			}
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
