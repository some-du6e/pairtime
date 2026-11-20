// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux && desktop

package keys

import (
	"bytes"
	"encoding/hex"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/platform/linux/fakesecret"
)

// These run in platform.yml's Linux job, inside the private session bus of
// tools/ci/desktop_session.sh, with fakesecret answering go-keyring's real
// D-Bus calls: the Linux round trip through the Secret Service (WP-OS7).

// osStoreForTest starts fakesecret for a test that uses this OS's stores,
// so in the desktop build those tests go through the Secret Service, and
// nothing on the private bus can start a real keyring. See osstore_test.go.
func osStoreForTest(t *testing.T) Kind {
	startFakeSecretService(t)
	return KindSecretService
}

func startFakeSecretService(t *testing.T) *fakesecret.Service {
	t.Helper()
	fake, err := fakesecret.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fake.Close() })
	return fake
}

// deviceItems returns the fake's items that hold this installation's
// device key.
func deviceItems(t *testing.T, fake *fakesecret.Service, dir string) []fakesecret.Item {
	t.Helper()
	m, err := readMarker(keysDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"service": keyringService, "username": m.deviceItem()}
	var out []fakesecret.Item
	for _, it := range fake.Items() {
		if maps.Equal(it.Attributes, want) {
			out = append(out, it)
		}
	}
	return out
}

func TestDesktopSecretService(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, fake *fakesecret.Service, opts Options)
	}{
		{"the first run keeps K_dev in the Secret Service as hex, and a second Open reads it back", func(t *testing.T, fake *fakesecret.Service, opts Options) {
			ks := mustOpen(t, opts)
			if st := ks.Status(); st.Store != KindSecretService || st.Fallback {
				t.Fatalf("status %+v, want the Secret Service", st)
			}
			items := deviceItems(t, fake, opts.Dir)
			if len(items) != 1 {
				t.Fatalf("the Secret Service has %d items for the device key, want 1", len(items))
			}
			err := ks.WithDeviceKey(func(kDev []byte) error {
				if !bytes.Equal(items[0].Secret, []byte(hex.EncodeToString(kDev))) {
					t.Error("the item doesn't hold K_dev's hex")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			again := mustOpen(t, opts)
			if again.Created() || !sameKeys(again.Keys(), ks.Keys()) {
				t.Error("the second Open didn't read back the first run's key")
			}
			if files := keyFiles(t, opts.Dir); len(files) != 0 {
				t.Errorf("key files exist beside the Secret Service: %v", files)
			}
		}},
		{"a Secret Service that refuses at the first run sends the key to the key file, with the reason", func(t *testing.T, fake *fakesecret.Service, opts Options) {
			fake.Refuse(true)
			defer fake.Refuse(false)
			st := mustOpen(t, opts).Status()
			if st.Store != KindFile || !strings.Contains(st.Reason, "the Secret Service isn't available") {
				t.Errorf("status %+v, want the key file because the Secret Service refused", st)
			}
			if !strings.Contains(st.Warning(), st.File) {
				t.Errorf("warning %q doesn't name the key file", st.Warning())
			}
		}},
		{"a Secret Service that refuses later is ErrUnavailable, and no second key is made", func(t *testing.T, fake *fakesecret.Service, opts Options) {
			mustOpen(t, opts)
			fake.Refuse(true)
			_, err := Open(opts)
			fake.Refuse(false)
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("got %v, want ErrUnavailable", err)
			}
			if n := len(deviceItems(t, fake, opts.Dir)); n != 1 {
				t.Errorf("the Secret Service has %d device keys, want the first run's alone", n)
			}
			if files := keyFiles(t, opts.Dir); len(files) != 0 {
				t.Errorf("Open made a key file: %v", files)
			}
			if _, err := Open(opts); err != nil {
				t.Errorf("Open once the Secret Service answers again: %v", err)
			}
		}},
		{"a Secret Service delete whose prompt is dismissed doesn't count, so Destroy fails and keeps keystore.json for a retry", func(t *testing.T, fake *fakesecret.Service, opts Options) {
			mustOpen(t, opts)
			fake.DismissDeletes(true)
			err := Destroy(opts)
			fake.DismissDeletes(false)
			if err == nil {
				t.Fatal("Destroy succeeded while the Secret Service kept the key")
			}
			if n := len(deviceItems(t, fake, opts.Dir)); n != 1 {
				t.Errorf("the Secret Service has %d device keys, want the one the dismissed delete left", n)
			}
			if err := Destroy(opts); err != nil {
				t.Fatalf("Destroy once deletes are allowed again: %v", err)
			}
			for _, it := range fake.Items() {
				if it.Attributes["service"] == keyringService {
					t.Errorf("an item is left after Destroy: %v", it.Attributes)
				}
			}
		}},
		{"Destroy deletes the device key and archived keys from the Secret Service", func(t *testing.T, fake *fakesecret.Service, opts Options) {
			ks := mustOpen(t, opts)
			if _, err := ks.Archive(bytes.Repeat([]byte{0x5a}, keySize)); err != nil {
				t.Fatal(err)
			}
			if err := Destroy(opts); err != nil {
				t.Fatal(err)
			}
			for _, it := range fake.Items() {
				if it.Attributes["service"] == keyringService {
					t.Errorf("an item is left after Destroy: %v", it.Attributes)
				}
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := startFakeSecretService(t)
			opts := Options{Dir: t.TempDir(), Timeout: osTimeout}
			t.Cleanup(func() { Destroy(opts) })
			tt.run(t, fake, opts)
		})
	}
}
