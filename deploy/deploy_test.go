// SPDX-License-Identifier: AGPL-3.0-or-later

package deploy

import (
	"path"
	"regexp"
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
		{name: "compose's health check is pairtimed healthcheck, since the image has no shell",
			got: line(`^\s*test: (\[.*\])\s*$`, ComposeYAML), want: `["CMD", "pairtimed", "healthcheck"]`},
		{name: "compose gives the server secrets.env, where pairtimed and Litestream read their variables",
			got: line(`^\s*env_file: (\./config/secrets\.env)\s*$`, ComposeYAML), want: "./config/secrets.env"},
		{name: "compose gives cloudflared only tunnel.env",
			got: line(`^\s*env_file: (\./config/tunnel\.env)\s*$`, ComposeYAML), want: "./config/tunnel.env"},
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
	if len(tests) != 2 {
		t.Fatalf("compose.yml has %d images, want pairtimed and cloudflared", len(tests))
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("%s isn't pinned: an upgrade is a reviewed diff of tag and digest (DESIGN 10.4)", tt.image)
			}
		})
	}
}
