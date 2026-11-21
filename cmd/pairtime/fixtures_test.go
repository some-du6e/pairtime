// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/pkg/redact"
)

func TestFixturesScrub(t *testing.T) {
	// Built at run time, so no file in the repo looks like a leaked key.
	stripe := "sk_" + "live_" + strings.Repeat("Qw81Zx4LmN", 3)
	anthropic := "sk-ant-" + "api03-" + strings.Repeat("aB3_x9Kq-Z", 10)[:93] + "AA"
	// encoding/json's escape for &, built here so no tool that edits this
	// file turns it into the character.
	amp := `\` + "u0026"
	maya := []string{"--account", "maya", "--home", "/home/maya"}
	tests := []struct {
		name      string
		files     map[string]string
		args      []string // before the directory; --no-detect is always first
		detect    *redact.Known
		wantCode  int
		wantFiles map[string]string // nil: the files don't change
		wantOut   []string          // substrings of stdout
		notOut    []string          // must appear in neither stdout nor stderr
	}{
		{
			name:     "known values and a secret become README's placeholders",
			files:    map[string]string{"session.jsonl": `{"cwd":"/home/maya/robot","text":"key ` + stripe + ` ok"}` + "\n"},
			args:     maya,
			wantCode: 0,
			wantFiles: map[string]string{
				"session.jsonl": `{"cwd":"/home/capture/robot","text":"key REDACTED_STRIPE_ACCESS_TOKEN ok"}` + "\n",
			},
			wantOut: []string{
				"session.jsonl:1:9: home, 10 bytes\n",
				"session.jsonl:1:39: secret stripe-access-token, 38 bytes\n",
				"removed 2 spans from 1 of 1 files",
			},
			notOut: []string{"maya", stripe},
		},
		{
			name: "a key inside a JSON string is found through the escapes around it",
			files: map[string]string{
				"write.jsonl":  `{"input":{"content":"STRIPE_KEY=\"` + stripe + `\"\n"}}` + "\n",
				"prompt.jsonl": `{"text":"my key:\n` + stripe + `"}` + "\n",
				"end.jsonl":    `{"text":"it is ` + stripe + `.\""}` + "\n" + `{"text":"` + stripe + `.\nthanks"}` + "\n",
			},
			wantCode: 0,
			wantFiles: map[string]string{
				"write.jsonl":  `{"input":{"content":"STRIPE_KEY=\"REDACTED_STRIPE_ACCESS_TOKEN\"\n"}}` + "\n",
				"prompt.jsonl": `{"text":"my key:\nREDACTED_STRIPE_ACCESS_TOKEN"}` + "\n",
				"end.jsonl":    `{"text":"it is REDACTED_STRIPE_ACCESS_TOKEN.\""}` + "\n" + `{"text":"REDACTED_STRIPE_ACCESS_TOKEN.\nthanks"}` + "\n",
			},
			wantOut: []string{
				"write.jsonl:1:35: secret stripe-access-token, 38 bytes\n",
				"prompt.jsonl:1:19: secret stripe-access-token, 38 bytes\n",
				"end.jsonl:1:16: secret stripe-access-token, 38 bytes\n",
				"end.jsonl:2:10: secret stripe-access-token, 38 bytes\n",
				"removed 4 spans from 3 of 3 files",
			},
			notOut: []string{stripe},
		},
		{
			name: "values and an email address right after a JSON escape are found, and the escape stays",
			files: map[string]string{
				"ps.jsonl":   `{"stdout":"USER  PID\nmaya  4242"}` + "\n",
				"mail.jsonl": `{"text":"hi\njonah@westfield.edu"}` + "\n",
				"log.jsonl":  `{"stdout":"    42\tMaya Kowalski"}` + "\n",
			},
			args:     []string{"--account", "maya", "--git-name", "Maya Kowalski"},
			wantCode: 0,
			wantFiles: map[string]string{
				"ps.jsonl":   `{"stdout":"USER  PID\ncapture  4242"}` + "\n",
				"mail.jsonl": `{"text":"hi\ncapture@example.invalid"}` + "\n",
				"log.jsonl":  `{"stdout":"    42\tCapture User"}` + "\n",
			},
			wantOut: []string{
				"ps.jsonl:1:23: account, 4 bytes\n",
				"mail.jsonl:1:14: email, 19 bytes\n",
				"log.jsonl:1:20: git_name, 13 bytes\n",
				"removed 3 spans from 3 of 3 files",
			},
			notOut: []string{"maya", "Maya", "jonah"},
		},
		{
			// Merged with the address around it, the account or the
			// password would take the address's type and stay with it
			// (D-email-redaction).
			name: "a known value or a URL password inside a kept address is still replaced",
			files: map[string]string{
				"log.txt": "rowanq@example.com\npostgres://app:Tr0ub4dor3x@db.example.com/app\nCo-Authored-By: Claude <noreply@anthropic.com>\n",
			},
			args:     []string{"--account", "rowanq"},
			wantCode: 0,
			wantFiles: map[string]string{
				"log.txt": "capture@example.com\npostgres://app:REDACTED_URL_PASSWORD@db.example.com/app\nCo-Authored-By: Claude <noreply@anthropic.com>\n",
			},
			wantOut: []string{
				"log.txt:1:1: account, 6 bytes\n",
				"log.txt:2:16: secret url-password, 11 bytes\n",
				"removed 2 spans from 1 of 1 files",
			},
			notOut: []string{"rowanq", "Tr0ub4dor3x"},
		},
		{
			// Most of gitleaks' rules end a key at a quote, a space, a
			// semicolon or the end of the text, so in place these keys,
			// followed by an @, match none of them, and the kept address
			// was their only span. TestScrub in tools/capture checks that
			// scrub.py replaces them too.
			name: "a key that is a kept address's local part, as a URL's user, is still replaced",
			files: map[string]string{
				"keys.txt": "https://" + stripe + "@api.example.com/v1/charges\n" +
					"https://" + anthropic + "@api.example.com/v1/messages\n",
			},
			wantCode: 0,
			wantFiles: map[string]string{
				"keys.txt": "https://REDACTED_STRIPE_ACCESS_TOKEN@api.example.com/v1/charges\n" +
					"https://REDACTED_ANTHROPIC_API_KEY@api.example.com/v1/messages\n",
			},
			wantOut: []string{
				"keys.txt:1:9: secret stripe-access-token, 38 bytes\n",
				"keys.txt:2:9: secret anthropic-api-key, 108 bytes\n",
				"removed 2 spans from 1 of 1 files",
			},
			notOut: []string{stripe, anthropic},
		},
		{
			// The kept address is x-oauth-basic@api.example.com here, and
			// the key, followed by a colon, is before it.
			name: "a key that is the user of a URL before its password, at a kept address, is still replaced",
			files: map[string]string{
				"remote.txt": "https://" + stripe + ":x-oauth-basic@api.example.com/v1/charges\n" +
					"https://" + anthropic + ":x-oauth-basic@api.example.com/v1/messages\n",
			},
			wantCode: 0,
			wantFiles: map[string]string{
				"remote.txt": "https://REDACTED_STRIPE_ACCESS_TOKEN:REDACTED_URL_PASSWORD@api.example.com/v1/charges\n" +
					"https://REDACTED_ANTHROPIC_API_KEY:REDACTED_URL_PASSWORD@api.example.com/v1/messages\n",
			},
			wantOut: []string{
				"remote.txt:1:9: secret stripe-access-token, 38 bytes\n",
				"remote.txt:1:48: secret url-password, 13 bytes\n",
				"remote.txt:2:9: secret anthropic-api-key, 108 bytes\n",
				"remote.txt:2:118: secret url-password, 13 bytes\n",
				"removed 4 spans from 1 of 1 files",
			},
			notOut: []string{stripe, anthropic, "oauth"},
		},
		{
			// The address after the key, if there is one, isn't kept here:
			// it covered only the password. The second URL has no password
			// and so no address, and the third's host isn't an address.
			name: "a key that is a URL's user is replaced whatever the host",
			files: map[string]string{
				"real.txt": "https://" + stripe + ":x-oauth-basic@api.stripe.com/v1/charges\n" +
					"https://" + anthropic + ":@api.anthropic.com/v1/messages\n" +
					"postgres://" + stripe + ":pw@db:5432/app\n",
			},
			wantCode: 0,
			wantFiles: map[string]string{
				"real.txt": "https://REDACTED_STRIPE_ACCESS_TOKEN:capture@example.invalid/v1/charges\n" +
					"https://REDACTED_ANTHROPIC_API_KEY:@api.anthropic.com/v1/messages\n" +
					"postgres://REDACTED_STRIPE_ACCESS_TOKEN:REDACTED_URL_PASSWORD@db:5432/app\n",
			},
			wantOut: []string{
				"real.txt:1:9: secret stripe-access-token, 38 bytes\n",
				"real.txt:1:48: email, 28 bytes\n",
				"real.txt:2:9: secret anthropic-api-key, 108 bytes\n",
				"real.txt:3:12: secret stripe-access-token, 38 bytes\n",
				"real.txt:3:51: secret url-password, 2 bytes\n",
				"removed 5 spans from 1 of 1 files",
			},
			notOut: []string{stripe, anthropic, "oauth"},
		},
		{
			// pkg/redact's url-password rule needs scheme://, which JSON's
			// \/\/, JSON inside JSON's \\/\\/ and a URL with no scheme
			// don't have, and the address after the password is kept. The
			// rule already takes a password with no user before it.
			name: "a URL's user and password at a kept address are replaced however the URL is written",
			files: map[string]string{
				"urls.txt": `{"url":"postgres:\/\/app:Tr0ub4dor3x@db.example.com\/app"}` + "\n" +
					"//app:Tr0ub4dor3x@db.example.com/app\n" +
					"redis://:hunter2hunter2@cache.example.com:6379\n" +
					`{"stdout":"{\"url\":\"https:\\/\\/` + stripe + `:x@api.example.com\"}"}` + "\n",
			},
			wantCode: 0,
			wantFiles: map[string]string{
				"urls.txt": `{"url":"postgres:\/\/app:REDACTED_URL_PASSWORD@db.example.com\/app"}` + "\n" +
					"//app:REDACTED_URL_PASSWORD@db.example.com/app\n" +
					"redis://:REDACTED_URL_PASSWORD@cache.example.com:6379\n" +
					`{"stdout":"{\"url\":\"https:\\/\\/REDACTED_STRIPE_ACCESS_TOKEN:REDACTED_URL_PASSWORD@api.example.com\"}"}` + "\n",
			},
			wantOut: []string{
				"urls.txt:1:26: secret url-password, 11 bytes\n",
				"urls.txt:2:7: secret url-password, 11 bytes\n",
				"urls.txt:3:10: secret url-password, 14 bytes\n",
				"urls.txt:4:35: secret stripe-access-token, 38 bytes\n",
				"urls.txt:4:74: secret url-password, 1 bytes\n",
				"removed 5 spans from 1 of 1 files",
			},
			notOut: []string{stripe, "Tr0ub4dor3x", "hunter2"},
		},
		{
			// What a first run writes for postgres://app:KEY@db.example.com,
			// postgres://app:pw@db.westfield.edu and the first line of the
			// case for a URL's user at any host. url-password finds a
			// password in each, so it would put its own placeholder in
			// place of the one there.
			name:     "a placeholder an earlier run wrote is left alone, even where another rule finds it",
			files:    map[string]string{"again.txt": "postgres://app:REDACTED_STRIPE_ACCESS_TOKEN@db.example.com/app\npostgres://app:capture@example.invalid/app\nhttps://REDACTED_STRIPE_ACCESS_TOKEN:capture@example.invalid/v1/charges\n"},
			wantCode: 0,
			wantOut:  []string{"removed 0 spans from 0 of 1 files"},
		},
		{
			// scrub.py ended this password at its first @, which left
			// ssw0rd glued to a placeholder.
			name:     "a span that runs on past a placeholder isn't one, so all of it is replaced",
			files:    map[string]string{"glued.txt": "postgres://app:REDACTED_URL_PASSWORD@ssw0rd@db.example.com/app\n"},
			wantCode: 0,
			wantFiles: map[string]string{
				"glued.txt": "postgres://app:REDACTED_URL_PASSWORD@db.example.com/app\n",
			},
			wantOut: []string{"glued.txt:1:16: secret url-password, 28 bytes\n", "removed 1 spans from 1 of 1 files"},
			notOut:  []string{"ssw0rd"},
		},
		{
			// RFC 3986 keeps ? and # out of an authority, but people put
			// them in passwords, and a password may hold an @ as well.
			name: "a URL's password with ?, # or @ in it runs to its last @, and the user before it is still scanned",
			files: map[string]string{
				"special.txt": "https://" + stripe + ":pa?ss1@api.example.com/v1/charges\n" +
					`{"url":"postgres:\/\/app:Tr0ub#4dor@db.example.com\/app"}` + "\n" +
					"//app:Tr0ub?4dor@db:5432/app\n" +
					"postgres://app:p@ssw0rd@db.example.com/app\n",
			},
			wantCode: 0,
			wantFiles: map[string]string{
				"special.txt": "https://REDACTED_STRIPE_ACCESS_TOKEN:REDACTED_URL_PASSWORD@api.example.com/v1/charges\n" +
					`{"url":"postgres:\/\/app:REDACTED_URL_PASSWORD@db.example.com\/app"}` + "\n" +
					"//app:REDACTED_URL_PASSWORD@db:5432/app\n" +
					"postgres://app:REDACTED_URL_PASSWORD@db.example.com/app\n",
			},
			wantOut: []string{
				"special.txt:1:9: secret stripe-access-token, 38 bytes\n",
				"special.txt:1:48: secret url-password, 6 bytes\n",
				"special.txt:2:26: secret url-password, 10 bytes\n",
				"special.txt:3:7: secret url-password, 10 bytes\n",
				"special.txt:4:16: secret url-password, 8 bytes\n",
				"removed 5 spans from 1 of 1 files",
			},
			notOut: []string{stripe, "pa?ss1", "Tr0ub", "ssw0rd"},
		},
		{
			// The address there is the password and the host, which
			// pkg/redact merges into one span, so the host goes too.
			name: "a key before a password with # or JSON's escaped & in it is replaced at a host that isn't kept",
			files: map[string]string{
				"realhost.txt": "https://" + stripe + ":pa#ss1@api.stripe.com/v1/charges\n" +
					`{"url":"https://` + stripe + `:pa` + amp + `ss@api.stripe.com/x"}` + "\n",
			},
			wantCode: 0,
			wantFiles: map[string]string{
				"realhost.txt": "https://REDACTED_STRIPE_ACCESS_TOKEN:capture@example.invalid/v1/charges\n" +
					`{"url":"https://REDACTED_STRIPE_ACCESS_TOKEN:capture@example.invalid/x"}` + "\n",
			},
			wantOut: []string{
				"realhost.txt:1:9: secret stripe-access-token, 38 bytes\n",
				"realhost.txt:1:48: email, 21 bytes\n",
				"realhost.txt:2:17: secret stripe-access-token, 38 bytes\n",
				"realhost.txt:2:56: email, 25 bytes\n",
				"removed 4 spans from 1 of 1 files",
			},
			notOut: []string{stripe},
		},
		{
			name: "an address that is a URL's user is replaced or kept like any other address",
			files: map[string]string{
				"users.txt": "ftp://jane@westfield.edu:s3cretpw@ftp.example.com/\nftp://noreply@anthropic.com:s3cretpw@ftp.example.com/\n",
			},
			wantCode: 0,
			wantFiles: map[string]string{
				"users.txt": "ftp://capture@example.invalid:REDACTED_URL_PASSWORD@ftp.example.com/\nftp://noreply@anthropic.com:REDACTED_URL_PASSWORD@ftp.example.com/\n",
			},
			wantOut: []string{
				"users.txt:1:7: email, 18 bytes\n",
				"users.txt:1:26: secret url-password, 8 bytes\n",
				"users.txt:2:29: secret url-password, 8 bytes\n",
				"removed 3 spans from 1 of 1 files",
			},
			notOut: []string{"jane", "s3cretpw"},
		},
		{
			// RFC 3986 keeps [ and ] out of a user and a password.
			name:     "two slashes with no scheme before them aren't a URL when a bracket follows, as in namespaced XPath",
			files:    map[string]string{"xpath.py": "tree.xpath('//xs:element[@name]', namespaces=ns)\npom.findall(\".//pom:dependency[@scope]\")\n"},
			wantCode: 0,
			wantOut:  []string{"removed 0 spans from 0 of 1 files"},
		},
		{
			// A sandbox anonymizer disables a real address by appending a
			// reserved label, and it still names someone
			// (D-email-redaction-tld).
			name: "under a reserved top-level domain only a one-label name is kept, so a real address with .invalid appended is replaced",
			files: map[string]string{
				"owners.txt": "Owner: jane.doe@westfield.edu.invalid\nci@runner.ci.test ci@build.example.invalid\n" +
					"user@myapp.test dev@build.localhost a@b.example capture@example.invalid\n",
			},
			wantCode: 0,
			wantFiles: map[string]string{
				"owners.txt": "Owner: capture@example.invalid\ncapture@example.invalid capture@example.invalid\n" +
					"user@myapp.test dev@build.localhost a@b.example capture@example.invalid\n",
			},
			wantOut: []string{
				"owners.txt:1:8: email, 30 bytes\n",
				"owners.txt:2:1: email, 17 bytes\n",
				"owners.txt:2:19: email, 24 bytes\n",
				"removed 3 spans from 1 of 1 files",
			},
			notOut: []string{"jane", "westfield", "runner"},
		},
		{
			name: "telemetry account and organization ids become README's id, or 0 for a number, and each is listed",
			files: map[string]string{
				"otlp/000001.json": `{"resourceMetrics":[{"resource":{"attributes":[` +
					`{"key":"user.account_uuid","value":{"stringValue":"9b1e7f9e-1111-4222-8333-444455556666"}},` +
					`{"value":{"stringValue":"77aa88bb-1111-4222-8333-444455556666"},"key":"organization.id"},` +
					`{"key":"user.account_id","value":{"intValue":"4242"}},` +
					`{"key":"enduser.id","value":{"intValue":77}},` +
					`{"key":"session.id","value":{"stringValue":"5d6c1e2a-7b8f-4c3d-9e0f-112233445566"}}]}}]}`,
			},
			wantCode: 0,
			wantFiles: map[string]string{
				"otlp/000001.json": `{"resourceMetrics":[{"resource":{"attributes":[` +
					`{"key":"user.account_uuid","value":{"stringValue":"00000000-0000-4000-8000-000000000000"}},` +
					`{"value":{"stringValue":"00000000-0000-4000-8000-000000000000"},"key":"organization.id"},` +
					`{"key":"user.account_id","value":{"intValue":"0"}},` +
					`{"key":"enduser.id","value":{"intValue":0}},` +
					`{"key":"session.id","value":{"stringValue":"5d6c1e2a-7b8f-4c3d-9e0f-112233445566"}}]}}]}`,
			},
			wantOut: []string{
				"otlp/000001.json:1:99: telemetry_id user.account_uuid, 36 bytes\n",
				"otlp/000001.json:1:164: telemetry_id organization.id, 36 bytes\n",
				"otlp/000001.json:1:274: telemetry_id user.account_id, 4 bytes\n",
				"otlp/000001.json:1:322: telemetry_id enduser.id, 2 bytes\n",
				"removed 4 spans from 1 of 1 files",
			},
			notOut: []string{"9b1e7f9e", "77aa88bb"},
		},
		{
			name:     "-n lists the spans and changes nothing",
			files:    map[string]string{"a.txt": "home is /home/maya\n"},
			args:     append([]string{"-n"}, maya...),
			wantCode: 0,
			wantOut:  []string{"a.txt:1:9: home, 10 bytes\n", "would remove 1 spans from 1 of 1 files"},
			notOut:   []string{"maya"},
		},
		{
			name:     "values read from the machine are removed too",
			files:    map[string]string{"hook.json": `{"host":"robo-pc-77"}`},
			detect:   &redact.Known{Hosts: []string{"robo-pc-77"}},
			wantCode: 0,
			wantFiles: map[string]string{
				"hook.json": `{"host":"capture-host"}`,
			},
			wantOut: []string{"hook.json:1:10: host, 10 bytes\n"},
			notOut:  []string{"robo-pc-77"},
		},
		{
			name:     "a binary file is reported, left alone, and fails the run",
			files:    map[string]string{"state.db": "SQLite\x00\x01maya"},
			args:     maya,
			wantCode: 1,
			wantOut:  []string{"state.db: binary, not scrubbed; review it by hand", "1 entries need a look by hand"},
		},
		{
			name:      "a name that holds a value is reported redacted and fails the run",
			files:     map[string]string{"-home-maya-robot/1.jsonl": "{}\n"},
			args:      maya,
			wantCode:  1,
			wantFiles: map[string]string{"-home-maya-robot/1.jsonl": "{}\n"},
			wantOut:   []string{"[HOME]-robot: the name holds home; rename it by hand"},
			notOut:    []string{"maya"},
		},
		{
			name: "a Claude Code project folder named after a Windows home is reported, and the home goes in every spelling",
			files: map[string]string{
				"transcripts/home/.claude/projects/C--Users-Maya-Chen-robot/1.jsonl": `{"cwd":"C:\\Users\\Maya Chen\\robot",` +
					`"transcript_path":"C:\\Users\\Maya Chen\\.claude\\projects\\C--Users-Maya-Chen-robot\\1.jsonl",` +
					`"uri":"file:///c%3A/Users/Maya%20Chen/robot"}` + "\n",
			},
			args:     []string{"--home", `C:\Users\Maya Chen`, "--account", "Maya Chen"},
			wantCode: 1,
			wantFiles: map[string]string{
				"transcripts/home/.claude/projects/C--Users-Maya-Chen-robot/1.jsonl": `{"cwd":"C:\\Users\\capture\\robot",` +
					`"transcript_path":"C:\\Users\\capture\\.claude\\projects\\C--Users-capture-robot\\1.jsonl",` +
					`"uri":"file:///c%3A/Users/capture/robot"}` + "\n",
			},
			wantOut: []string{
				"transcripts/home/.claude/projects/[HOME]-robot: the name holds home; rename it by hand\n",
				"transcripts/home/.claude/projects/[HOME]-robot/1.jsonl:1:9: home, 20 bytes\n",
				"transcripts/home/.claude/projects/[HOME]-robot/1.jsonl:1:57: home, 20 bytes\n",
				"transcripts/home/.claude/projects/[HOME]-robot/1.jsonl:1:98: home, 18 bytes\n",
				"transcripts/home/.claude/projects/[HOME]-robot/1.jsonl:1:147: home, 23 bytes\n",
				"removed 4 spans from 1 of 1 files",
				"1 entries need a look by hand",
			},
			notOut: []string{"Maya", "Chen"},
		},
		{
			name:     "PROVENANCE.md is left alone",
			files:    map[string]string{"PROVENANCE.md": "captured_by: maya\n", "x.txt": "hi maya\n"},
			args:     maya,
			wantCode: 0,
			wantFiles: map[string]string{
				"PROVENANCE.md": "captured_by: maya\n",
				"x.txt":         "hi capture\n",
			},
			wantOut: []string{"x.txt:1:4: account, 4 bytes\n", "removed 1 spans from 1 of 1 files"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := detectKnown
			t.Cleanup(func() { detectKnown = old })
			args := []string{"fixtures", "scrub"}
			if tt.detect != nil {
				k := *tt.detect
				detectKnown = func() redact.Known { return k }
			} else {
				args = append(args, "--no-detect")
			}
			dir := t.TempDir()
			for name, content := range tt.files {
				p := filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer
			code := run(append(append(args, tt.args...), dir), &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tt.wantCode, stdout.String(), stderr.String())
			}
			for _, w := range tt.wantOut {
				if !strings.Contains(stdout.String(), w) {
					t.Errorf("stdout = %q, want it to contain %q", stdout.String(), w)
				}
			}
			for _, n := range tt.notOut {
				if strings.Contains(stdout.String(), n) || strings.Contains(stderr.String(), n) {
					t.Errorf("output shows %q:\nstdout:\n%s\nstderr:\n%s", n, stdout.String(), stderr.String())
				}
			}
			want := tt.wantFiles
			if want == nil {
				want = tt.files
			}
			for name, content := range want {
				b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
				if err != nil {
					t.Fatal(err)
				}
				if string(b) != content {
					t.Errorf("%s = %q, want %q", name, b, content)
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".pairtime-scrub-") {
					t.Errorf("left a temporary file %s", e.Name())
				}
			}
		})
	}
}

// TestFixturesScrubPlantedCapture runs the scrub over the planted capture
// that tools/capture tests WP-AD0's scrub.py with, and checks what that
// test checks: no planted value survives, each one takes README's
// placeholder in the file's own spelling, ids that aren't secrets and
// public addresses stay, and every JSON file still parses. Here the
// Anthropic key and the GitHub token are real ones' length and shape,
// since gitleaks ignores scrub.py's short ones, and --deny stands in for
// scrub.py's --also. Unlike scrub.py, the scrub reports a folder whose
// name holds a value instead of renaming it.
func TestFixturesScrubPlantedCapture(t *testing.T) {
	const (
		session = "5d6c1e2a-7b8f-4c3d-9e0f-112233445566"
		tool    = "toolu_01ABCDEFGHIJKLMNOPQRSTUV"
		project = "transcripts/home/.claude/projects/-home-rowanq-pairtime-capture-project/"
	)
	// Built at run time, so no file in the repo looks like a leaked key.
	anthropic := "sk-ant-" + "api03-" + strings.Repeat("aB3_x9Kq-Z", 10)[:93] + "AA"
	ghp := "gh" + "p_" + strings.Repeat("Zx81Qw4LmN", 4)[:36]
	aws := "AKIA" + "ABCDEFGHIJKLMNOP"
	jwt := "eyJhbGciOiJIUzI1NiJ9" + ".eyJzdWIiOiIxMjM0NTY3ODkwIn0" + ".dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	files := map[string]string{
		"hooks/000001/stdin": `{"session_id":"` + session + `","transcript_path":"/home/rowanq/.claude/projects/-home-rowanq-pairtime-capture-project/` +
			session + `.jsonl","cwd":"/home/rowanq/pairtime-capture/project","hook_event_name":"UserPromptSubmit","prompt":"What does count_words do? Answer in one sentence."}`,
		"hooks/000001/env":   "HOME\nPATH\nROWANQ_TOKEN\n",
		"hooks/000002/stdin": `{"hook_event_name":"UserPromptSubmit","prompt":"my diary entry, which is not in the script"}`,
		project + session + ".jsonl": `{"type":"user","cwd":"C:\\Users\\RowanQ\\pairtime-capture\\project","sessionId":"` + session + `"}` + "\n" +
			`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"` + tool + `","input":{"command":"whoami"}}]}}` + "\n" +
			`{"toolUseResult":{"stdout":"rowanq\nrowanq@rowans-laptop:~$ git log\nAuthor: Rowan Quinn <rowan.quinn@westfield.edu>\n` + anthropic +
			`"},"uri":"file:///c%3A/Users/RowanQ/pairtime-capture/project"}` + "\n" +
			`{"nested":"{\"stdout\":\"C:\\\\Users\\\\RowanQ\\\\x\"}","school":"Westfield High"}` + "\n" +
			`{"keys":"` + ghp + " " + aws + " " + jwt + `","trailer":"Co-Authored-By: Claude <noreply@anthropic.com>\nCo-authored-by: aider (gpt-5) <aider@aider.chat>"}` + "\n",
		"otlp/000001.json": `{"resourceLogs":[{"resource":{"attributes":[` +
			`{"key":"user.account_uuid","value":{"stringValue":"9b1e7f9e-1111-4222-8333-444455556666"}},` +
			`{"key":"organization.id","value":{"stringValue":"77aa88bb-1111-4222-8333-444455556666"}},` +
			`{"key":"user.email","value":{"stringValue":"rowan.quinn@westfield.edu"}},` +
			`{"key":"session.id","value":{"stringValue":"` + session + `"}}]}}]}`,
		"otlp/000002.bin": "\xff\xfe\x00\x81",
	}
	args := []string{"fixtures", "scrub", "--no-detect", "--home", "/home/rowanq", "--home", `C:\Users\RowanQ`, "--account", "rowanq",
		"--host", "rowans-laptop", "--git-name", "Rowan Quinn", "--git-email", "rowan.quinn@westfield.edu", "--deny", "Westfield High"}
	dir := t.TempDir()
	writeTree(t, dir, files)
	var stdout, stderr bytes.Buffer
	if code := run(append(args, dir), &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1 for the folder name and the binary file\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	out := readTree(t, dir)
	var all strings.Builder
	for _, text := range out {
		all.WriteString(text + "\n")
	}
	everything := strings.ToLower(all.String())

	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"no planted value survives in any file, in any case", func(t *testing.T) {
			for _, v := range []string{"rowan", "westfield", strings.ToLower(anthropic[:24]), strings.ToLower(ghp), "akiaabcd", "eyjhbgci", "9b1e7f9e", "77aa88bb"} {
				if strings.Contains(everything, v) {
					t.Errorf("%q survived the scrub", v)
				}
			}
		}},
		{"the listing shows no planted value either", func(t *testing.T) {
			listing := strings.ToLower(stdout.String() + stderr.String())
			for _, v := range []string{"rowan", "westfield"} {
				if strings.Contains(listing, v) {
					t.Errorf("the listing shows %q:\n%s", v, stdout.String())
				}
			}
		}},
		{"a home path is replaced in every spelling", func(t *testing.T) {
			for _, want := range []string{`/home/capture/pairtime-capture/project`, `C:\\Users\\capture\\pairtime-capture`,
				`file:///c%3A/Users/capture/pairtime-capture`, `C:\\\\Users\\\\capture\\\\x`, `-home-capture-pairtime-capture-project`} {
				if !strings.Contains(all.String(), want) {
					t.Errorf("no %s in the output", want)
				}
			}
		}},
		{"names and addresses get their placeholders", func(t *testing.T) {
			for _, want := range []string{"capture@capture-host", "Capture User <capture@example.invalid>", `"school":"REDACTED"`, "capture_TOKEN",
				`"stringValue":"capture@example.invalid"`} {
				if !strings.Contains(all.String(), want) {
					t.Errorf("no %s in the output", want)
				}
			}
		}},
		{"telemetry account ids become README's id", func(t *testing.T) {
			if got := out["otlp/000001.json"]; strings.Count(got, "00000000-0000-4000-8000-000000000000") != 2 {
				t.Errorf("otlp body: %s", got)
			}
		}},
		{"keys and tokens become REDACTED and their rule", func(t *testing.T) {
			for _, want := range []string{"REDACTED_ANTHROPIC_API_KEY", "REDACTED_GITHUB_PAT", "REDACTED_AWS_ACCESS_TOKEN", "REDACTED_JWT"} {
				if !strings.Contains(all.String(), want) {
					t.Errorf("no %s in the output", want)
				}
			}
		}},
		{"ids that aren't secrets and public tool addresses are kept", func(t *testing.T) {
			for _, want := range []string{session, tool, "noreply@anthropic.com", "aider@aider.chat"} {
				if !strings.Contains(all.String(), want) {
					t.Errorf("%s was removed", want)
				}
			}
		}},
		{"every scrubbed JSON file is still valid JSON", func(t *testing.T) {
			for path, text := range out {
				if !strings.HasSuffix(path, "stdin") && !strings.HasSuffix(path, ".json") && !strings.HasSuffix(path, ".jsonl") {
					continue
				}
				for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
					if !json.Valid([]byte(line)) {
						t.Errorf("%s: invalid JSON after the scrub: %s", path, line)
					}
				}
			}
		}},
		{"the folder named after the home and the binary file are reported", func(t *testing.T) {
			for _, want := range []string{"transcripts/home/.claude/projects/[HOME]-pairtime-capture-project: the name holds home; rename it by hand\n",
				"otlp/000002.bin: binary, not scrubbed; review it by hand\n"} {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout = %q, want it to contain %q", stdout.String(), want)
				}
			}
		}},
		{"a second run changes nothing", func(t *testing.T) {
			var again bytes.Buffer
			run(append(args, dir), &again, &bytes.Buffer{})
			if !strings.Contains(again.String(), "removed 0 spans from 0 of") {
				t.Errorf("the second run listed spans:\n%s", again.String())
			}
			for path, text := range readTree(t, dir) {
				if text != out[path] {
					t.Errorf("the second run changed %s:\n%s\nto\n%s", path, out[path], text)
				}
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.check)
	}
}

// writeTree writes files, by slash-separated path, under dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// readTree reads every file under dir, by slash-separated path.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFixturesUsage(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"no subcommand is a usage error", []string{"fixtures"}, 2, "", "pairtime fixtures scrub [flags] <dir>"},
		{"an unknown subcommand is a usage error", []string{"fixtures", "polish"}, 2, "", `unknown command "polish"`},
		{"help prints the usage", []string{"fixtures", "help"}, 0, "Run it with -n first", ""},
		{"scrub without a directory is a usage error", []string{"fixtures", "scrub", "--no-detect"}, 2, "", "want one directory"},
		{"scrub of a missing directory is a usage error", []string{"fixtures", "scrub", "--no-detect", "/nonexistent/pairtime-test"}, 2, "", "isn't a directory"},
		{"an unknown flag is a usage error", []string{"fixtures", "scrub", "--polish", "."}, 2, "", "flag provided but not defined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tt.args, &stdout, &stderr); code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			if tt.wantStdout != "" && !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}
