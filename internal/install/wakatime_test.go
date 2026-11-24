// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/install/wakacfg"
)

// The keys of the WakaTime tests: pairtime's, the teen's Hackatime key
// and another server's, as in testdata/wakatime; and the time install
// runs at there.
const (
	wakaKey      = "00000000-0000-4000-8000-00000000a9e7"
	hackatimeKey = "00000000-0000-4000-8000-0000000048c1"
	otherWakaKey = "00000000-0000-4000-8000-00000000b0b0"
)

var wakaNow = time.Date(2026, 11, 24, 5, 30, 0, 0, time.UTC)

// wakaGolden reads testdata/wakatime/<dir>/<name>, or returns nil when
// there is no such file.
func wakaGolden(t *testing.T, dir, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "wakatime", dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// wakaHome is a home and a pairtime for the WakaTime tests: an Env in a new
// home folder, pairtime's data directory and its installer.
type wakaHome struct {
	env  Env
	data string
	in   *Installer
}

func newWakaHome(t *testing.T, vars map[string]string) wakaHome {
	t.Helper()
	data := t.TempDir()
	return wakaHome{env: testEnv(t, "linux", vars), data: data, in: &Installer{Dir: filepath.Join(data, "install"), Registry: fakeRegistry{}}}
}

// cfg is ~/.wakatime.cfg, and internal wakatime-internal.cfg, in h's home.
func (h wakaHome) cfg() string      { return filepath.Join(h.env.Home, ".wakatime.cfg") }
func (h wakaHome) internal() string { return filepath.Join(h.env.Home, ".wakatime", "wakatime-internal.cfg") }

// item returns the WakaTime item for h on port with key, "" for the one
// install finds.
func (h wakaHome) item(t *testing.T, port int, key string) Item {
	t.Helper()
	it, err := WakaTime(h.env, WakaTimeOptions{Data: h.data, Port: port, Now: wakaNow, Key: key})
	if err != nil {
		t.Fatalf("WakaTime: %v", err)
	}
	return it
}

func TestWakaTimeGoldens(t *testing.T) {
	tests := []struct {
		name string
		dir  string // under testdata/wakatime
	}{
		{"hackatime-setup's config gets pairtime's line in a new [api_urls] section", "hackatime-setup"},
		{"another server's .* line keeps its server, and pairtime's .+ goes after it", "other-server"},
		{"with .+ taken by another server, pairtime's line is ^.*$", "plus-taken"},
		{"CRLF, a byte order mark and comments stay, and the new lines are CRLF too", "crlf-comments"},
		{"with no config, install makes one for the agent and seeds wakatime-cli's watermark, and uninstall deletes both", "new-file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, want, internal := wakaGolden(t, tt.dir, "before.cfg"), wakaGolden(t, tt.dir, "after.cfg"), wakaGolden(t, tt.dir, "internal.cfg")
			if want == nil {
				t.Fatalf("testdata/wakatime/%s has no after.cfg", tt.dir)
			}
			h := newWakaHome(t, nil)
			if before != nil {
				writeFile(t, h.cfg(), before)
			}
			item := h.item(t, 7247, wakaKey)
			p, err := h.in.PlanInstall([]Item{item})
			if err != nil {
				t.Fatalf("PlanInstall: %v", err)
			}
			planned := map[string][]byte{}
			for _, ch := range p.Changes {
				planned[ch.Target.Path] = ch.After.Data
			}
			if got := planned[h.cfg()]; !bytes.Equal(got, want) {
				t.Fatalf("the plan would write\n%q\nwant testdata/wakatime/%s/after.cfg:\n%q", got, tt.dir, want)
			}
			if got := planned[h.internal()]; !bytes.Equal(got, internal) {
				t.Errorf("the plan would write\n%q\nto wakatime-internal.cfg, want\n%q", got, internal)
			}
			d := p.Diff()
			if strings.Contains(d, hackatimeKey) || strings.Contains(d, otherWakaKey) {
				t.Errorf("the diff shows a key of the teen's:\n%s", d)
			}
			if !strings.Contains(d, "+++ "+h.cfg()+"\n") || !strings.Contains(d, "http://127.0.0.1:7247/api/v1") {
				t.Errorf("the diff doesn't show pairtime's line in %s:\n%s", h.cfg(), d)
			}
			if err := p.Apply(); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got := readOrNil(t, h.cfg()); !bytes.Equal(got, want) {
				t.Fatalf("after install the config holds\n%q\nwant\n%q", got, want)
			}
			if s, err := wakacfg.ReadSetup(wakacfg.SetupFile(h.data)); err != nil || s.Key != wakaKey || s.Config != h.cfg() {
				t.Errorf("the setup file reads %+v, %v, want key %s and config %s", s, err, wakaKey, h.cfg())
			}

			again, err := h.in.PlanInstall([]Item{h.item(t, 7247, "")})
			if err != nil {
				t.Fatalf("second PlanInstall: %v", err)
			}
			if !again.Empty() {
				t.Errorf("a second install plans changes:\n%s", again.Diff())
			}
			again.Close()

			// Taking pairtime's line out of after.cfg, without the backup,
			// gives back before.cfg byte for byte, or no file.
			base := Content{Exists: before != nil, Data: before}
			undone, err := undoWakaTimeLine(Content{Exists: true, Data: want}, base, recordKey(wakaKey))
			if err != nil {
				t.Fatalf("undoWakaTimeLine: %v", err)
			}
			if !undone.Same(base) {
				t.Errorf("taking pairtime's line out of after.cfg gives\n%q\nwant\n%q", undone.Data, before)
			}

			uninstall(t, h.in)
			if before == nil {
				checkGone(t, h.cfg(), "the config pairtime made")
			} else if got := readOrNil(t, h.cfg()); !bytes.Equal(got, before) {
				t.Errorf("after uninstall the config holds\n%q\nwant\n%q", got, before)
			}
			checkGone(t, filepath.Dir(h.internal()), "the .wakatime folder")
			checkGone(t, wakacfg.SetupFile(h.data), "the setup file")
			checkGone(t, h.in.Dir, "pairtime's install directory")
		})
	}
}

