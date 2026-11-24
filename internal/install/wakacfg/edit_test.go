// SPDX-License-Identifier: AGPL-3.0-or-later

package wakacfg

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// testMark is the mark a config pairtime made for testKey starts with: its
// hash is the first twelve hex digits of the key's SHA-256.
const testMark = "; made by pairtime install for the key 6d61e4224e2f; pairtime uninstall takes it out\n"

// ours is pairtime's line on port 7247 under spelling.
func ours(spelling string) string { return spelling + " = http://127.0.0.1:7247/api/v1|" + testKey }

// hackatime is the config hackatime-setup writes (HC 1.5).
const hackatime = "[settings]\napi_url = https://hackatime.hackclub.com/api/hackatime/v1\napi_key = " + otherKey + "\nheartbeat_rate_limit_seconds = 30\n"

// other is another server's line under [api_urls].
const other = " = https://other.example/api/v1|" + otherKey

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		data string
		// want lists the keys as section/key=value, and wantErr a part of
		// the refusal, with its line.
		want    []string
		wantErr string
	}{
		{name: "keys, sections, comments and blank lines read as wakatime-cli reads them", data: "; top\ntop = 1\n[settings]\n# note\n\napi_key = k\n", want: []string{"/top=1", "settings/api_key=k"}},
		{name: "= and : both end a key, and the value keeps a second one", data: "[s]\na: b=c\nd = http://x:1/y\n", want: []string{"s/a=b=c", "s/d=http://x:1/y"}},
		{name: "a # or ; ends a value", data: "[s]\na = b ; c\nd = e#f\n", want: []string{"s/a=b", "s/d=e"}},
		{name: "quotes around a value come off", data: "[s]\na = \"b c\"\nd = 'e'\n", want: []string{"s/a=b c", "s/d=e"}},
		{name: "an indented line goes on with the value before it, comments and all", data: "[s]\nexclude =\n  ^/tmp/\n\t; still the value\nnext = 1\n", want: []string{"s/exclude=\n  ^/tmp/\n\t; still the value", "s/next=1"}},
		{name: "a blank line ends a value", data: "[s]\na = b\n\n  c = d\n", want: []string{"s/a=b", "s/c=d"}},
		{name: "a byte order mark and CRLF are read past", data: "\xef\xbb\xbf[s]\r\na = b\r\n", want: []string{"s/a=b"}},
		{name: "the last line needs no line break", data: "[s]\na = b", want: []string{"s/a=b"}},
		{name: "a section's name keeps its case and its spaces", data: "[API_URLS]\na = b\n[ s ]\nc = d\n", want: []string{"API_URLS/a=b", " s /c=d"}},
		{name: "a key in backticks, as go-ini's SaveTo writes one with = : or \" in it, is read without them", data: "[s]\n`C:\\x` = b\n`a=b\"c` = d\n", want: []string{"s/C:\\x=b", "s/a=b\"c=d"}},
		{name: "a key in triple quotes, as SaveTo writes one with a backtick in it, is read without them", data: "[s]\n\"\"\"t`u\"\"\" = d\n", want: []string{"s/t`u=d"}},
		{name: "a key in double quotes, which go-ini reads too, is read without them", data: "[s]\n\"q\" = c\n", want: []string{"s/q=c"}},
		{name: "a value in triple quotes over lines, as SaveTo writes one with a line break in it, is read up to the closing quotes, line breaks and all", data: "[s]\na = \"\"\"\n  b\n  c\"\"\" ; after\nd = 1\n", want: []string{"s/a=\n  b\n  c", "s/d=1"}},
		{name: "a value in triple quotes on one line, as SaveTo writes one with a backtick in it, keeps its # and ;", data: "[s]\na = \"\"\"b # c; d`e\"\"\"\n", want: []string{"s/a=b # c; d`e"}},
		{name: "a value in backticks, as SaveTo writes one with # or ; in it, keeps them", data: "[s]\na = `b;c#d`\n", want: []string{"s/a=b;c#d"}},
		{name: "a value in double quotes, as SaveTo writes one with space around it, keeps the space", data: "[s]\na = \" b \"\n", want: []string{"s/a= b "}},
		{name: "a value in triple quotes that are never closed is refused", data: "[s]\na = \"\"\"b\nc\n", wantErr: "line 2 has a value in quotes that aren't closed"},
		{name: "a key in quotes that aren't closed is refused", data: "[s]\n`a = b\n", wantErr: "line 2 has a key in quotes that aren't closed"},
		{name: "a value in quotes that goes on after a backslash is refused", data: "[s]\na = \"\"\"b\nc\"\"\" \\\nd\n", wantErr: "line 2 has a value in quotes that goes on after a backslash"},
		{name: "a value that goes on after a backslash is refused", data: "[s]\na = b \\\nc\n", wantErr: "line 2 has a value that goes on"},
		{name: "an empty key in double quotes is refused, as go-ini then fails the whole file", data: "[s]\n\"\" = b\n", wantErr: "line 2 has a key in quotes with no name"},
		{name: "an empty key in backticks is refused", data: "[s]\n`` = b\n", wantErr: "line 2 has a key in quotes with no name"},
		{name: "an empty key in triple quotes is refused", data: "[s]\n\"\"\"\"\"\" = b\n", wantErr: "line 2 has a key in quotes with no name"},
		{name: "a key in quotes of nothing but spaces is refused", data: "[s]\n\"  \" = b\n", wantErr: "line 2 has a key in quotes with no name"},
		{name: "indented lines that go on past the 4096 bytes go-ini peeks after their key's line are refused", data: "[s]\nexclude =\n" + strings.Repeat("    ^/x/\n", 600), wantErr: "line 2 has indented lines after it that go on past the 4096 bytes"},
		{name: "indented lines within the 4096 bytes go-ini peeks are read whole, in a file longer than that", data: "[s]\nexclude =\n" + strings.Repeat("    ^/x/\n", 400) + "b = 1\n" + strings.Repeat("; pad\n", 200), want: []string{"s/exclude=" + strings.Repeat("\n    ^/x/", 400), "s/b=1"}},
		{name: "a section that isn't closed is refused", data: "[s\na = b\n", wantErr: "line 1 opens a section"},
		{name: "a section without a name is refused", data: "[]\n", wantErr: "line 1 has a section with no name"},
		{name: "a line that is no key is refused", data: "[s]\njust words\n", wantErr: "line 2 is neither"},
		{name: "a value without a key is refused", data: "[s]\n= b\n", wantErr: "line 2 has a value with no key"},
		{name: "a NUL byte is refused", data: "[s]\na = b\x00\n", wantErr: "line 2 holds a NUL byte"},
		{name: "UTF-16 is refused", data: "\xff\xfe[\x00s\x00]\x00", wantErr: "line 1 starts the file as UTF-16"},
		{name: "a line that isn't UTF-8 is refused", data: "[s]\na = \xff\n", wantErr: "line 2 isn't UTF-8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := parse([]byte(tt.data))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parse = %v, want an error saying %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, e := range f.pairs() {
				got = append(got, e.section+"/"+e.key+"="+e.value)
			}
			if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", tt.want) {
				t.Errorf("parse reads %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAdd(t *testing.T) {
	// taken is a config pairtime made whose api_key the teen replaced,
	// without pairtime's mark.
	const taken = "[settings]\napi_url = http://127.0.0.1:7250/api/v1\napi_key = " + otherKey + "\n"
	tests := []struct {
		name, before, after string
		key                 string // "" for testKey
		port                int    // 0 for 7247
		// recorded says install's record says it made the config
		// (Recorded); without it, only the mark does (ByMark).
		recorded bool
		wantErr  string
		wantIs   error
	}{
		{name: "a config pairtime made whose api_key the teen replaced is refused by its mark", before: testMark + taken, port: 7250, wantIs: ErrNotOwn},
		{name: "with install's record, a config pairtime made whose mark the teen deleted with pairtime's key is refused too", before: taken, port: 7250, recorded: true, wantIs: ErrNotOwn},
		{name: "with install's record, a config whose api_url is on 127.0.0.1 on another port than the agent's, as Wakapi's setup writes it, is the teen's own server, and gets pairtime's line", before: "[settings]\napi_url = http://127.0.0.1:3000/api/v1\napi_key = " + otherKey + "\n", recorded: true, after: "[settings]\napi_url = http://127.0.0.1:3000/api/v1\napi_key = " + otherKey + "\n\n[api_urls]\n" + ours(".+") + "\n"},
		{name: "without a record or a mark, a config on 127.0.0.1 with another key gets pairtime's line, since it may be the teen's own server", before: taken, after: taken + "\n[api_urls]\n" + ours(".+") + "\n"},
		{name: "hackatime-setup's config gets an [api_urls] section after a blank line", before: hackatime, after: hackatime + "\n[api_urls]\n" + ours(".+") + "\n"},
		{name: "an existing .* line keeps its server, and pairtime's .+ goes after it", before: "[api_urls]\n.*" + other + "\n", after: "[api_urls]\n.*" + other + "\n" + ours(".+") + "\n"},
		{name: "with .+ taken, pairtime's line is ^.*$", before: "[api_urls]\n.+" + other + "\n", after: "[api_urls]\n.+" + other + "\n" + ours("^.*$") + "\n"},
		{name: "with .+ and ^.*$ taken, pairtime's line is (?s).*", before: "[api_urls]\n.+" + other + "\n^.*$" + other + "\n", after: "[api_urls]\n.+" + other + "\n^.*$" + other + "\n" + ours("(?s).*") + "\n"},
		{name: "with every spelling taken, the file is refused", before: "[api_urls]\n.+" + other + "\n^.*$" + other + "\n(?s).*" + other + "\n", wantErr: "for each of the patterns"},
		{name: "a spelling in another case is taken, as viper lowercases keys", before: "[api_urls]\n.+" + other + "\n^.*$" + other + "\n(?S).*" + other + "\n", wantErr: "for each of the patterns"},
		{name: "[API_URLS] in capitals is the section wakatime-cli reads", before: "[API_URLS]\n.*" + other + "\n", after: "[API_URLS]\n.*" + other + "\n" + ours(".+") + "\n"},
		{name: "a section spelled two ways is refused", before: "[api_urls]\n.*" + other + "\n[Api_Urls]\nx" + other + "\n", wantErr: "spelled both"},
		{name: "CRLF stays CRLF and comments stay", before: "; mine\r\n[settings]\r\napi_key = " + otherKey + " ; keep\r\n# api_url = old\r\n", after: "; mine\r\n[settings]\r\napi_key = " + otherKey + " ; keep\r\n# api_url = old\r\n\r\n[api_urls]\r\n" + ours(".+") + "\r\n"},
		{name: "a byte order mark stays first", before: "\xef\xbb\xbf[settings]\napi_key = " + otherKey + "\n", after: "\xef\xbb\xbf[settings]\napi_key = " + otherKey + "\n\n[api_urls]\n" + ours(".+") + "\n"},
		{name: "a file without a final line break gets one before the new section", before: "[settings]\napi_key = " + otherKey, after: "[settings]\napi_key = " + otherKey + "\n\n[api_urls]\n" + ours(".+") + "\n"},
		{name: "a file that ends with a blank line gets no second one", before: "[settings]\napi_key = " + otherKey + "\n\n", after: "[settings]\napi_key = " + otherKey + "\n\n[api_urls]\n" + ours(".+") + "\n"},
		{name: "a blank file gets the section and nothing before it", before: "\n", after: "\n[api_urls]\n" + ours(".+") + "\n"},
		{name: "the line goes after the section's last key, before what follows it", before: "[api_urls]\n.*" + other + "\n; note\n\n[settings]\napi_key = " + otherKey + "\n", after: "[api_urls]\n.*" + other + "\n" + ours(".+") + "\n; note\n\n[settings]\napi_key = " + otherKey + "\n"},
		{name: "an empty section gets the line after its header", before: "[api_urls]\n\n[settings]\napi_key = " + otherKey + "\n", after: "[api_urls]\n" + ours(".+") + "\n\n[settings]\napi_key = " + otherKey + "\n"},
		{name: "before an indented line, which would go on with it, the line goes at the section's end", before: "[api_urls]\n  ; a note\n[settings]\napi_key = " + otherKey + "\n", after: "[api_urls]\n  ; a note\n" + ours(".+") + "\n[settings]\napi_key = " + otherKey + "\n"},
		{name: "a section with an indented line after each place is refused", before: "[api_urls]\n  ; a note\n  [settings]\napi_key = " + otherKey + "\n", wantErr: "indented line"},
		{name: "an [api_urls] key's indented lines stay with it, and the line goes after them", before: "[api_urls]\n.* =\n    https://other.example/api/v1|" + otherKey + "\n[settings]\napi_key = " + otherKey + "\n", after: "[api_urls]\n.* =\n    https://other.example/api/v1|" + otherKey + "\n" + ours(".+") + "\n[settings]\napi_key = " + otherKey + "\n"},
		{name: "an indented list at the end of the file stays one value", before: "[settings]\nexclude =\n    ^/tmp/\n", after: "[settings]\nexclude =\n    ^/tmp/\n\n[api_urls]\n" + ours(".+") + "\n"},
		{name: "a last key without a final line break gets the line after a new one", before: "[api_urls]\n.*" + other, after: "[api_urls]\n.*" + other + "\n" + ours(".+")},
		{name: "pairtime's line that is there stays as it is", before: hackatime + "\n[api_urls]\n" + ours(".+") + "\n", after: hackatime + "\n[api_urls]\n" + ours(".+") + "\n"},
		{name: "pairtime's line on another port gets the new port and nothing else", before: "[api_urls]\n^.*$ = http://127.0.0.1:7248/api/v1|" + testKey + "\r\n.*" + other + "\r\n", after: "[api_urls]\n" + ours("^.*$") + "\r\n.*" + other + "\r\n"},
		{name: "a config pairtime made gets the new port in its api_url", before: "[settings]\napi_url = http://127.0.0.1:7248/api/v1\napi_key = " + testKey + "\n", after: "[settings]\napi_url = http://127.0.0.1:7247/api/v1\napi_key = " + testKey + "\n"},
		{name: "a config that sets a proxy is refused, since wakatime-cli would send pairtime's line through it too", before: hackatime + "proxy = https://user:secret@proxy.example:3128\n", wantIs: ErrProxy},
		{name: "[Settings] PROXY in capitals is the proxy wakatime-cli reads", before: "[Settings]\nPROXY = socks5://proxy.example:1080\napi_key = " + otherKey + "\n", wantIs: ErrProxy},
		{name: "with a proxy, a line of pairtime's that is there isn't moved to the new port either", before: "[settings]\nproxy = http://proxy.example:3128\n[api_urls]\n.+ = http://127.0.0.1:7250/api/v1|" + testKey + "\n", wantIs: ErrProxy},
		{name: "an empty proxy is none, as wakatime-cli reads it, so the line goes in", before: hackatime + "proxy = ''\n", after: hackatime + "proxy = ''\n\n[api_urls]\n" + ours(".+") + "\n"},
		{name: "a config that imports another is refused, since wakatime-cli reads the import over it", before: hackatime + "import_cfg = ~/.private/wakatime.cfg\n", wantIs: ErrImport},
		{name: "an empty import_cfg imports nothing", before: hackatime + "import_cfg =\n", after: hackatime + "import_cfg =\n\n[api_urls]\n" + ours(".+") + "\n"},
		{name: "a proxy in another section isn't wakatime-cli's", before: "[other]\nproxy = http://proxy.example:3128\n", after: "[other]\nproxy = http://proxy.example:3128\n\n[api_urls]\n" + ours(".+") + "\n"},
		{name: "a key not in WakaTime's format is refused before the file is read, even a file the package can't read", before: "[settings\napi_key = " + otherKey + "\n", key: "00000000-0000-4000-8000-00000000A9E7", wantIs: ErrKey},
		{name: "a port out of range is refused", before: hackatime, port: 65536, wantIs: ErrPort},
		{name: "a file go-ini's parser fails on is refused, though wakatime-cli's salvage reads part of it", before: "[settings\napi_key = " + otherKey + "\n", wantErr: "line 1 opens a section"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, port := tt.key, tt.port
			if key == "" {
				key = testKey
			}
			if port == 0 {
				port = 7247
			}
			made := ByMark
			if tt.recorded {
				made = Recorded
			}
			got, err := Add([]byte(tt.before), key, port, made)
			checkResult(t, got, err, tt.after, tt.wantErr, tt.wantIs)
		})
	}
}

