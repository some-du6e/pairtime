// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"bufio"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

// SecretPrefix starts the name of every secret pairtimed reads.
const SecretPrefix = "PAIRTIME_"

// The secrets pairtimed reads. Later work packages add theirs here.
const (
	// DatabaseURL is the Postgres connection URL, for database.engine =
	// "postgres".
	DatabaseURL = "PAIRTIME_DATABASE_URL"
	// HCAClientSecret is the client secret of pairtimed's Hack Club Auth
	// app, needed once hca.client_id is set (DESIGN 3.10).
	HCAClientSecret = "PAIRTIME_HCA_CLIENT_SECRET"
	// EmailKey is the 32-byte key that encrypts account emails at rest,
	// as 64 hex digits or base64 (DESIGN 4.4). "openssl rand -hex 32"
	// makes one. Sign-in needs it.
	EmailKey = "PAIRTIME_EMAIL_KEY"
	// S3AccessKeyID and S3SecretAccessKey are object storage's access key,
	// scoped to its two buckets (DESIGN 10.5 item 4). litestream.yml can
	// name the same variables, since both run from one secrets.env.
	S3AccessKeyID     = "PAIRTIME_S3_ACCESS_KEY_ID"
	S3SecretAccessKey = "PAIRTIME_S3_SECRET_ACCESS_KEY"
)

// Environment variables that say where the configuration is. They aren't
// secrets.
const (
	EnvConfig      = "PAIRTIME_CONFIG"
	EnvSecretsFile = "PAIRTIME_SECRETS_FILE"
)

// Secrets holds named values that never go in pairtime.toml or in logs:
// the PAIRTIME_* variables of the environment and of a secrets.env file.
//
// In the deploy bundle, compose reads config/secrets.env, mode 0600, and
// passes it to the container as environment variables (DESIGN 10.1). Run
// outside compose, pairtimed reads the file itself when given -secrets or
// PAIRTIME_SECRETS_FILE. Formatting a Secrets prints names, never values.
type Secrets struct {
	values map[string]string
}

// Get returns the value of the secret name, or "" when it isn't set.
func (s Secrets) Get(name string) string {
	return s.values[name]
}

// Names returns the names of the secrets that are set, sorted.
func (s Secrets) Names() []string {
	return slices.Sorted(maps.Keys(s.values))
}

// String lists the names only.
func (s Secrets) String() string {
	return "secrets[" + strings.Join(s.Names(), " ") + "]"
}

// GoString lists the names only, for %#v.
func (s Secrets) GoString() string {
	return s.String()
}

// LogValue lists the names only, for log/slog.
func (s Secrets) LogValue() slog.Value {
	return slog.StringValue(s.String())
}

// LoadSecrets reads file, when it isn't "", and then the PAIRTIME_*
// variables of environ, which win over the file's. A file that group or
// others can read is refused.
func LoadSecrets(file string, environ []string) (Secrets, error) {
	values := make(map[string]string)
	if file != "" {
		fromFile, err := readSecretsFile(file)
		if err != nil {
			return Secrets{}, err
		}
		maps.Copy(values, fromFile)
	}
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, SecretPrefix) || name == EnvConfig || name == EnvSecretsFile {
			continue
		}
		values[name] = value
	}
	return Secrets{values: values}, nil
}

// envName is a variable name as compose's env_file accepts it.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// readSecretsFile reads the subset of compose's env_file format that means
// the same thing to compose and to pairtimed: blank lines, lines starting
// with '#', and NAME=value lines, where a value wrapped in matching single
// or double quotes loses them and anything else is taken as written.
// Variables without the PAIRTIME_ prefix, such as Litestream's, are
// skipped. Errors name the line, never its text, which may hold a secret.
func readSecretsFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	defer f.Close()
	if runtime.GOOS != "windows" {
		fi, err := f.Stat()
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		if perm := fi.Mode().Perm(); perm&0o077 != 0 {
			return nil, fmt.Errorf("config: %s has mode %04o, and a secrets file must not be readable by group or others: chmod 600 %s (DESIGN 10.1)", path, perm, path)
		}
	}

	values := make(map[string]string)
	seen := make(map[string]bool)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || !envName.MatchString(name) {
			return nil, fmt.Errorf("config: %s line %d: want NAME=value", path, n)
		}
		if seen[name] {
			return nil, fmt.Errorf("config: %s line %d: %s is set twice", path, n, name)
		}
		seen[name] = true
		if strings.HasPrefix(name, SecretPrefix) {
			values[name] = unquote(strings.TrimSpace(value))
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("config: reading %s: %w", path, err)
	}
	return values, nil
}

// unquote removes one pair of matching quotes around v.
func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}
