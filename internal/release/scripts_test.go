// SPDX-License-Identifier: AGPL-3.0-or-later

package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stampVersion is the release version the stamp tests fill in.
const stampVersion = "v0.1.0-alpha.1"

func TestStamp(t *testing.T) {
	scripts := InstallScripts()
	sh, ps := scripts[0], scripts[1]
	a, b := newKey(t).Public().String(), newKey(t).Public().String()
	// The Go source stays ASCII too; this is "caf" and a two-byte e-acute.
	nonASCII := "echo caf" + string([]byte{0xc3, 0xa9}) + "\n"
	tests := []struct {
		name    string
		script  InstallScript
		src     string
		version string
		keys    []string
		want    string
		wantErr string
	}{
		{
			name:    "install.sh gets the version and every key, the rest byte for byte",
			script:  sh,
			src:     "#!/bin/sh\n# pinned_version='' stays in a comment\npinned_version=''\npinned_keys=''\nmain \"$@\"\n",
			version: stampVersion,
			keys:    []string{a, b},
			want:    "#!/bin/sh\n# pinned_version='' stays in a comment\npinned_version='" + stampVersion + "'\npinned_keys='" + a + " " + b + "'\nmain \"$@\"\n",
		},
		{
			name:    "install.sh trusts no key when the release has none",
			script:  sh,
			src:     "pinned_version=''\npinned_keys=''\n",
			version: stampVersion,
			want:    "pinned_version='" + stampVersion + "'\npinned_keys=''\n",
		},
		{
			name:    "install.ps1 gets the version and each key quoted, keeping the lines' indentation",
			script:  ps,
			src:     "& {\n  $PinnedVersion = ''\n  $PinnedKeys = @()\n}\n",
			version: stampVersion,
			keys:    []string{a, b},
			want:    "& {\n  $PinnedVersion = '" + stampVersion + "'\n  $PinnedKeys = @('" + a + "', '" + b + "')\n}\n",
		},
		{
			name:    "install.ps1 trusts no key when the release has none",
			script:  ps,
			src:     "$PinnedVersion = ''\n$PinnedKeys = @()\n",
			version: stampVersion,
			want:    "$PinnedVersion = '" + stampVersion + "'\n$PinnedKeys = @()\n",
		},
		{
			name:    "a script without the version line is refused",
			script:  sh,
			src:     "pinned_keys=''\n",
			version: stampVersion,
			wantErr: "has 0 lines that read pinned_version=''",
		},
		{
			name:    "a line that only contains the marker doesn't count",
			script:  sh,
			src:     "echo pinned_version=''\npinned_keys=''\n",
			version: stampVersion,
			wantErr: "has 0 lines that read pinned_version=''",
		},
		{
			name:    "a script with the keys line twice is refused",
			script:  ps,
			src:     "$PinnedVersion = ''\n$PinnedKeys = @()\n  $PinnedKeys = @()\n",
			version: stampVersion,
			wantErr: "has 2 lines that read $PinnedKeys = @()",
		},
		{
			name:    "a script with CRLF line ends is refused",
			script:  ps,
			src:     "$PinnedVersion = ''\r\n$PinnedKeys = @()\r\n",
			version: stampVersion,
			wantErr: "printable ASCII with LF line ends",
		},
		{
			name:    "a script with a byte outside ASCII is refused",
			script:  sh,
			src:     "pinned_version=''\npinned_keys=''\n" + nonASCII,
			version: stampVersion,
			wantErr: "byte 41 is 0xc3",
		},
		{
			name:    "a version that isn't pairtime's is refused before it's quoted into the script",
			script:  sh,
			src:     "pinned_version=''\npinned_keys=''\n",
			version: "v1.0.0'; rm -rf ~; '",
			wantErr: "isn't v and a SemVer 2.0.0 version",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.script.Stamp([]byte(tc.src), tc.version, tc.keys)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("stamp error = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("stamped script:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

// TestStampCheckedInScripts stamps install/install.sh and install.ps1 as
// the repository has them: each takes the version and the keys once, and
// the stamped install.sh still parses.
func TestStampCheckedInScripts(t *testing.T) {
	key := newKey(t).Public().String()
	var sh []byte
	for _, s := range InstallScripts() {
		t.Run(s.Name+" takes the version and the keys once each", func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join("..", "..", "install", s.Name))
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.Stamp(src, stampVersion, []string{key})
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{s.version(stampVersion), s.keys([]string{key})} {
				if n := strings.Count(string(got), want); n != 1 {
					t.Errorf("the stamped copy has %q %d times, want once", want, n)
				}
			}
			if s.Name == "install.sh" {
				sh = got
			}
		})
	}
	t.Run("the stamped install.sh still parses", func(t *testing.T) {
		if sh == nil {
			t.Fatal("install.sh wasn't stamped")
		}
		if _, err := exec.LookPath("sh"); err != nil {
			t.Skip("no sh to parse install.sh with")
		}
		p := filepath.Join(t.TempDir(), "install.sh")
		if err := os.WriteFile(p, sh, 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("sh", "-n", p).CombinedOutput(); err != nil {
			t.Errorf("sh -n on the stamped install.sh: %v\n%s", err, out)
		}
	})
}