func TestMake(t *testing.T) {
	tests := []struct {
		name, want string
		key        string
		wantIs     error
	}{
		{name: "the config made for someone with none sends to the agent alone, after pairtime's mark, which names a hash of the key", key: testKey, want: testMark + "[settings]\napi_url = http://127.0.0.1:7247/api/v1\napi_key = " + testKey + "\n"},
		{name: "a key not in WakaTime's format is refused", key: "nope", wantIs: ErrKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Make(tt.key, 7247)
			checkResult(t, got, err, tt.want, "", tt.wantIs)
			if err == nil {
				if s, err := Inspect(got, tt.key, ByMark); err != nil || !s.Own || s.Port != 7247 {
					t.Errorf("Inspect reads the config Make made as %+v, %v, want pairtime's own on port 7247", s, err)
				}
			}
		})
	}
}

func TestSetPort(t *testing.T) {
	tests := []struct {
		name, before, after string
		wantIs              error
	}{
		{name: "the line's port changes and its spelling stays", before: "[api_urls]\n(?s).* = http://127.0.0.1:7250/api/v1|" + testKey + "\n", after: "[api_urls]\n" + ours("(?s).*") + "\n"},
		{name: "a line on the port already leaves the file as it is", before: "[api_urls]\n" + ours(".+"), after: "[api_urls]\n" + ours(".+")},
		{name: "another key's line isn't pairtime's to change", before: "[api_urls]\n.+ = http://127.0.0.1:7250/api/v1|" + otherKey + "\n", wantIs: ErrNoLine},
		{name: "a file without pairtime's line is ErrNoLine", before: hackatime, wantIs: ErrNoLine},
		{name: "a config go-ini's SaveTo rewrote, with a list in triple quotes, gets the new port", before: "[settings]\nexclude = \"\"\"\n  ^/tmp/\"\"\"\n\n[api_urls]\n.+ = http://127.0.0.1:7250/api/v1|" + testKey + "\n", after: "[settings]\nexclude = \"\"\"\n  ^/tmp/\"\"\"\n\n[api_urls]\n" + ours(".+") + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SetPort([]byte(tt.before), testKey, 7247)
			checkResult(t, got, err, tt.after, "", tt.wantIs)
		})
	}
}