func TestWakaTimeRefusesAMalformedKey(t *testing.T) {
	tests := []struct {
		name, key string
	}{
		{"a key in capitals", strings.ToUpper(wakaKey)},
		{"a key of another UUID version", "00000000-0000-1000-8000-00000000a9e7"},
		{"a key with a space after it", wakaKey + " "},
	}
	for _, tt := range tests {
		t.Run(tt.name+" is refused before the config is opened for writing", func(t *testing.T) {
			h := newWakaHome(t, nil)
			before := wakaGolden(t, "hackatime-setup", "before.cfg")
			writeFile(t, h.cfg(), before)
			if _, err := WakaTime(h.env, WakaTimeOptions{Data: h.data, Port: 7247, Now: wakaNow, Key: tt.key}); !errors.Is(err, wakacfg.ErrKey) {
				t.Errorf("WakaTime = %v, want %v", err, wakacfg.ErrKey)
			}
			if _, err := (wakaTimeLine{path: h.cfg(), key: tt.key, port: 7247}).Apply(Content{Exists: true, Data: before}); !errors.Is(err, wakacfg.ErrKey) {
				t.Errorf("the line's Apply = %v, want %v", err, wakacfg.ErrKey)
			}
			if got := readOrNil(t, h.cfg()); !bytes.Equal(got, before) {
				t.Errorf("the config changed:\n%q", got)
			}
			checkGone(t, wakacfg.SetupFile(h.data), "a setup file")
		})
	}
}

