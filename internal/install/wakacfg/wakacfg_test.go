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

func TestProxyVar(t *testing.T) {
	tests := []struct {
		name string
		vars map[string]string
		want string
	}{
		{name: "with no proxy variable there is none", want: ""},
		{name: "HTTPS_PROXY is one", vars: map[string]string{"HTTPS_PROXY": "http://proxy.example:3128"}, want: "HTTPS_PROXY"},
		{name: "http_proxy in lower case is one, as Go reads it", vars: map[string]string{"http_proxy": "http://proxy.example:3128"}, want: "http_proxy"},
		{name: "an empty variable sets no proxy", vars: map[string]string{"HTTPS_PROXY": "", "HTTP_PROXY": ""}, want: ""},
		{name: "NO_PROXY sets none, and doesn't keep one from counting", vars: map[string]string{"NO_PROXY": "*", "HTTP_PROXY": "http://proxy.example:3128"}, want: "HTTP_PROXY"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ProxyVar(func(k string) string { return tt.vars[k] }); got != tt.want {
				t.Errorf("ProxyVar = %q, want %q", got, tt.want)
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