func TestRemove(t *testing.T) {
	made := "[settings]\napi_url = http://127.0.0.1:7250/api/v1\napi_key = " + testKey + "\n"
	// all is a section install added with a blank line before it, and
	// broke a line break after the file's last line besides.
	all := Section{Header: true, Blank: true}
	broke := Section{Header: true, Blank: true, Break: true}
	tests := []struct {
		name, before, after string
		key                 string
		made                Made
		sec                 Section
		// ports are the agent's own ports, nil for any (takenOver).
		ports      []int
		gone, left bool
		wantErr    string // a part of the refusal
	}{
		{name: "pairtime's line comes out of a section that was there", before: "[api_urls]\n.*" + other + "\n" + ours(".+") + "\n; note\n", after: "[api_urls]\n.*" + other + "\n; note\n", key: testKey},
		{name: "pairtime's section goes with its line and the blank line before it", before: hackatime + "\n[api_urls]\n" + ours(".+") + "\n", after: hackatime, key: testKey, sec: all},
		{name: "a blank line the file ended with before install stays, as install added none", before: hackatime + "\n[api_urls]\n" + ours(".+") + "\n", after: hackatime + "\n", key: testKey, sec: Section{Header: true}},
		{name: "the line break install added after the file's last line goes too", before: hackatime + "\n[api_urls]\n" + ours(".+") + "\n", after: strings.TrimSuffix(hackatime, "\n"), key: testKey, sec: broke},
		{name: "a CRLF line break install added goes whole", before: "[s]\r\na = b\r\n\r\n[api_urls]\r\n" + ours(".+") + "\r\n", after: "[s]\r\na = b", key: testKey, sec: broke},
		{name: "the line break stays when the teen added something after pairtime's section", before: hackatime + "\n[api_urls]\n" + ours(".+") + "\n[mine]\nx = 1\n", after: hackatime + "[mine]\nx = 1\n", key: testKey, sec: broke},
		{name: "pairtime's section stays when a comment was added under it since, as pairtime writes none there", before: hackatime + "\n[api_urls]\n" + ours(".+") + "\n; mine\n", after: hackatime + "\n[api_urls]\n; mine\n", key: testKey, sec: broke},
		{name: "pairtime's section stays when a key was added to it since", before: hackatime + "\n[api_urls]\n" + ours(".+") + "\nmine" + other + "\n", after: hackatime + "\n[api_urls]\nmine" + other + "\n", key: testKey, sec: broke},
		{name: "a section that was there before stays empty", before: "[api_urls]\n" + ours(".+") + "\n", after: "[api_urls]\n", key: testKey},
		{name: "a line ending the file without a line break takes the one before it", before: "[api_urls]\n.*" + other + "\n" + ours(".+"), after: "[api_urls]\n.*" + other, key: testKey},
		{name: "another key's line stays", before: "[api_urls]\n" + ours(".+") + "\n", after: "[api_urls]\n" + ours(".+") + "\n", key: otherKey},
		{name: "with no key, every line of pairtime's goes", before: "[api_urls]\n" + ours(".+") + "\n^.*$ = http://127.0.0.1:7250/api/v1|" + otherKey + "\n.*" + other + "\n", after: "[api_urls]\n.*" + other + "\n"},
		{name: "a config pairtime made goes when nothing else is left in it", before: made, after: "[settings]\n", key: testKey, made: Recorded, sec: all, gone: true},
		{name: "a config pairtime made keeps what was added to it since", before: made + "hostname = mine\n", after: "[settings]\nhostname = mine\n", key: testKey, made: Recorded, sec: all},
		{name: "with no key, a config pairtime made is recognized by its mark, which goes with it", before: testMark + made, after: "[settings]\n", made: ByMark, sec: all, gone: true},
		{name: "without a record, a config pairtime made whose api_key the teen replaced is refused by its mark, which stays, and the refusal says how to keep the teen's key before it says to delete the file", before: testMark + strings.Replace(made, testKey, otherKey, 1), key: testKey, made: ByMark, sec: all, wantErr: "isn't pairtime's any more; set its api_url to your own server and keep your api_key, or delete the file, which deletes that key too"},
		{name: "with install's record, a config pairtime made whose mark the teen deleted with pairtime's key is refused too", before: strings.Replace(made, testKey, otherKey, 1), key: testKey, made: Recorded, sec: all, ports: []int{7250}, wantErr: "isn't pairtime's any more; set its api_url to your own server"},
		{name: "with install's record, a config whose api_url is on 127.0.0.1 on another port than the agent's own, as Wakapi's setup writes it, is the teen's own server: it isn't refused, and is left to the teen as it is", before: "[settings]\napi_url = http://127.0.0.1:3000/api/v1\napi_key = " + otherKey + "\n", after: "[settings]\napi_url = http://127.0.0.1:3000/api/v1\napi_key = " + otherKey + "\n", key: testKey, made: Recorded, sec: all, ports: []int{7250}, left: true},
		{name: "without a record or a mark, a config on 127.0.0.1 with another key stays as it is, since it may be the teen's own server", before: strings.Replace(made, testKey, otherKey, 1), after: strings.Replace(made, testKey, otherKey, 1), key: testKey, made: ByMark, sec: all},
		{name: "with no key, a config whose mark names another key's hash than its api_key's is refused too: the key is the teen's", before: testMark + strings.Replace(made, testKey, otherKey, 1), made: ByMark, sec: all, wantErr: "isn't pairtime's any more"},
		{name: "with no key and no mark, a config on 127.0.0.1 stays, since it may be the teen's own server", before: made, after: made, made: ByMark, sec: all},
		{name: "with install's record, a config pairtime made that sends to the teen's own server now loses pairtime's mark and line, with the section install added for the line, and is left to the teen", before: testMark + hackatime + "\n[api_urls]\n" + ours(".+") + "\n", after: hackatime, key: testKey, made: Recorded, sec: all, left: true},
		{name: "without a record, a config with pairtime's mark that sends to another server now loses the mark, and is left to the teen", before: testMark + hackatime, after: hackatime, made: ByMark, sec: all, left: true},
		{name: "with install's record, a config pairtime made that sends to the teen's own server now, without the mark, keeps its api_url and api_key, and is left to the teen", before: hackatime, after: hackatime, key: testKey, made: Recorded, sec: all, left: true},
		{name: "without a record or a mark, a config of another server is no config pairtime made, so nothing is left to the teen", before: hackatime, after: hackatime, key: testKey, made: ByMark, sec: all},
		{name: "a config that was there before install keeps a mark of pairtime's, which only a config pairtime made loses", before: testMark + hackatime + "\n[api_urls]\n" + ours(".+") + "\n", after: testMark + hackatime, key: testKey, made: Existed, sec: all},
		{name: "a file that held only pairtime's line stays, empty, even as made, since install never makes one of that shape", before: "[api_urls]\n" + ours(".+") + "\n", after: "", key: testKey, made: Recorded, sec: all, left: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gone, left, err := Remove([]byte(tt.before), tt.key, tt.made, tt.sec, tt.ports...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Remove = %q, %v, want an error saying %q", got, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.after || gone != tt.gone || left != tt.left {
				t.Errorf("Remove = %q, gone %v, left %v, want %q, gone %v, left %v", got, gone, left, tt.after, tt.gone, tt.left)
			}
		})
	}
}

