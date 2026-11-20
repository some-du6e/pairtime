// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// full sets every key DESIGN 10.2 lists, and the server's own.
const full = `
base_url = "https://pairtime.example.org"
signups_open = true
profile_default = "ask"
scoring_version = "1.0.0"
listen = ":8081"
ops_listen = "127.0.0.1:9191"
data_dir = "/srv/pairtime"

[hca]
client_id = "hca-client"

[privacy_hub]
aud = "pairtime"
jwks_url = "https://privacy.example.org/.well-known/jwks.json"

[sentinel]
socket = "/run/pairtime/sentinel.sock"

[database]
engine = "sqlite"
path = "/srv/pairtime/db/pairtime.db"

[[reviewers]]
sub = "ident!abc123"
roles = ["qi", "program_reviewer:stardance"]

[[reviewers]]
sub = "ident!def456"
roles = ["fraud_squad", "spot_checker", "admin"]
`

// objectStorage is an [object_storage] section with the keys a deployment
// needs, and s3Secrets the secrets that go with it.
const objectStorage = `
[object_storage]
endpoint = "https://acct.r2.cloudflarestorage.com/"
bucket = "pairtime"
snapshot_bucket = "pairtime-snapshots"
`

var s3Secrets = []string{"PAIRTIME_S3_ACCESS_KEY_ID=AKIDR2", "PAIRTIME_S3_SECRET_ACCESS_KEY=hunter2-r2"}

