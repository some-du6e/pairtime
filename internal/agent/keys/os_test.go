// SPDX-License-Identifier: AGPL-3.0-or-later

package keys

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// osTimeout bounds each store call in tests that use this OS's real store,
// so a runner whose keyring hangs fails in seconds, not minutes.
const osTimeout = 20 * time.Second

// TestThisOSStore is the round trip through the store the first run picks
// on this OS: DPAPI on windows-latest and the login Keychain on
// macos-latest (platform.yml); on Linux, the Secret Service when the
// session bus has one, else the key file (ci.yml).
func TestThisOSStore(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, opts Options, ks *Keystore, kDev []byte)
	}{
		{"the first run picks this OS's store, and a second Open reads the same key", func(t *testing.T, opts Options, ks *Keystore, _ []byte) {
			st := ks.Status()
			if want := wantOSKind(); want != "" && st.Store != want {
				t.Errorf("the key is in %s, want %s (reason: %s)", st.Store, want, st.Reason)
			}
			if st.Fallback && st.Warning() == "" {
				t.Error("a key in the key file has no warning")
			}
			again, err := Open(opts)
			if err != nil {
				t.Fatal(err)
			}
			if again.Created() || !sameKeys(again.Keys(), ks.Keys()) {
				t.Error("the second Open didn't read back the first run's key")
			}
		}},
		{"no file in the data directory holds K_dev, except the key file when it is the store", func(t *testing.T, opts Options, ks *Keystore, kDev []byte) {
			st := ks.Status()
			hexKey := []byte(hex.EncodeToString(kDev))
			err := filepath.WalkDir(opts.Dir, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() || (st.Fallback && path == st.File) {
					return err
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if bytes.Contains(data, kDev) || bytes.Contains(data, hexKey) {
					t.Errorf("%s holds K_dev", path)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"Destroy removes the key from this OS's store", func(t *testing.T, opts Options, ks *Keystore, _ []byte) {
			st := ks.Status()
			m, err := readMarker(keysDir(opts.Dir))
			if err != nil {
				t.Fatal(err)
			}
			if err := Destroy(opts); err != nil {
				t.Fatal(err)
			}
			o, err := opts.resolve()
			if err != nil {
				t.Fatal(err)
			}
			s := o.storeOf(st.Store)
			if _, err := s.Get(m.deviceItem()); !errors.Is(err, ErrNotFound) {
				t.Errorf("after Destroy, %s gives %v, want ErrNotFound", st.Store.Description(), err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := Options{Dir: t.TempDir(), Timeout: osTimeout}
			t.Cleanup(func() { Destroy(opts) })
			ks, err := Open(opts)
			if err != nil {
				t.Fatal(err)
			}
			var kDev []byte
			if err := ks.WithDeviceKey(func(k []byte) error { kDev = bytes.Clone(k); return nil }); err != nil {
				t.Fatal(err)
			}
			tt.run(t, opts, ks, kDev)
		})
	}
}
