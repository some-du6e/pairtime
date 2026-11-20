// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/release"
)

func publicKeyText(t *testing.T) string {
	t.Helper()
	k, err := release.GenerateSecretKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k.Public().String()
}

func TestStampScripts(t *testing.T) {
	sh, ps := installScripts[0], installScripts[1]
	a, b := publicKeyText(t), publicKeyText(t)
	// The Go source stays ASCII too; this is "caf" and a two-byte e-acute.
	nonASCII := "echo caf" + string([]byte{0xc3, 0xa9}) + "\n"
	tests := []struct {
		name    string
		script  installScript
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
			version: testVersion,
			keys:    []string{a, b},
			want:    "#!/bin/sh\n# pinned_version='' stays in a comment\npinned_version='" + testVersion + "'\npinned_keys='" + a + " " + b + "'\nmain \"$@\"\n",
		},
		{
			name:    "install.sh trusts no key when the release has none",
			script:  sh,
			src:     "pinned_version=''\npinned_keys=''\n",
			version: testVersion,
			want:    "pinned_version='" + testVersion + "'\npinned_keys=''\n",
		},
		{
			name:    "install.ps1 gets the version and each key quoted, keeping the lines' indentation",
			script:  ps,
			src:     "& {\n  $PinnedVersion = ''\n  $PinnedKeys = @()\n}\n",
			version: testVersion,
			keys:    []string{a, b},
			want:    "& {\n  $PinnedVersion = '" + testVersion + "'\n  $PinnedKeys = @('" + a + "', '" + b + "')\n}\n",
		},
		{
			name:    "install.ps1 trusts no key when the release has none",
			script:  ps,
			src:     "$PinnedVersion = ''\n$PinnedKeys = @()\n",
			version: testVersion,
			want:    "$PinnedVersion = '" + testVersion + "'\n$PinnedKeys = @()\n",
		},
		{
			name:    "a script without the version line is refused",
			script:  sh,
			src:     "pinned_keys=''\n",
			version: testVersion,
			wantErr: "has 0 lines that read pinned_version=''",
		},
		{
			name:    "a line that only contains the marker doesn't count",
			script:  sh,
			src:     "echo pinned_version=''\npinned_keys=''\n",
			version: testVersion,
			wantErr: "has 0 lines that read pinned_version=''",
		},
		{
			name:    "a script with the keys line twice is refused",
			script:  ps,
			src:     "$PinnedVersion = ''\n$PinnedKeys = @()\n  $PinnedKeys = @()\n",
			version: testVersion,
			wantErr: "has 2 lines that read $PinnedKeys = @()",
		},
		{
			name:    "a script with CRLF line ends is refused",
			script:  ps,
			src:     "$PinnedVersion = ''\r\n$PinnedKeys = @()\r\n",
			version: testVersion,
			wantErr: "printable ASCII with LF line ends",
		},
		{
			name:    "a script with a byte outside ASCII is refused",
			script:  sh,
			src:     "pinned_version=''\npinned_keys=''\n" + nonASCII,
			version: testVersion,
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
			got, err := tc.script.stamp([]byte(tc.src), tc.version, tc.keys)
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

func TestScriptsCommand(t *testing.T) {
	repoInstall := filepath.Join("..", "..", "install")
	key := publicKeyText(t)
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "the checked-in scripts take a version and keys, and install.sh still parses",
			run: func(t *testing.T) {
				var sh []byte
				for _, s := range installScripts {
					src := read(t, filepath.Join(repoInstall, s.name))
					got, err := s.stamp(src, testVersion, []string{key})
					if err != nil {
						t.Fatalf("%s: %v", s.name, err)
					}
					for _, want := range []string{s.version(testVersion), s.keys([]string{key})} {
						if n := strings.Count(string(got), want); n != 1 {
							t.Errorf("%s: the stamped copy has %q %d times, want once", s.name, want, n)
						}
					}
					if s.name == "install.sh" {
						sh = got
					}
				}
				if _, err := exec.LookPath("sh"); err != nil {
					t.Skip("no sh to parse install.sh with")
				}
				p := filepath.Join(t.TempDir(), "install.sh")
				write(t, p, string(sh))
				if out, err := exec.Command("sh", "-n", p).CombinedOutput(); err != nil {
					t.Errorf("sh -n on the stamped install.sh: %v\n%s", err, out)
				}
			},
		},
		{
			name: "scripts writes both stamped copies with the compiled-in keys",
			run: func(t *testing.T) {
				out := t.TempDir()
				stdout := mustCLI(t, "scripts", "-version", testVersion, "-in", repoInstall, "-out", out)
				compiled, err := release.TrustedKeys()
				if err != nil {
					t.Fatal(err)
				}
				var texts []string
				for _, k := range compiled {
					texts = append(texts, k.String())
				}
				for _, s := range installScripts {
					got := string(read(t, filepath.Join(out, s.name)))
					want, err := s.stamp(read(t, filepath.Join(repoInstall, s.name)), testVersion, texts)
					if err != nil {
						t.Fatal(err)
					}
					if got != string(want) {
						t.Errorf("%s isn't the checked-in script stamped with %s and the compiled-in keys", s.name, testVersion)
					}
				}
				if !strings.Contains(stdout, "they install "+testVersion) {
					t.Errorf("stdout %q doesn't name the version", stdout)
				}
			},
		},
		{
			name: "scripts writes neither copy when one script can't be stamped",
			run: func(t *testing.T) {
				in, out := t.TempDir(), t.TempDir()
				write(t, filepath.Join(in, "install.sh"), "pinned_version=''\npinned_keys=''\n")
				write(t, filepath.Join(in, "install.ps1"), "$PinnedVersion = ''\n")
				code, _, errOut := cli(t, "scripts", "-version", testVersion, "-in", in, "-out", out)
				if code != 1 || !strings.Contains(errOut, "install.ps1: has 0 lines that read $PinnedKeys = @()") {
					t.Fatalf("exit %d, stderr %q", code, errOut)
				}
				if entries, err := os.ReadDir(out); err != nil || len(entries) != 0 {
					t.Fatalf("-out holds %d files (%v), want none", len(entries), err)
				}
			},
		},
		{
			name: "scripts needs -version and -out",
			run: func(t *testing.T) {
				for _, args := range [][]string{{"scripts", "-out", t.TempDir()}, {"scripts", "-version", testVersion}} {
					if code, _, _ := cli(t, args...); code != 2 {
						t.Errorf("release %s: exit %d, want 2", strings.Join(args, " "), code)
					}
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