func TestLoad(t *testing.T) {
	const base = "base_url = \"https://pairtime.example.org\"\n"
	tests := []struct {
		name    string
		toml    string
		environ []string
		wantErr string // substring; "" means Load must succeed
		check   func(t *testing.T, c *Config, dir string)
	}{
		{
			name: "a full file sets every DESIGN 10.2 key",
			toml: full,
			check: func(t *testing.T, c *Config, _ string) {
				want := Config{
					BaseURL:        "https://pairtime.example.org",
					SignupsOpen:    true,
					ProfileDefault: ProfileAsk,
					ScoringVersion: "1.0.0",
					Reviewers: []Reviewer{
						{Sub: "ident!abc123", Roles: []string{"qi", "program_reviewer:stardance"}},
						{Sub: "ident!def456", Roles: []string{"fraud_squad", "spot_checker", "admin"}},
					},
					HCA:        HCA{ClientID: "hca-client", Issuer: DefaultHCAIssuer},
					PrivacyHub: PrivacyHub{Aud: "pairtime", JWKSURL: "https://privacy.example.org/.well-known/jwks.json"},
					Sentinel:   Sentinel{Socket: "/run/pairtime/sentinel.sock"},
					Listen:     ":8081",
					OpsListen:  "127.0.0.1:9191",
					DataDir:    "/srv/pairtime",
					Database:   Database{Engine: EngineSQLite, Path: "/srv/pairtime/db/pairtime.db"},
				}
				got := *c
				got.Secrets, got.Path = Secrets{}, ""
				if !reflect.DeepEqual(got, want) {
					t.Errorf("Load =\n%+v\nwant\n%+v", got, want)
				}
			},
		},
		{
			name: "a file with only base_url takes every default",
			toml: base,
			check: func(t *testing.T, c *Config, _ string) {
				if c.SignupsOpen {
					t.Error("signups_open defaults to true, want false")
				}
				for _, f := range []struct{ key, got, want string }{
					{"profile_default", c.ProfileDefault, ProfilePublic},
					{"listen", c.Listen, ":8080"},
					{"ops_listen", c.OpsListen, "127.0.0.1:9091"},
					{"data_dir", c.DataDir, "/data"},
					{"database.engine", c.Database.Engine, "sqlite"},
					{"database.path", c.Database.Path, "/data/pairtime.db"},
					{"scoring_version", c.ScoringVersion, ""},
					{"sentinel.socket", c.Sentinel.Socket, ""},
					{"hca.client_id", c.HCA.ClientID, ""},
					{"hca.issuer", c.HCA.Issuer, "https://auth.hackclub.com"},
				} {
					if f.got != f.want {
						t.Errorf("%s = %q, want %q", f.key, f.got, f.want)
					}
				}
			},
		},
		{name: "base_url is required", toml: "", wantErr: "base_url: it is required"},
		{name: "an unknown key is refused, so a typo can't fall back to a default", toml: base + "signup_open = true\n", wantErr: "unknown keys: signup_open"},
		{name: "an unknown key inside a table is refused", toml: base + "[hca]\nclient = \"x\"\n", wantErr: "unknown keys: hca.client"},
		{name: "an unknown key inside a reviewer is refused", toml: base + "[[reviewers]]\nsub = \"a\"\nrole = \"qi\"\n", wantErr: "reviewers.role"},
		{name: "a value of the wrong type is refused", toml: base + "signups_open = \"no\"\n", wantErr: "signups_open"},
		{name: "base_url over plain http is refused", toml: "base_url = \"http://pairtime.example.org\"\n", wantErr: "only https"},
		{name: "base_url over http is allowed on localhost", toml: "base_url = \"http://localhost:8080\"\n"},
		{name: "base_url over http is allowed on a loopback address", toml: "base_url = \"http://127.0.0.1:8080\"\n"},
		{name: "base_url can't have a path", toml: "base_url = \"https://example.org/pairtime\"\n", wantErr: "must be an origin"},
		{name: "base_url can't carry a user", toml: "base_url = \"https://me@example.org\"\n", wantErr: "must be an origin"},
		{
			name: "a trailing slash on base_url is dropped",
			toml: "base_url = \"https://pairtime.example.org/\"\n",
			check: func(t *testing.T, c *Config, _ string) {
				if c.BaseURL != "https://pairtime.example.org" {
					t.Errorf("base_url = %q, want no trailing slash", c.BaseURL)
				}
			},
		},
		{name: "profile_default is public or ask, never private", toml: base + "profile_default = \"private\"\n", wantErr: "profile_default"},
		{name: "scoring_version must be X.Y.Z", toml: base + "scoring_version = \"0.x\"\n", wantErr: "scoring_version"},
		{name: "scoring_version can't have a leading zero", toml: base + "scoring_version = \"01.0.0\"\n", wantErr: "scoring_version"},
		{name: "a reviewer role must be one DESIGN 10.2 names", toml: base + "[[reviewers]]\nsub = \"a\"\nroles = [\"superuser\"]\n", wantErr: `unknown role "superuser"`},
		{name: "program_reviewer names a program", toml: base + "[[reviewers]]\nsub = \"a\"\nroles = [\"program_reviewer:\"]\n", wantErr: "needs a program name"},
		{name: "a reviewer needs at least one role", toml: base + "[[reviewers]]\nsub = \"a\"\nroles = []\n", wantErr: "has no roles"},
		{name: "a reviewer needs a sub", toml: base + "[[reviewers]]\nroles = [\"qi\"]\n", wantErr: "has no sub"},
		{name: "a reviewer sub is listed once", toml: base + "[[reviewers]]\nsub = \"a\"\nroles = [\"qi\"]\n[[reviewers]]\nsub = \"a\"\nroles = [\"admin\"]\n", wantErr: "listed twice"},
		{name: "a role is listed once per reviewer", toml: base + "[[reviewers]]\nsub = \"a\"\nroles = [\"qi\", \"qi\"]\n", wantErr: "twice"},
		{name: "hca.issuer over plain http is refused", toml: base + "[hca]\nissuer = \"http://auth.example.org\"\n", wantErr: "hca.issuer"},
		{name: "hca.issuer over http is allowed on localhost, for a fake HCA", toml: base + "[hca]\nissuer = \"http://localhost:8080/hca\"\n"},
		{name: "hca.issuer can't carry a query", toml: base + "[hca]\nissuer = \"https://auth.example.org/?x=1\"\n", wantErr: "hca.issuer"},
		{name: "privacy_hub.jwks_url must be https", toml: base + "[privacy_hub]\njwks_url = \"http://example.org/jwks.json\"\n", wantErr: "jwks_url"},
		{name: "sentinel.socket must be absolute", toml: base + "[sentinel]\nsocket = \"sentinel.sock\"\n", wantErr: "sentinel.socket"},
		{name: "listen must be host:port", toml: base + "listen = \"8080\"\n", wantErr: "listen is"},
		{name: "listen and ops_listen can't share a port", toml: base + "listen = \":9000\"\nops_listen = \":9000\"\n", wantErr: "own private port"},
		{name: "two listeners on port 0 each get a free port", toml: base + "listen = \"127.0.0.1:0\"\nops_listen = \"127.0.0.1:0\"\n"},
		{
			name: "relative paths are relative to the config file's directory",
			toml: base + "data_dir = \"data\"\n",
			check: func(t *testing.T, c *Config, dir string) {
				if want := filepath.Join(dir, "data"); c.DataDir != want {
					t.Errorf("data_dir = %q, want %q", c.DataDir, want)
				}
				if want := filepath.Join(dir, "data", "pairtime.db"); c.Database.Path != want {
					t.Errorf("database.path = %q, want %q", c.Database.Path, want)
				}
			},
		},
		{name: "database.engine is sqlite or postgres", toml: base + "[database]\nengine = \"mysql\"\n", wantErr: "database.engine"},
		{name: "database.path can't hold a '?'", toml: base + "[database]\npath = \"/data/x?mode=ro\"\n", wantErr: "can't contain"},
		{name: "postgres needs PAIRTIME_DATABASE_URL", toml: base + "[database]\nengine = \"postgres\"\n", wantErr: "PAIRTIME_DATABASE_URL"},
		{
			name:    "postgres takes its URL from the environment",
			toml:    base + "[database]\nengine = \"postgres\"\n",
			environ: []string{"PAIRTIME_DATABASE_URL=postgres://pt:hunter2@db:5432/pairtime"},
			check: func(t *testing.T, c *Config, _ string) {
				if got := c.Secrets.Get(DatabaseURL); got != "postgres://pt:hunter2@db:5432/pairtime" {
					t.Errorf("%s = %q", DatabaseURL, got)
				}
			},
		},
		{
			name:    "a bad postgres URL is refused without quoting its password",
			toml:    base + "[database]\nengine = \"postgres\"\n",
			environ: []string{"PAIRTIME_DATABASE_URL=mysql://pt:hunter2@db/pairtime"},
			wantErr: "must be a URL of the form postgres://",
		},
		{
			name: "object storage is off without an endpoint",
			toml: base,
			check: func(t *testing.T, c *Config, _ string) {
				if _, on := c.ObjectStore(); on || c.ObjectStorage.Enabled() {
					t.Error("object storage is on with no [object_storage] section")
				}
			},
		},
		{
			name:    "object storage takes R2's region by default and the access key from the secrets",
			toml:    base + objectStorage,
			environ: s3Secrets,
			check: func(t *testing.T, c *Config, _ string) {
				want := ObjectStorage{Endpoint: "https://acct.r2.cloudflarestorage.com", Region: "auto", Bucket: "pairtime", SnapshotBucket: "pairtime-snapshots"}
				if c.ObjectStorage != want {
					t.Errorf("object_storage = %+v, want %+v", c.ObjectStorage, want)
				}
				oc, on := c.ObjectStore()
				if !on || oc.Endpoint != want.Endpoint || oc.Region != "auto" || oc.Credentials.AccessKeyID != "AKIDR2" || oc.Credentials.SecretAccessKey != "hunter2-r2" {
					t.Errorf("ObjectStore() = %v, %v", oc.Endpoint, on)
				}
			},
		},
		{
			name:    "object storage takes a region and virtual-hosted URLs",
			toml:    base + objectStorage + "region = \"eu-central-1\"\nvirtual_host = true\n",
			environ: s3Secrets,
			check: func(t *testing.T, c *Config, _ string) {
				if oc, _ := c.ObjectStore(); oc.Region != "eu-central-1" || !oc.VirtualHost {
					t.Errorf("ObjectStore() region %q, virtual host %v", oc.Region, oc.VirtualHost)
				}
			},
		},
		{name: "object storage needs its access key id", toml: base + objectStorage, environ: s3Secrets[1:], wantErr: "set the secret PAIRTIME_S3_ACCESS_KEY_ID"},
		{name: "object storage needs its secret access key", toml: base + objectStorage, environ: s3Secrets[:1], wantErr: "set the secret PAIRTIME_S3_SECRET_ACCESS_KEY"},
		{
			name:    "object storage needs a snapshot bucket",
			toml:    base + "[object_storage]\nendpoint = \"https://acct.r2.cloudflarestorage.com\"\nbucket = \"pairtime\"\n",
			environ: s3Secrets,
			wantErr: "object_storage.snapshot_bucket is required",
		},
		{
			name:    "the snapshot bucket can't be the main one, whose evidence its lifecycle would delete",
			toml:    base + "[object_storage]\nendpoint = \"https://acct.r2.cloudflarestorage.com\"\nbucket = \"pairtime\"\nsnapshot_bucket = \"pairtime\"\n",
			environ: s3Secrets,
			wantErr: "30-day lifecycle",
		},
		{
			name:    "a bucket name outside R2's and S3's rules is refused",
			toml:    base + "[object_storage]\nendpoint = \"https://acct.r2.cloudflarestorage.com\"\nbucket = \"Pairtime_Main\"\nsnapshot_bucket = \"pairtime-snapshots\"\n",
			environ: s3Secrets,
			wantErr: "object_storage.bucket",
		},
		{
			name:    "an object storage endpoint over http is refused off loopback",
			toml:    base + "[object_storage]\nendpoint = \"http://s3.example\"\nbucket = \"a-bucket\"\nsnapshot_bucket = \"b-bucket\"\n",
			environ: s3Secrets,
			wantErr: "only https",
		},
		{
			name:    "an object storage endpoint with a key in it is refused without quoting it",
			toml:    base + "[object_storage]\nendpoint = \"https://AKIDR2:hunter2@acct.r2.cloudflarestorage.com\"\nbucket = \"a-bucket\"\nsnapshot_bucket = \"b-bucket\"\n",
			environ: s3Secrets,
			wantErr: "user or password",
		},
		{name: "object storage keys without an endpoint are refused", toml: base + "[object_storage]\nbucket = \"pairtime\"\n", wantErr: "object_storage.endpoint is empty"},
		{name: "an unknown object_storage key is refused", toml: base + objectStorage + "buckets = \"x\"\n", environ: s3Secrets, wantErr: "unknown keys: object_storage.buckets"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "pairtime.toml")
			if err := os.WriteFile(path, []byte(tt.toml), 0o600); err != nil {
				t.Fatal(err)
			}
			c, err := Load(path, "", tt.environ)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Load error = %v, want one containing %q", err, tt.wantErr)
				}
				if strings.Contains(err.Error(), "hunter2") {
					t.Errorf("Load error quotes a password: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if c.Path != path {
				t.Errorf("Path = %q, want %q", c.Path, path)
			}
			if tt.check != nil {
				tt.check(t, c, dir)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "a missing config file is an error naming it", path: filepath.Join(t.TempDir(), "missing.toml")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(tt.path, "", nil)
			if err == nil || !strings.Contains(err.Error(), tt.path) {
				t.Errorf("Load error = %v, want one naming %s", err, tt.path)
			}
		})
	}
}
