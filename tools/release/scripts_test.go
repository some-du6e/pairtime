// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/release"
)

func TestScriptsCommand(t *testing.T) {
	repoInstall := filepath.Join("..", "..", "install")
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
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
				for _, s := range release.InstallScripts() {
					got := string(read(t, filepath.Join(out, s.Name)))
					want, err := s.Stamp(read(t, filepath.Join(repoInstall, s.Name)), testVersion, texts)
					if err != nil {
						t.Fatal(err)
					}
					if got != string(want) {
						t.Errorf("%s isn't the checked-in script stamped with %s and the compiled-in keys", s.Name, testVersion)
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

// TestScriptsInPayload checks that scripts -payload lists the stamped
// copies' SHA-256s in the payload before it's signed, and that verify holds
// a release's copies to them (D-immutable-releases).
func TestScriptsInPayload(t *testing.T) {
	repoInstall := filepath.Join("..", "..", "install")
	scripts := func(f fixture, more ...string) []string {
		return append([]string{"scripts", "-version", testVersion, "-in", repoInstall, "-out", f.out}, more...)
	}
	// built makes a release the way release.yml's build job does: dist,
	// then scripts -payload, then sign.
	built := func(t *testing.T) fixture {
		t.Helper()
		f := newFixture(t)
		mustCLI(t, f.distArgs(f.out, f.payload)...)
		mustCLI(t, scripts(f, "-payload", f.payload)...)
		mustCLI(t, "sign", "-throwaway", "-payload", f.payload, "-out", f.manifest, "-pub-out", f.pub)
		return f
	}
	refused := func(t *testing.T, f fixture, want string) {
		t.Helper()
		code, _, errOut := cli(t, "verify", "-dir", f.out, "-pub", f.pub)
		if code != 1 || !strings.Contains(errOut, want) {
			t.Fatalf("verify: exit %d, stderr %q; want exit 1 and %q", code, errOut, want)
		}
	}
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "the payload lists both stamped copies' SHA-256s, and nothing else in it changes",
			run: func(t *testing.T) {
				f := newFixture(t)
				mustCLI(t, f.distArgs(f.out, f.payload)...)
				before := read(t, f.payload)
				out := mustCLI(t, scripts(f, "-payload", f.payload)...)
				m, err := release.DecodeManifest(read(t, f.payload))
				if err != nil {
					t.Fatal(err)
				}
				if len(m.InstallScripts) != len(release.InstallScripts()) {
					t.Errorf("install_scripts = %v, want one entry per script", m.InstallScripts)
				}
				for _, s := range release.InstallScripts() {
					if got, want := m.InstallScripts[s.Name], sha256Hex(read(t, filepath.Join(f.out, s.Name))); got != want {
						t.Errorf("install_scripts[%s] = %q, want the stamped copy's %s", s.Name, got, want)
					}
				}
				m.InstallScripts = nil
				if again, err := release.EncodeManifest(m); err != nil || !bytes.Equal(again, before) {
					t.Errorf("without install_scripts the payload is\n%s\nwant dist's\n%s", again, before)
				}
				if !strings.Contains(out, "listed their SHA-256s in "+f.payload) {
					t.Errorf("stdout %q doesn't say where the SHA-256s went", out)
				}
			},
		},
		{
			name: "verify accepts a release whose scripts are listed, and counts them",
			run: func(t *testing.T) {
				f := built(t)
				out := mustCLI(t, "verify", "-dir", f.out, "-pub", f.pub, "-targets", testTargets)
				if !strings.Contains(out, "and its 2 install scripts match it") {
					t.Errorf("verify printed %q", out)
				}
			},
		},
		{
			name: "verify refuses an install script changed after signing",
			run: func(t *testing.T) {
				f := built(t)
				p := filepath.Join(f.out, "install.sh")
				write(t, p, string(read(t, p))+"echo changed\n")
				refused(t, f, "install.sh has SHA-256")
			},
		},
		{
			name: "verify refuses a release without an install script its manifest lists",
			run: func(t *testing.T) {
				f := built(t)
				if err := os.Remove(filepath.Join(f.out, "install.ps1")); err != nil {
					t.Fatal(err)
				}
				refused(t, f, "the manifest lists install script install.ps1")
			},
		},
		{
			name: "verify refuses an install script that the manifest doesn't list",
			run: func(t *testing.T) {
				f := newFixture(t)
				mustCLI(t, f.distArgs(f.out, f.payload)...)
				mustCLI(t, scripts(f)...)
				mustCLI(t, "sign", "-throwaway", "-payload", f.payload, "-out", f.manifest, "-pub-out", f.pub)
				refused(t, f, "install.sh is in "+f.out+", but the manifest doesn't list its SHA-256")
			},
		},
		{
			name: "scripts refuses another release's payload and writes nothing",
			run: func(t *testing.T) {
				f := newFixture(t)
				mustCLI(t, f.distArgs(f.out, f.payload)...)
				before := read(t, f.payload)
				code, _, errOut := cli(t, "scripts", "-version", "v0.2.0", "-in", repoInstall, "-out", f.out, "-payload", f.payload)
				if code != 1 || !strings.Contains(errOut, "is the payload for "+testVersion+", not v0.2.0") {
					t.Fatalf("exit %d, stderr %q", code, errOut)
				}
				if _, err := os.Stat(filepath.Join(f.out, "install.sh")); err == nil {
					t.Error("install.sh was written")
				}
				if !bytes.Equal(read(t, f.payload), before) {
					t.Error("the payload changed")
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
