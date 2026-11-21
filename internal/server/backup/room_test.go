// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckRoom(t *testing.T) {
	const dbSize = 3 << 20
	tests := []struct {
		name    string
		free    uint64
		ok      bool
		wantErr bool
	}{
		{name: "a disk with room for the copy and the margin passes", free: dbSize + SnapshotMargin + 1, ok: true},
		{name: "a disk without room for the copy and the margin fails before VACUUM INTO fills it", free: dbSize + SnapshotMargin - 1, ok: true, wantErr: true},
		{name: "where free space can't be read, the snapshot goes ahead", free: 0, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			db := filepath.Join(dir, "pairtime.db")
			if err := os.WriteFile(db, make([]byte, dbSize-1024), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(db+"-wal", make([]byte, 1024), 0o600); err != nil {
				t.Fatal(err)
			}
			old := diskSpace
			diskSpace = func(string) (uint64, uint64, bool, error) { return tt.free, 1 << 40, tt.ok, nil }
			t.Cleanup(func() { diskSpace = old })
			err := CheckRoom(dir, db)
			if (err != nil) != tt.wantErr || (err != nil && !errors.Is(err, ErrNoRoom)) {
				t.Fatalf("CheckRoom = %v, want an ErrNoRoom: %v", err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "the database is 3.0 MiB") {
				t.Errorf("the error doesn't give the database's size with its WAL: %v", err)
			}
		})
	}
}

func TestBytes(t *testing.T) {
	tests := []struct {
		name string
		n    uint64
		want string
	}{
		{name: "under a KiB is bytes", n: 512, want: "512 B"},
		{name: "a KiB and a half", n: 1536, want: "1.5 KiB"},
		{name: "objstore's smallest part", n: 5 << 20, want: "5.0 MiB"},
		{name: "a 20 GiB disk", n: 20 << 30, want: "20.0 GiB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Bytes(tt.n); got != tt.want {
				t.Errorf("Bytes(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}

func TestKeyBackupNames(t *testing.T) {
	keys, err := Generate(filepath.Join(t.TempDir(), "keys"), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub := keys.Public()
	made := time.Date(2026, 11, 20, 10, 15, 0, 0, time.UTC)
	email := strings.Repeat("ab", 32)
	tests := []struct {
		name    string
		file    string
		want    KeyBackup
		wantErr bool
	}{
		{name: "a name KeyBackupName makes reads back as the keys and email key it names",
			file: KeyBackupName(made, pub, email),
			want: KeyBackup{Made: made, Named: true, ServerKeyID: pub.ServerKeyID(), EvidenceKeyID: pub.EvidenceKeyID(), EmailKey: EmailKeyFingerprint(email)}},
		{name: "a backup without an email key is named none",
			file: KeyBackupName(made, pub, " "),
			want: KeyBackup{Made: made, Named: true, ServerKeyID: pub.ServerKeyID(), EvidenceKeyID: pub.EvidenceKeyID(), EmailKey: "none"}},
		{name: "a name from before backups named their keys still reads, as a stale one",
			file: "pairtime-keys-20261120T101500Z.tar.age", want: KeyBackup{Made: made}},
		{name: "a file that isn't a key backup doesn't read", file: "notes.txt", wantErr: true},
		{name: "a name with a key id of the wrong length doesn't read", file: "pairtime-keys-20261120T101500Z-abc-def-none.tar.age", wantErr: true},
		{name: "a name with a time that isn't one doesn't read", file: "pairtime-keys-20261340T101500Z.tar.age", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseKeyBackupName(tt.file)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseKeyBackupName(%q) = %+v, %v; want an error: %v", tt.file, got, err, tt.wantErr)
			}
			if err == nil && (!got.Made.Equal(tt.want.Made) || got.Named != tt.want.Named || got.ServerKeyID != tt.want.ServerKeyID || got.EvidenceKeyID != tt.want.EvidenceKeyID || got.EmailKey != tt.want.EmailKey) {
				t.Errorf("ParseKeyBackupName(%q) = %+v, want %+v", tt.file, got, tt.want)
			}
		})
	}
	t.Run("an email key's fingerprint isn't the pepper's for the same bytes, since each has its own label", func(t *testing.T) {
		if Fingerprint("pairtime email key", keys.Pepper) == keys.PepperFingerprint() {
			t.Error("the two labels give one fingerprint")
		}
	})
}
