// SPDX-License-Identifier: AGPL-3.0-or-later

package wakacfg

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testKey is pairtime's key in these tests, and otherKey another server's,
// the two public keys of the wakatime-cli capture
// (testdata/fixtures/wakatime/2.28.4/linux).
const (
	testKey  = "00000000-0000-4000-8000-00000000a9e7"
	otherKey = "00000000-0000-4000-8000-0000000048c1"
)

func TestValidKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want bool
	}{
		{"a lowercase UUIDv4 is a key", testKey, true},
		{"a key may start with waka_", "waka_" + testKey, true},
		{"capitals aren't in WakaTime's format", strings.ToUpper(testKey), false},
		{"a UUID of another version isn't a key", "00000000-0000-1000-8000-00000000a9e7", false},
		{"a UUID of another variant isn't a key", "00000000-0000-4000-c000-00000000a9e7", false},
		{"a key needs all its digits", "00000000-0000-4000-8000-00000000a9e", false},
		{"a key has nothing after it", testKey + " ", false},
		{"the empty string isn't a key", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidKey(tt.key); got != tt.want {
				t.Errorf("ValidKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestNewKey(t *testing.T) {
	tests := []struct {
		name string
		n    int
	}{
		{"every new key is in WakaTime's format, and none comes twice", 64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seen := map[string]bool{}
			for range tt.n {
				k, err := NewKey()
				if err != nil {
					t.Fatal(err)
				}
				if !ValidKey(k) {
					t.Errorf("NewKey() = %q, which isn't in WakaTime's format", k)
				}
				if seen[k] {
					t.Errorf("NewKey() returned %q twice", k)
				}
				seen[k] = true
			}
		})
	}
}

func TestFind(t *testing.T) {
	tests := []struct {
		name string
		// vars are the environment, with {home} for the home folder.
		vars map[string]string
		// files are made in the home folder before Find.
		files []string
		// config and internal are the paths Find must return, under the
		// home folder; wantErr is a part of its error instead.
		config, internal, wantErr string
	}{
		{name: "with nothing there, the config is ~/.wakatime.cfg", config: ".wakatime.cfg", internal: ".wakatime/wakatime-internal.cfg"},
		{name: "a ~/.wakatime.cfg that is there is the config", files: []string{".wakatime.cfg", ".config/wakatime/wakatime.cfg"}, config: ".wakatime.cfg", internal: ".wakatime/wakatime-internal.cfg"},
		{name: "without ~/.wakatime.cfg, wakatime-cli reads ~/.config/wakatime/wakatime.cfg", files: []string{".config/wakatime/wakatime.cfg"}, config: ".config/wakatime/wakatime.cfg", internal: ".wakatime/wakatime-internal.cfg"},
		{name: "XDG_CONFIG_HOME moves the second place", vars: map[string]string{"XDG_CONFIG_HOME": "{home}/xdg"}, files: []string{"xdg/wakatime/wakatime.cfg"}, config: "xdg/wakatime/wakatime.cfg", internal: ".wakatime/wakatime-internal.cfg"},
		{name: "an XDG_CONFIG_HOME that isn't absolute is passed over", vars: map[string]string{"XDG_CONFIG_HOME": "xdg"}, files: []string{".config/wakatime/wakatime.cfg"}, config: ".config/wakatime/wakatime.cfg", internal: ".wakatime/wakatime-internal.cfg"},
		{name: "WAKATIME_HOME holds both files, whatever else is there", vars: map[string]string{"WAKATIME_HOME": "{home}/waka"}, files: []string{".wakatime.cfg"}, config: "waka/.wakatime.cfg", internal: "waka/wakatime-internal.cfg"},
		{name: "a WAKATIME_HOME under ~ is in the home folder", vars: map[string]string{"WAKATIME_HOME": "~/waka"}, config: "waka/.wakatime.cfg", internal: "waka/wakatime-internal.cfg"},
		{name: "a WAKATIME_HOME of another user's home is passed over, as wakatime-cli passes it over", vars: map[string]string{"WAKATIME_HOME": "~someone/waka"}, config: ".wakatime.cfg", internal: ".wakatime/wakatime-internal.cfg"},
		{name: "a relative WAKATIME_HOME is refused", vars: map[string]string{"WAKATIME_HOME": "waka"}, wantErr: "a relative path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			for _, f := range tt.files {
				p := filepath.Join(home, filepath.FromSlash(f))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			getenv := func(k string) string { return strings.ReplaceAll(tt.vars[k], "{home}", home) }
			got, err := Find(home, getenv)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Find = %+v, %v, want an error saying %q", got, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := Paths{Config: filepath.Join(home, filepath.FromSlash(tt.config)), Internal: filepath.Join(home, filepath.FromSlash(tt.internal))}
			if got != want {
				t.Errorf("Find = %+v, want %+v", got, want)
			}
		})
	}
}

func TestProxyFor(t *testing.T) {
	const (
		hackatime = "https://hackatime.hackclub.com/api/hackatime/v1"
		proxy     = "http://proxy.example:3128"
	)
	tests := []struct {
		name   string
		server string // the config's main api_url
		vars   map[string]string
		want   string
	}{
		{name: "with no proxy variable there is none", server: hackatime},
		{name: "HTTPS_PROXY is the proxy of an https server", server: hackatime, vars: map[string]string{"HTTPS_PROXY": proxy}, want: "HTTPS_PROXY"},
		{name: "https_proxy in lower case comes first, as httpproxy reads it", server: hackatime, vars: map[string]string{"HTTPS_PROXY": proxy, "https_proxy": proxy}, want: "https_proxy"},
		{name: "HTTP_PROXY alone is no proxy of an https server", server: hackatime, vars: map[string]string{"HTTP_PROXY": proxy, "http_proxy": proxy}},
		{name: "HTTP_PROXY is the proxy of an http server", server: "http://wakapi.example:3000/api", vars: map[string]string{"HTTP_PROXY": proxy}, want: "HTTP_PROXY"},
		{name: "http_proxy in lower case comes first for an http server", server: "http://wakapi.example:3000/api", vars: map[string]string{"HTTP_PROXY": proxy, "http_proxy": proxy}, want: "http_proxy"},
		{name: "under CGI an http server takes no proxy, as httpproxy refuses HTTP_PROXY there", server: "http://wakapi.example:3000/api", vars: map[string]string{"HTTP_PROXY": proxy, "REQUEST_METHOD": "GET"}},
		{name: "under CGI an https server still takes HTTPS_PROXY, as httpproxy refuses only HTTP_PROXY there", server: hackatime, vars: map[string]string{"HTTPS_PROXY": proxy, "REQUEST_METHOD": "GET"}, want: "HTTPS_PROXY"},
		{name: "a loopback server takes no proxy", server: "http://127.0.0.1:7247/api/v1", vars: map[string]string{"HTTP_PROXY": proxy}},
		{name: "an IPv6 loopback server takes no proxy", server: "http://[::1]:7247/api/v1", vars: map[string]string{"HTTP_PROXY": proxy}},
		{name: "localhost takes no proxy", server: "https://localhost:8443/api", vars: map[string]string{"HTTPS_PROXY": proxy}},
		{name: "LOCALHOST in capitals takes the proxy, as httpproxy compares localhost as written", server: "https://LOCALHOST:8443/api", vars: map[string]string{"HTTPS_PROXY": proxy}, want: "HTTPS_PROXY"},
		{name: "a host that isn't ASCII takes the proxy even when NO_PROXY names it, since ProxyFor doesn't work out the IDNA form httpproxy matches", server: "https://wak\u00e4pi.example/api", vars: map[string]string{"HTTPS_PROXY": proxy, "NO_PROXY": "wak\u00e4pi.example"}, want: "HTTPS_PROXY"},
		{name: "an empty variable is no proxy", server: hackatime, vars: map[string]string{"HTTPS_PROXY": ""}},
		{name: "a proxy that doesn't parse is none", server: hackatime, vars: map[string]string{"HTTPS_PROXY": "%zz"}},
		{name: "NO_PROXY naming the host keeps it from the proxy", server: hackatime, vars: map[string]string{"HTTPS_PROXY": proxy, "NO_PROXY": "hackatime.hackclub.com"}},
		{name: "NO_PROXY matches a host written in capitals, as httpproxy lowers it for NO_PROXY", server: "https://HACKATIME.hackclub.com/api/hackatime/v1", vars: map[string]string{"HTTPS_PROXY": proxy, "NO_PROXY": "hackatime.hackclub.com"}},
		{name: "NO_PROXY's domain covers its subdomains too", server: hackatime, vars: map[string]string{"HTTPS_PROXY": proxy, "NO_PROXY": "hackclub.com"}},
		{name: "NO_PROXY's domain doesn't cover a host that only ends in its letters", server: "https://nothackclub.com/api", vars: map[string]string{"HTTPS_PROXY": proxy, "NO_PROXY": "hackclub.com"}, want: "HTTPS_PROXY"},
		{name: "NO_PROXY with a leading dot covers subdomains", server: hackatime, vars: map[string]string{"HTTPS_PROXY": proxy, "NO_PROXY": "other.example, .hackclub.com"}},
		{name: "NO_PROXY with a leading dot doesn't cover the domain itself", server: "https://hackclub.com/api", vars: map[string]string{"HTTPS_PROXY": proxy, "NO_PROXY": ".hackclub.com"}, want: "HTTPS_PROXY"},
		{name: "NO_PROXY with *. covers subdomains", server: hackatime, vars: map[string]string{"HTTPS_PROXY": proxy, "NO_PROXY": "*.hackclub.com"}},
		{name: "NO_PROXY with *. doesn't cover the domain itself", server: "https://hackclub.com/api", vars: map[string]string{"HTTPS_PROXY": proxy, "NO_PROXY": "*.hackclub.com"}, want: "HTTPS_PROXY"},
		{name: "NO_PROXY's * covers every host", server: hackatime, vars: map[string]string{"HTTPS_PROXY": proxy, "NO_PROXY": "*"}},
		{name: "no_proxy in lower case comes first", server: hackatime, vars: map[string]string{"HTTPS_PROXY": proxy, "no_proxy": "hackatime.hackclub.com", "NO_PROXY": "other.example"}},
		{name: "NO_PROXY of another host covers nothing here", server: hackatime, vars: map[string]string{"HTTPS_PROXY": proxy, "NO_PROXY": "other.example"}, want: "HTTPS_PROXY"},
		{name: "NO_PROXY of the host on another port covers nothing here", server: hackatime, vars: map[string]string{"HTTPS_PROXY": proxy, "NO_PROXY": "hackatime.hackclub.com:8443"}, want: "HTTPS_PROXY"},
		{name: "NO_PROXY of the host on its default port covers it", server: hackatime, vars: map[string]string{"HTTPS_PROXY": proxy, "NO_PROXY": "hackatime.hackclub.com:443"}},
		{name: "NO_PROXY's *example.com, without a dot, covers nothing, as httpproxy reads it", server: "https://example.com/api", vars: map[string]string{"HTTPS_PROXY": proxy, "NO_PROXY": "*example.com"}, want: "HTTPS_PROXY"},
		{name: "a CIDR range covers an IP host in it", server: "https://10.1.2.3/api", vars: map[string]string{"HTTPS_PROXY": proxy, "NO_PROXY": "10.0.0.0/8"}},
		{name: "an IP covers that host", server: "https://10.1.2.3/api", vars: map[string]string{"HTTPS_PROXY": proxy, "NO_PROXY": "10.1.2.3"}},
		{name: "a domain covers no IP host", server: "https://10.1.2.3/api", vars: map[string]string{"HTTPS_PROXY": proxy, "NO_PROXY": "1.2.3"}, want: "HTTPS_PROXY"},
		{name: "a server that doesn't parse takes no proxy, as wakatime-cli sends nothing to it", server: "%zz", vars: map[string]string{"HTTPS_PROXY": proxy}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ProxyFor(tt.server, func(k string) string { return tt.vars[k] }); got != tt.want {
				t.Errorf("ProxyFor(%q) = %q, want %q", tt.server, got, tt.want)
			}
		})
	}
}

