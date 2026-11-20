// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/some-du6e/pairtime/deploy"
	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/version"
)

const initSummary = "Make the server's keys in <data_dir>/keys and write pairtime.toml from the deploy template (DESIGN 10.5 step 2)"

func init() {
	register(&command{name: "init", summary: initSummary, run: runInit})
}

// The variables init reads the buckets from: the ones litestream.yml
// reads, from the same secrets.env (deploy/secrets.env.example).
const (
	envS3Endpoint       = "PAIRTIME_S3_ENDPOINT"
	envS3Region         = "PAIRTIME_S3_REGION"
	envS3Bucket         = "PAIRTIME_S3_BUCKET"
	envS3SnapshotBucket = "PAIRTIME_S3_SNAPSHOT_BUCKET"
)

// templateData is what pairtime.toml.tmpl is rendered with.
type templateData struct {
	BaseURL       string
	Version       string
	ConfigVersion int
	DataDir       string
	DatabasePath  string
	HCAClientID   string
	S3            struct{ Endpoint, Region, Bucket, SnapshotBucket string }
}

func runInit(ctx context.Context, e *env, args []string) int {
	fs := newFlagSet("init", initSummary, e)
	baseURL := fs.String("base-url", "", "the server's public origin, such as https://pairtime.example.org (required)")
	clientID := fs.String("hca-client-id", "", "the Hack Club Auth app's client id, if there is one yet")
	dataDir := fs.String("data-dir", config.DefaultDataDir, "where the database and keys/ live; mode 0700")
	defaultOut := e.getenv(config.EnvConfig)
	if defaultOut == "" {
		defaultOut = config.DefaultPath
	}
	out := fs.String("config-out", defaultOut, `where to write pairtime.toml, never over an existing file; "-" writes it to standard output, which compose's read-only config mount needs`)
	if code, done := parse(fs, args); done {
		return code
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(e.stderr, "pairtimed init: "+format+"\n", a...)
		return 1
	}
	if *baseURL == "" {
		return fail("-base-url is required, such as -base-url https://pairtime.example.org")
	}
	// Notes go where the configuration doesn't.
	notes := e.stdout
	if *out == "-" {
		notes = e.stderr
	}

	dir, err := filepath.Abs(*dataDir)
	if err != nil {
		return fail("%v", err)
	}
	if err := checkDataDir(dir); err != nil {
		return fail("%v", err)
	}
	if *out != "-" {
		if _, err := os.Lstat(*out); err == nil {
			return fail("%s exists, and init never replaces a pairtime.toml. Move it aside to write a new one", *out)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fail("%v", err)
		}
	}

	data := templateData{
		Version:       version.Get().Version,
		ConfigVersion: config.ConfigVersion,
		DataDir:       dir,
		DatabasePath:  filepath.Join(dir, config.DefaultDatabaseFile),
		HCAClientID:   *clientID,
	}
	data.S3.Endpoint = e.getenv(envS3Endpoint)
	data.S3.Region = e.getenv(envS3Region)
	data.S3.Bucket = e.getenv(envS3Bucket)
	data.S3.SnapshotBucket = e.getenv(envS3SnapshotBucket)
	if data.S3.Endpoint != "" && data.S3.Region == "" {
		data.S3.Region = "auto"
	}
	// The base URL goes in as Load would write it, so the file says what
	// the server will use.
	data.BaseURL = config.CanonicalOrigin(*baseURL)
	if data.BaseURL == "" {
		data.BaseURL = *baseURL
	}
	toml, err := renderConfig(data, e.environ)
	if err != nil {
		return fail("%v", err)
	}

	keysDir := backup.KeysDir(dir)
	keys, made, err := ensureKeys(keysDir)
	if err != nil {
		return fail("%v", err)
	}

	if *out == "-" {
		if _, err := e.stdout.Write(toml); err != nil {
			return fail("%v", err)
		}
	} else if err := writeConfigFile(*out, toml); err != nil {
		return fail("%v. The keys in %s are in place; run init again, which keeps them, once it can write the file", err, keysDir)
	} else {
		fmt.Fprintf(notes, "wrote %s\n", *out)
	}

	pub := keys.Public()
	if made {
		fmt.Fprintf(notes, "made the keys in %s, each with mode 0600: %s\n", keysDir, strings.Join(backup.KeyFiles, ", "))
	} else {
		fmt.Fprintf(notes, "kept the keys already in %s\n", keysDir)
	}
	fmt.Fprintf(notes, "server key (Ed25519)  id %s  public key %s\n", pub.ServerKeyID(), base64.StdEncoding.EncodeToString(pub.Server))
	fmt.Fprintf(notes, "evidence key (P-256)  id %s  public key %s\n", pub.EvidenceKeyID(), base64.StdEncoding.EncodeToString(pub.Evidence.Bytes()))
	fmt.Fprintf(notes, "Back the keys up to Karim's age key before anything else (deploy/runbook.md, step 8):\n  docker compose run --rm -T pairtimed pairtimed keys backup -upload > pairtime-keys-<time>.tar.age\n")
	return 0
}

