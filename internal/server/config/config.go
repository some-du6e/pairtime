// SPDX-License-Identifier: AGPL-3.0-or-later

// Package config loads pairtimed's configuration: pairtime.toml, and the
// secrets that compose passes in from secrets.env (DESIGN 10.1, 10.2).
//
// pairtime.toml holds DESIGN 10.2's keys and the server's own listen and
// storage settings, with these defaults:
//
//	base_url        = ""                # required, e.g. "https://<host>"
//	signups_open    = false             # DESIGN 8.6; true needs [controller]
//	operators       = []                # HCA subs that may sign in while signups_open is false (WP-SV14)
//	profile_default = "public"          # or "ask" (DESIGN 8.3)
//	scoring_version = ""                # X.Y.Z; empty means the built-in scorer's (DESIGN 6.7)
//	listen          = ":8080"           # the API, behind cloudflared (DESIGN 10.5)
//	ops_listen      = "127.0.0.1:9091"  # /healthz, /readyz and /metrics; never tunneled (DESIGN 10.4)
//	data_dir        = "/data"           # DESIGN 10.1
//
//	[database]
//	engine = "sqlite"                   # or "postgres", with PAIRTIME_DATABASE_URL as a secret
//	path   = "<data_dir>/pairtime.db"
//
//	[controller]                        # who is responsible for the data (DESIGN 8.6)
//	name    = ""                        # empty: the privacy notice says "not named yet"
//	contact = ""                        # an email address or an https URL
//	dpo     = ""                        # Hack Club's Data Protection Officer, when Hack Club is the controller
//
//	[hca]
//	client_id = ""
//
//	[privacy_hub]
//	aud      = ""
//	jwks_url = ""                       # https only
//
//	[sentinel]
//	socket = ""                         # empty: the sentinel is off (DESIGN 7.2)
//
//	[[reviewers]]                       # one table per HCA sub
//	sub   = "..."
//	roles = ["qi", "program_reviewer:<program>"]
//
// A key the file doesn't need can be left out. A key this package doesn't
// know is an error, so a misspelt key can't silently fall back to its
// default. Relative paths are relative to the directory of pairtime.toml.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// DefaultPath is where the deploy bundle mounts pairtime.toml (DESIGN 10.1).
const DefaultPath = "/etc/pairtime/pairtime.toml"

// Defaults for keys pairtime.toml leaves out.
const (
	DefaultListen    = ":8080"
	DefaultOpsListen = "127.0.0.1:9091"
	DefaultDataDir   = "/data"
	// DefaultDatabaseFile is the SQLite file's name inside data_dir.
	DefaultDatabaseFile = "pairtime.db"
)

// The values profile_default takes (DESIGN 8.3). Public is the code
// default, as R5 asks; Karim's instance launches with Ask.
const (
	ProfilePublic = "public"
	ProfileAsk    = "ask"
)

// The database engines.
const (
	EngineSQLite   = "sqlite"
	EnginePostgres = "postgres"
)

// The reviewer roles of DESIGN 10.2, besides program_reviewer:<program>.
var reviewerRoles = []string{"qi", "fraud_squad", "spot_checker", "admin"}

// ProgramReviewerPrefix starts the one role that names a program.
const ProgramReviewerPrefix = "program_reviewer:"

// Config is a loaded and checked pairtime.toml.
type Config struct {
	BaseURL        string     `toml:"base_url"`
	SignupsOpen    bool       `toml:"signups_open"`
	ProfileDefault string     `toml:"profile_default"`
	ScoringVersion string     `toml:"scoring_version"`
	Reviewers      []Reviewer `toml:"reviewers"`
	// Operators may sign in, and get an account, while sign-ups are
	// closed, so an operator can smoke-test a deployed server. Karim
	// decides who is on the list (DESIGN 8.6; WP-SV14).
	Operators  []string   `toml:"operators"`
	Controller Controller `toml:"controller"`
	HCA        HCA        `toml:"hca"`
	PrivacyHub PrivacyHub `toml:"privacy_hub"`
	Sentinel   Sentinel   `toml:"sentinel"`

	Listen    string   `toml:"listen"`
	OpsListen string   `toml:"ops_listen"`
	DataDir   string   `toml:"data_dir"`
	Database  Database `toml:"database"`

	// Secrets come from secrets.env and the environment, never from
	// pairtime.toml.
	Secrets Secrets `toml:"-"`
	// Path is the file the configuration was loaded from.
	Path string `toml:"-"`
}

// Reviewer is one entry of the reviewer allowlist: an HCA subject and its
// roles (DESIGN 10.2).
type Reviewer struct {
	Sub   string   `toml:"sub"`
	Roles []string `toml:"roles"`
}

