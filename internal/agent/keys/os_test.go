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

// osCase is what each case of TestThisOSStore gets: a keystore that a first
// run with this OS's stores just made, and its K_dev.
type osCase struct {
	opts Options
	ks   *Keystore
	kDev []byte
	want Kind // the store the first run should have picked, or "" for either
}

// TestThisOSStore is the round trip through the store the first run picks
// on this OS: DPAPI on windows-latest and the login Keychain on
// macos-latest (platform.yml); on Linux, the Secret Service that fakesecret
// provides in the desktop build (platform.yml), and in ci.yml the Secret
// Service when the runner's session bus has one, else the key file.
func TestThisOSStore(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, c osCase)
	}{
		{"the first run picks this OS's store, and a second Open reads the same key", func(t *testing.T, c osCase) {
			st := c.ks.Status()
			if c.want != "" && st.Store != c.want {
				t.Errorf("the key is in %s, want %s (reason: %s)", st.Store, c.want, st.Reason)
			}
			if st.Fallback && st.Warning() == "" {
				t.Error("a key in the key file has no warning")
			}
			again, err := Open(c.opts)
			if err != nil {
				t.Fatal(err)
			}
			if again.Created() || !sameKeys(again.Keys(), c.ks.Keys()) {
				t.Error("the second Open didn't read back the first run's key")
			}
		}},
		{"no file in the data directory holds K_dev, except the key file when it is the store", func(t *testing.T, c osCase) {
			st := c.ks.Status()
			hexKey := []byte(hex.EncodeToString(c.kDev))
			err := filepath.WalkDir(c.opts.Dir, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() || (st.Fallback && path == st.File) {
					return err
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if bytes.Contains(data, c.kDev) || bytes.Contains(data, hexKey) {
					t.Errorf("%s holds K_dev", path)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"an item that isn't in the form the store writes is ErrDamaged, not ErrUnavailable", func(t *testing.T, c osCase) {
			st := c.ks.Status()
			m, err := readMarker(keysDir(c.opts.Dir))
			if err != nil {
				t.Fatal(err)
			}
			if !overwriteRaw(t, st.Store, m.deviceItem(), "QQQQ") {
				t.Skipf("%s keeps raw bytes, so it has no other form", st.Store.Description())
			}
			_, err = Open(c.opts)
			if !errors.Is(err, ErrDamaged) || errors.Is(err, ErrUnavailable) {
				t.Fatalf("got %v, want ErrDamaged alone", err)
			}
		}},
		{"Destroy removes the key from this OS's store", func(t *testing.T, c osCase) {
			st := c.ks.Status()
			m, err := readMarker(keysDir(c.opts.Dir))
			if err != nil {
				t.Fatal(err)
			}
			if err := Destroy(c.opts); err != nil {
				t.Fatal(err)
			}
			o, err := c.opts.resolve()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := o.storeOf(st.Store).Get(m.deviceItem()); !errors.Is(err, ErrNotFound) {
				t.Errorf("after Destroy, %s gives %v, want ErrNotFound", st.Store.Description(), err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := osCase{opts: Options{Dir: t.TempDir(), Timeout: osTimeout}, want: osStoreForTest(t)}
			t.Cleanup(func() { Destroy(c.opts) })
			var err error
			if c.ks, err = Open(c.opts); err != nil {
				t.Fatal(err)
			}
			if err := c.ks.WithDeviceKey(func(k []byte) error { c.kDev = bytes.Clone(k); return nil }); err != nil {
				t.Fatal(err)
			}
			tt.run(t, c)
		})
	}
}
