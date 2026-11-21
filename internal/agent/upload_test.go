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
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/devtoken"
)

func TestLocalOnlyMode(t *testing.T) {
	reads := []struct {
		name         string
		cfg          config.Config
		local, known bool
	}{
		{"a config.toml that reads mode = \"local\" is local-only", config.Config{Mode: config.Local}, true, true},
		{"one that reads mode = \"online\" isn't", config.Config{Mode: modeOnline}, false, true},
		{"one with no mode is local-only, as its default is", config.Config{}, true, true},
	}
	for _, tc := range reads {
		t.Run(tc.name, func(t *testing.T) {
			var a agent
			a.mode.Store(modeRead(tc.cfg))
			if local, known := a.localOnly(); local != tc.local || known != tc.known {
				t.Errorf("localOnly() = %v, %v; want %v, %v", local, known, tc.local, tc.known)
			}
		})
	}

	// loadConfig as the agent runs it, on files that read and files that
	// don't.
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	newAgent := func() *agent {
		return &agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: map[string]string{}}
	}
	write := func(t *testing.T, text string) {
		t.Helper()
		if err := os.WriteFile(dirs.ConfigFile(), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	loads := []struct {
		name         string
		run          func(t *testing.T) *agent
		local, known bool
	}{
		{"before config.toml has read, the mode isn't known", func(t *testing.T) *agent { return newAgent() }, false, false},
		{"a file that reads sets the mode", func(t *testing.T) *agent {
			write(t, "mode = \"local\"\n")
			a := newAgent()
			a.loadConfig(true)
			return a
		}, true, true},
		{"a file that isn't TOML, as after a typo, leaves the mode as it last read", func(t *testing.T) *agent {
			write(t, "mode = \"local\"\n")
			a := newAgent()
			a.loadConfig(true)
			write(t, "mode = \"loc") // an editor halfway through writing it
			a.loadConfig(false)
			return a
		}, true, true},
		{"a file that isn't TOML at the start leaves the mode unknown", func(t *testing.T) *agent {
			write(t, "mode = [")
			a := newAgent()
			a.loadConfig(true)
			return a
		}, false, false},
		{"no config.toml reads as the defaults, which are local-only", func(t *testing.T) *agent {
			if err := os.Remove(dirs.ConfigFile()); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			a := newAgent()
			a.loadConfig(false)
			return a
		}, true, true},
	}
	for _, tc := range loads {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.run(t)
			if local, known := a.localOnly(); local != tc.local || known != tc.known {
				t.Errorf("localOnly() = %v, %v; want %v, %v", local, known, tc.local, tc.known)
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
