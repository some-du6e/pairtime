// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"filippo.io/age/armor"

	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/objstore"
	"github.com/some-du6e/pairtime/internal/server/objstore/objstoretest"
)

// backupHost is a deployed server's files for the keys and snapshot
// commands: keys in a private data directory, and pairtime.toml naming
// the fake S3 server's two buckets.
type backupHost struct {
	s3      *objstoretest.Server
	config  string
	dataDir string
	karim   *age.X25519Identity
	environ []string
}

func newBackupHost(t *testing.T) *backupHost {
	t.Helper()
	h := &backupHost{s3: objstoretest.New(t, "pairtime-data", "pairtime-snapshots"), dataDir: privateDir(t)}
	var err error
	if h.karim, err = age.GenerateX25519Identity(); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Generate(backup.KeysDir(h.dataDir), rand.Reader); err != nil {
		t.Fatal(err)
	}
	h.config = filepath.Join(t.TempDir(), "pairtime.toml")
	writeFile(t, h.config, fmt.Sprintf(`base_url = "https://pairtime.example.org"
data_dir = %q
[object_storage]
endpoint = %q
bucket = "pairtime-data"
snapshot_bucket = "pairtime-snapshots"
`, h.dataDir, h.s3.URL), 0o644)
	h.environ = []string{
		"PAIRTIME_S3_ACCESS_KEY_ID=" + objstoretest.AccessKeyID,
		"PAIRTIME_S3_SECRET_ACCESS_KEY=" + objstoretest.SecretAccessKey,
		"PAIRTIME_EMAIL_KEY=" + strings.Repeat("cd", 32),
		"PAIRTIME_AGE_RECIPIENT=" + h.karim.Recipient().String(),
	}
	return h
}

func (h *backupHost) run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	e, out, errOut := testEnv(h.environ...)
	code = run(context.Background(), append(args, "-config", h.config), e)
	return out.String(), errOut.String(), code
}

// openKeyBackup decrypts a key backup and returns its files by name.
func openKeyBackup(t *testing.T, data []byte, id age.Identity) map[string][]byte {
	t.Helper()
	plain, err := age.Decrypt(armor.NewReader(bytes.NewReader(data)), id)
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string][]byte)
	tr := tar.NewReader(plain)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		files[strings.TrimPrefix(hdr.Name, "pairtime-keys/")] = b
	}
}

func TestKeysBackupCommand(t *testing.T) {
	secret, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		// args go after "keys backup"; "OUT" becomes a fresh file path, and
		// "BADOUT" one in a directory that doesn't exist.
		args     []string
		environ  []string
		setup    func(t *testing.T, out string)
		wantCode int
		wantErr  string
		// wantUpload is whether the main bucket holds a backup afterwards.
		wantUpload bool
	}{
		{name: "the backup goes to -o with mode 0600, and Karim's identity decrypts it to the keys and the email key",
			args: []string{"-o", "OUT"}},
		{name: "-upload also stores it in the main bucket, under backups/keys/",
			args: []string{"-upload", "-o", "OUT"}, wantUpload: true},
		{name: "-age names the recipient instead of PAIRTIME_AGE_RECIPIENT",
			args: []string{"-age", "KARIM", "-o", "OUT"}, environ: []string{"PAIRTIME_AGE_RECIPIENT="}},
		{name: "an age private key in place of the recipient is refused, and never printed",
			args: []string{"-o", "OUT"}, environ: []string{"PAIRTIME_AGE_RECIPIENT=" + secret.String()},
			wantCode: 1, wantErr: "private key"},
		{name: "without a recipient nothing is written",
			args: []string{"-o", "OUT"}, environ: []string{"PAIRTIME_AGE_RECIPIENT="},
			wantCode: 1, wantErr: "no age recipient"},
		{name: "-o never writes over a file",
			args:     []string{"-o", "OUT"},
			setup:    func(t *testing.T, out string) { writeFile(t, out, "an older backup\n", 0o600) },
			wantCode: 1, wantErr: "exists"},
		{name: "a -o that can't be made fails before -upload stores anything, so a second try stores no second backup",
			args:     []string{"-upload", "-o", "BADOUT"},
			wantCode: 1, wantErr: "no such file"},
		{name: "-o over a file also stores nothing with -upload",
			args:     []string{"-upload", "-o", "OUT"},
			setup:    func(t *testing.T, out string) { writeFile(t, out, "an older backup\n", 0o600) },
			wantCode: 1, wantErr: "exists"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newBackupHost(t)
			h.environ = append(h.environ, tt.environ...)
			out := filepath.Join(t.TempDir(), "pairtime-keys.tar.age")
			if tt.setup != nil {
				tt.setup(t, out)
			}
			before, _ := os.ReadFile(out)
			args := []string{"keys", "backup"}
			for _, a := range tt.args {
				switch a {
				case "OUT":
					a = out
				case "BADOUT":
					a = filepath.Join(t.TempDir(), "missing", "pairtime-keys.tar.age")
				case "KARIM":
					a = h.karim.Recipient().String()
				}
				args = append(args, a)
			}
			_, stderr, code := h.run(t, args...)
			if code != tt.wantCode {
				t.Fatalf("exit code %d, want %d; stderr:\n%s", code, tt.wantCode, stderr)
			}
			if strings.Contains(stderr, secret.String()) {
				t.Error("stderr prints the private key")
			}
			uploads := 0
			for _, k := range h.s3.Keys("pairtime-data") {
				if strings.HasPrefix(k, backup.KeyBackupPrefix) {
					uploads++
				}
			}
			if (uploads == 1) != tt.wantUpload {
				t.Errorf("%d backups in the main bucket, want one: %v", uploads, tt.wantUpload)
			}
			if code != 0 {
				if !strings.Contains(stderr, tt.wantErr) {
					t.Errorf("stderr = %q, want it to say %q", stderr, tt.wantErr)
				}
				if after, _ := os.ReadFile(out); !bytes.Equal(after, before) {
					t.Error("a failed backup changed the file")
				}
				return
			}
			fi, err := os.Stat(out)
			if err != nil || fi.Mode().Perm() != 0o600 {
				t.Fatalf("the backup file: %v, %v; want mode 0600", fi, err)
			}
			data, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			files := openKeyBackup(t, data, h.karim)
			for _, name := range backup.KeyFiles {
				want, err := os.ReadFile(filepath.Join(backup.KeysDir(h.dataDir), name))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(files[name], want) {
					t.Errorf("%s in the backup isn't the key file", name)
				}
			}
			if string(files[backup.EmailKeyFile]) != strings.Repeat("cd", 32)+"\n" {
				t.Errorf("the backup's email key is %q", files[backup.EmailKeyFile])
			}
			if tt.wantUpload {
				for _, k := range h.s3.Keys("pairtime-data") {
					if stored, _ := h.s3.Object("pairtime-data", k); strings.HasPrefix(k, backup.KeyBackupPrefix) && !bytes.Equal(stored, data) {
						t.Error("the uploaded backup isn't the file")
					}
				}
			}
		})
	}
}

