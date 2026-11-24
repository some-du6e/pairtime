// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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

// madeConfig returns the config pairtime makes for key on port, and
// taken that config once the teen gave it their own key, teen, as
// WakaTime's extension writes one.
func madeConfig(t *testing.T, key string, port int, teen string) (made, taken string) {
	t.Helper()
	b, err := wakacfg.Make(key, port)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), strings.Replace(string(b), "api_key = "+key, "api_key = "+teen, 1)
}

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
func (h wakaHome) cfg() string { return filepath.Join(h.env.Home, ".wakatime.cfg") }
func (h wakaHome) internal() string {
	return filepath.Join(h.env.Home, ".wakatime", "wakatime-internal.cfg")
}

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
			if s, err := wakacfg.ReadSetup(wakacfg.SetupFile(h.data)); err != nil || s.Key != wakaKey || s.Config != h.cfg() || s.Made != (before == nil) {
				t.Errorf("the setup file reads %+v, %v, want key %s and config %s, made only by install: %v", s, err, wakaKey, h.cfg(), before == nil)
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
			undone, _, err := undoWakaTimeLine(Content{Exists: true, Data: want}, base, recordKey(wakaKey, before == nil))
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

// TestWakaTimeAfterHackatimeSetup follows what the card for a home with no
// config tells a teen who joins a program on Robert: install makes the
// config, Hackatime's setup writes a new one in its place (HC 1.5), and
// install, run again, adds pairtime's line to that one, which uninstall
// takes out again.
func TestWakaTimeAfterHackatimeSetup(t *testing.T) {
	tests := []struct {
		name string
		dir  string // under testdata/wakatime, the config Hackatime's setup writes, and pairtime's line in it
	}{
		{"hackatime-setup's config written over the one pairtime made gets pairtime's line from the next install, and uninstall gives it back byte for byte", "hackatime-setup"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, want := wakaGolden(t, tt.dir, "before.cfg"), wakaGolden(t, tt.dir, "after.cfg")
			h := newWakaHome(t, nil)
			install(t, h.in, h.item(t, 7247, wakaKey))
			writeFile(t, h.cfg(), before)
			install(t, h.in, h.item(t, 7247, ""))
			if got := readOrNil(t, h.cfg()); !bytes.Equal(got, want) {
				t.Fatalf("after the second install the config holds\n%q\nwant testdata/wakatime/%s/after.cfg:\n%q", got, tt.dir, want)
			}
			uninstall(t, h.in)
			if got := readOrNil(t, h.cfg()); !bytes.Equal(got, before) {
				t.Errorf("after uninstall the config holds\n%q\nwant what Hackatime's setup wrote:\n%q", got, before)
			}
			checkGone(t, filepath.Dir(h.internal()), "the .wakatime folder")
			checkGone(t, wakacfg.SetupFile(h.data), "the setup file")
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
	madeOther, _ := madeConfig(t, otherWakaKey, 7250, "")
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
		{name: "a config pairtime made keeps its api_key", cfg: madeOther, want: otherWakaKey},
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
			if st, err := wakacfg.Inspect(cfg, s.Key, wakacfg.ByMark); err != nil || !st.Line || st.Port != 7247 {
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
		{
			name:   "with the data directory gone and pairtime's line taken out by hand, uninstall leaves the teen's empty [api_urls] section alone",
			before: append(append([]byte(nil), hackatime...), "\n[api_urls]\n"...),
			afterInstall: func(t *testing.T, h wakaHome) {
				writeFile(t, h.cfg(), append(append([]byte(nil), hackatime...), "\n[api_urls]\n"...))
				if err := os.RemoveAll(h.data); err != nil {
					t.Fatal(err)
				}
			},
			items: true,
			want:  append(append([]byte(nil), hackatime...), "\n[api_urls]\n"...),
		},
		{
			name: "with pairtime's data directory gone, uninstall still deletes the config pairtime made, by its mark",
			afterInstall: func(t *testing.T, h wakaHome) {
				if err := os.RemoveAll(h.data); err != nil {
					t.Fatal(err)
				}
			},
			items: true,
		},
		{
			name: "a reinstall on another port with the data directory gone records the config pairtime made as pairtime's, so uninstall deletes it",
			afterInstall: func(t *testing.T, h wakaHome) {
				if err := os.RemoveAll(h.data); err != nil {
					t.Fatal(err)
				}
				install(t, h.in, h.item(t, 7250, ""))
			},
		},
		{
			name:   "a reinstall on another port with the data directory gone records hackatime-setup's config without the section install added, so uninstall gives back the bytes",
			before: hackatime,
			afterInstall: func(t *testing.T, h wakaHome) {
				if err := os.RemoveAll(h.data); err != nil {
					t.Fatal(err)
				}
				install(t, h.in, h.item(t, 7250, ""))
			},
			want: hackatime,
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

func TestWakaTimeUninstallWithoutRecord(t *testing.T) {
	tests := []struct {
		name string
		cfg  string // the config, which install never changed
		// problem says uninstall reports one: the file is one it can't
		// read, and names the agent's endpoint.
		problem bool
	}{
		{name: "a config the editor can't read, with nothing of pairtime's in it, is no problem for uninstall, which leaves it alone", cfg: "[settings]\napi_key = " + hackatimeKey + "\nexclude = C:\\x\\\n"},
		{name: "a config the editor reads, with nothing of pairtime's in it, stays as it is", cfg: "[settings]\napi_key = " + hackatimeKey + "\n[api_urls]\n"},
		{name: "a config the editor can't read that names the agent's endpoint is a problem uninstall reports", cfg: "[settings]\nexclude = C:\\x\\\n[api_urls]\n.+ = http://127.0.0.1:7247/api/v1|" + wakaKey + "\n", problem: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newWakaHome(t, nil)
			writeFile(t, h.cfg(), []byte(tt.cfg))
			p, err := h.in.PlanUninstall(h.item(t, 7247, ""))
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			if got := len(p.Problems) > 0; got != tt.problem {
				t.Errorf("uninstall's problems are %q, want some: %v", p.Problems, tt.problem)
			}
			if !tt.problem && !p.Empty() {
				t.Errorf("uninstall would change what install never did:\n%s", p.Diff())
			}
		})
	}
}

func TestWakaTimeRefusals(t *testing.T) {
	// aKey matches a key in WakaTime's format. No refusal shows one: a line
	// added by hand with it would have no record for the agent to take it
	// by, and the agent's refusals hold back what wakatime-cli sends the
	// teen's own server too.
	aKey := regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`)
	_, taken := madeConfig(t, wakaKey, 7247, hackatimeKey)
	tests := []struct {
		name string
		vars map[string]string
		cfg  string // the config before install
		key  string // the item's key, "" for the one install finds or makes
		want string // part of the refusal
	}{
		{name: "a config with an unclosed section is left alone, and the refusal says to change that and run install again", cfg: "[settings\napi_key = " + hackatimeKey + "\n", key: wakaKey, want: "line 1 opens a section with [ and doesn't close it with ], so pairtime leaves it alone; change that, then run pairtime install again to add pairtime's line"},
		{name: "on a first install the refusal shows no key, since nothing would record the new one for the agent", cfg: "[settings]\nexclude = C:\\Users\\teen\\Downloads\\\n", want: "line 2 has a value that goes on"},
		{name: "a config that sets a proxy is left alone, saying why, and doesn't show the proxy", cfg: "[settings]\napi_key = " + hackatimeKey + "\nproxy = http://teen:secret@proxy.example:3128\n", key: wakaKey, want: "sets a proxy, and wakatime-cli would send pairtime's line through it too"},
		{name: "with HTTPS_PROXY set, a config of the teen's is left alone, naming the variable and never its value", vars: map[string]string{"HTTPS_PROXY": "http://teen:secret@proxy.example:3128"}, cfg: "[settings]\napi_key = " + hackatimeKey + "\n", key: wakaKey, want: "would get pairtime's line, but HTTPS_PROXY is set here"},
		{name: "a config pairtime made whose api_key the teen replaced is left alone, and the refusal says how to send to pairtime too and keep the teen's key, which may be their only copy", cfg: taken, key: wakaKey, want: "is the config pairtime made, with an api_key that isn't pairtime's any more, so pairtime leaves it alone; to send your editor time to pairtime too, set its api_url to your own server, such as Hackatime's, keep your api_key, and run pairtime install again"},
		{name: "a config whose patterns are all taken is left alone", cfg: "[api_urls]\n.+ = https://a.example|" + hackatimeKey + "\n^.*$ = https://b.example|" + hackatimeKey + "\n(?s).* = https://c.example|" + hackatimeKey + "\n", key: wakaKey, want: "for each of the patterns pairtime's line can take"},
		{name: "a relative WAKATIME_HOME is refused", vars: map[string]string{"WAKATIME_HOME": "waka"}, key: wakaKey, want: "a relative path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newWakaHome(t, tt.vars)
			if tt.cfg != "" {
				writeFile(t, h.cfg(), []byte(tt.cfg))
			}
			it, err := WakaTime(h.env, WakaTimeOptions{Data: h.data, Port: 7247, Now: wakaNow, Key: tt.key})
			if err == nil {
				var p *Plan
				if p, err = h.in.PlanInstall([]Item{it}); err == nil {
					p.Close()
				}
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("planning = %v, want an error saying %q", err, tt.want)
			}
			if k := aKey.FindString(err.Error()); k != "" || strings.Contains(err.Error(), "secret") {
				t.Errorf("the refusal shows a key (%q) or the proxy: %v", k, err)
			}
			if got := readOrNil(t, wakacfg.SetupFile(h.data)); got != nil {
				t.Errorf("a refused install wrote the setup file:\n%s", got)
			}
			if tt.cfg != "" {
				if got := readOrNil(t, h.cfg()); string(got) != tt.cfg {
					t.Errorf("the config changed:\n%q", got)
				}
			}
		})
	}
}

func TestWakaTimeLeavesTheTeensLocalServer(t *testing.T) {
	// local is the config of a server of the teen's own on 127.0.0.1,
	// such as Wakapi, whose api_url looks like the agent's.
	const local = "[settings]\napi_url = http://127.0.0.1:3000/api/v1\napi_key = " + hackatimeKey + "\n"
	tests := []struct {
		name string
		// dataGone deletes pairtime's data directory, manifest and setup
		// file included, before uninstall, which then knows pairtime's line
		// by its marker alone.
		dataGone bool
	}{
		{name: "a config of the teen's own server on 127.0.0.1 keeps its key and api_url, and gets pairtime's line beside it"},
		{name: "with pairtime's data directory gone, uninstall takes only pairtime's line out of it, never taking it for a config pairtime made whose key the teen replaced", dataGone: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newWakaHome(t, nil)
			writeFile(t, h.cfg(), []byte(local))
			install(t, h.in, h.item(t, 7247, ""))
			s, err := wakacfg.ReadSetup(wakacfg.SetupFile(h.data))
			if err != nil {
				t.Fatal(err)
			}
			if s.Key == hackatimeKey {
				t.Errorf("install took the teen's key %s as pairtime's", s.Key)
			}
			cfg := readOrNil(t, h.cfg())
			if !bytes.HasPrefix(cfg, []byte(local)) {
				t.Errorf("install changed the teen's settings:\n%s", cfg)
			}
			if st, err := wakacfg.Inspect(cfg, s.Key, wakacfg.ByMark); err != nil || !st.Line || st.Own || st.Port != 7247 {
				t.Errorf("Inspect = %+v, %v, want pairtime's line under [api_urls] on port 7247", st, err)
			}
			if tt.dataGone {
				if err := os.RemoveAll(h.data); err != nil {
					t.Fatal(err)
				}
				uninstall(t, h.in, h.item(t, 7247, ""))
			} else {
				uninstall(t, h.in)
			}
			if got := readOrNil(t, h.cfg()); string(got) != local {
				t.Errorf("after uninstall the config holds\n%q\nwant\n%q", got, local)
			}
		})
	}
}

func TestWakaTimeKeyOrder(t *testing.T) {
	made, taken := madeConfig(t, wakaKey, 7247, hackatimeKey)
	tests := []struct {
		name          string
		cfg, setupKey string // "" for no config, no setup file
		want          string // "" for a new key
	}{
		{name: "a line of pairtime's under [api_urls] comes before the setup file's key", cfg: "[api_urls]\n.+ = http://127.0.0.1:7250/api/v1|" + otherWakaKey + "\n", setupKey: wakaKey, want: otherWakaKey},
		{name: "the setup file's key comes before the api_key of a config pairtime made", cfg: made, setupKey: otherWakaKey, want: otherWakaKey},
		{name: "with a setup file, a config pairtime made whose api_key the teen replaced gives the setup file's key, never the teen's", cfg: taken, setupKey: otherWakaKey, want: otherWakaKey},
		{name: "with no setup file, a config pairtime made gives its own api_key, whose hash its mark names", cfg: made, want: wakaKey},
		{name: "with no setup file, a config pairtime made whose api_key the teen replaced gives a new key, never the teen's", cfg: taken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setup := wakacfg.SetupFile(t.TempDir())
			if tt.setupKey != "" {
				writeFile(t, setup, wakacfg.Setup{Key: tt.setupKey, Config: filepath.Join(t.TempDir(), ".wakatime.cfg")}.Encode())
			}
			cfg := Content{Exists: tt.cfg != "", Data: []byte(tt.cfg)}
			got, err := wakaTimeKey(cfg, setup)
			switch {
			case err != nil:
				t.Fatal(err)
			case tt.want != "" && got != tt.want:
				t.Errorf("wakaTimeKey = %s, want %s", got, tt.want)
			case tt.want == "" && (got == hackatimeKey || got == wakaKey || !wakacfg.ValidKey(got)):
				t.Errorf("wakaTimeKey = %s, want a new key", got)
			}
		})
	}
}

func TestWakaTimeTakenOver(t *testing.T) {
	tests := []struct {
		name string
		// record says install made the config, so its manifest and setup
		// file name pairtime's key; without one, only the mark does.
		record bool
		// noMark says the teen deleted pairtime's mark too, with the key.
		noMark bool
		// uninstall runs uninstall, and otherwise install runs again.
		uninstall bool
	}{
		{name: "with no record, uninstall names a config pairtime made whose api_key the teen replaced, leaves its bytes, and shows the teen's key nowhere", uninstall: true},
		{name: "with a record, uninstall names it the same way", record: true, uninstall: true},
		{name: "with a record, uninstall names a config pairtime made whose mark the teen deleted with pairtime's key the same way, by the record", record: true, noMark: true, uninstall: true},
		{name: "with no record, install leaves it alone, and keeps the teen's key nowhere"},
		{name: "with a record, install leaves it alone too", record: true},
		{name: "with a record, install leaves a config pairtime made whose mark the teen deleted with pairtime's key alone too, adding no second key beside the teen's", record: true, noMark: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newWakaHome(t, nil)
			if tt.record {
				install(t, h.in, h.item(t, 7247, wakaKey))
			} else {
				made, _ := madeConfig(t, wakaKey, 7247, "")
				writeFile(t, h.cfg(), []byte(made))
			}
			// The teen gives the config their own key, as WakaTime's
			// extension writes one.
			taken := bytes.Replace(readOrNil(t, h.cfg()), []byte("api_key = "+wakaKey), []byte("api_key = "+hackatimeKey), 1)
			if tt.noMark {
				mark, rest, ok := bytes.Cut(taken, []byte("\n"))
				if !ok || !bytes.HasPrefix(mark, []byte("; made by pairtime install for the key ")) {
					t.Fatalf("the config pairtime made doesn't start with its mark:\n%s", taken)
				}
				taken = rest
			}
			writeFile(t, h.cfg(), taken)
			if tt.uninstall {
				p, err := h.in.PlanUninstall(h.item(t, 7247, ""))
				if err != nil {
					t.Fatal(err)
				}
				problems := strings.Join(p.Problems, "\n")
				if want := h.cfg() + " is the config pairtime made, with an api_key that isn't pairtime's any more; set its api_url to your own server and keep your api_key, or delete the file, which deletes that key too"; problems != want {
					t.Errorf("uninstall's problems are %q, want the config named, then what it is and what to change:\n%q", p.Problems, want)
				}
				if strings.Contains(p.Diff()+problems, hackatimeKey) {
					t.Errorf("uninstall shows the teen's key:\n%s\n%s", p.Diff(), problems)
				}
				if err := p.Apply(); err != nil {
					t.Fatal(err)
				}
			} else {
				_, err := h.in.PlanInstall([]Item{h.item(t, 7247, "")})
				if want := h.cfg() + " is the config pairtime made, with an api_key that isn't pairtime's any more, so pairtime leaves it alone; to send your editor time to pairtime too, set its api_url to your own server, such as Hackatime's, keep your api_key, and run pairtime install again"; err == nil || err.Error() != want {
					t.Fatalf("PlanInstall = %v, want the refusal of a config the teen took over:\n%s", err, want)
				}
				if strings.Contains(err.Error(), hackatimeKey) {
					t.Errorf("the refusal shows the teen's key: %v", err)
				}
			}
			if s, err := wakacfg.ReadSetup(wakacfg.SetupFile(h.data)); err == nil && s.Key == hackatimeKey {
				t.Errorf("the setup file holds the teen's key")
			}
			if got := readOrNil(t, h.cfg()); !bytes.Equal(got, taken) {
				t.Errorf("the config is now\n%q\nwant it as the teen left it:\n%q", got, taken)
			}
		})
	}
}

// TestWakaTimeLeftToTheTeen checks uninstall on a config pairtime made that
// the teen pointed at their own server since: it takes out pairtime's mark,
// which isn't true of the file any more, with pairtime's lines and nothing
// else, and a note naming the file says it leaves the file to the teen, by
// install's record or, without one, by the mark. A reinstall in between
// takes the mark out of what uninstall puts back, with no note, since
// uninstall then puts back what that install recorded.
func TestWakaTimeLeftToTheTeen(t *testing.T) {
	tests := []struct {
		name string
		// record says install made the config, so its manifest names it;
		// without one, only the mark does.
		record bool
		// reinstall runs install again after the teen's change, which adds
		// pairtime's line under [api_urls].
		reinstall bool
		// note says uninstall's plan has the note.
		note bool
	}{
		{name: "with a record, uninstall takes pairtime's mark out of a config pairtime made that sends to the teen's own server now, keeps the rest, and says so in a note naming the file", record: true, note: true},
		{name: "without a record, uninstall knows the config by its mark, and does the same", note: true},
		{name: "with a record and a reinstall since, which added pairtime's line, uninstall takes out the line and the mark, and keeps the rest", record: true, reinstall: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newWakaHome(t, nil)
			if tt.record {
				install(t, h.in, h.item(t, 7247, wakaKey))
			} else {
				made, _ := madeConfig(t, wakaKey, 7247, "")
				writeFile(t, h.cfg(), []byte(made))
			}
			// The teen points the config at Hackatime with their own key, in
			// place, and leaves pairtime's mark.
			mark, rest, _ := strings.Cut(string(readOrNil(t, h.cfg())), "\n")
			theirs := strings.Replace(strings.Replace(rest, "api_url = "+wakacfg.URL(7247), "api_url = https://hackatime.hackclub.com/api/hackatime/v1", 1), "api_key = "+wakaKey, "api_key = "+hackatimeKey, 1)
			if theirs == rest || !strings.HasPrefix(mark, "; made by pairtime install for the key ") {
				t.Fatalf("the config pairtime made is\n%s\n%s\nwant its mark first, then its api_url and api_key", mark, rest)
			}
			writeFile(t, h.cfg(), []byte(mark+"\n"+theirs))
			if tt.reinstall {
				install(t, h.in, h.item(t, 7247, ""))
			}
			p, err := h.in.PlanUninstall(h.item(t, 7247, ""))
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Problems) > 0 {
				t.Errorf("uninstall's problems are %q, want none", p.Problems)
			}
			note := h.cfg() + " is the config pairtime made, and it sends to your own server now, so pairtime leaves it to you and took out only its own lines."
			if got := slices.Contains(p.Notes(), note); got != tt.note {
				t.Errorf("uninstall's notes are %q; want %q among them: %v", p.Notes(), note, tt.note)
			}
			if shown := p.Diff() + strings.Join(p.Notes(), "\n"); strings.Contains(shown, hackatimeKey) {
				t.Errorf("uninstall shows the teen's key:\n%s", shown)
			}
			if err := p.Apply(); err != nil {
				t.Fatal(err)
			}
			if got := string(readOrNil(t, h.cfg())); got != theirs {
				t.Errorf("after uninstall the config holds\n%q\nwant the teen's, without pairtime's mark:\n%q", got, theirs)
			}
		})
	}
}

func TestWakaTimeProxyVariables(t *testing.T) {
	made, _ := madeConfig(t, wakaKey, 7250, "")
	elsewhere := strings.Replace(made, "api_url = http://127.0.0.1:7250/api/v1", "api_url = https://hackatime.hackclub.com/api/hackatime/v1", 1)
	const proxy = "http://proxy.example:3128"
	tests := []struct {
		name string
		vars map[string]string
		cfg  string // the config before install, "" for none
		// refused is a part of the refusal, "" when install takes the
		// item.
		refused string
	}{
		{name: "with HTTPS_PROXY set and no config, install still makes one, whose only api_url is the agent on 127.0.0.1, which no environment proxy reaches", vars: map[string]string{"HTTPS_PROXY": proxy}},
		{name: "with http_proxy set, a config pairtime made still gets the agent's port, as its only api_url is the agent", vars: map[string]string{"http_proxy": proxy}, cfg: made},
		{name: "with HTTPS_PROXY set, a config pairtime made whose api_url now names another server is refused, as any config of the teen's", vars: map[string]string{"HTTPS_PROXY": proxy}, cfg: elsewhere, refused: "would get pairtime's line, but HTTPS_PROXY is set here"},
		{name: "with https_proxy in lower case set, the teen's https config is refused, naming it", vars: map[string]string{"https_proxy": proxy}, cfg: "[settings]\napi_url = https://hackatime.hackclub.com/api/hackatime/v1\napi_key = " + hackatimeKey + "\n", refused: "but https_proxy is set here"},
		{name: "with HTTPS_PROXY set and NO_PROXY naming the server's host, the line goes in, as wakatime-cli takes no proxy then", vars: map[string]string{"HTTPS_PROXY": proxy, "NO_PROXY": "hackatime.hackclub.com"}, cfg: "[settings]\napi_url = https://hackatime.hackclub.com/api/hackatime/v1\napi_key = " + hackatimeKey + "\n"},
		{name: "with only HTTP_PROXY set, the teen's https config gets the line, as wakatime-cli takes HTTP_PROXY only for an http api_url", vars: map[string]string{"HTTP_PROXY": proxy, "http_proxy": proxy}, cfg: "[settings]\napi_url = https://hackatime.hackclub.com/api/hackatime/v1\napi_key = " + hackatimeKey + "\n"},
		{name: "with HTTP_PROXY set, a teen's own server on 127.0.0.1 gets the line, as no environment proxy reaches loopback", vars: map[string]string{"HTTP_PROXY": proxy}, cfg: "[settings]\napi_url = http://127.0.0.1:3000/api/v1\napi_key = " + hackatimeKey + "\n"},
		{name: "an empty HTTPS_PROXY sets no proxy, as Go reads it", vars: map[string]string{"HTTPS_PROXY": ""}, cfg: "[settings]\napi_key = " + hackatimeKey + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newWakaHome(t, tt.vars)
			if tt.cfg != "" {
				writeFile(t, h.cfg(), []byte(tt.cfg))
			}
			if tt.refused != "" {
				_, err := h.in.PlanInstall([]Item{h.item(t, 7247, "")})
				if err == nil || !strings.Contains(err.Error(), tt.refused) {
					t.Fatalf("PlanInstall = %v, want a refusal saying %q", err, tt.refused)
				}
				if got := readOrNil(t, h.cfg()); string(got) != tt.cfg {
					t.Errorf("the config changed:\n%q", got)
				}
				return
			}
			install(t, h.in, h.item(t, 7247, ""))
			s, err := wakacfg.ReadSetup(wakacfg.SetupFile(h.data))
			if err != nil {
				t.Fatal(err)
			}
			cfg := readOrNil(t, h.cfg())
			st, err := wakacfg.Inspect(cfg, s.Key, wakacfg.ByMark)
			if err != nil || !st.Line || st.Port != 7247 {
				t.Errorf("Inspect = %+v, %v, want pairtime's line on port 7247", st, err)
			}
			if tt.cfg == "" && (!st.Own || strings.Count(string(cfg), "api_url") != 1 || !strings.Contains(string(cfg), "api_url = http://127.0.0.1:7247/api/v1\n")) {
				t.Errorf("the config install made is\n%s\nwant pairtime's own, whose only api_url is the agent on 127.0.0.1", cfg)
			}
		})
	}
}

func TestWakaTimeWatermark(t *testing.T) {
	// changes returns the paths the plan for the WakaTime item in h
	// changes, in the order Apply writes them.
	changes := func(t *testing.T, h wakaHome) []string {
		t.Helper()
		p, err := h.in.PlanInstall([]Item{h.item(t, 7247, wakaKey)})
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		var paths []string
		for _, c := range p.Changes {
			paths = append(paths, c.Target.Path)
		}
		return paths
	}
	index := func(paths []string, path string) int {
		for i, p := range paths {
			if p == path {
				return i
			}
		}
		return -1
	}
	tests := []struct {
		name  string
		check func(t *testing.T, h wakaHome)
	}{
		{
			name: "with no config, the watermark is written before the config, so wakatime-cli never reads the config without it",
			check: func(t *testing.T, h wakaHome) {
				paths := changes(t, h)
				if w, c := index(paths, h.internal()), index(paths, h.cfg()); w < 0 || c < 0 || w > c {
					t.Errorf("install writes %q, want wakatime-internal.cfg before the config", paths)
				}
			},
		},
		{
			name: "install run again seeds the watermark in a config pairtime made that has none, as after an install that stopped",
			check: func(t *testing.T, h wakaHome) {
				install(t, h.in, h.item(t, 7247, wakaKey))
				if err := os.Remove(h.internal()); err != nil {
					t.Fatal(err)
				}
				install(t, h.in, h.item(t, 7247, wakaKey))
				if got, want := readOrNil(t, h.internal()), wakaGolden(t, "new-file", "internal.cfg"); !bytes.Equal(got, want) {
					t.Errorf("after install ran again wakatime-internal.cfg holds %q, want %q", got, want)
				}
			},
		},
		{
			name: "the teen's own config gets no watermark, as wakatime-cli has kept its own",
			check: func(t *testing.T, h wakaHome) {
				writeFile(t, h.cfg(), wakaGolden(t, "hackatime-setup", "before.cfg"))
				if paths := changes(t, h); index(paths, h.internal()) >= 0 {
					t.Errorf("install writes %q, want no wakatime-internal.cfg", paths)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, newWakaHome(t, nil))
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
