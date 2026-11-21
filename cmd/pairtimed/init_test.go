// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/objstore/objstoretest"
)

// s3URL stands for the endpoint in s3Environ. TestInit puts each case's own
// objstoretest server in its place, which init asks whether a server ran
// on the bucket before.
const s3URL = "http://127.0.0.1:1"

// s3Environ is the bucket part of a filled-in secrets.env.
var s3Environ = []string{
	"PAIRTIME_S3_ENDPOINT=" + s3URL,
	"PAIRTIME_S3_BUCKET=pairtime-data",
	"PAIRTIME_S3_SNAPSHOT_BUCKET=pairtime-snapshots",
	"PAIRTIME_S3_ACCESS_KEY_ID=" + objstoretest.AccessKeyID,
	"PAIRTIME_S3_SECRET_ACCESS_KEY=" + objstoretest.SecretAccessKey,
}

// hcaEnviron is what -hca-client-id needs beside it.
var hcaEnviron = []string{
	"PAIRTIME_HCA_CLIENT_SECRET=hca-secret",
	"PAIRTIME_EMAIL_KEY=" + strings.Repeat("ab", 32),
}

// notRoot skips a test that relies on file modes, which root ignores.
func notRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root writes where the mode says it may not")
	}
}

// privateDir makes a directory with mode 0700, as the runbook makes data/.
func privateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestInit(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		environ []string
		// setup prepares the data directory, the config path and the
		// bucket.
		setup func(t *testing.T, data, out string, s3 *objstoretest.Server)
		// stdout sends the file to standard output with -config-out -.
		stdout   bool
		wantCode int
		// wantErr is in stderr when the code isn't 0.
		wantErr string
		// wantStderr is in stderr, and notStderr isn't, whatever the code.
		wantStderr, notStderr string
		// wantKeys is whether the key files exist afterwards.
		wantKeys bool
		// check looks at the loaded file and its text.
		check func(t *testing.T, cfg *config.Config, text string)
	}{
		{name: "init writes a pairtime.toml that loads, with sign-ups closed, no operators, an empty [controller] and profile_default ask",
			environ: s3Environ, wantKeys: true,
			check: func(t *testing.T, cfg *config.Config, text string) {
				if cfg.SignupsOpen || len(cfg.Operators) != 0 || cfg.Controller != (config.Controller{}) || cfg.ProfileDefault != config.ProfileAsk {
					t.Errorf("signups_open %v, operators %q, controller %+v, profile_default %q", cfg.SignupsOpen, cfg.Operators, cfg.Controller, cfg.ProfileDefault)
				}
				for _, want := range []string{"\nsignups_open = false\n", "\noperators = []\n", "\n[controller]\n", "\nname = \"\"\n", "\ncontact = \"\"\n", "\ndpo = \"\"\n", "\nprofile_default = \"ask\"\n"} {
					if !strings.Contains(text, want) {
						t.Errorf("the file doesn't say %q", strings.TrimSpace(want))
					}
				}
				if cfg.ConfigVersion != config.ConfigVersion || cfg.BaseURL != "https://pairtime.example.org" {
					t.Errorf("config_version %d, base_url %q", cfg.ConfigVersion, cfg.BaseURL)
				}
			}},
		{name: "the buckets come from the PAIRTIME_S3_ variables that litestream.yml reads",
			environ: s3Environ, wantKeys: true,
			check: func(t *testing.T, cfg *config.Config, _ string) {
				o := cfg.ObjectStorage
				if !strings.HasPrefix(o.Endpoint, "http://127.0.0.1:") || o.Endpoint == s3URL || o.Region != "auto" || o.Bucket != "pairtime-data" || o.SnapshotBucket != "pairtime-snapshots" {
					t.Errorf("object_storage = %+v", o)
				}
			}},
		{name: "the rendered file keeps sign-ups closed until the controller is named",
			environ: s3Environ, wantKeys: true,
			check: func(t *testing.T, cfg *config.Config, text string) {
				open := strings.Replace(text, "\nsignups_open = false\n", "\nsignups_open = true\n", 1)
				path := filepath.Join(t.TempDir(), "open.toml")
				if err := os.WriteFile(path, []byte(open), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := config.Load(path, "", s3Environ); err == nil || !strings.Contains(err.Error(), "controller.name and controller.contact") {
					t.Errorf("signups_open = true with an empty [controller] loaded: %v", err)
				}
			}},
		{name: "without PAIRTIME_S3_ENDPOINT init refuses, since the bundle's Litestream would start serve on a pairtime.toml without buckets in a loop",
			wantCode: 1, wantErr: "-no-object-storage"},
		{name: "-no-object-storage writes a file with object storage off, which says how to turn it on",
			args: []string{"-no-object-storage"}, wantKeys: true,
			check: func(t *testing.T, cfg *config.Config, text string) {
				if cfg.ObjectStorage.Enabled() || !strings.Contains(text, "ran with -no-object-storage") || !strings.Contains(text, "run init again with -force") {
					t.Errorf("object_storage = %+v", cfg.ObjectStorage)
				}
			}},
		{name: "-no-object-storage beside PAIRTIME_S3_ENDPOINT is refused",
			args: []string{"-no-object-storage"}, environ: s3Environ,
			wantCode: 1, wantErr: "leave one of them out"},
		{name: "-hca-client-id without the client secret and the email key is refused, since serve wouldn't start",
			args: []string{"-hca-client-id", "app-123"}, environ: s3Environ,
			wantCode: 1, wantErr: "PAIRTIME_HCA_CLIENT_SECRET and PAIRTIME_EMAIL_KEY"},
		{name: "-hca-client-id with an email key that isn't 32 bytes is refused",
			args: []string{"-hca-client-id", "app-123"}, environ: append([]string{"PAIRTIME_HCA_CLIENT_SECRET=hca-secret", "PAIRTIME_EMAIL_KEY=short"}, s3Environ...),
			wantCode: 1, wantErr: "PAIRTIME_EMAIL_KEY"},
		{name: "-hca-client-id goes into [hca]",
			args: []string{"-hca-client-id", "app-123"}, environ: append(append([]string{}, hcaEnviron...), s3Environ...), wantKeys: true,
			check: func(t *testing.T, cfg *config.Config, _ string) {
				if cfg.HCA.ClientID != "app-123" {
					t.Errorf("hca.client_id = %q", cfg.HCA.ClientID)
				}
			}},
		{name: "-config-out - writes the file to standard output and the notes to standard error",
			environ: s3Environ, stdout: true, wantKeys: true},
		{name: "an existing pairtime.toml is never replaced, and no key is made",
			environ: s3Environ,
			setup: func(t *testing.T, _, out string, _ *objstoretest.Server) {
				if err := os.WriteFile(out, []byte("base_url = \"https://kept.example.org\"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: 1, wantErr: "doesn't replace a pairtime.toml unless -force"},
		{name: "with -config-out -, a pairtime.toml where pairtimed reads it is never replaced, since the shell's redirect would wipe its operators",
			environ: append([]string{"PAIRTIME_CONFIG=OUT"}, s3Environ...), stdout: true,
			setup: func(t *testing.T, _, out string, _ *objstoretest.Server) {
				if err := os.WriteFile(out, []byte("base_url = \"https://kept.example.org\"\noperators = [\"op\"]\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: 1, wantErr: "unless -force"},
		{name: "-force writes a new pairtime.toml over the old one, and keeps the keys",
			args: []string{"-force"}, environ: s3Environ, wantKeys: true,
			setup: func(t *testing.T, data, out string, _ *objstoretest.Server) {
				if _, err := backup.Generate(backup.KeysDir(data), rand.Reader); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(out, []byte("base_url = \"https://old.example.org\"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, cfg *config.Config, _ string) {
				if cfg.BaseURL != "https://pairtime.example.org" {
					t.Errorf("base_url = %q, want the new file's", cfg.BaseURL)
				}
			}},
		{name: "keys that are all there are kept, so init can run again after it failed to write the file",
			environ: s3Environ, wantKeys: true,
			setup: func(t *testing.T, data, _ string, _ *objstoretest.Server) {
				if _, err := backup.Generate(backup.KeysDir(data), rand.Reader); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "keys restored from their backup are kept on a host whose bucket holds the replica, so init runs after the restore",
			environ: s3Environ, wantKeys: true,
			setup: func(t *testing.T, data, _ string, s3 *objstoretest.Server) {
				if _, err := backup.Generate(backup.KeysDir(data), rand.Reader); err != nil {
					t.Fatal(err)
				}
				s3.SetObject("pairtime-data", "litestream/0000/0000000000000001-0000000000000001.ltx", []byte("ltx"))
			}},
		{name: "a database in the data directory and no keys is refused, since a server ran here and its keys come from their backup",
			environ: s3Environ,
			setup: func(t *testing.T, data, _ string, _ *objstoretest.Server) {
				writeFile(t, filepath.Join(data, config.DefaultDatabaseFile), "", 0o600)
			},
			wantCode: 1, wantErr: "pass -new-keys"},
		{name: "a bucket that holds Litestream's replica is refused without keys here, as on a rebuilt host",
			environ: s3Environ,
			setup: func(t *testing.T, _, _ string, s3 *objstoretest.Server) {
				s3.SetObject("pairtime-data", "litestream/0000/0000000000000001-0000000000000001.ltx", []byte("ltx"))
			},
			wantCode: 1, wantErr: "pairtime-data holds litestream/0000/"},
		{name: "a bucket that holds key backups is refused without keys here too",
			environ: s3Environ,
			setup: func(t *testing.T, _, _ string, s3 *objstoretest.Server) {
				s3.SetObject("pairtime-data", backup.KeyBackupPrefix+"pairtime-keys-20261120T101500Z.tar.age", []byte("armored"))
			},
			wantCode: 1, wantErr: "Restoring the keys"},
		{name: "-new-keys makes keys anyway, for a server that starts over, and warns that serve refuses the database the replica still in the bucket brings back",
			args: []string{"-new-keys"}, environ: s3Environ, wantKeys: true,
			setup: func(t *testing.T, _, _ string, s3 *objstoretest.Server) {
				s3.SetObject("pairtime-data", "litestream/0000/0000000000000001-0000000000000001.ltx", []byte("ltx"))
			},
			wantStderr: "pairtime-data holds litestream/0000/0000000000000001-0000000000000001.ltx. pairtimed serve refuses a database written with other keys"},
		{name: "-new-keys beside a database in the data directory warns that serve refuses it, naming the runbook's way to start over",
			args: []string{"-new-keys"}, environ: s3Environ, wantKeys: true,
			setup: func(t *testing.T, data, _ string, _ *objstoretest.Server) {
				writeFile(t, filepath.Join(data, config.DefaultDatabaseFile), "", 0o600)
			},
			wantStderr: "Starting over with new keys"},
		{name: "-new-keys with object storage off warns only to move the database aside, since no replica can bring it back",
			args: []string{"-new-keys", "-no-object-storage"}, wantKeys: true,
			setup: func(t *testing.T, data, _ string, _ *objstoretest.Server) {
				writeFile(t, filepath.Join(data, config.DefaultDatabaseFile), "", 0o600)
			},
			wantStderr: "so move it aside before the first start", notStderr: "litestream/"},
		{name: "-new-keys where only old key backups are left makes keys without a warning, since they stop nothing",
			args: []string{"-new-keys"}, environ: s3Environ, wantKeys: true,
			setup: func(t *testing.T, _, _ string, s3 *objstoretest.Server) {
				s3.SetObject("pairtime-data", backup.KeyBackupPrefix+"pairtime-keys-20261120T101500Z.tar.age", []byte("armored"))
			},
			notStderr: "warning"},
		{name: "a bucket init can't ask is refused, since it can't tell whether a server ran there",
			environ: s3Environ,
			setup: func(t *testing.T, _, _ string, s3 *objstoretest.Server) {
				s3.FailMatching(10, func(r *http.Request) bool { return r.URL.Query().Get("list-type") == "2" }, http.StatusForbidden, "AccessDenied", false)
			},
			wantCode: 1, wantErr: "can't tell whether pairtime-data holds another server's data"},
		{name: "a data directory this user can't write is refused, naming the chown that fixes it",
			environ: s3Environ,
			setup: func(t *testing.T, data, _ string, _ *objstoretest.Server) {
				notRoot(t)
				if err := os.Chmod(data, 0o500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(data, 0o700) })
			},
			wantCode: 1, wantErr: "chown 65532:65532 data"},
		{name: "a -config-out in a directory init can't write is refused before any key is made, naming -config-out -",
			environ: s3Environ,
			setup: func(t *testing.T, _, out string, _ *objstoretest.Server) {
				notRoot(t)
				dir := filepath.Dir(out)
				if err := os.Chmod(dir, 0o555); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(dir, 0o755) })
			},
			wantCode: 1, wantErr: "-config-out -"},
		{name: "a partial set of keys is refused",
			environ: s3Environ,
			setup: func(t *testing.T, data, _ string, _ *objstoretest.Server) {
				keys := backup.KeysDir(data)
				if err := os.Mkdir(keys, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(keys, backup.PepperFile), []byte("x\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: 1, wantErr: "holds only pepper"},
		{name: "a data directory that group can enter is refused before anything is written",
			environ: s3Environ,
			setup: func(t *testing.T, data, _ string, _ *objstoretest.Server) {
				if err := os.Chmod(data, 0o750); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: 1, wantErr: "chmod 700"},
		{name: "a missing data directory is refused",
			environ: s3Environ,
			setup: func(t *testing.T, data, _ string, _ *objstoretest.Server) {
				if err := os.Remove(data); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: 1, wantErr: "doesn't exist"},
		{name: "an http base URL off localhost is refused before anything is written",
			args: []string{"-base-url", "http://pairtime.example.org"}, environ: s3Environ,
			wantCode: 1, wantErr: "only https"},
		{name: "a bucket missing beside PAIRTIME_S3_ENDPOINT is refused",
			environ:  s3Environ[:2],
			wantCode: 1, wantErr: "PAIRTIME_S3_SNAPSHOT_BUCKET"},
		{name: "object storage without its access key is refused, as serve would refuse it",
			environ:  s3Environ[:3],
			wantCode: 1, wantErr: "PAIRTIME_S3_ACCESS_KEY_ID"},
		{name: "-base-url is required",
			args: []string{"-base-url", ""}, environ: s3Environ,
			wantCode: 1, wantErr: "-base-url is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := privateDir(t)
			out := filepath.Join(t.TempDir(), "pairtime.toml")
			s3 := objstoretest.New(t, "pairtime-data", "pairtime-snapshots")
			if tt.setup != nil {
				tt.setup(t, data, out, s3)
			}
			before, _ := os.ReadFile(out)
			args := []string{"init", "-base-url", "https://pairtime.example.org", "-data-dir", data}
			if tt.stdout {
				args = append(args, "-config-out", "-")
			} else {
				args = append(args, "-config-out", out)
			}
			args = append(args, tt.args...)
			environ := make([]string, len(tt.environ))
			for i, v := range tt.environ {
				environ[i] = strings.NewReplacer("=OUT", "="+out, s3URL, s3.URL).Replace(v)
			}
			e, stdout, stderr := testEnv(environ...)
			code := run(context.Background(), args, e)
			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d; stderr:\n%s", code, tt.wantCode, stderr)
			}
			_, err := os.Stat(filepath.Join(backup.KeysDir(data), backup.ServerKeyFile))
			if (err == nil) != tt.wantKeys {
				t.Errorf("server key exists: %v, want %v", err == nil, tt.wantKeys)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to say %q", stderr, tt.wantStderr)
			}
			if tt.notStderr != "" && strings.Contains(stderr.String(), tt.notStderr) {
				t.Errorf("stderr = %q, which says %q", stderr, tt.notStderr)
			}
			if code != 0 {
				if !strings.Contains(stderr.String(), tt.wantErr) {
					t.Errorf("stderr = %q, want it to say %q", stderr, tt.wantErr)
				}
				if after, _ := os.ReadFile(out); string(after) != string(before) {
					t.Errorf("a failed init changed %s", out)
				}
				return
			}

			notes, text := stdout.String(), ""
			if tt.stdout {
				notes, text = stderr.String(), stdout.String()
				if err := os.WriteFile(out, []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			} else {
				b, err := os.ReadFile(out)
				if err != nil {
					t.Fatal(err)
				}
				text = string(b)
				if fi, err := os.Stat(out); err != nil || fi.Mode().Perm() != 0o644 {
					t.Errorf("pairtime.toml mode %v, %v; want 0644, readable by the container's user", fi.Mode(), err)
				}
			}
			keys, err := backup.Load(backup.KeysDir(data))
			if err != nil {
				t.Fatal(err)
			}
			if pub := keys.Public(); !strings.Contains(notes, pub.ServerKeyID()) || !strings.Contains(notes, pub.EvidenceKeyID()) {
				t.Errorf("the notes don't name the key ids %s and %s:\n%s", pub.ServerKeyID(), pub.EvidenceKeyID(), notes)
			}
			cfg, err := config.Load(out, "", environ)
			if err != nil {
				t.Fatalf("the written file doesn't load: %v\n%s", err, text)
			}
			if cfg.DataDir != data || cfg.Database.Path != filepath.Join(data, config.DefaultDatabaseFile) {
				t.Errorf("data_dir %s, database.path %s", cfg.DataDir, cfg.Database.Path)
			}
			if tt.check != nil {
				tt.check(t, cfg, text)
			}
		})
	}
}

func TestTOMLString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "a plain value is quoted", in: "https://pairtime.example.org", want: `"https://pairtime.example.org"`},
		{name: "quotes and backslashes are escaped", in: "a\"b\x5cc", want: "\"a\x5c\"b\x5c\x5cc\""},
		{name: "a control character is escaped as a code point", in: "a\nb", want: "\"a\x5cu000Ab\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tomlString(tt.in); got != tt.want {
				t.Errorf("tomlString(%q) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}

func TestLitestreamQuotable(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "a key with a slash and a plus fits in single quotes", value: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"},
		{name: "a backslash fits too, since single quotes keep it as written", value: "abc" + "\x5c" + "def"},
		{name: "an Uptime Kuma push URL fits", value: "http://kuma.lan:3001/api/push/tok?status=up&msg=OK&ping="},
		{name: "a ' would end the quotes early, so it is refused", value: "it's", wantErr: true},
		{name: "a line break is refused", value: "two\nlines", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := litestreamQuotable(func(name string) string {
				if name == "PAIRTIME_S3_SECRET_ACCESS_KEY" {
					return tt.value
				}
				return ""
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, want an error: %v", err, tt.wantErr)
			}
			if err != nil && (strings.Contains(err.Error(), tt.value) || !strings.Contains(err.Error(), "PAIRTIME_S3_SECRET_ACCESS_KEY")) {
				t.Errorf("the error should name the variable and not quote its value: %v", err)
			}
		})
	}
}