func TestAdded(t *testing.T) {
	tests := []struct {
		name, base string
		want       Section
	}{
		{name: "hackatime-setup's config gets the section after a blank line", base: hackatime, want: Section{Header: true, Blank: true}},
		{name: "a config that ends with a blank line gets no second one", base: hackatime + "\n", want: Section{Header: true}},
		{name: "a config without a final line break gets one, then the blank line", base: strings.TrimSuffix(hackatime, "\n"), want: Section{Header: true, Blank: true, Break: true}},
		{name: "a blank file gets the section with nothing before it", base: "\n", want: Section{Header: true}},
		{name: "an empty file gets the section alone", base: "", want: Section{Header: true}},
		{name: "a file of a byte order mark alone gets the section alone", base: "\xef\xbb\xbf", want: Section{Header: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Added([]byte(tt.base)); got != tt.want {
				t.Errorf("Added(%q) = %+v, want %+v", tt.base, got, tt.want)
			}
		})
	}
}

// TestAddThenRemove checks that uninstall's changed path gives back every
// byte: a config install added pairtime's line to, whose port the agent
// changed since, loses only what Add put there (DESIGN 5.6, "Uninstall").
func TestAddThenRemove(t *testing.T) {
	tests := []struct {
		name, base string
	}{
		{"hackatime-setup's config comes back as it was", hackatime},
		{"a config that ended with a blank line keeps it", hackatime + "\n"},
		{"a config that ended without a line break ends without one again", strings.TrimSuffix(hackatime, "\n")},
		{"a blank file stays one blank line", "\n"},
		{"an empty file stays empty", ""},
		{"CRLF without a final line break comes back as it was", "[settings]\r\napi_key = " + otherKey},
		{"a config with a byte order mark and an indented list at the end comes back as it was", "\xef\xbb\xbf[settings]\nexclude =\n    ^/tmp/\n"},
		{"a config with a section of another server keeps it, with pairtime's line gone from it", "[api_urls]\n.*" + other + "\n"},
		{"a config go-ini's SaveTo wrote, with a list in triple quotes and a key in backticks, comes back as it was", "[settings]\napi_key = " + otherKey + "\nexclude = \"\"\"\n    ^/tmp/\n    ^/private/\"\"\"\n\n[projectmap]\n`C:\\Users\\teen\\x` = mine\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			added, err := Add([]byte(tt.base), testKey, 7247, ByMark)
			if err != nil {
				t.Fatal(err)
			}
			moved, err := SetPort(added, testKey, 7250)
			if err != nil {
				t.Fatal(err)
			}
			var sec Section
			if !HasAPIURLs([]byte(tt.base)) {
				sec = Added([]byte(tt.base))
			}
			got, gone, left, err := Remove(moved, testKey, Existed, sec)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.base || gone || left {
				t.Errorf("after Add, SetPort and Remove the file holds %q, gone %v, left %v, want %q", got, gone, left, tt.base)
			}
		})
	}
}