func TestWakaTimeKeepsTheKey(t *testing.T) {
	hackatime := wakaGolden(t, "hackatime-setup", "before.cfg")
	tests := []struct {
		name string
		// cfg is the config before install, "" for none, and setupKey the
		// key a setup file names, "" for no setup file.
		cfg, setupKey string
		want          string // the key install uses, "" for a new one
	}{
		{name: "a line of pairtime's that is there keeps its key", cfg: string(hackatime) + "[api_urls]\n.+ = http://127.0.0.1:7250/api/v1|" + otherWakaKey + "\n", want: otherWakaKey},
		{name: "the line's key wins over the setup file's", cfg: string(hackatime) + "[api_urls]\n.+ = http://127.0.0.1:7250/api/v1|" + otherWakaKey + "\n", setupKey: wakaKey, want: otherWakaKey},
		{name: "without a line, the setup file's key comes back", cfg: string(hackatime), setupKey: wakaKey, want: wakaKey},
		{name: "a config pairtime made keeps its api_key", cfg: "[settings]\napi_url = http://127.0.0.1:7250/api/v1\napi_key = " + otherWakaKey + "\n", want: otherWakaKey},
		{name: "with neither, the key is new", cfg: string(hackatime)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newWakaHome(t, nil)
			if tt.cfg != "" {
				writeFile(t, h.cfg(), []byte(tt.cfg))
			}
			if tt.setupKey != "" {
				writeFile(t, wakacfg.SetupFile(h.data), wakacfg.Setup{Key: tt.setupKey, Config: h.cfg()}.Encode())
			}
			p := install(t, h.in, h.item(t, 7247, ""))
			s, err := wakacfg.ReadSetup(wakacfg.SetupFile(h.data))
			if err != nil {
				t.Fatal(err)
			}
			if tt.want != "" && s.Key != tt.want {
				t.Errorf("install used key %s, want %s", s.Key, tt.want)
			}
			if tt.want == "" && (s.Key == wakaKey || s.Key == otherWakaKey || !wakacfg.ValidKey(s.Key)) {
				t.Errorf("install used key %s, want a new one", s.Key)
			}
			cfg := readOrNil(t, h.cfg())
			if n := strings.Count(string(cfg), "127.0.0.1:"); n != 1 {
				t.Errorf("the config has %d lines to the agent, want 1:\n%s\n(the plan was\n%s)", n, cfg, p.Diff())
			}
			if st, err := wakacfg.Inspect(cfg, s.Key); err != nil || !st.Line || st.Port != 7247 {
				t.Errorf("Inspect = %+v, %v, want pairtime's line on port 7247", st, err)
			}
		})
	}
}

