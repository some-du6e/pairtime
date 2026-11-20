// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// TestLaunchAgentReadsBackWithPlutil checks the LaunchAgent with plutil,
// macOS's own property list tool, which this package didn't write: the
// file lints, and the values read back are the ones meant, odd characters
// in the path included.
func TestLaunchAgentReadsBackWithPlutil(t *testing.T) {
	tests := []struct {
		name string
		exe  string
	}{
		{"the LaunchAgent for the usual path lints and reads back", "/Users/teen/.local/bin/pairtime"},
		{"a path with spaces reads back as it is", "/Users/Te En/.local/bin/pairtime"},
		{"a path with XML's special characters reads back as it is", `/Users/a&b <c> "d" 'e'/pairtime`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), LaunchAgentLabel+".plist")
			if err := os.WriteFile(path, launchAgent(tt.exe), 0o600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
				t.Fatalf("plutil -lint: %v\n%s", err, out)
			}
			want := map[string]any{
				"Label":            LaunchAgentLabel,
				"ProgramArguments": []any{tt.exe, "agent"},
				"RunAtLoad":        true,
				"ProcessType":      "Background",
			}
			out, err := exec.Command("plutil", "-convert", "json", "-o", "-", path).Output()
			if err != nil {
				t.Fatalf("plutil -convert json: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("plutil -convert json printed %q: %v", out, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("the LaunchAgent reads back as %#v, want %#v", got, want)
			}
		})
	}
}
