// SPDX-License-Identifier: AGPL-3.0-or-later

// Package wakacfg edits ~/.wakatime.cfg, the file wakatime-cli reads its
// servers from, for the WakaTime dual-send (DESIGN 5.6; docs/PLAN.md,
// WP-IN3). In a config someone has, it adds one line under [api_urls]
// that sends every heartbeat to pairtime's agent as well as to their own
// server, such as Hackatime (Add). For someone with no config, it makes
// one that sends them to the agent alone (Make), and seeds wakatime-cli's
// watermark for agent transcripts (Watermark).
//
// A line wakatime-cli reads other than as meant can stop every send,
// Hackatime's included: a key under [api_urls] that isn't in WakaTime's
// format fails the whole run (HC 1.5), an indented line after pairtime's
// would become part of its value, and a pattern that is there twice keeps
// only the destination written last. So the package reads the file as
// wakatime-cli v2.28.4 reads it, with gopkg.in/ini.v1 and Python multiline
// values on (its pkg/vipertools), changes nothing but pairtime's own line,
// and reads the result again to check that every other key holds what it
// held and pairtime's holds what was meant. It refuses a file it can't
// follow that way, and a config that sets a proxy, which wakatime-cli
// would send pairtime's line through too (ErrProxy).
package wakacfg

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
)

// Spellings are the patterns of pairtime's [api_urls] line, each matching
// every heartbeat, in the order the line takes them: the first that isn't
// a key there already, since of two lines with one pattern wakatime-cli
// keeps only the later destination (DESIGN 5.6).
var Spellings = []string{".+", "^.*$", "(?s).*"}

// keyFormat is wakatime-cli's format for an API key (apiKeyRegex in its
// pkg/params/params.go at v2.28.4), compiled when first used rather than
// at package init, which every pairtime hook call would pay for.
var keyFormat = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`^(waka_)?[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
})

// agentURL matches the agent's endpoint anywhere in a config: the URL of
// pairtime's line and of a config pairtime made, on any port (URL).
var agentURL = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`http://127\.0\.0\.1:[1-9][0-9]{0,4}/api/v1`)
})