func TestWakaTimeUninstall(t *testing.T) {
	hackatime := wakaGolden(t, "hackatime-setup", "before.cfg")
	tests := []struct {
		name   string
		before []byte // nil for no config
		// afterInstall changes the home after install, as the agent or the
		// teen would.
		afterInstall func(t *testing.T, h wakaHome)
		// items passes the WakaTime item to uninstall, as uninstall does
		// for targets the manifest doesn't know.
		items bool
		want  []byte // nil for no config
	}{
		{
			name:   "after the agent moved to another port, uninstall takes the line out and gives back the bytes",
			before: hackatime,
			afterInstall: func(t *testing.T, h wakaHome) {
				setPort(t, h.cfg(), 7250)
			},
			want: hackatime,
		},
		{
			name: "after the agent moved to another port, a config pairtime made goes",
			afterInstall: func(t *testing.T, h wakaHome) {
				setPort(t, h.cfg(), 7250)
			},
		},
		{
			name:   "a key the teen added since stays, and only pairtime's line goes",
			before: hackatime,
			afterInstall: func(t *testing.T, h wakaHome) {
				writeFile(t, h.cfg(), append(readOrNil(t, h.cfg()), "hostname = mine\n"...))
			},
			want: append(append([]byte(nil), hackatime...), "\n[api_urls]\nhostname = mine\n"...),
		},
		{
			name:   "with pairtime's data directory gone, uninstall still finds pairtime's line and takes it out",
			before: hackatime,
			afterInstall: func(t *testing.T, h wakaHome) {
				if err := os.RemoveAll(h.data); err != nil {
					t.Fatal(err)
				}
			},
			items: true,
			want:  hackatime,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newWakaHome(t, nil)
			if tt.before != nil {
				writeFile(t, h.cfg(), tt.before)
			}
			install(t, h.in, h.item(t, 7247, wakaKey))
			tt.afterInstall(t, h)
			var items []Item
			if tt.items {
				items = append(items, h.item(t, 7247, ""))
			}
			uninstall(t, h.in, items...)
			if got := readOrNil(t, h.cfg()); !bytes.Equal(got, tt.want) {
				t.Errorf("after uninstall the config holds\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

// setPort moves pairtime's line in the config at path to port, as the
// agent does when it listens on another port.
func setPort(t *testing.T, path string, port int) {
	t.Helper()
	out, err := wakacfg.SetPort(readOrNil(t, path), wakaKey, port)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, out)
}

func TestWakaTimeRefusals(t *testing.T) {
	tests := []struct {
		name string
		vars map[string]string
		cfg  string // the config before install
		want string // part of the refusal
	}{
		{name: "a config with a key in quotes is left alone, with the line to add by hand", cfg: "[settings]\n\"api_key\" = " + hackatimeKey + "\n", want: "so pairtime leaves it alone; to send your editor time to pairtime, add this line under [api_urls] yourself"},
		{name: "a config whose patterns are all taken is left alone", cfg: "[api_urls]\n.+ = https://a.example|" + hackatimeKey + "\n^.*$ = https://b.example|" + hackatimeKey + "\n(?s).* = https://c.example|" + hackatimeKey + "\n", want: "for each of the patterns pairtime's line can take"},
		{name: "a relative WAKATIME_HOME is refused", vars: map[string]string{"WAKATIME_HOME": "waka"}, want: "a relative path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newWakaHome(t, tt.vars)
			if tt.cfg != "" {
				writeFile(t, h.cfg(), []byte(tt.cfg))
			}
			it, err := WakaTime(h.env, WakaTimeOptions{Data: h.data, Port: 7247, Now: wakaNow, Key: wakaKey})
			if err == nil {
				var p *Plan
				if p, err = h.in.PlanInstall([]Item{it}); err == nil {
					p.Close()
				}
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("planning = %v, want an error saying %q", err, tt.want)
			}
			if tt.cfg != "" {
				if got := readOrNil(t, h.cfg()); string(got) != tt.cfg {
					t.Errorf("the config changed:\n%q", got)
				}
			}
		})
	}
}

func TestWakaTimeFollowsWakatimeHome(t *testing.T) {
	tests := []struct {
		name string
		dir  string // WAKATIME_HOME, under the home folder
	}{
		{"with WAKATIME_HOME set, the line and the watermark go where wakatime-cli reads them", "waka"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, tt.dir)
			data := t.TempDir()
			env := Env{GOOS: "linux", Home: home, Getenv: func(k string) string { return map[string]string{"WAKATIME_HOME": dir}[k] }, Exe: testExe["linux"]}
			in := &Installer{Dir: filepath.Join(data, "install"), Registry: fakeRegistry{}}
			it, err := WakaTime(env, WakaTimeOptions{Data: data, Port: 7247, Now: wakaNow, Key: wakaKey})
			if err != nil {
				t.Fatal(err)
			}
			install(t, in, it)
			if got := readOrNil(t, filepath.Join(dir, ".wakatime.cfg")); !bytes.Equal(got, wakaGolden(t, "new-file", "after.cfg")) {
				t.Errorf("WAKATIME_HOME's config holds %q", got)
			}
			if got := readOrNil(t, filepath.Join(dir, "wakatime-internal.cfg")); !bytes.Equal(got, wakaGolden(t, "new-file", "internal.cfg")) {
				t.Errorf("WAKATIME_HOME's wakatime-internal.cfg holds %q", got)
			}
			checkGone(t, filepath.Join(home, ".wakatime.cfg"), "a config in the home folder")
			if s, err := wakacfg.ReadSetup(wakacfg.SetupFile(data)); err != nil || s.Config != filepath.Join(dir, ".wakatime.cfg") {
				t.Errorf("the setup file reads %+v, %v, want WAKATIME_HOME's config", s, err)
			}
			uninstall(t, in)
			checkGone(t, dir, "WAKATIME_HOME, which pairtime made for the config")
		})
	}
}