func TestSetup(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), ".wakatime.cfg")
	tests := []struct {
		name    string
		data    string
		want    Setup
		wantErr string
	}{
		{name: "the setup file reads back as written", data: string(Setup{Key: testKey, Config: cfg}.Encode()), want: Setup{V: 1, Key: testKey, Config: cfg}},
		{name: "a file of another format is refused", data: `{"v": 2, "key": "` + testKey + `", "config": ` + quote(cfg) + `}`, wantErr: "format 2"},
		{name: "a key not in WakaTime's format is refused", data: `{"v": 1, "key": "nope", "config": ` + quote(cfg) + `}`, wantErr: ErrKey.Error()},
		{name: "a config that isn't an absolute path is refused", data: `{"v": 1, "key": "` + testKey + `", "config": ".wakatime.cfg"}`, wantErr: "isn't an absolute path"},
		{name: "a file that isn't JSON is refused", data: "key = " + testKey, wantErr: "isn't JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeSetup([]byte(tt.data))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("DecodeSetup = %+v, %v, want an error saying %q", got, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("DecodeSetup = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestReadSetup(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name     string
		write    bool
		notThere bool
	}{
		{name: "a setup file install wrote is read", write: true},
		{name: "no setup file is fs.ErrNotExist", notThere: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := filepath.Join(dir, strings.ReplaceAll(tt.name, " ", "-"))
			path := SetupFile(data)
			want := Setup{V: 1, Key: testKey, Config: filepath.Join(data, ".wakatime.cfg")}
			if tt.write {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, want.Encode(), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := ReadSetup(path)
			switch {
			case tt.notThere:
				if !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("ReadSetup = %+v, %v, want fs.ErrNotExist", got, err)
				}
			case err != nil:
				t.Fatal(err)
			case got != want:
				t.Errorf("ReadSetup = %+v, want %+v", got, want)
			}
		})
	}
}

// quote writes s as a JSON string.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
