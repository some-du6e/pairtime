// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"archive/tar"
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"filippo.io/age/armor"
)

func TestParseRecipient(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	secret := id.String()
	tests := []struct {
		name    string
		in      string
		wantErr string
	}{
		{name: "an age public key is a recipient", in: id.Recipient().String()},
		{name: "spaces around the key don't count", in: "  " + id.Recipient().String() + "\n"},
		{name: "an empty recipient is refused", in: "", wantErr: "no age recipient"},
		{name: "an age private key is refused as one that must never be on the host", in: secret, wantErr: "private key"},
		{name: "two recipients are refused", in: id.Recipient().String() + "\n" + id.Recipient().String(), wantErr: "one age recipient"},
		{name: "something else is refused", in: "ssh-ed25519 AAAA", wantErr: "isn't an age public key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := ParseRecipient(tt.in)
			if tt.wantErr == "" {
				if err != nil || r == nil {
					t.Fatalf("ParseRecipient = %v, %v", r, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to say %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), strings.TrimPrefix(secret, "AGE-SECRET-KEY-")) {
				t.Error("the error quotes the private key")
			}
		})
	}
}

func TestWriteKeyBackup(t *testing.T) {
	karim, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 20, 10, 15, 0, 0, time.UTC)
	tests := []struct {
		name     string
		emailKey string
		edit     func(t *testing.T, keys string)
		// identity decrypts the backup.
		identity  age.Identity
		wantErr   string
		wantFiles []string
	}{
		{name: "the backup holds the three keys byte for byte, the email key and a README, and decrypts with Karim's identity",
			emailKey: strings.Repeat("ab", 32), identity: karim,
			wantFiles: []string{"pairtime-keys/", "pairtime-keys/server.ed25519", "pairtime-keys/evidence.p256", "pairtime-keys/pepper", "pairtime-keys/PAIRTIME_EMAIL_KEY", "pairtime-keys/README.txt"}},
		{name: "without an email key the backup holds the keys and the README",
			identity:  karim,
			wantFiles: []string{"pairtime-keys/", "pairtime-keys/server.ed25519", "pairtime-keys/evidence.p256", "pairtime-keys/pepper", "pairtime-keys/README.txt"}},
		{name: "another identity can't decrypt the backup",
			identity: stranger, wantErr: "no identity matched"},
		{name: "keys pairtimed can't load aren't backed up",
			edit:    func(t *testing.T, keys string) { rewrite(t, filepath.Join(keys, PepperFile), "short\n") },
			wantErr: "64 hex digits"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys := KeysDir(t.TempDir())
			if _, err := Generate(keys, rand.Reader); err != nil {
				t.Fatal(err)
			}
			if tt.edit != nil {
				tt.edit(t, keys)
			}
			var out bytes.Buffer
			err := WriteKeyBackup(&out, keys, tt.emailKey, karim.Recipient(), now)
			if err == nil && tt.identity != nil {
				if !strings.HasPrefix(out.String(), "-----BEGIN AGE ENCRYPTED FILE-----\n") {
					t.Errorf("the backup isn't ASCII-armored: %.40q", out.String())
				}
				var files map[string][]byte
				files, err = readBackup(t, &out, tt.identity)
				if err == nil {
					checkBackup(t, keys, tt.emailKey, files, tt.wantFiles)
				}
			}
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

// readBackup decrypts a key backup and returns its entries, by name, and
// fails the test on an entry whose mode isn't 0600 or 0700.
func readBackup(t *testing.T, r io.Reader, id age.Identity) (map[string][]byte, error) {
	t.Helper()
	plain, err := age.Decrypt(armor.NewReader(r), id)
	if err != nil {
		return nil, err
	}
	files := make(map[string][]byte)
	var order []string
	tr := tar.NewReader(plain)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		want := int64(0o600)
		if hdr.Typeflag == tar.TypeDir {
			want = 0o700
		}
		if hdr.Mode != want {
			t.Errorf("%s has mode %04o in the archive, want %04o", hdr.Name, hdr.Mode, want)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		files[hdr.Name] = data
		order = append(order, hdr.Name)
	}
	files["\x00order"] = []byte(strings.Join(order, ","))
	return files, nil
}

func checkBackup(t *testing.T, keys, emailKey string, files map[string][]byte, wantFiles []string) {
	t.Helper()
	if got := string(files["\x00order"]); got != strings.Join(wantFiles, ",") {
		t.Errorf("entries %s, want %s", got, strings.Join(wantFiles, ","))
	}
	for _, name := range KeyFiles {
		want, err := os.ReadFile(filepath.Join(keys, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(files["pairtime-keys/"+name], want) {
			t.Errorf("%s in the backup isn't the file byte for byte", name)
		}
	}
	if emailKey != "" && string(files["pairtime-keys/"+EmailKeyFile]) != emailKey+"\n" {
		t.Errorf("the email key in the backup is %q", files["pairtime-keys/"+EmailKeyFile])
	}
	if !strings.Contains(string(files["pairtime-keys/README.txt"]), "age -d -i") {
		t.Error("the README doesn't say how to decrypt")
	}
}
