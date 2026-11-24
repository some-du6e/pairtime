// SPDX-License-Identifier: AGPL-3.0-or-later

package wakacfg

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// line is pairtime's line on port 7247 under spelling.
func line(spelling string) string { return spelling + " = http://127.0.0.1:7247/api/v1|" + testKey }

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
		{name: "a key in quotes is refused", data: "[s]\n\"a\" = b\n", wantErr: "line 2 has a key in quotes"},
		{name: "a value in triple quotes is refused", data: "[s]\na = \"\"\"b\n", wantErr: "line 2 has a value in triple quotes"},
		{name: "a value in backticks is refused", data: "[s]\na = `b`\n", wantErr: "line 2 has a value in triple quotes or backticks"},
		{name: "a value that goes on after a backslash is refused", data: "[s]\na = b \\\nc\n", wantErr: "line 2 has a value that goes on"},
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
	tests := []struct {
		name, before, after string
		key                 string // "" for testKey
		port                int    // 0 for 7247
		wantErr             string
		wantIs              error
	}{
		{name: "hackatime-setup's config gets an [api_urls] section after a blank line", before: hackatime, after: hackatime + "\n[api_urls]\n" + line(".+") + "\n"},
		{name: "an existing .* line keeps its server, and pairtime's .+ goes after it", before: "[api_urls]\n.*" + other + "\n", after: "[api_urls]\n.*" + other + "\n" + line(".+") + "\n"},
		{name: "with .+ taken, pairtime's line is ^.*$", before: "[api_urls]\n.+" + other + "\n", after: "[api_urls]\n.+" + other + "\n" + line("^.*$") + "\n"},
		{name: "with .+ and ^.*$ taken, pairtime's line is (?s).*", before: "[api_urls]\n.+" + other + "\n^.*$" + other + "\n", after: "[api_urls]\n.+" + other + "\n^.*$" + other + "\n" + line("(?s).*") + "\n"},
		{name: "with every spelling taken, the file is refused", before: "[api_urls]\n.+" + other + "\n^.*$" + other + "\n(?s).*" + other + "\n", wantErr: "for each of the patterns"},
		{name: "a spelling in another case is taken, as viper lowercases keys", before: "[api_urls]\n.+" + other + "\n^.*$" + other + "\n(?S).*" + other + "\n", wantErr: "for each of the patterns"},
		{name: "[API_URLS] in capitals is the section wakatime-cli reads", before: "[API_URLS]\n.*" + other + "\n", after: "[API_URLS]\n.*" + other + "\n" + line(".+") + "\n"},
		{name: "a section spelled two ways is refused", before: "[api_urls]\n.*" + other + "\n[Api_Urls]\nx" + other + "\n", wantErr: "spelled both"},
		{name: "CRLF stays CRLF and comments stay", before: "; mine\r\n[settings]\r\napi_key = " + otherKey + " ; keep\r\n# api_url = old\r\n", after: "; mine\r\n[settings]\r\napi_key = " + otherKey + " ; keep\r\n# api_url = old\r\n\r\n[api_urls]\r\n" + line(".+") + "\r\n"},
		{name: "a byte order mark stays first", before: "\xef\xbb\xbf[settings]\napi_key = " + otherKey + "\n", after: "\xef\xbb\xbf[settings]\napi_key = " + otherKey + "\n\n[api_urls]\n" + line(".+") + "\n"},
		{name: "a file without a final line break gets one before the new section", before: "[settings]\napi_key = " + otherKey, after: "[settings]\napi_key = " + otherKey + "\n\n[api_urls]\n" + line(".+") + "\n"},
		{name: "a file that ends with a blank line gets no second one", before: "[settings]\napi_key = " + otherKey + "\n\n", after: "[settings]\napi_key = " + otherKey + "\n\n[api_urls]\n" + line(".+") + "\n"},
		{name: "a blank file gets the section and nothing before it", before: "\n", after: "\n[api_urls]\n" + line(".+") + "\n"},
		{name: "the line goes after the section's last key, before what follows it", before: "[api_urls]\n.*" + other + "\n; note\n\n[settings]\napi_key = " + otherKey + "\n", after: "[api_urls]\n.*" + other + "\n" + line(".+") + "\n; note\n\n[settings]\napi_key = " + otherKey + "\n"},
		{name: "an empty section gets the line after its header", before: "[api_urls]\n\n[settings]\napi_key = " + otherKey + "\n", after: "[api_urls]\n" + line(".+") + "\n\n[settings]\napi_key = " + otherKey + "\n"},
		{name: "before an indented line, which would go on with it, the line goes at the section's end", before: "[api_urls]\n  ; a note\n[settings]\napi_key = " + otherKey + "\n", after: "[api_urls]\n  ; a note\n" + line(".+") + "\n[settings]\napi_key = " + otherKey + "\n"},
		{name: "a section with an indented line after each place is refused", before: "[api_urls]\n  ; a note\n  [settings]\napi_key = " + otherKey + "\n", wantErr: "indented line"},
		{name: "a key's indented lines stay with it, and the line goes after them", before: "[api_urls]\n.*" + other + "\n[settings]\nexclude =\n    ^/tmp/\n    ^/private/\n", after: "[api_urls]\n.*" + other + "\n" + line(".+") + "\n[settings]\nexclude =\n    ^/tmp/\n    ^/private/\n"},
		{name: "an indented list at the end of the file stays one value", before: "[settings]\nexclude =\n    ^/tmp/\n", after: "[settings]\nexclude =\n    ^/tmp/\n\n[api_urls]\n" + line(".+") + "\n"},
		{name: "a last key without a final line break gets the line after a new one", before: "[api_urls]\n.*" + other, after: "[api_urls]\n.*" + other + "\n" + line(".+")},
		{name: "pairtime's line that is there stays as it is", before: hackatime + "\n[api_urls]\n" + line(".+") + "\n", after: hackatime + "\n[api_urls]\n" + line(".+") + "\n"},
		{name: "pairtime's line on another port gets the new port and nothing else", before: "[api_urls]\n^.*$ = http://127.0.0.1:7248/api/v1|" + testKey + "\r\n.*" + other + "\r\n", after: "[api_urls]\n" + line("^.*$") + "\r\n.*" + other + "\r\n"},
		{name: "a config pairtime made gets the new port in its api_url", before: "[settings]\napi_url = http://127.0.0.1:7248/api/v1\napi_key = " + testKey + "\n", after: "[settings]\napi_url = http://127.0.0.1:7247/api/v1\napi_key = " + testKey + "\n"},
		{name: "a key not in WakaTime's format is refused before the file is read", before: hackatime, key: "00000000-0000-4000-8000-00000000A9E7", wantIs: ErrKey},
		{name: "a port out of range is refused", before: hackatime, port: 65536, wantIs: ErrPort},
		{name: "a file wakatime-cli can't read is refused", before: "[settings\napi_key = " + otherKey + "\n", wantErr: "line 1 opens a section"},
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
			got, err := Add([]byte(tt.before), key, port)
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
		{name: "the config made for someone with none sends to the agent alone", key: testKey, want: "[settings]\napi_url = http://127.0.0.1:7247/api/v1\napi_key = " + testKey + "\n"},
		{name: "a key not in WakaTime's format is refused", key: "nope", wantIs: ErrKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Make(tt.key, 7247)
			checkResult(t, got, err, tt.want, "", tt.wantIs)
			if err == nil {
				if s, err := Inspect(got, tt.key); err != nil || !s.Own || s.Port != 7247 {
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
		{name: "the line's port changes and its spelling stays", before: "[api_urls]\n(?s).* = http://127.0.0.1:7250/api/v1|" + testKey + "\n", after: "[api_urls]\n" + line("(?s).*") + "\n"},
		{name: "a line on the port already leaves the file as it is", before: "[api_urls]\n" + line(".+"), after: "[api_urls]\n" + line(".+")},
		{name: "another key's line isn't pairtime's to change", before: "[api_urls]\n.+ = http://127.0.0.1:7250/api/v1|" + otherKey + "\n", wantIs: ErrNoLine},
		{name: "a file without pairtime's line is ErrNoLine", before: hackatime, wantIs: ErrNoLine},
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
	tests := []struct {
		name, before, after string
		key                 string
		made, section, gone bool
	}{
		{name: "pairtime's line comes out of a section that was there", before: "[api_urls]\n.*" + other + "\n" + line(".+") + "\n; note\n", after: "[api_urls]\n.*" + other + "\n; note\n", key: testKey},
		{name: "pairtime's section goes with its line and the blank line before it", before: hackatime + "\n[api_urls]\n" + line(".+") + "\n", after: hackatime, key: testKey, section: true},
		{name: "pairtime's section stays when a key was added to it since", before: hackatime + "\n[api_urls]\n" + line(".+") + "\nmine" + other + "\n", after: hackatime + "\n[api_urls]\nmine" + other + "\n", key: testKey, section: true},
		{name: "a section that was there before stays empty", before: "[api_urls]\n" + line(".+") + "\n", after: "[api_urls]\n", key: testKey},
		{name: "a line ending the file without a line break takes the one before it", before: "[api_urls]\n.*" + other + "\n" + line(".+"), after: "[api_urls]\n.*" + other, key: testKey},
		{name: "another key's line stays", before: "[api_urls]\n" + line(".+") + "\n", after: "[api_urls]\n" + line(".+") + "\n", key: otherKey},
		{name: "with no key, every line of pairtime's goes", before: "[api_urls]\n" + line(".+") + "\n^.*$ = http://127.0.0.1:7250/api/v1|" + otherKey + "\n.*" + other + "\n", after: "[api_urls]\n.*" + other + "\n"},
		{name: "a config pairtime made goes when nothing else is left in it", before: made, after: "[settings]\n", key: testKey, made: true, section: true, gone: true},
		{name: "a config pairtime made keeps what was added to it since", before: made + "hostname = mine\n", after: "[settings]\nhostname = mine\n", key: testKey, made: true, section: true},
		{name: "with no key, a config pairtime made is recognized by its agent api_url", before: made, after: "[settings]\n", made: true, section: true, gone: true},
		{name: "a config someone else made keeps its api_url, which isn't pairtime's", before: hackatime, after: hackatime, key: testKey, made: true, section: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gone, err := Remove([]byte(tt.before), tt.key, tt.made, tt.section)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.after || gone != tt.gone {
				t.Errorf("Remove = %q, gone %v, want %q, gone %v", got, gone, tt.after, tt.gone)
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
		want       State
	}{
		{name: "hackatime-setup's config with pairtime's line has two destinations", data: hackatime + "\n[api_urls]\n" + line("^.*$") + "\n", want: State{Line: true, Port: 7247, Server: "https://hackatime.hackclub.com/api/hackatime/v1", ServerKey: true}},
		{name: "without pairtime's line, Line is false", data: hackatime, want: State{Server: "https://hackatime.hackclub.com/api/hackatime/v1", ServerKey: true}},
		{name: "a config with no api_url sends to WakaTime's own", data: "[settings]\napi_key = " + otherKey + "\n[api_urls]\n" + line(".+") + "\n", want: State{Line: true, Port: 7247, Server: DefaultURL, ServerKey: true}},
		{name: "a config with no api_key sends nothing", data: "[api_urls]\n" + line(".+") + "\n", want: State{Line: true, Port: 7247, Server: DefaultURL}},
		{name: "a key from a vault command counts", data: "[settings]\napi_key_vault_cmd = pass waka\n", want: State{Server: DefaultURL, ServerKey: true}},
		{name: "a config pairtime made has only the agent", data: "[settings]\napi_url = http://127.0.0.1:7250/api/v1\napi_key = " + testKey + "\n", want: State{Line: true, Own: true, Port: 7250}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Inspect([]byte(tt.data), testKey)
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
		{name: "the api_key of a config pairtime made", data: "[settings]\napi_url = http://127.0.0.1:7250/api/v1\napi_key = " + testKey + "\n", want: testKey, ok: true},
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

func TestByHand(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "the hint names the whole line, as wakatime-cli must read it", want: line(".+")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ByHand(testKey, 7247)
			if !strings.HasSuffix(got, tt.want) || !lineFormat().MatchString(tt.want) {
				t.Errorf("ByHand = %q, want it to end with %q", got, tt.want)
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
