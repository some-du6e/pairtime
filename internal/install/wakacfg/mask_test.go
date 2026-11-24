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
	}{
		{name: "hackatime-setup's values are masked and pairtime's line shows", data: hackatime + "\n[api_urls]\n" + ours(".+") + "\n", want: "[settings]\napi_url = ***\napi_key = ***\nheartbeat_rate_limit_seconds = ***\n\n[api_urls]\n" + ours(".+") + "\n"},
		{name: "another server's line is masked beside pairtime's", data: "[api_urls]\n.*" + other + "\n" + ours(".+") + "\n", want: "[api_urls]\n.* = ***\n" + ours(".+") + "\n"},
		{name: "comments are masked after their # or ;", data: "; my key is x\n# note\n[settings]\napi_key = " + otherKey + " ; inline\n", want: "; ***\n# ***\n[settings]\napi_key = ***\n"},
		{name: "a header keeps its name and loses what follows it", data: "[settings] ; who\napi_key = " + otherKey + "\n", want: "[settings] ***\napi_key = ***\n"},
		{name: "an indented list is masked line by line", data: "[settings]\nexclude =\n    ^/secret/\n", want: "[settings]\nexclude =\n    ***\n"},
		{name: "a config pairtime made shows as it is", data: string(made), want: string(made)},
		{name: "CRLF and a byte order mark stay", data: "\xef\xbb\xbf[settings]\r\napi_key = " + otherKey + "\r\n", want: "\xef\xbb\xbf[settings]\r\napi_key = ***\r\n"},
		{name: "a file the package can't read is masked whole, line by line", data: "[settings\napi_key = " + otherKey + "\n", want: "***\n***\n"},
		{name: "a file of a byte order mark alone stays", data: "\xef\xbb\xbf", want: "\xef\xbb\xbf"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(Masked([]byte(tt.data))); got != tt.want {
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