// TestKeyBackupNames checks that an uploaded key backup's name says which
// keys and which email key it holds, which doctor and keys list read
// (#133).
func TestKeyBackupNames(t *testing.T) {
	tests := []struct {
		name      string
		environ   []string
		wantEmail func() string
	}{
		{name: "the name gives the server and evidence key ids and the email key's fingerprint",
			wantEmail: func() string { return backup.EmailKeyFingerprint(strings.Repeat("cd", 32)) }},
		{name: "without PAIRTIME_EMAIL_KEY the name says none",
			environ: []string{"PAIRTIME_EMAIL_KEY="}, wantEmail: func() string { return "none" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newBackupHost(t)
			h.environ = append(h.environ, tt.environ...)
			if _, stderr, code := h.run(t, "keys", "backup", "-upload", "-o", filepath.Join(t.TempDir(), "k.tar.age")); code != 0 {
				t.Fatalf("keys backup: %s", stderr)
			}
			keys, err := backup.Load(backup.KeysDir(h.dataDir))
			if err != nil {
				t.Fatal(err)
			}
			var names []backup.KeyBackup
			for _, k := range h.s3.Keys("pairtime-data") {
				if n, ok := strings.CutPrefix(k, backup.KeyBackupPrefix); ok {
					b, err := backup.ParseKeyBackupName(n)
					if err != nil {
						t.Fatal(err)
					}
					names = append(names, b)
				}
			}
			pub := keys.Public()
			if len(names) != 1 || !names[0].Named || names[0].ServerKeyID != pub.ServerKeyID() || names[0].EvidenceKeyID != pub.EvidenceKeyID() || names[0].EmailKey != tt.wantEmail() {
				t.Errorf("the backups are named %+v; want one naming %s, %s and %s", names, pub.ServerKeyID(), pub.EvidenceKeyID(), tt.wantEmail())
			}
		})
	}
}

