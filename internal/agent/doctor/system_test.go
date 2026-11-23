// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestSystem checks what doctor reads of the computer it runs on, with the
// OS's own calls: platform.yml runs it on Windows and macOS, ci.yml on
// Linux.
func TestSystem(t *testing.T) {
	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"the OS build names this OS and never the computer", func(t *testing.T) {
			b := OSBuild()
			ok := map[string]bool{
				"windows": strings.HasPrefix(b, "Windows") && strings.Contains(b, ", build "),
				"darwin":  strings.HasPrefix(b, "macOS ") && len(b) > len("macOS "),
				"linux":   strings.Contains(b, "Linux"),
			}
			if want, known := ok[runtime.GOOS]; known && !want {
				t.Errorf("OSBuild() = %q on %s", b, runtime.GOOS)
			}
			if host, err := os.Hostname(); err == nil && len(host) >= 3 && strings.Contains(strings.ToLower(b), strings.ToLower(host)) {
				t.Errorf("OSBuild() = %q names the computer, %q", b, host)
			}
		}},
		{"the disk of a temporary folder spins, doesn't, or the OS doesn't say, and on macOS pairtime doesn't ask", func(t *testing.T) {
			got := DiskKind(t.TempDir())
			if !slices.Contains([]string{"ssd", "hdd", "unknown"}, got) && !(runtime.GOOS == "darwin" && got == "unread") {
				t.Errorf("DiskKind = %q on %s", got, runtime.GOOS)
			}
			if runtime.GOOS == "darwin" && got != "unread" {
				t.Errorf("DiskKind = %q on macOS, which pairtime doesn't ask yet", got)
			}
		}},
		{"the home folder's spellings start with itself, and on Windows add its short alias, another name of the same folder, when it has one", func(t *testing.T) {
			home, err := os.UserHomeDir()
			if err != nil {
				t.Skip(err)
			}
			got := HomeSpellings(home)
			if len(got) == 0 || got[0] != home {
				t.Fatalf("HomeSpellings(%q) = %q", home, got)
			}
			for _, short := range got[1:] {
				if runtime.GOOS != "windows" {
					t.Errorf("HomeSpellings(%q) adds %q on %s", home, short, runtime.GOOS)
					continue
				}
				// NTFS promises no form for a short name, such as a ~, only
				// that it names the same folder.
				a, errA := os.Stat(home)
				b, errB := os.Stat(short)
				if short == home || errA != nil || errB != nil || !os.SameFile(a, b) {
					t.Errorf("HomeSpellings(%q) adds %q, which isn't another name of the same folder", home, short)
				}
			}
		}},
		{"a test binary has the signature its linker gives it: none on Windows and Intel Macs, ad hoc on Apple silicon", func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]Verdict{"windows": Unsigned, "darwin": Unsigned, "linux": NoSigning}[runtime.GOOS]
			if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
				want = AdHoc
			}
			if got, words := OSSignature(exe); got != want || words == "" {
				t.Errorf("OSSignature = %v, %q; want %v", got, words, want)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}
