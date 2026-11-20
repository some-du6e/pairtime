// SPDX-License-Identifier: AGPL-3.0-or-later

package deploy

import (
	"os"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/server/config"
)

// line returns the first submatch of re, a multi-line pattern, in text, or
// "" when it doesn't match.
func line(re, text string) string {
	m := regexp.MustCompile(`(?m)` + re).FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[1]
}

// all returns every first submatch of re, a multi-line pattern, in text,
// joined by commas.
func all(re, text string) string {
	var got []string
	for _, m := range regexp.MustCompile(`(?m)`+re).FindAllStringSubmatch(text, -1) {
		got = append(got, m[1])
	}
	return strings.Join(got, ",")
}

func TestBundleFilesAgree(t *testing.T) {
	db := path.Join(config.DefaultDataDir, config.DefaultDatabaseFile)
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "litestream.yml replicates the database in pairtime.toml's default data_dir",
			got: line(`^\s*-\s*path:\s*(\S+)\s*$`, LitestreamYAML), want: db},
		{name: "litestream.yml restores the database when the file is missing (DESIGN 10.1)",
			got: line(`^\s*restore-if-db-not-exists:\s*(\S+)\s*$`, LitestreamYAML), want: "true"},
		{name: "litestream.yml runs pairtimed serve and tells it the replica path it replicates to",
			got:  line(`^exec:\s*pairtimed serve -litestream-replica (\S+)\s*$`, LitestreamYAML),
			want: line(`^\s+path:\s*(\S+)\s*$`, LitestreamYAML)},
		{name: "Litestream syncs every 10 s (DESIGN 10.3)",
			got: line(`^\s*sync-interval:\s*(\S+)\s*$`, LitestreamYAML), want: "10s"},
		{name: "Litestream takes a snapshot every 24 h (DESIGN 10.3)",
			got: line(`^\s*interval:\s*(\S+)\s*$`, LitestreamYAML), want: "24h"},
		{name: "Litestream keeps snapshots 72 h (DESIGN 10.3)",
			got: line(`^\s*retention:\s*(\S+)\s*$`, LitestreamYAML), want: "72h"},
		{name: "compose runs Litestream on the litestream.yml it mounts",
			got:  line(`^\s*command: \["litestream", "replicate", "-config", "([^"]+)"\]`, ComposeYAML),
			want: line(`^\s*- \./litestream\.yml:([^:]+):ro\s*$`, ComposeYAML)},
		{name: "compose mounts the data directory at pairtime.toml's default data_dir",
			got: line(`^\s*- \./data:(\S+)\s*$`, ComposeYAML), want: config.DefaultDataDir},
		{name: "compose mounts config/, read-only, where pairtimed looks for pairtime.toml",
			got: line(`^\s*- \./config:(\S+):ro\s*$`, ComposeYAML) + "/pairtime.toml", want: config.DefaultPath},
		{name: "compose's health check is pairtimed healthcheck on /healthz, which writes nothing, so Docker's checks cost no uploads",
			got: line(`^\s*test: (\[.*\])\s*$`, ComposeYAML), want: `["CMD", "pairtimed", "healthcheck", "-live"]`},
		{name: "Litestream reads new WAL every 10 s, so a busy minute is 6 uploads and not 60",
			got: line(`^\s*monitor-interval:\s*(\S+)\s*$`, LitestreamYAML), want: "10s"},
		{name: "Litestream lists level 0 and level 1 for removal every 5 minutes, not every 15 s",
			got: line(`^l0-retention-check-interval:\s*(\S+)\s*$`, LitestreamYAML), want: "5m"},
		{name: "Litestream compacts L1, L2 and L3 every 5 minutes, hour and 6 hours, which keeps L1 small",
			got: all(`^[ \t]*-[ \t]*interval:[ \t]*(\S+)[ \t]*$`, LitestreamYAML), want: "5m,1h,6h"},
		{name: "compose gives the server secrets.env, where pairtimed and Litestream read their variables",
			got: line(`^\s*env_file: (\./config/secrets\.env)\s*$`, ComposeYAML), want: "./config/secrets.env"},
		{name: "compose gives cloudflared only tunnel.env",
			got: line(`^\s*env_file: (\./config/tunnel\.env)\s*$`, ComposeYAML), want: "./config/tunnel.env"},
		{name: "pairtimed writes the day roots where the witness sidecar reads them",
			got:  line(`^\s*- \./witness-roots:(/\S+)\s*$`, ComposeYAML),
			want: line(`^\s*- \./witness-roots:(/\S+):ro\s*$`, ComposeYAML)},
		{name: "the witness sidecar runs the loop the bundle ships",
			got: line(`^\s*entrypoint: \["/bin/sh", "([^"]+)"\]`, ComposeYAML), want: line(`^\s*- \./witness/push\.sh:([^:]+):ro\s*$`, ComposeYAML)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got == "" || tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestImagesArePinned(t *testing.T) {
	images := regexp.MustCompile(`(?m)^\s*image:\s*(\S+)\s*$`).FindAllStringSubmatch(ComposeYAML, -1)
	pinned := regexp.MustCompile(`^[a-z0-9./-]+:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}$`)
	tests := []struct {
		name  string
		image string
		ok    bool
	}{}
	for _, m := range images {
		image := m[1]
		tests = append(tests, struct {
			name  string
			image string
			ok    bool
		}{
			name:  image + " is pinned by tag and digest, or is the placeholder from before the first release",
			image: image,
			ok:    pinned.MatchString(image) || image == "ghcr.io/some-du6e/pairtimed:UNRELEASED",
		})
	}
	if len(tests) != 3 {
		t.Fatalf("compose.yml has %d images, want pairtimed, cloudflared and witness", len(tests))
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("%s isn't pinned: an upgrade is a reviewed diff of tag and digest (DESIGN 10.4)", tt.image)
			}
		})
	}
}

func TestWitnessFiles(t *testing.T) {
	tests := []struct {
		name string
		path string
		mode os.FileMode
	}{
		{name: "the sidecar's loop is in the bundle, executable", path: "witness/push.sh", mode: 0o100},
		{name: "GitHub's host keys are in the bundle", path: "witness/known_hosts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fi, err := os.Stat(tt.path)
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode().Perm()&tt.mode != tt.mode {
				t.Errorf("%s has mode %v", tt.path, fi.Mode())
			}
			if !regexp.MustCompile(`(?m)^\s*- \./` + regexp.QuoteMeta(tt.path) + `:`).MatchString(ComposeYAML) {
				t.Errorf("compose.yml doesn't mount %s", tt.path)
			}
		})
	}
}

func TestLitestreamQuotesEveryVariable(t *testing.T) {
	var tests []struct{ name, line string }
	for _, l := range strings.Split(LitestreamYAML, "\n") {
		if strings.Contains(l, "${") && !strings.HasPrefix(strings.TrimSpace(l), "#") {
			tests = append(tests, struct{ name, line string }{
				name: strings.TrimSpace(l) + " holds its variable in single quotes, where YAML keeps a backslash as written",
				line: l,
			})
		}
	}
	if len(tests) != 6 {
		t.Fatalf("litestream.yml has %d lines with a variable, want 6", len(tests))
	}
	quoted := regexp.MustCompile(`:\s'\$\{[A-Z0-9_]+\}'\s*$`)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !quoted.MatchString(tt.line) {
				t.Errorf("%q isn't key: '${NAME}'", tt.line)
			}
		})
	}
}
