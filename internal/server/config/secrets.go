// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"unicode"
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
	// EmailKey is the 32-byte key that encrypts account emails at rest
	// and keys their lookup index, as 64 hex digits or base64 (DESIGN
	// 4.4). "openssl rand -hex 32" makes one. Sign-in needs it. Losing it
	// loses the stored emails and breaks lookups by email until each
	// owner signs in again, so back it up with /data/keys (DESIGN 10.2).
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

// readSecretsFile reads the part of compose's env_file format that compose
// and pairtimed read the same way, and refuses every line outside it, so
// -secrets and compose's env_file can't hand pairtimed different secrets.
// That part is blank lines, comment lines starting with '#', and
// NAME=value lines, where NAME is letters, digits and '_', not starting
// with a digit. Spaces and tabs at the ends of a line and around NAME and
// value don't count. The value is one of:
//
//   - in single quotes, taken as written. It can't hold a ' or end in a
//     '\', since compose reads \' as a quote inside the value.
//   - in double quotes, with no '$' or '\', which compose expands.
//   - unquoted, with no '$' or '\', and no '#' after a space or tab, which
//     compose reads as the start of a comment.
//
// Nothing may follow a closing quote, and no line may hold a control
// character other than tab or a space other than ' ' and tab. Every line is
// checked, even the ones pairtimed skips, since an unclosed quote makes
// compose read the next lines into its value. Single quotes keep '$' and
// '\'; a value that needs a ' as well goes in the environment instead.
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
		line := strings.Trim(sc.Text(), " \t")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.ContainsFunc(line, oddSpace) {
			return nil, fmt.Errorf("config: %s line %d holds a control character or a space other than ' ' and tab, which compose and pairtimed trim differently", path, n)
		}
		name, raw, ok := strings.Cut(line, "=")
		name = strings.Trim(name, " \t")
		if !ok || !envName.MatchString(name) {
			return nil, fmt.Errorf("config: %s line %d: want NAME=value", path, n)
		}
		if seen[name] {
			return nil, fmt.Errorf("config: %s line %d: %s is set twice", path, n, name)
		}
		seen[name] = true
		value, err := envValue(raw)
		if err != nil {
			return nil, fmt.Errorf("config: %s line %d: %w", path, n, err)
		}
		if strings.HasPrefix(name, SecretPrefix) {
			values[name] = value
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("config: reading %s: %w", path, err)
	}
	return values, nil
}

// oddSpace reports a character compose and pairtimed trim differently: a
// control character other than tab, or a space other than ' ' and tab.
func oddSpace(r rune) bool {
	return r != ' ' && r != '\t' && (unicode.IsControl(r) || unicode.IsSpace(r))
}

// envValue returns the value the text after a line's '=' holds, or an
// error when compose would read the text differently (compose-go's
// dotenv parser). Errors never quote the text.
func envValue(raw string) (string, error) {
	v := strings.Trim(raw, " \t")
	if v == "" || (v[0] != '\'' && v[0] != '"') {
		if strings.ContainsAny(v, `$\`) {
			return "", errors.New(`an unquoted value can't hold '$' or '\', which compose expands; put the value in single quotes`)
		}
		if strings.Contains(v, " #") || strings.Contains(v, "\t#") {
			return "", errors.New("compose reads a '#' after a space or tab as the start of a comment; put the value in single quotes")
		}
		return v, nil
	}
	quote := v[0]
	end := strings.IndexByte(v[1:], quote) + 1
	switch {
	case end == 0:
		return "", errors.New("the value's quote doesn't close on this line, so compose would read the next lines into it")
	case end != len(v)-1:
		return "", errors.New("the line goes on past the value's first closing quote, which compose reads as a comment, another variable or an escape; put a comment on a line of its own, and wrap a value that holds this quote in the other kind")
	}
	inner := v[1:end]
	if quote == '"' && strings.ContainsAny(inner, `$\`) {
		return "", errors.New(`a double-quoted value can't hold '$' or '\', which compose expands; use single quotes`)
	}
	if quote == '\'' && strings.HasSuffix(inner, `\`) {
		return "", errors.New(`a single-quoted value can't end in '\', since compose reads \' as a quote inside the value`)
	}
	return inner, nil
}
