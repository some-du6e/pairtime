// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
	"github.com/some-du6e/pairtime/internal/devtoken"
)

// TestUploadMode is config.toml as uploads obey it, read from the file:
// the uploader's LocalOnly for each sealed batch, and its Login before
// each upload, which agree on every file.
func TestUploadMode(t *testing.T) {
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	// A linked computer: account.json, and a sign-in that reads.
	if err := os.WriteFile(filepath.Join(dir, "account.json"), []byte(`{"v":1,"server":"https://pairtime.example","device_id":"d_testdevice00000000000000a","linked_at":"2026-11-21T09:30:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := accountCredentials
	t.Cleanup(func() { accountCredentials = saved })
	accountCredentials = func(home.Dirs, []keys.Store) (uploader.Credentials, bool, error) {
		return uploader.Credentials{Server: "https://pairtime.example", Token: "ptd_test"}, true, nil
	}
	write := func(t *testing.T, text *string) {
		t.Helper()
		if text == nil {
			if err := os.Remove(dirs.ConfigFile()); err != nil && !errors.Is(err, fs.ErrNotExist) {
				t.Fatal(err)
			}
			return
		}
		if err := os.WriteFile(dirs.ConfigFile(), []byte(*text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	str := func(s string) *string { return &s }
	tests := []struct {
		name string
		// before is the file the config watch last loaded, and text the
		// file as it is now.
		before, text *string
		// local is whether a batch sealed now is local-only, and so whether
		// nothing uploads; broken whether doctor hears config.toml has an
		// error.
		local, broken bool
	}{
		{"mode = \"online\" uploads, and isn't local-only", nil, str("mode = \"online\"\n"), false, false},
		{"mode = \"local\" is local-only, and uploads nothing", nil, str("mode = \"local\"\n"), true, false},
		{"a file with no mode is local-only, as its default is", nil, str("[display]\nshadow = true\n"), true, false},
		{"no config.toml is local-only, as the defaults are", nil, nil, true, false},
		{"a file that isn't TOML is local-only until it's fixed, whatever it said before", str("mode = \"online\"\n"), str("mode = \"onl"), true, true},
		{"a value of the wrong type keeps it local-only too, whatever its mode says", str("mode = \"online\"\n"), str("mode = \"online\"\n[http]\nport = \"8123\"\n"), true, true},
		{"an edit the config watch didn't see takes effect at once", str("mode = \"online\"\n"), str("mode = \"local\"\n"), true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: map[string]string{}}
			if tc.before != nil {
				write(t, tc.before)
				a.loadConfig(false)
			}
			write(t, tc.text)
			if local, known := a.localOnly(); local != tc.local || !known {
				t.Errorf("localOnly() = %v, %v; want %v, true", local, known, tc.local)
			}
			if _, ok := a.credentials(); ok == tc.local {
				t.Errorf("uploads go: %v, while a batch sealed now is local-only: %v", ok, tc.local)
			}
			if got := strings.Contains(a.currentProblems()["account"], ConfigBroken); got != tc.broken {
				t.Errorf("doctor hears %q", a.currentProblems()["account"])
			}
		})
	}
}

func TestSignedIn(t *testing.T) {
	const (
		server = "https://pairtime.example"
		device = "d_testdevice00000000000000a"
	)
	linkedAt := time.Date(2026, 11, 21, 9, 30, 0, 0, time.UTC)
	seed := bytes.Repeat([]byte{7}, ed25519.SeedSize)
	pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	token := devtoken.New()
	stores := []keys.Store{} // K_dev and the sign-in go in the key file
	writeAccount := func(t *testing.T, dir, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "account.json"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// link does what pairtime login leaves behind: the token and the key's
	// seed in the keystore, and account.json.
	link := func(t *testing.T, dir string) *keys.Keystore {
		t.Helper()
		ks, err := keys.Open(keys.Options{Dir: dir, Stores: stores})
		if err != nil {
			t.Fatal(err)
		}
		if err := ks.SetAccount([]byte(token), seed); err != nil {
			t.Fatal(err)
		}
		writeAccount(t, dir, fmt.Sprintf(`{"v":1,"server":%q,"device_id":%q,"public_key":%q,"linked_at":%q}`,
			server, device, base64.StdEncoding.EncodeToString(pub), linkedAt.Format(time.RFC3339)))
		return ks
	}
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string)
		mode  config.Mode
		// ok is whether uploads go, fails whether doctor hears why they
		// don't, and keystore whether the data directory has a keystore
		// afterwards.
		ok, fails, keystore bool
	}{
		{"a computer that isn't linked uploads nothing and opens no keystore, which would make K_dev", func(*testing.T, string) {}, modeOnline, false, false, false},
		{"a linked computer uploads with account.json's server, device id and linked_at, and the keystore's token and key", func(t *testing.T, dir string) { link(t, dir) }, modeOnline, true, false, true},
		{"config.toml's local mode uploads nothing on a linked computer", func(t *testing.T, dir string) { link(t, dir) }, config.Local, false, false, true},
		{"a keystore without the sign-in, as after part of a logout, uploads nothing", func(t *testing.T, dir string) {
			if err := link(t, dir).ForgetAccount(); err != nil {
				t.Fatal(err)
			}
		}, modeOnline, false, false, true},
		{"an account.json pairtime didn't write uploads nothing, and doctor hears why", func(t *testing.T, dir string) {
			writeAccount(t, dir, `{"v":9}`)
		}, modeOnline, false, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir)
			c, ok, err := SignedIn(home.Dirs{Data: dir}, config.Config{Mode: tc.mode}, stores)
			if ok != tc.ok || (err != nil) != tc.fails {
				t.Fatalf("SignedIn = %v, %v; want %v, an error %v", ok, err, tc.ok, tc.fails)
			}
			if _, err := os.Stat(filepath.Join(dir, "keys")); errors.Is(err, fs.ErrNotExist) == tc.keystore {
				t.Errorf("the keys directory: %v, want a keystore %v", err, tc.keystore)
			}
			if !ok {
				if c.Token != "" || c.Sign != nil {
					t.Errorf("credentials %q, %v without a sign-in", c.Token, c.Sign != nil)
				}
				return
			}
			if c.Server != server || c.DeviceID != device || c.Token != token || !c.LinkedAt.Equal(linkedAt) {
				t.Errorf("credentials: server %q, device %q, token %q, linked_at %v", c.Server, c.DeviceID, c.Token, c.LinkedAt)
			}
			msg := []byte("pairtime-ingest-v1\n")
			sig, err := c.Sign(msg)
			if err != nil || !ed25519.Verify(pub, msg, sig) {
				t.Errorf("the signer doesn't sign with the key pairtime login registered: %v", err)
			}
		})
	}
}