func TestWatermark(t *testing.T) {
	now := time.Date(2026, 11, 24, 5, 30, 0, 0, time.FixedZone("", 3600))
	const at = "ai_logs_last_parsed_at = 2026-11-24T04:30:00Z"
	tests := []struct {
		name, before, after string
	}{
		{name: "with no file, it is made with the watermark at now, in UTC", after: "[internal]\n" + at + "\n"},
		{name: "a watermark that is there stays", before: "[internal]\nai_logs_last_parsed_at = 2026-01-02T03:04:05.123Z\n", after: "[internal]\nai_logs_last_parsed_at = 2026-01-02T03:04:05.123Z\n"},
		{name: "the watermark goes after wakatime-cli's own keys", before: "[internal]\nbackoff_at = \nbackoff_retries = 0\n", after: "[internal]\nbackoff_at = \nbackoff_retries = 0\n" + at + "\n"},
		{name: "an empty watermark gets now", before: "[internal]\nai_logs_last_parsed_at =\nbackoff_retries = 0\n", after: "[internal]\n" + at + "\nbackoff_retries = 0\n"},
		{name: "a file without [internal] gets the section", before: "[other]\nx = 1\n", after: "[other]\nx = 1\n\n[internal]\n" + at + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var before []byte
			if tt.before != "" {
				before = []byte(tt.before)
			}
			got, err := Watermark(before, now)
			checkResult(t, got, err, tt.after, "", nil)
		})
	}
}