// renderConfig renders the template and checks that what it wrote loads,
// with the secrets of environ, before anything is written.
func renderConfig(data templateData, environ []string) ([]byte, error) {
	t, err := template.New("pairtime.toml.tmpl").Option("missingkey=error").Funcs(template.FuncMap{"toml": tomlString}).Parse(deploy.ConfigTemplate)
	if err != nil {
		return nil, fmt.Errorf("the template: %w", err)
	}
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return nil, fmt.Errorf("the template: %w", err)
	}
	if data.S3.Endpoint != "" && (data.S3.Bucket == "" || data.S3.SnapshotBucket == "") {
		return nil, fmt.Errorf("%s is set, so set %s and %s too: one bucket for the replica and evidence, a second for snapshots (DESIGN 10.5 item 4)", envS3Endpoint, envS3Bucket, envS3SnapshotBucket)
	}
	tmp, err := os.CreateTemp("", "pairtime-init-*.toml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b.Bytes()); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if _, err := config.Load(tmp.Name(), "", environ); err != nil {
		return nil, fmt.Errorf("the pairtime.toml init would write doesn't load, so it wrote nothing: %w", err)
	}
	return b.Bytes(), nil
}

// ensureKeys loads the keys in dir when all of them are there, and makes
// them when none is. made says which. A partial set is an error: init
// never guesses which half of a key set to keep.
func ensureKeys(dir string) (keys *backup.Keys, made bool, err error) {
	var there []string
	for _, name := range backup.KeyFiles {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			there = append(there, name)
		}
	}
	switch len(there) {
	case 0:
		keys, err := backup.Generate(dir, rand.Reader)
		return keys, true, err
	case len(backup.KeyFiles):
		keys, err := backup.Load(dir)
		return keys, false, err
	default:
		return nil, false, fmt.Errorf("%s holds only %s of the keys %s; restore the rest from the key backup (deploy/runbook.md, Restoring the keys). init makes keys only where there are none",
			dir, strings.Join(there, ", "), strings.Join(backup.KeyFiles, ", "))
	}
}

// writeConfigFile writes pairtime.toml, which must not exist, with mode 0644:
// it holds no secret, and the container's user reads it.
func writeConfigFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}

// checkDataDir refuses a data directory that is missing, or that group or
// others can enter, as pairtimed serve would, since Litestream writes
// copies of the database there (DESIGN 10.1).
func checkDataDir(dir string) error {
	fi, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("the data directory %s doesn't exist; make it first with mode 0700, owned by the server's user (deploy/runbook.md, step 2)", dir)
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s isn't a directory", dir)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("the data directory %s has mode %04o, so group or others can enter it, and pairtimed serve would refuse it: chmod 700 %s (DESIGN 10.1)", dir, fi.Mode().Perm(), dir)
	}
	return nil
}

// tomlString quotes s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\x5c':
			b.WriteByte('\x5c')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\x5cu%04X", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
