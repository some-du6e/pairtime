// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerate(t *testing.T) {
	tests := []struct {
		name string
		// setup prepares the data directory; the keys go in its keys/.
		setup   func(t *testing.T, keys string)
		wantErr string
		wantIs  error
		// wantFiles are the key files there afterwards.
		wantFiles []string
	}{
		{name: "a new directory gets the three keys, each with mode 0600, in a directory with mode 0700",
			wantFiles: KeyFiles},
		{name: "an existing key is never replaced, and no other key is written",
			setup: func(t *testing.T, keys string) {
				mkdir(t, keys, 0o700)
				write(t, filepath.Join(keys, PepperFile), "an old pepper\n", 0o600)
			},
			wantIs: ErrKeysExist, wantFiles: []string{PepperFile}},
		{name: "a keys directory that group or others can enter is refused",
			setup:   func(t *testing.T, keys string) { mkdir(t, keys, 0o755) },
			wantErr: "chmod 700", wantFiles: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys := KeysDir(t.TempDir())
			if tt.setup != nil {
				tt.setup(t, keys)
			}
			k, err := Generate(keys, rand.Reader)
			switch {
			case tt.wantIs != nil || tt.wantErr != "":
				if err == nil {
					t.Fatal("Generate succeeded")
				}
				if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
					t.Errorf("error = %v, want %v", err, tt.wantIs)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %v, want it to say %q", err, tt.wantErr)
				}
			case err != nil:
				t.Fatal(err)
			default:
				fi, err := os.Stat(keys)
				if err != nil || fi.Mode().Perm() != 0o700 {
					t.Errorf("keys directory: %v, %v; want mode 0700", fi.Mode(), err)
				}
				loaded, err := Load(keys)
				if err != nil {
					t.Fatal(err)
				}
				if !loaded.Server.Equal(k.Server) || !bytes.Equal(loaded.Evidence.Bytes(), k.Evidence.Bytes()) || !bytes.Equal(loaded.Pepper, k.Pepper) {
					t.Error("Load returned other keys than Generate wrote")
				}
			}
			var got []string
			for _, name := range KeyFiles {
				fi, err := os.Lstat(filepath.Join(keys, name))
				if err != nil {
					continue
				}
				got = append(got, name)
				if fi.Mode().Perm() != 0o600 {
					t.Errorf("%s has mode %04o, want 0600", name, fi.Mode().Perm())
				}
			}
			if strings.Join(got, ",") != strings.Join(tt.wantFiles, ",") {
				t.Errorf("key files %q, want %q", got, tt.wantFiles)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	_, otherServer, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		edit    func(t *testing.T, keys string)
		wantErr string
	}{
		{name: "the keys Generate wrote load", edit: func(*testing.T, string) {}},
		{name: "a key that group can read is refused",
			edit:    func(t *testing.T, keys string) { chmod(t, filepath.Join(keys, ServerKeyFile), 0o640) },
			wantErr: "chmod 600"},
		{name: "a keys directory that others can enter is refused",
			edit:    func(t *testing.T, keys string) { chmod(t, keys, 0o701) },
			wantErr: "chmod 700"},
		{name: "a missing key fails",
			edit:    func(t *testing.T, keys string) { remove(t, filepath.Join(keys, EvidenceKeyFile)) },
			wantErr: EvidenceKeyFile},
		{name: "an evidence key in the server key's place is refused",
			edit: func(t *testing.T, keys string) {
				data, err := os.ReadFile(filepath.Join(keys, EvidenceKeyFile))
				if err != nil {
					t.Fatal(err)
				}
				rewrite(t, filepath.Join(keys, ServerKeyFile), string(data))
			},
			wantErr: "not an Ed25519 key"},
		{name: "a server key in the evidence key's place is refused",
			edit: func(t *testing.T, keys string) {
				files, err := (&Keys{Server: otherServer, Evidence: mustP256(t), Pepper: make([]byte, PepperSize)}).encode()
				if err != nil {
					t.Fatal(err)
				}
				rewrite(t, filepath.Join(keys, EvidenceKeyFile), string(files[ServerKeyFile]))
			},
			wantErr: "not a P-256 key"},
		{name: "a pepper that isn't 32 bytes of hex is refused",
			edit:    func(t *testing.T, keys string) { rewrite(t, filepath.Join(keys, PepperFile), "abcd\n") },
			wantErr: "64 hex digits"},
		{name: "a key file with text after its PEM block is refused",
			edit: func(t *testing.T, keys string) {
				data, err := os.ReadFile(filepath.Join(keys, ServerKeyFile))
				if err != nil {
					t.Fatal(err)
				}
				rewrite(t, filepath.Join(keys, ServerKeyFile), string(data)+"extra\n")
			},
			wantErr: "one PEM block"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys := KeysDir(t.TempDir())
			if _, err := Generate(keys, rand.Reader); err != nil {
				t.Fatal(err)
			}
			tt.edit(t, keys)
			_, err := Load(keys)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to say %q", err, tt.wantErr)
			}
		})
	}
}

func TestKeyID(t *testing.T) {
	pub := []byte("a public key's bytes")
	sum := sha256.Sum256(pub)
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "a key id is the first 16 hex digits of the key's SHA-256, as release key ids are",
			got: KeyID(pub), want: hex.EncodeToString(sum[:])[:16]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("KeyID = %s, want %s", tt.got, tt.want)
			}
		})
	}
}

func mustP256(t *testing.T) *ecdh.PrivateKey {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func mkdir(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(dir, mode); err != nil {
		t.Fatal(err)
	}
	chmod(t, dir, mode)
}

func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func remove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
	chmod(t, path, mode)
}

// rewrite replaces a file's contents and keeps its mode.
func rewrite(t *testing.T, path, data string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, data, fi.Mode().Perm())
}
