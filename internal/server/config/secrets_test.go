// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestLoadSecrets(t *testing.T) {
	tests := []struct {
		name     string
		file     string // secrets.env content; "" means no file
		mode     os.FileMode
		environ  []string
		want     map[string]string
		wantErr  string // substring
		unixOnly bool
	}{
		{
			name: "the file's PAIRTIME_ values load, comments and blank lines are skipped, and quotes come off",
			file: "# HCA and storage\n\nPAIRTIME_A=plain\nPAIRTIME_B=\"double quoted\"\nPAIRTIME_C='single # not a comment'\n  PAIRTIME_D = spaced  \n",
			want: map[string]string{"PAIRTIME_A": "plain", "PAIRTIME_B": "double quoted", "PAIRTIME_C": "single # not a comment", "PAIRTIME_D": "spaced"},
		},
		{
			name:    "the environment wins over the file",
			file:    "PAIRTIME_A=from-file\nPAIRTIME_B=only-in-file\n",
			environ: []string{"PAIRTIME_A=from-env"},
			want:    map[string]string{"PAIRTIME_A": "from-env", "PAIRTIME_B": "only-in-file"},
		},
		{
			name:    "variables without the PAIRTIME_ prefix, such as Litestream's, are skipped",
			file:    "LITESTREAM_ACCESS_KEY_ID=abc\nPAIRTIME_A=1\n",
			environ: []string{"HOME=/root", "LITESTREAM_SECRET_ACCESS_KEY=def"},
			want:    map[string]string{"PAIRTIME_A": "1"},
		},
		{
			name:    "PAIRTIME_CONFIG and PAIRTIME_SECRETS_FILE say where things are and aren't secrets",
			environ: []string{"PAIRTIME_CONFIG=/etc/pairtime/pairtime.toml", "PAIRTIME_SECRETS_FILE=/x", "PAIRTIME_A=1"},
			want:    map[string]string{"PAIRTIME_A": "1"},
		},
		{
			name: "CRLF line endings are read",
			file: "PAIRTIME_A=1\r\nPAIRTIME_B=2\r\n",
			want: map[string]string{"PAIRTIME_A": "1", "PAIRTIME_B": "2"},
		},
		{
			name: "a value may contain '='",
			file: "PAIRTIME_DATABASE_URL=postgres://u:p@h/db?sslmode=require\n",
			want: map[string]string{"PAIRTIME_DATABASE_URL": "postgres://u:p@h/db?sslmode=require"},
		},
		{
			name: "single quotes keep '$', '\\' and ' #' as written, as compose does",
			file: "PAIRTIME_A='pa$$word\x5cn #1'\n",
			want: map[string]string{"PAIRTIME_A": "pa$$word\x5cn #1"},
		},
		{
			name: "a double-quoted value may hold a single quote",
			file: "PAIRTIME_A=\"it's\"\n",
			want: map[string]string{"PAIRTIME_A": "it's"},
		},
		{
			name: "a '#' with no space before it is part of an unquoted value, as in compose",
			file: "PAIRTIME_A=abc#def\nPAIRTIME_B=#ghi\n",
			want: map[string]string{"PAIRTIME_A": "abc#def", "PAIRTIME_B": "#ghi"},
		},
		{
			name: "tabs around the name and the value don't count",
			file: "\tPAIRTIME_A\t=\tvalue\t\n",
			want: map[string]string{"PAIRTIME_A": "value"},
		},
		{name: "a line that isn't NAME=value is refused by number, without its text", file: "PAIRTIME_A=1\nexport PAIRTIME_B=hunter2\n", wantErr: "line 2: want NAME=value"},
		{name: "an unquoted value with '$' is refused, since compose expands it", file: "PAIRTIME_A=hunter2$HOME\n", wantErr: "line 1: an unquoted value can't hold '$'"},
		{name: "an unquoted value with '\\' is refused, since compose may read an escape", file: "PAIRTIME_A=hunter2\x5cn\n", wantErr: "line 1: an unquoted value can't hold '$' or '\x5c'"},
		{name: "an unquoted value with a space before '#' is refused, since compose cuts off a comment", file: "PAIRTIME_A=hunter2 #rotated\n", wantErr: "line 1: compose cuts an unquoted value at ' #'"},
		{name: "an unquoted value with a tab before '#' is refused too, as it reads as a comment, without claiming compose cuts it", file: "PAIRTIME_A=hunter2\t#rotated\n", wantErr: "line 1: a '#' after a tab in an unquoted value reads as a comment, though compose keeps it in the value"},
		{name: "a double-quoted value with '$' is refused, since compose expands it", file: "PAIRTIME_A=\"hunter2$HOME\"\n", wantErr: "line 1: a double-quoted value can't hold"},
		{name: "a double-quoted value with '\\' is refused, since compose reads escapes in it", file: "PAIRTIME_A=\"hunter2\x5cn\"\n", wantErr: "line 1: a double-quoted value can't hold"},
		{name: "a variable after a closing quote is refused, since compose sets it too", file: "PAIRTIME_A='hunter2' PAIRTIME_B=x\n", wantErr: "line 1: the line goes on past the value's first closing quote"},
		{name: "a comment after a closing quote is refused", file: "PAIRTIME_A=\"hunter2\" # rotated\n", wantErr: "line 1: the line goes on past the value's first closing quote"},
		{name: "an escaped quote inside single quotes is refused, since only compose reads it as an escape", file: "PAIRTIME_A='hunter2\x5c'x'\n", wantErr: "line 1: the line goes on past the value's first closing quote"},
		{name: "a single-quoted value ending in '\\' is refused, since compose reads the quote after it as escaped", file: "PAIRTIME_A='hunter2\x5c'\n", wantErr: "line 1: a single-quoted value can't end in"},
		{name: "a quote that doesn't close on its line is refused, since compose reads on into the next lines", file: "PAIRTIME_A=\"hunter2\nPAIRTIME_B=x\"\n", wantErr: "line 1: the value's quote doesn't close"},
		{name: "a line without the PAIRTIME_ prefix is checked too, since its quotes decide where compose's next variable starts", file: "LITESTREAM_X=\"abc\nPAIRTIME_A=hunter2\"\n", wantErr: "line 1: the value's quote doesn't close"},
		{name: "a space other than ' ' and tab is refused, since compose and pairtimed trim it differently", file: "PAIRTIME_A=\xc2\xa0hunter2\n", wantErr: "line 1 holds a control character or a space other than"},
		{name: "a control character is refused", file: "PAIRTIME_A=hunter2\x0b\n", wantErr: "line 1 holds a control character"},
		{name: "a name set twice is refused", file: "PAIRTIME_A=1\nPAIRTIME_A=2\n", wantErr: "PAIRTIME_A is set twice"},
		{name: "a file group or others can read is refused", file: "PAIRTIME_A=1\n", mode: 0o640, wantErr: "chmod 600", unixOnly: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.unixOnly && runtime.GOOS == "windows" {
				t.Skip("file modes are a Unix check")
			}
			path := ""
			if tt.file != "" {
				path = filepath.Join(t.TempDir(), "secrets.env")
				mode := tt.mode
				if mode == 0 {
					mode = 0o600
				}
				if err := os.WriteFile(path, []byte(tt.file), mode); err != nil {
					t.Fatal(err)
				}
				// WriteFile's mode passes through the umask; set it exactly.
				if err := os.Chmod(path, mode); err != nil {
					t.Fatal(err)
				}
			}
			s, err := LoadSecrets(path, tt.environ)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("LoadSecrets error = %v, want one containing %q", err, tt.wantErr)
				}
				if strings.Contains(err.Error(), "hunter2") {
					t.Errorf("the error quotes a secret: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadSecrets: %v", err)
			}
			got := make(map[string]string)
			for _, n := range s.Names() {
				got[n] = s.Get(n)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("secrets = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSecretsNeverPrintValues(t *testing.T) {
	s, err := LoadSecrets("", []string{"PAIRTIME_DATABASE_URL=postgres://u:hunter2@h/db"})
	if err != nil {
		t.Fatal(err)
	}
	c := Config{BaseURL: "https://pairtime.example.org", Secrets: s}
	var logged bytes.Buffer
	slog.New(slog.NewTextHandler(&logged, nil)).Info("loaded", "secrets", s, "config", c)
	tests := []struct {
		name string
		text string
	}{
		{name: "%v of Secrets prints names only", text: fmt.Sprintf("%v", s)},
		{name: "%+v of a Config prints its secrets' names only", text: fmt.Sprintf("%+v", c)},
		{name: "%#v of a Config prints its secrets' names only", text: fmt.Sprintf("%#v", c)},
		{name: "%s of Secrets prints names only", text: fmt.Sprintf("%s", s)},
		{name: "slog prints names only", text: logged.String()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if strings.Contains(tt.text, "hunter2") {
				t.Errorf("a secret value got out: %s", tt.text)
			}
			if !strings.Contains(tt.text, DatabaseURL) {
				t.Errorf("the secret's name is missing: %s", tt.text)
			}
		})
	}
}
