// SPDX-License-Identifier: AGPL-3.0-or-later

package wakacfg

import "testing"

func TestMasked(t *testing.T) {
	made, err := Make(testKey, 7247)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, data, want string
		key              string // pairtime's key, "" for none known
	}{
		{name: "hackatime-setup's values are masked and pairtime's line shows", data: hackatime + "\n[api_urls]\n" + ours(".+") + "\n", want: "[settings]\napi_url = ***\napi_key = ***\nheartbeat_rate_limit_seconds = ***\n\n[api_urls]\n" + ours(".+") + "\n"},
		{name: "another server's line is masked beside pairtime's", data: "[api_urls]\n.*" + other + "\n" + ours(".+") + "\n", want: "[api_urls]\n.* = ***\n" + ours(".+") + "\n"},
		{name: "comments are masked after their # or ;", data: "; my key is x\n# note\n[settings]\napi_key = " + otherKey + " ; inline\n", want: "; ***\n# ***\n[settings]\napi_key = ***\n"},
		{name: "a header keeps its name and loses what follows it", data: "[settings] ; who\napi_key = " + otherKey + "\n", want: "[settings] ***\napi_key = ***\n"},
		{name: "an indented list is masked line by line", data: "[settings]\nexclude =\n    ^/secret/\n", want: "[settings]\nexclude =\n    ***\n"},
		{name: "a config pairtime made shows as it is", data: string(made), want: string(made)},
		{name: "a config pairtime made shows as it is for its own key too", data: string(made), key: testKey, want: string(made)},
		{name: "a config pairtime made whose api_key isn't pairtime's any more is masked, since that key is the teen's", data: "; made by pairtime install; pairtime uninstall takes it out\n[settings]\napi_url = http://127.0.0.1:7247/api/v1\napi_key = " + otherKey + "\n", key: testKey, want: "; ***\n[settings]\napi_url = ***\napi_key = ***\n"},
		{name: "a line to the agent with another key than pairtime's is masked", data: "[api_urls]\n.+ = http://127.0.0.1:7247/api/v1|" + otherKey + "\n" + ours("^.*$") + "\n", key: testKey, want: "[api_urls]\n.+ = ***\n" + ours("^.*$") + "\n"},
		{name: "a config of the teen's own server on 127.0.0.1, without pairtime's mark, is masked", data: "[settings]\napi_url = http://127.0.0.1:3000/api/v1\napi_key = " + otherKey + "\n", want: "[settings]\napi_url = ***\napi_key = ***\n"},
		{name: "CRLF and a byte order mark stay", data: "\xef\xbb\xbf[settings]\r\napi_key = " + otherKey + "\r\n", want: "\xef\xbb\xbf[settings]\r\napi_key = ***\r\n"},
		{name: "a file the package can't read is masked whole, line by line", data: "[settings\napi_key = " + otherKey + "\n", want: "***\n***\n"},
		{name: "a key in backticks keeps its name, : and all, and loses its value", data: "[projectmap]\n`C:\\Users\\teen\\x` = mine\n", want: "[projectmap]\n`C:\\Users\\teen\\x` = ***\n"},
		{name: "a value in triple quotes is masked line by line", data: "[settings]\nexclude = \"\"\"\n  ^/secret/\"\"\"\n", want: "[settings]\nexclude = ***\n  ***\n"},
		{name: "a file of a byte order mark alone stays", data: "\xef\xbb\xbf", want: "\xef\xbb\xbf"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(Masked([]byte(tt.data), tt.key)); got != tt.want {
				t.Errorf("Masked =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestHasAPIURLs(t *testing.T) {
	tests := []struct {
		name, data string
		want       bool
	}{
		{name: "an [api_urls] section in any case is one", data: "[API_URLS]\n", want: true},
		{name: "hackatime-setup's config has none", data: hackatime},
		{name: "a file the package can't read has none it knows of", data: "[api_urls\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasAPIURLs([]byte(tt.data)); got != tt.want {
				t.Errorf("HasAPIURLs(%q) = %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}