// lineFormat is the whole of pairtime's [api_urls] line, which the
// package checks every line it writes against.
var lineFormat = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`^(\.\+|\^\.\*\$|\(\?s\)\.\*) = http://127\.0\.0\.1:[1-9][0-9]{0,4}/api/v1\|(waka_)?[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
})

// ValidKey reports whether key is in wakatime-cli's format for an API
// key. A key that isn't makes wakatime-cli fail every run and queue every
// heartbeat, Hackatime's included (HC 1.5).
func ValidKey(key string) bool { return keyFormat().MatchString(key) }

// NewKey returns a new key for pairtime's line: a random UUIDv4 in lower
// case, which is WakaTime's format.
func NewKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // the RFC 9562 variant
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}

// URL returns the URL of the agent's WakaTime endpoint on port, which
// wakatime-cli appends WakaTime's paths to (internal/adapters/wakatime,
// Prefix).
func URL(port int) string { return "http://127.0.0.1:" + strconv.Itoa(port) + "/api/v1" }

// DefaultURL is the api_url wakatime-cli sends to when its config names
// none: WakaTime's own (api.BaseURL in its pkg/api).
const DefaultURL = "https://api.wakatime.com/api/v1"

// Errors a change refuses with.
var (
	// ErrKey is a key that isn't in WakaTime's format, refused before
	// anything is written.
	ErrKey = errors.New("pairtime's key for wakatime-cli isn't in WakaTime's format")
	// ErrPort is a port outside 1 to 65535.
	ErrPort = errors.New("pairtime's port for wakatime-cli isn't between 1 and 65535")
	// ErrNoLine is a config without pairtime's line for the key, which
	// SetPort has nothing to change in.
	ErrNoLine = errors.New("the config has no line of pairtime's for this key")
	// ErrProxy is a config that sets a proxy (Add). wakatime-cli would
	// send pairtime's line through it too: the proxy would get the
	// heartbeats in plain text, and unless it passes them on to the agent,
	// each send that fails holds back what wakatime-cli sends the teen's
	// own server.
	// ErrNotOwn is a config pairtime made whose api_key isn't the key the
	// line has now (Add): the teen gave it a key of their own, as WakaTime's
	// extension writes one, and adding pairtime's line beside it would have
	// wakatime-cli send with both, the one the agent refuses failing every
	// send.
	ErrNotOwn = errors.New("is the config pairtime made, with an api_key that isn't pairtime's any more")
	// ErrImport is a config that imports another (Add): wakatime-cli reads
	// the file [settings] import_cfg names over this one (cmd/run.go
	// parseConfigFiles), so a line of the same pattern there takes
	// pairtime's line's place, and pairtime edits only the one file.
	ErrImport = errors.New("imports another config with import_cfg, which wakatime-cli reads over this one, so a line there can take the place of pairtime's")
	ErrProxy  = errors.New("sets a proxy, and wakatime-cli would send pairtime's line through it too: the proxy would get your project and file names unencrypted, and while it doesn't pass them on to pairtime's agent, each send that fails holds back what wakatime-cli sends your own server")
)

// ProxyVars are the variables a proxy for wakatime-cli's requests comes
// from in its environment: those Go's http.ProxyFromEnvironment reads,
// which wakatime-cli's loadProxyURL reads through httpproxy.FromEnvironment
// for its main api_url, and then sends every destination through. Their
// values can hold a password, so only their names are ever shown.
var ProxyVars = []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"}

// ProxyVar returns the first of ProxyVars that getenv gives a value, ""
// when none has one. NO_PROXY is no help to pairtime's line: wakatime-cli
// works out the proxy once, for the main api_url.
func ProxyVar(getenv func(string) string) string {
	for _, v := range ProxyVars {
		if getenv(v) != "" {
			return v
		}
	}
	return ""
}

// checkArgs refuses a key or a port the line can't hold.
func checkArgs(key string, port int) error {
	if !ValidKey(key) {
		return ErrKey
	}
	if port < 1 || port > 65535 {
		return ErrPort
	}
	return nil
}

// Paths are the two files of wakatime-cli's that pairtime writes to.
type Paths struct {
	// Config is the config wakatime-cli reads, ~/.wakatime.cfg in most
	// homes.
	Config string
	// Internal is wakatime-internal.cfg, where wakatime-cli keeps its own
	// state, the watermark of its AI sync included.
	Internal string
}

// Find returns the paths wakatime-cli v2.28.4 uses for home and the
// environment getenv reads, found as its pkg/ini finds them (FilePath,
// InternalFilePath, WakaHomeDir):
//
//   - with WAKATIME_HOME set, .wakatime.cfg and wakatime-internal.cfg in
//     that folder;
//   - otherwise ~/.wakatime.cfg, and wakatime-internal.cfg in ~/.wakatime;
//   - and when ~/.wakatime.cfg isn't there, wakatime/wakatime.cfg in
//     XDG_CONFIG_HOME, or in ~/.config when that isn't an absolute path,
//     if that is there. With neither file there, the config pairtime
//     makes is ~/.wakatime.cfg, which wakatime-cli then reads first.
//
// It refuses a WAKATIME_HOME that is a relative path, which each editor's
// wakatime-cli reads from the folder it runs in.
func Find(home string, getenv func(string) string) (Paths, error) {
	if waka := getenv("WAKATIME_HOME"); waka != "" {
		switch dir, ok := expand(waka, home); {
		case !ok:
			// wakatime-cli can't expand ~user either, and reads the home
			// folder's files instead.
		case !filepath.IsAbs(dir):
			return Paths{}, fmt.Errorf("WAKATIME_HOME is %q, a relative path, so which config wakatime-cli reads depends on the folder each editor runs it in", waka)
		default:
			return Paths{Config: filepath.Join(dir, ".wakatime.cfg"), Internal: filepath.Join(dir, "wakatime-internal.cfg")}, nil
		}
	}
	p := Paths{Config: filepath.Join(home, ".wakatime.cfg"), Internal: filepath.Join(home, ".wakatime", "wakatime-internal.cfg")}
	if there(p.Config) {
		return p, nil
	}
	configHome := getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(configHome) {
		configHome = filepath.Join(home, ".config")
	}
	if xdg := filepath.Join(configHome, "wakatime", "wakatime.cfg"); there(xdg) {
		p.Config = xdg
	}
	return p, nil
}

// expand expands a ~ at the start of p as go-homedir's Expand does, which
// wakatime-cli uses, and reports false for ~user, which it can't expand.
func expand(p, home string) (string, bool) {
	if p == "" || p[0] != '~' {
		return p, true
	}
	if len(p) > 1 && p[1] != '/' && p[1] != '\\' {
		return "", false
	}
	return filepath.Join(home, p[1:]), true
}

// there reports whether a file is at p, as wakatime-cli tells: anything
// but a file that isn't there counts, an error reading it included.
func there(p string) bool {
	_, err := os.Stat(p)
	return !errors.Is(err, fs.ErrNotExist)
}

// Setup is what pairtime install records of the line it added, for the
// agent, which takes heartbeats only with Key (internal/adapters/wakatime,
// Options.Key) and rewrites the line in Config when it listens on another
// port (SetPort), and for pairtime doctor, which checks both of
// wakatime-cli's destinations. The agent may run without the WAKATIME_HOME
// install saw, so Config names the file rather than the agent finding it
// again.
type Setup struct {
	V int `json:"v"`
	// Key is the key of pairtime's line.
	Key string `json:"key"`
	// Config is the config install added the line to, or made.
	Config string `json:"config"`
}

// setupVersion is Setup's format.
const setupVersion = 1

// SetupFile returns where Setup is kept in pairtime's data directory data:
// in its install folder, which "delete everything" keeps (DESIGN 8.8), as
// it keeps the line in the config.
func SetupFile(data string) string { return filepath.Join(data, "install", "wakatime.json") }

// Encode returns s as the setup file holds it.
func (s Setup) Encode() []byte {
	s.V = setupVersion
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		panic(err) // a struct of strings and an int always encodes
	}
	return append(b, '\n')
}

// DecodeSetup reads the setup file's bytes, refusing a file of another
// format or with a key that isn't in WakaTime's.
func DecodeSetup(data []byte) (Setup, error) {
	var s Setup
	if err := json.Unmarshal(data, &s); err != nil {
		return Setup{}, fmt.Errorf("the WakaTime setup file isn't JSON: %w", err)
	}
	switch {
	case s.V != setupVersion:
		return Setup{}, fmt.Errorf("the WakaTime setup file is in format %d, not %d", s.V, setupVersion)
	case !ValidKey(s.Key):
		return Setup{}, ErrKey
	case !filepath.IsAbs(s.Config):
		return Setup{}, fmt.Errorf("the WakaTime setup file names %q, which isn't an absolute path", s.Config)
	}
	return s, nil
}

// ReadSetup reads the setup file at path. It fails with fs.ErrNotExist
// when install made none.
func ReadSetup(path string) (Setup, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Setup{}, err
	}
	return DecodeSetup(data)
}