// Controller names who is responsible for the data on this server, for
// the privacy notice at /privacy (DESIGN 8.6). Until Name and Contact are
// both set, the notice shows them as "not named yet" and sign-ups can't
// open.
type Controller struct {
	Name string `toml:"name"`
	// Contact is an email address or an https URL.
	Contact string `toml:"contact"`
	// DPO is how to reach Hack Club's Data Protection Officer, when Hack
	// Club is the controller: an email address or an https URL.
	DPO string `toml:"dpo"`
}

// HCA configures the Hack Club Auth client (DESIGN 3.10). Its secret is
// in secrets.env.
type HCA struct {
	ClientID string `toml:"client_id"`
}

// PrivacyHub configures the privacy hub endpoints (DESIGN 8.9).
type PrivacyHub struct {
	Aud     string `toml:"aud"`
	JWKSURL string `toml:"jwks_url"`
}

// Sentinel configures the optional private sentinel (DESIGN 7.2).
type Sentinel struct {
	Socket string `toml:"socket"`
}

// Database says where the data lives. The Postgres URL holds a password,
// so it is the secret PAIRTIME_DATABASE_URL, not a key here.
type Database struct {
	Engine string `toml:"engine"`
	Path   string `toml:"path"`
}

// Load reads the configuration file at path and the secrets, fills in
// defaults and checks every value. secretsFile is a secrets.env to read,
// or "" for none; environ is the process environment, as os.Environ
// returns it, and its PAIRTIME_* values win over the file's.
func Load(path, secretsFile string, environ []string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	var c Config
	md, err := toml.Decode(string(data), &c)
	if err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("config: %s: unknown keys: %s", path, strings.Join(keys, ", "))
	}
	c.Path = path
	c.Secrets, err = LoadSecrets(secretsFile, environ)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if err := c.finish(dir); err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	return &c, nil
}

// finish fills in defaults and checks every value. Relative paths are
// resolved against dir.
func (c *Config) finish(dir string) error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	base, err := checkBaseURL(c.BaseURL)
	if err != nil {
		fail("base_url: %v", err)
	}
	c.BaseURL = base

	switch c.ProfileDefault {
	case "":
		c.ProfileDefault = ProfilePublic
	case ProfilePublic, ProfileAsk:
	default:
		fail("profile_default is %q; use %q or %q (DESIGN 8.3)", c.ProfileDefault, ProfilePublic, ProfileAsk)
	}

	if c.ScoringVersion != "" && !semver.MatchString(c.ScoringVersion) {
		fail("scoring_version is %q; use a version such as \"0.1.0\" (DESIGN 6.6)", c.ScoringVersion)
	}

	for _, err := range checkReviewers(c.Reviewers) {
		fail("reviewers: %v", err)
	}
	operators := make(map[string]bool)
	for _, sub := range c.Operators {
		if !validSub(sub) {
			fail("operators: %q must be 1 to 255 printable ASCII characters without spaces", sub)
		}
		if operators[sub] {
			fail("operators: %q is listed twice", sub)
		}
		operators[sub] = true
	}
	if n := c.Controller.Name; strings.ContainsFunc(n, func(r rune) bool { return r < ' ' || r == 0x7f }) || len(n) > 200 {
		fail("controller.name must be at most 200 characters, on one line")
	}
	for _, f := range []struct{ key, value string }{{"controller.contact", c.Controller.Contact}, {"controller.dpo", c.Controller.DPO}} {
		if f.value != "" {
			if err := checkContact(f.value); err != nil {
				fail("%s: %v", f.key, err)
			}
		}
	}
	if c.SignupsOpen && (c.Controller.Name == "" || c.Controller.Contact == "") {
		fail("signups_open is true, but controller.name and controller.contact aren't both set; sign-ups stay closed until the privacy notice names the controller (DESIGN 8.6)")
	}

	if u := c.PrivacyHub.JWKSURL; u != "" {
		if p, err := url.Parse(u); err != nil || p.Scheme != "https" || p.Host == "" {
			fail("privacy_hub.jwks_url is %q; it must be an https URL", u)
		}
	}
	if s := c.Sentinel.Socket; s != "" && !filepath.IsAbs(s) {
		fail("sentinel.socket is %q; it must be an absolute path", s)
	}

	if c.Listen == "" {
		c.Listen = DefaultListen
	}
	if c.OpsListen == "" {
		c.OpsListen = DefaultOpsListen
	}
	var port string
	for _, l := range []struct{ key, addr string }{{"listen", c.Listen}, {"ops_listen", c.OpsListen}} {
		_, p, err := net.SplitHostPort(l.addr)
		if err != nil {
			fail("%s is %q; use host:port, such as \":8080\"", l.key, l.addr)
		}
		port = p
	}
	// Port 0 asks the OS for a free port, so two of them never clash.
	if c.Listen == c.OpsListen && port != "0" {
		fail("listen and ops_listen are both %q; the ops endpoints need their own private port (DESIGN 10.4)", c.Listen)
	}

	if c.DataDir == "" {
		c.DataDir = DefaultDataDir
	}
	c.DataDir = resolve(dir, c.DataDir)

	switch c.Database.Engine {
	case "":
		c.Database.Engine = EngineSQLite
	case EngineSQLite, EnginePostgres:
	default:
		fail("database.engine is %q; use %q or %q", c.Database.Engine, EngineSQLite, EnginePostgres)
	}
	if c.Database.Path == "" {
		c.Database.Path = filepath.Join(c.DataDir, DefaultDatabaseFile)
	}
	c.Database.Path = resolve(dir, c.Database.Path)
	if strings.Contains(c.Database.Path, "?") {
		fail("database.path %q can't contain '?'", c.Database.Path)
	}
	if c.Database.Engine == EnginePostgres {
		if err := checkPostgresURL(c.Secrets.Get(DatabaseURL)); err != nil {
			fail("database.engine is %q: %v", EnginePostgres, err)
		}
	}
	return errors.Join(errs...)
}

