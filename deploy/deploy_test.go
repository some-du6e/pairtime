// SPDX-License-Identifier: AGPL-3.0-or-later

package deploy

import (
	"cmp"
	"errors"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"
	"time"

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
	// The image's Dockerfile, at the repository's root.
	data, err := os.ReadFile("../Dockerfile.pairtimed")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(data)
	litestreamConfig := line(`^\s*command: \["litestream", "replicate", "-config", "([^"]+)"\]`, composeYAML)
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
		{name: "compose runs Litestream on the litestream.yml the image carries",
			got:  litestreamConfig,
			want: line(`^COPY --chmod=0644 deploy/litestream\.yml (\S+)\s*$`, dockerfile)},
		{name: "the frozen litestream.yml compose mounts, for an image from before it carried one, goes where the image keeps its own",
			got:  cmp.Or(line(`^\s*- \./litestream\.frozen\.yml:([^:]+):ro\s*$`, composeYAML), litestreamConfig),
			want: litestreamConfig},
		{name: "compose mounts the data directory at pairtime.toml's default data_dir",
			got: line(`^\s*- \./data:(\S+)\s*$`, composeYAML), want: config.DefaultDataDir},
		{name: "compose mounts config/, read-only, where pairtimed looks for pairtime.toml",
			got: line(`^\s*- \./config:(\S+):ro\s*$`, composeYAML) + "/pairtime.toml", want: config.DefaultPath},
		{name: "compose's health check is pairtimed healthcheck on /healthz, which writes nothing, so Docker's checks cost no uploads",
			got: line(`^\s*test: (\[.*\])\s*$`, composeYAML), want: `["CMD", "pairtimed", "healthcheck", "-live"]`},
		{name: "Litestream reads new WAL every 10 s, so a busy minute is 6 uploads and not 60",
			got: line(`^\s*monitor-interval:\s*(\S+)\s*$`, LitestreamYAML), want: "10s"},
		{name: "Litestream lists level 0 and level 1 for removal every 5 minutes, not every 15 s",
			got: line(`^l0-retention-check-interval:\s*(\S+)\s*$`, LitestreamYAML), want: "5m"},
		{name: "Litestream compacts L1, L2 and L3 every 5 minutes, hour and 6 hours, which keeps L1 small",
			got: all(`^[ \t]*-[ \t]*interval:[ \t]*(\S+)[ \t]*$`, LitestreamYAML), want: "5m,1h,6h"},
		{name: "compose gives the server secrets.env, where pairtimed and Litestream read their variables",
			got: line(`^\s*env_file: (\./config/secrets\.env)\s*$`, composeYAML), want: "./config/secrets.env"},
		{name: "compose gives cloudflared only tunnel.env",
			got: line(`^\s*env_file: (\./config/tunnel\.env)\s*$`, composeYAML), want: "./config/tunnel.env"},
		{name: "pairtimed writes the day roots where the witness sidecar reads them",
			got:  line(`^\s*- \./witness-roots:(/\S+)\s*$`, composeYAML),
			want: line(`^\s*- \./witness-roots:(/\S+):ro\s*$`, composeYAML)},
		{name: "SQLite's temporary files go to the data directory's disk, off the container's small /tmp",
			got: line(`^\s*SQLITE_TMPDIR: (\S+)\s*$`, composeYAML), want: path.Join(config.DefaultDataDir, "tmp")},
		{name: "the container's /tmp is a tmpfs of 256 MiB, so nothing written there takes the host's memory",
			got: line(`(?s)- type: tmpfs\n\s+target: /tmp\n\s+tmpfs:\n\s+size: (\d+)\n`, composeYAML), want: "268435456"},
		{name: "the witness sidecar runs the loop the bundle ships",
			got: line(`^\s*entrypoint: \["/bin/sh", "([^"]+)"\]`, composeYAML), want: line(`^\s*- \./witness/push\.sh:([^:]+):ro\s*$`, composeYAML)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got == "" || tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

// TestFrozenLitestream checks litestream.frozen.yml, the copy of
// litestream.yml that compose.yml mounts while it pins an image from before
// #123, which carries none of its own (#119). The copy goes with its mount,
// and until then it replicates the database litestream.yml does, to the
// same replica, so the next image, which reads its own copy, carries on
// from the replica the pinned one wrote.
func TestFrozenLitestream(t *testing.T) {
	data, err := os.ReadFile("litestream.frozen.yml")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	exists := err == nil
	frozen := string(data)
	mounted := regexp.MustCompile(`(?m)^\s*- \./litestream\.frozen\.yml:/etc/litestream\.yml:ro\s*$`).MatchString(composeYAML)
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "litestream.frozen.yml is in the bundle exactly while compose.yml mounts it, so the PR that drops the mount drops the file", ok: exists == mounted},
	}
	if exists {
		grace, err1 := time.ParseDuration(line(`^\s*stop_grace_period: (\S+)\s*$`, composeYAML))
		shutdown, err2 := time.ParseDuration(line(`^shutdown-timeout: (\S+)\s*$`, frozen))
		dbPath := `^\s*-\s*path:\s*(\S+)\s*$`
		replicaPath := `^\s+path:\s*(\S+)\s*$`
		quoted := regexp.MustCompile(`:\s'\$\{[A-Z0-9_]+\}'\s*$`)
		allQuoted := true
		for _, l := range strings.Split(frozen, "\n") {
			if strings.Contains(l, "${") && !strings.HasPrefix(strings.TrimSpace(l), "#") && !quoted.MatchString(l) {
				allQuoted = false
			}
		}
		tests = append(tests, []struct {
			name string
			ok   bool
		}{
			{name: "the frozen copy replicates the database litestream.yml replicates",
				ok: line(dbPath, frozen) != "" && line(dbPath, frozen) == line(dbPath, LitestreamYAML)},
			{name: "the frozen copy replicates to litestream.yml's replica path, so the next image carries on from the replica",
				ok: line(replicaPath, frozen) != "" && line(replicaPath, frozen) == line(replicaPath, LitestreamYAML)},
			{name: "the frozen copy restores the database when the file is missing (DESIGN 10.1)",
				ok: line(`^\s*restore-if-db-not-exists:\s*(\S+)\s*$`, frozen) == "true"},
			{name: "the frozen copy runs pairtimed serve and tells it the replica path it replicates to",
				ok: line(`^exec:\s*pairtimed serve -litestream-replica (\S+)\s*$`, frozen) == line(replicaPath, frozen)},
			{name: "the frozen copy gives pairtimed and its last sync less time than Docker gives the container",
				ok: errors.Join(err1, err2) == nil && shutdown < grace},
			{name: "the frozen copy holds each variable in single quotes, as litestream.yml does",
				ok: allQuoted && strings.Contains(frozen, "${PAIRTIME_S3_SECRET_ACCESS_KEY}")},
		}...)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("litestream.frozen.yml (there: %v) and compose.yml (mounts it: %v) break this", exists, mounted)
			}
		})
	}
}

func TestShutdownFitsTheGracePeriod(t *testing.T) {
	grace, err1 := time.ParseDuration(line(`^\s*stop_grace_period: (\S+)\s*$`, composeYAML))
	litestream, err2 := time.ParseDuration(line(`^shutdown-timeout: (\S+)\s*$`, LitestreamYAML))
	if err := errors.Join(err1, err2); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "Litestream gives pairtimed and its last sync less time than Docker gives the container, so Docker never kills the sync", ok: litestream < grace},
		{name: "Litestream's shutdown outlasts pairtimed's own 20 s for requests in flight, with time left to sync", ok: litestream >= 20*time.Second+30*time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("litestream.yml's shutdown-timeout is %s and compose.yml's stop_grace_period %s", litestream, grace)
			}
		})
	}
}

func TestImagesArePinned(t *testing.T) {
	images := regexp.MustCompile(`(?m)^\s*image:\s*(\S+)\s*$`).FindAllStringSubmatch(composeYAML, -1)
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
			if !regexp.MustCompile(`(?m)^\s*- \./` + regexp.QuoteMeta(tt.path) + `:`).MatchString(composeYAML) {
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