// TestKeysList checks that keys list prints the bucket's key backups
// oldest first, with what each name says it holds and "-" for a name from
// before names said (#133).
func TestKeysList(t *testing.T) {
	h := newBackupHost(t)
	keys, err := backup.Load(backup.KeysDir(h.dataDir))
	if err != nil {
		t.Fatal(err)
	}
	pub := keys.Public()
	made := time.Date(2026, 11, 20, 10, 15, 0, 0, time.UTC)
	named := backup.KeyBackupName(made, pub, "")
	h.s3.SetObject("pairtime-data", backup.KeyBackupPrefix+named, []byte("armored"))
	h.s3.SetObject("pairtime-data", backup.KeyBackupPrefix+"pairtime-keys-20261101T000000Z.tar.age", []byte("armored"))
	h.s3.SetObject("pairtime-data", backup.KeyBackupPrefix+"notes.txt", []byte("not a backup"))
	stdout, stderr, code := h.run(t, "keys", "list")
	if code != 0 {
		t.Fatalf("keys list: %s", stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	tests := []struct {
		name, got, want string
	}{
		{name: "two backups are listed, and an object that isn't one is left out", got: fmt.Sprint(len(lines)), want: "2"},
		{name: "a backup from before names said what it holds comes first, older, with - for each key",
			got: lines[0], want: backup.KeyBackupPrefix + "pairtime-keys-20261101T000000Z.tar.age\t2026-11-01T00:00:00Z\t-\t-\t-"},
		{name: "a named backup gives its keys and says none for no email key",
			got: lines[len(lines)-1], want: backup.KeyBackupPrefix + named + "\t2026-11-20T10:15:00Z\t" + pub.ServerKeyID() + "\t" + pub.EvidenceKeyID() + "\tnone"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestSnapshotCommands(t *testing.T) {
	tests := []struct {
		name     string
		environ  []string
		wantCode int
		wantErr  string
		// wantPings is how many pings the snapshot's monitor gets.
		wantPings int
	}{
		{name: "take stores a snapshot that fetch downloads and Karim's identity decrypts to an intact database, and pings the snapshot's monitor",
			environ: []string{"PAIRTIME_SNAPSHOT_HEARTBEAT_URL=MONITOR"}, wantPings: 1},
		{name: "without a snapshot monitor take still stores the snapshot"},
		{name: "without PAIRTIME_AGE_RECIPIENT take refuses, since there is no key to encrypt to",
			environ: []string{"PAIRTIME_AGE_RECIPIENT="}, wantCode: 1, wantErr: "PAIRTIME_AGE_RECIPIENT isn't set"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newBackupHost(t)
			m := newMonitor(t)
			for _, v := range tt.environ {
				h.environ = append(h.environ, strings.Replace(v, "MONITOR", m.URL+"/snapshot", 1))
			}
			dbPath := filepath.Join(h.dataDir, "pairtime.db")
			createDatabase(t, dbPath)
			if _, stderr, code := h.run(t, "migrate"); code != 0 {
				t.Fatalf("migrate: %s", stderr)
			}

			stdout, stderr, code := h.run(t, "snapshot", "take")
			if code != tt.wantCode {
				t.Fatalf("take: exit code %d, want %d; stderr:\n%s", code, tt.wantCode, stderr)
			}
			if got := m.count("/snapshot"); got != tt.wantPings {
				t.Errorf("the snapshot's monitor got %d pings, want %d", got, tt.wantPings)
			}
			if code != 0 {
				if !strings.Contains(stderr, tt.wantErr) {
					t.Errorf("stderr = %q, want it to say %q", stderr, tt.wantErr)
				}
				return
			}
			if !strings.Contains(stdout, objstore.SnapshotPrefix) {
				t.Errorf("take printed %q", stdout)
			}
			out := filepath.Join(t.TempDir(), "latest.db.age")
			if _, stderr, code := h.run(t, "snapshot", "fetch", "-o", out); code != 0 {
				t.Fatalf("fetch: %s", stderr)
			}
			sealed, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			// -o - streams the same bytes to standard output, with the
			// note on standard error (#133).
			streamed, notes, code := h.run(t, "snapshot", "fetch", "-o", "-")
			if code != 0 || streamed != string(sealed) || !strings.Contains(notes, "fetched "+objstore.SnapshotPrefix) {
				t.Errorf("fetch -o - exited %d with %d bytes, not the file's %d; stderr %q", code, len(streamed), len(sealed), notes)
			}
			plain, err := age.Decrypt(bytes.NewReader(sealed), h.karim)
			if err != nil {
				t.Fatal(err)
			}
			restored := filepath.Join(t.TempDir(), "restored.db")
			b, err := io.ReadAll(plain)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, restored, string(b), 0o600)
			db, err := sql.Open("sqlite", restored)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var check string
			var migrations int
			if err := db.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
				t.Errorf("integrity_check = %q, %v", check, err)
			}
			if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&migrations); err != nil || migrations == 0 {
				t.Errorf("the snapshot holds %d migrations: %v", migrations, err)
			}
			if left, _ := os.ReadDir(backup.SnapshotDir(h.dataDir)); len(left) > 0 {
				t.Errorf("take left %d files in the snapshot directory", len(left))
			}
		})
	}
}