// semver matches the MAJOR.MINOR.PATCH scoring versions of DESIGN 6.6.
var semver = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// programName is the <program> of program_reviewer:<program>.
var programName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// checkBaseURL returns base_url without a trailing slash. It must be an
// https origin, or http on a loopback host for local runs.
func checkBaseURL(raw string) (string, error) {
	if raw == "" {
		return "", errors.New(`it is required, such as "https://pairtime.example.org"`)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw, fmt.Errorf("%q isn't a URL", raw)
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return raw, fmt.Errorf("%q must be an origin, such as \"https://pairtime.example.org\", with no path, query or user", raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		host := u.Hostname()
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return raw, fmt.Errorf("%q uses http; only https is allowed, except on localhost", raw)
		}
	default:
		return raw, fmt.Errorf("%q must start with https://", raw)
	}
	return strings.TrimSuffix(raw, "/"), nil
}

// checkReviewers checks the allowlist: each sub once, each role known.
func checkReviewers(rs []Reviewer) []error {
	var errs []error
	seen := make(map[string]bool)
	for i, r := range rs {
		where := fmt.Sprintf("entry %d", i+1)
		if r.Sub == "" {
			errs = append(errs, fmt.Errorf("%s has no sub", where))
		} else {
			where = fmt.Sprintf("sub %q", r.Sub)
			if !validSub(r.Sub) {
				errs = append(errs, fmt.Errorf("%s must be 1 to 255 printable ASCII characters without spaces", where))
			}
			if seen[r.Sub] {
				errs = append(errs, fmt.Errorf("%s is listed twice; give it one entry with all its roles", where))
			}
			seen[r.Sub] = true
		}
		if len(r.Roles) == 0 {
			errs = append(errs, fmt.Errorf("%s has no roles", where))
		}
		roles := make(map[string]bool)
		for _, role := range r.Roles {
			if roles[role] {
				errs = append(errs, fmt.Errorf("%s lists role %q twice", where, role))
			}
			roles[role] = true
			if program, ok := strings.CutPrefix(role, ProgramReviewerPrefix); ok {
				if !programName.MatchString(program) {
					errs = append(errs, fmt.Errorf("%s: role %q needs a program name of lowercase letters, digits, '-' and '_'", where, role))
				}
			} else if !slices.Contains(reviewerRoles, role) {
				errs = append(errs, fmt.Errorf("%s: unknown role %q; the roles are %s and %s<program> (DESIGN 10.2)",
					where, role, strings.Join(reviewerRoles, ", "), ProgramReviewerPrefix))
			}
		}
	}
	return errs
}

// validSub reports whether sub can be an HCA subject: 1 to 255 printable
// ASCII characters without spaces.
func validSub(sub string) bool {
	return sub != "" && len(sub) <= 255 && !strings.ContainsFunc(sub, func(c rune) bool { return c <= ' ' || c > '~' })
}

// checkContact accepts an email address or an https URL, which the privacy
// notice shows as a link.
func checkContact(s string) error {
	if strings.ContainsAny(s, " \t\r\n()<>\"'\\") {
		return fmt.Errorf("%q can't contain spaces, quotes, brackets or backslashes", s)
	}
	if strings.HasPrefix(s, "https://") {
		if u, err := url.Parse(s); err != nil || u.Host == "" {
			return fmt.Errorf("%q isn't an https URL", s)
		}
		return nil
	}
	if at := strings.Index(s, "@"); at <= 0 || at == len(s)-1 || strings.Count(s, "@") != 1 || strings.Contains(s, ":") {
		return fmt.Errorf("%q must be an email address or an https URL", s)
	}
	return nil
}

// checkPostgresURL checks the URL's form without ever quoting it, because
// it holds a password.
func checkPostgresURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("set the secret %s to its postgres:// URL", DatabaseURL)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		return fmt.Errorf("the secret %s must be a URL of the form postgres://user:password@host:port/database", DatabaseURL)
	}
	return nil
}

// resolve makes p absolute against dir, unless it already is.
func resolve(dir, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(dir, p)
}
