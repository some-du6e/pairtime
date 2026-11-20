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
	"time"

	"github.com/some-du6e/pairtime/deploy"
	"github.com/some-du6e/pairtime/internal/server/accounts"
	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/objstore"
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
	out := fs.String("config-out", defaultOut, `where to write pairtime.toml; "-" writes it to standard output, which compose's read-only config mount needs`)
	force := fs.Bool("force", false, "write pairtime.toml even where one exists, losing operators and every edit made to it; the keys are never replaced")
	newKeys := fs.Bool("new-keys", false, "make new keys even where the data directory or the main bucket shows that a server ran before, whose keys should come from their backup instead (deploy/runbook.md, Restoring the keys)")
	noObjects := fs.Bool("no-object-storage", false, "write pairtime.toml with object storage off, for a server without buckets, which the deploy bundle can't run")
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
	// What serve would refuse at start, refused here before anything is
	// written (#133).
	switch endpoint := e.getenv(envS3Endpoint); {
	case endpoint == "" && !*noObjects:
		return fail("%s isn't set, so pairtime.toml would turn object storage off, and pairtimed serve, which the deploy bundle's Litestream starts with -litestream-replica, would refuse to start. Fill in the PAIRTIME_S3_* lines of config/secrets.env (deploy/runbook.md, step 3), or pass -no-object-storage for a server without buckets", envS3Endpoint)
	case endpoint != "" && *noObjects:
		return fail("-no-object-storage, and %s is set too: leave one of them out", envS3Endpoint)
	}
	if *clientID != "" {
		var missing []string
		for _, name := range []string{config.HCAClientSecret, config.EmailKey} {
			if e.getenv(name) == "" {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			return fail("-hca-client-id is set, so pairtimed serve needs %s as well, or it won't start: add them to config/secrets.env (deploy/runbook.md, step 3)", strings.Join(missing, " and "))
		}
		if _, err := accounts.ParseEmailKey(e.getenv(config.EmailKey)); err != nil {
			return fail("%s: %v", config.EmailKey, err)
		}
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
	// The file init would replace: -config-out, or with "-" the one
	// pairtimed reads, where the shell's redirect will put it.
	target := *out
	if target == "-" {
		target = defaultOut
	}
	if !*force {
		if _, err := os.Lstat(target); err == nil {
			return fail("%s exists, and init doesn't replace a pairtime.toml unless -force: a new one would lose operators and every edit. To add something init would have written, such as hca.client_id, edit the file by hand (deploy/runbook.md, step 7)", target)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fail("%v", err)
		}
	}
	// The file has to be writable before any key is made: in compose's
	// container, config/ is mounted read-only (#133).
	if *out != "-" {
		if err := probeWritable(filepath.Dir(*out)); err != nil {
			return fail("can't write %s (%v). compose mounts config/ read-only, so in the container run init with -config-out - and redirect it to config/pairtime.toml.new, as deploy/runbook.md step 7 does", *out, err)
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
	if err := litestreamQuotable(e.getenv); err != nil {
		return fail("%v", err)
	}
	toml, cfg, err := renderConfig(data, e.environ)
	if err != nil {
		return fail("%v", err)
	}

	keysDir := backup.KeysDir(dir)
	if !*newKeys && !anyKey(keysDir) {
		objs, err := openBuckets(cfg)
		if err != nil {
			return fail("%v", err)
		}
		lctx, cancel := context.WithTimeout(ctx, time.Minute)
		why, err := usedBefore(lctx, dir, objs)
		cancel()
		if err != nil {
			return fail("%v. Pass -new-keys only if this server is to start over with new keys", err)
		}
		if why != "" {
			return fail("%s, so a server ran here before, and %s holds no keys. Restore that server's keys from their backup first (deploy/runbook.md, Restoring the keys), then run init again, which keeps them. New keys would break every receipt and every project's identity, and pairtimed serve refuses a database written with other keys. Only to start over with new keys anyway, pass -new-keys", why, keysDir)
		}
	}
	keys, made, err := ensureKeys(keysDir)
	if err != nil {
		return fail("%v", err)
	}

	if *out == "-" {
		if _, err := e.stdout.Write(toml); err != nil {
			return fail("%v", err)
		}
	} else if err := writeConfigFile(*out, toml, *force); err != nil {
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
// with the secrets of environ, before anything is written. It returns the
// file and the configuration it loads as.
func renderConfig(data templateData, environ []string) ([]byte, *config.Config, error) {
	t, err := template.New("pairtime.toml.tmpl").Option("missingkey=error").Funcs(template.FuncMap{"toml": tomlString}).Parse(deploy.ConfigTemplate)
	if err != nil {
		return nil, nil, fmt.Errorf("the template: %w", err)
	}
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return nil, nil, fmt.Errorf("the template: %w", err)
	}
	if data.S3.Endpoint != "" && (data.S3.Bucket == "" || data.S3.SnapshotBucket == "") {
		return nil, nil, fmt.Errorf("%s is set, so set %s and %s too: one bucket for the replica and evidence, a second for snapshots (DESIGN 10.5 item 4)", envS3Endpoint, envS3Bucket, envS3SnapshotBucket)
	}
	tmp, err := os.CreateTemp("", "pairtime-init-*.toml")
	if err != nil {
		return nil, nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b.Bytes()); err != nil {
		tmp.Close()
		return nil, nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, nil, err
	}
	cfg, err := config.Load(tmp.Name(), "", environ)
	if err != nil {
		return nil, nil, fmt.Errorf("the pairtime.toml init would write doesn't load, so it wrote nothing: %w", err)
	}
	return b.Bytes(), cfg, nil
}

// anyKey reports whether dir holds any of the key files.
func anyKey(dir string) bool {
	for _, name := range backup.KeyFiles {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// usedBefore says what shows that a server ran on this data directory or
// these buckets before, or "" when nothing does: a database in dataDir,
// or Litestream's replica, evidence or key backups in the main bucket.
// init makes keys only where none are, so on a host that was rebuilt or
// moved, or lost its disk, it would otherwise make new ones for the old
// database that Litestream then restores (#133).
func usedBefore(ctx context.Context, dataDir string, objs *buckets) (string, error) {
	db := filepath.Join(dataDir, config.DefaultDatabaseFile)
	if _, err := os.Lstat(db); err == nil {
		return db + " exists", nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if objs == nil {
		return "", nil
	}
	// litestream/ is the replica's path in deploy/litestream.yml.
	for _, prefix := range []string{"litestream/", objstore.EvidencePrefix, backup.KeyBackupPrefix} {
		for o, err := range objs.main.List(ctx, prefix) {
			if err != nil {
				return "", fmt.Errorf("can't tell whether %s holds another server's data: %w", objs.main.Name(), err)
			}
			return fmt.Sprintf("%s holds %s", objs.main.Name(), o.Key), nil
		}
	}
	return "", nil
}

// probeWritable checks that this user can make a file in dir.
func probeWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".pairtime-probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
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

// writeConfigFile writes pairtime.toml with mode 0644: it holds no secret,
// and the container's user reads it. The file must not exist unless
// replace is set, and then the new file takes its place in one rename.
func writeConfigFile(path string, data []byte, replace bool) error {
	name := path
	if replace {
		name = path + ".new"
		os.Remove(name)
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && replace {
		err = os.Rename(name, path)
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}

// checkDataDir refuses a data directory that is missing, that group or
// others can enter, as pairtimed serve would, since Litestream writes
// copies of the database there (DESIGN 10.1), or that this user can't
// write.
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
	// One that root still owns passes the mode check, and then fails at
	// the first key with a bare "permission denied" (#133).
	if err := probeWritable(dir); err != nil {
		return fmt.Errorf("can't write the data directory %s (%v). It must belong to the server's user, 65532 in the image: sudo chown 65532:65532 data (deploy/runbook.md, step 2)", dir, err)
	}
	return nil
}

// litestreamValues are the variables litestream.yml reads, each inside
// single quotes.
var litestreamValues = []string{envS3Endpoint, envS3Region, envS3Bucket,
	"PAIRTIME_S3_ACCESS_KEY_ID", "PAIRTIME_S3_SECRET_ACCESS_KEY", envLitestreamHeartbeatURL}

// litestreamQuotable refuses a value litestream.yml's single quotes can't
// hold: Litestream puts the value in before YAML reads the file, and in
// single quotes YAML keeps a backslash as written but ends the string at
// a ', and no quoting holds a line break. Errors name the variable, never
// its value.
func litestreamQuotable(get func(string) string) error {
	for _, name := range litestreamValues {
		if strings.ContainsFunc(get(name), func(r rune) bool { return r == '\'' || r < ' ' || r == 0x7f }) {
			return fmt.Errorf("%s holds a ' or a control character, which litestream.yml can't take inside its single quotes; Litestream wouldn't start. Make a new key or URL without one", name)
		}
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