func TestInspect(t *testing.T) {
	tests := []struct {
		name, data string
		// recorded says install's record says it made the config
		// (Recorded); without it, only the mark does (ByMark).
		recorded bool
		ports    []int // the agent's own ports, nil for any (takenOver)
		want     State
	}{
		{name: "hackatime-setup's config with pairtime's line has two destinations", data: hackatime + "\n[api_urls]\n" + ours("^.*$") + "\n", want: State{Line: true, Port: 7247, Server: "https://hackatime.hackclub.com/api/hackatime/v1", ServerKey: true}},
		{name: "without pairtime's line, Line is false", data: hackatime, want: State{Server: "https://hackatime.hackclub.com/api/hackatime/v1", ServerKey: true}},
		{name: "a config with no api_url sends to WakaTime's own", data: "[settings]\napi_key = " + otherKey + "\n[api_urls]\n" + ours(".+") + "\n", want: State{Line: true, Port: 7247, Server: DefaultURL, ServerKey: true}},
		{name: "a config with no api_key sends nothing", data: "[api_urls]\n" + ours(".+") + "\n", want: State{Line: true, Port: 7247, Server: DefaultURL}},
		{name: "a key from a vault command counts", data: "[settings]\napi_key_vault_cmd = pass waka\n", want: State{Server: DefaultURL, ServerKey: true}},
		{name: "an apikey is the server's key too, as wakatime-cli reads it after api_key", data: "[settings]\napikey = " + otherKey + "\n[api_urls]\n" + ours(".+") + "\n", want: State{Line: true, Port: 7247, Server: DefaultURL, ServerKey: true}},
		{name: "an empty api_key hides an apikey, as wakatime-cli reads the first of them that is there", data: "[settings]\napi_key =\napikey = " + otherKey + "\n", want: State{Server: DefaultURL}},
		{name: "an api_key not in WakaTime's format is one wakatime-cli refuses", data: "[settings]\napi_key = nope\n", want: State{Server: DefaultURL, ServerKey: true, BadKey: "[settings] api_key"}},
		{name: "a key not in WakaTime's format under [api_urls] is one wakatime-cli refuses", data: hackatime + "[api_urls]\n.* = https://other.example/api/v1|nope\n", want: State{Server: "https://hackatime.hackclub.com/api/hackatime/v1", ServerKey: true, BadKey: "[api_urls]"}},
		{name: "an [api_urls] line with an empty key after its | is one wakatime-cli refuses", data: hackatime + "[api_urls]\n.* = https://other.example/api/v1|\n", want: State{Server: "https://hackatime.hackclub.com/api/hackatime/v1", ServerKey: true, BadKey: "[api_urls]"}},
		{name: "an [api_urls] line without a | takes [settings]'s key", data: hackatime + "[api_urls]\n.* = https://other.example/api/v1\n", want: State{Server: "https://hackatime.hackclub.com/api/hackatime/v1", ServerKey: true}},
		{name: "a [project_api_key] value not in WakaTime's format is one wakatime-cli refuses", data: hackatime + "[project_api_key]\nmine = nope\n", want: State{Server: "https://hackatime.hackclub.com/api/hackatime/v1", ServerKey: true, BadKey: "[project_api_key]"}},
		{name: "of an [api_urls] key written twice, wakatime-cli reads the last value", data: hackatime + "[api_urls]\n.* = https://a.example|nope\n.* = https://b.example|" + otherKey + "\n", want: State{Server: "https://hackatime.hackclub.com/api/hackatime/v1", ServerKey: true}},
		{name: "a config pairtime made has only the agent", data: "[settings]\napi_url = http://127.0.0.1:7250/api/v1\napi_key = " + testKey + "\n", want: State{Line: true, Own: true, Port: 7250}},
		{name: "a config that imports another says so", data: hackatime + "import_cfg = ~/.private/wakatime.cfg\n", want: State{Server: "https://hackatime.hackclub.com/api/hackatime/v1", ServerKey: true, Import: true}},
		{name: "an api_url ending in a path wakatime-cli takes off is the server it sends to", data: "[settings]\napi_url = https://api.wakatime.com/api/v1/heartbeats\napi_key = " + otherKey + "\n", want: State{Server: DefaultURL, ServerKey: true}},
		{name: "an api_url in quotes with a final slash is the server without them", data: "[settings]\napi_url = \"https://hackatime.hackclub.com/api/hackatime/v1/\"\napi_key = " + otherKey + "\n", want: State{Server: "https://hackatime.hackclub.com/api/hackatime/v1", ServerKey: true}},
		{name: "an api_url in double quotes inside single ones loses both, as go-ini takes off the outer and wakatime-cli the inner", data: "[settings]\napi_url = '\"https://hackatime.hackclub.com/api/hackatime/v1/\"'\napi_key = " + otherKey + "\n", want: State{Server: "https://hackatime.hackclub.com/api/hackatime/v1", ServerKey: true}},
		{name: "an [api_urls] key with a quote after it is one wakatime-cli takes, as it takes quotes off a value's ends first", data: hackatime + "[api_urls]\n.* = https://other.example/api/v1|" + otherKey + "'\n", want: State{Server: "https://hackatime.hackclub.com/api/hackatime/v1", ServerKey: true}},
		{name: "a config pairtime made whose api_key the teen replaced is taken over, with the agent as its server", data: testMark + "[settings]\napi_url = http://127.0.0.1:7247/api/v1\napi_key = " + otherKey + "\n", want: State{TakenOver: true, Server: "http://127.0.0.1:7247/api/v1", ServerKey: true}},
		{name: "with install's record, a config pairtime made whose mark the teen deleted with pairtime's key is taken over too", data: "[settings]\napi_url = http://127.0.0.1:7247/api/v1\napi_key = " + otherKey + "\n", recorded: true, ports: []int{7247}, want: State{TakenOver: true, Server: "http://127.0.0.1:7247/api/v1", ServerKey: true}},
		{name: "with install's record, a config whose api_url is on 127.0.0.1 on another port than the agent's own, as Wakapi's setup writes it, isn't taken over", data: "[settings]\napi_url = http://127.0.0.1:3000/api/v1\napi_key = " + otherKey + "\n", recorded: true, ports: []int{7247}, want: State{Server: "http://127.0.0.1:3000/api/v1", ServerKey: true}},
		{name: "without a record or a mark, the same config may be the teen's own server on 127.0.0.1, and isn't taken over", data: "[settings]\napi_url = http://127.0.0.1:7247/api/v1\napi_key = " + otherKey + "\n", want: State{Server: "http://127.0.0.1:7247/api/v1", ServerKey: true}},
		{name: "a config that sets a proxy says so, beside pairtime's line", data: hackatime + "proxy = http://proxy.example:3128\n\n[api_urls]\n" + ours(".+") + "\n", want: State{Line: true, Port: 7247, Server: "https://hackatime.hackclub.com/api/hackatime/v1", ServerKey: true, Proxy: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			made := ByMark
			if tt.recorded {
				made = Recorded
			}
			got, err := Inspect([]byte(tt.data), testKey, made, tt.ports...)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("Inspect = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestKey(t *testing.T) {
	tests := []struct {
		name, data, want string
		ok               bool
	}{
		{name: "the key of pairtime's line under [api_urls]", data: hackatime + "[api_urls]\n^.*$ = http://127.0.0.1:7250/api/v1|" + testKey + "\n", want: testKey, ok: true},
		{name: "the api_key of a config pairtime made, by its mark", data: testMark + "[settings]\napi_url = http://127.0.0.1:7250/api/v1\napi_key = " + testKey + "\n", want: testKey, ok: true},
		{name: "a config pairtime made whose api_key the teen replaced gives no key, since its mark names a hash of pairtime's", data: testMark + "[settings]\napi_url = http://127.0.0.1:7250/api/v1\napi_key = " + otherKey + "\n"},
		{name: "a mark without a key's hash, as no version of pairtime writes, makes no config pairtime's", data: "; made by pairtime install; pairtime uninstall takes it out\n[settings]\napi_url = http://127.0.0.1:7250/api/v1\napi_key = " + testKey + "\n"},
		{name: "a config of the teen's own server on 127.0.0.1, without pairtime's mark, isn't pairtime's", data: "[settings]\napi_url = http://127.0.0.1:3000/api/v1\napi_key = " + otherKey + "\n"},
		{name: "another server's line under a spelling of pairtime's isn't pairtime's", data: "[api_urls]\n.+" + other + "\n"},
		{name: "a line to the agent under another pattern isn't pairtime's", data: "[api_urls]\n.* = http://127.0.0.1:7250/api/v1|" + testKey + "\n"},
		{name: "a line to the agent's port with a leading zero isn't pairtime's", data: "[api_urls]\n.+ = http://127.0.0.1:07250/api/v1|" + testKey + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Key([]byte(tt.data))
			if got != tt.want || ok != tt.ok {
				t.Errorf("Key = %q, %v, want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// checkResult checks a change's result: the bytes want, or an error that
// says wantErr or is wantIs.
func checkResult(t *testing.T, got []byte, err error, want, wantErr string, wantIs error) {
	t.Helper()
	switch {
	case wantIs != nil:
		if !errors.Is(err, wantIs) {
			t.Fatalf("got %q, %v, want %v", got, err, wantIs)
		}
	case wantErr != "":
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("got %q, %v, want an error saying %q", got, err, wantErr)
		}
	case err != nil:
		t.Fatal(err)
	case string(got) != want:
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}
