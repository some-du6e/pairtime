// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
)

// noticeWorld is a fake build for the notices: a GOROOT, a module cache
// holding two modules, a repository, and the build info and go list output
// of a binary that links both modules and one package that embeds
// third-party rules.
type noticeWorld struct {
	dir    string
	info   *debug.BuildInfo
	env    goEnv
	pkgs   []listedPackage
	extras []extraNotice
}

func newNoticeWorld(t *testing.T) *noticeWorld {
	t.Helper()
	dir := t.TempDir()
	goroot := filepath.Join(dir, "goroot")
	write(t, filepath.Join(goroot, "LICENSE"), "Copyright 2009 The Go Authors.\n")
	write(t, filepath.Join(goroot, "PATENTS"), "Additional IP Rights Grant (Patents)\n")

	a := filepath.Join(dir, "mod", "example.com", "a@v1.2.0")
	write(t, filepath.Join(a, "LICENSE"), "a's MIT license\n")
	write(t, filepath.Join(a, "a.go"), "package a\n")
	write(t, filepath.Join(a, "notice.go"), "package a // code, not a notice\n")
	write(t, filepath.Join(a, "internal", "vendored", "LICENSE.md"), "the vendored package's license, with no final newline")
	write(t, filepath.Join(a, "unlinked", "COPYING"), "a package the binary doesn't link\n")
	b := filepath.Join(dir, "mod", "example.com", "b@v0.3.1")
	write(t, filepath.Join(b, "COPYING"), "b's BSD license\n")
	write(t, filepath.Join(b, "NOTICE"), "b's Apache NOTICE\n")

	repo := filepath.Join(dir, "repo")
	write(t, filepath.Join(repo, "pkg", "embeds", "embeds.go"), "package embeds\n")
	write(t, filepath.Join(repo, "pkg", "embeds", "rules", "LICENSE"), "the embedded rules' license\n")
	write(t, filepath.Join(repo, "pkg", "other", "other.go"), "package other\n")
	write(t, filepath.Join(repo, "third_party", "LICENSE"), "material only an unlinked package embeds\n")
	list := filepath.Join(dir, "extra-notices.txt")
	write(t, list, "# a comment\n\n"+
		"pkg/embeds pkg/embeds/rules/LICENSE Example rules 1.0, embedded in pkg/embeds\n"+
		"pkg/other third_party/LICENSE Material only pkg/other embeds\n")
	extras, err := readExtraNotices(list, repo)
	if err != nil {
		t.Fatal(err)
	}

	modA := &listedModule{Path: "example.com/a", Version: "v1.2.0", Dir: a}
	modB := &listedModule{Path: "example.com/b", Version: "v0.3.1", Dir: b}
	repoMod := &listedModule{Path: "example.com/repo", Dir: repo, Main: true}
	return &noticeWorld{
		dir: dir,
		info: &debug.BuildInfo{
			GoVersion: "go1.26.3",
			Path:      "example.com/repo/cmd/x",
			Main:      debug.Module{Path: "example.com/repo", Version: "(devel)"},
			Deps: []*debug.Module{
				{Path: "example.com/b", Version: "v0.3.1", Sum: "h1:b"},
				{Path: "example.com/a", Version: "v1.2.0", Sum: "h1:a"},
			},
			Settings: []debug.BuildSetting{{Key: "GOOS", Value: "darwin"}, {Key: "GOARCH", Value: "arm64"}, {Key: "CGO_ENABLED", Value: "0"}},
		},
		env: goEnv{GOROOT: goroot, GOVERSION: "go1.26.3"},
		pkgs: []listedPackage{
			{ImportPath: "fmt", Dir: filepath.Join(goroot, "src", "fmt"), Standard: true},
			{ImportPath: "example.com/a/internal/vendored", Dir: filepath.Join(a, "internal", "vendored"), Module: modA},
			{ImportPath: "example.com/a", Dir: a, Module: modA},
			{ImportPath: "example.com/b", Dir: b, Module: modB},
			{ImportPath: "example.com/repo/pkg/embeds", Dir: filepath.Join(repo, "pkg", "embeds"), Module: repoMod},
			{ImportPath: "example.com/repo/cmd/x", Dir: filepath.Join(repo, "cmd", "x"), Module: repoMod},
		},
		extras: extras,
	}
}

// section is how the notices frame one file.
func section(title, body string) string {
	return "\n" + rule + title + "\n" + rule + "\n" + body
}

func TestBuildNotices(t *testing.T) {
	w := newNoticeWorld(t)
	n, err := buildNotices(w.info, w.env, w.pkgs, w.extras)
	if err != nil {
		t.Fatal(err)
	}
	text := string(n.render())
	again, err := buildNotices(w.info, w.env, w.pkgs, w.extras)
	if err != nil {
		t.Fatal(err)
	}
	goPart := "Go go1.26.3, the runtime and standard library"
	want := "Third-party notices\n\n" +
		"Besides the code of example.com/repo, whose license is in LICENSE,\n" +
		"this binary contains the software listed below. After the list come the\n" +
		"license and notice files that software comes with, word for word.\n\n" +
		"Binary:   example.com/repo/cmd/x\n" +
		"Platform: darwin/arm64\n" +
		"Go:       go1.26.3\n\n" +
		"Software:\n" +
		"  " + goPart + "\n" +
		"  example.com/a v1.2.0\n" +
		"  example.com/b v0.3.1\n" +
		"  Example rules 1.0, embedded in pkg/embeds\n" +
		section(goPart+": LICENSE", "Copyright 2009 The Go Authors.\n") +
		section(goPart+": PATENTS", "Additional IP Rights Grant (Patents)\n") +
		section("example.com/a v1.2.0: LICENSE", "a's MIT license\n") +
		section("example.com/a v1.2.0: internal/vendored/LICENSE.md", "the vendored package's license, with no final newline\n") +
		section("example.com/b v0.3.1: COPYING", "b's BSD license\n") +
		section("example.com/b v0.3.1: NOTICE", "b's Apache NOTICE\n") +
		section("Example rules 1.0, embedded in pkg/embeds: pkg/embeds/rules/LICENSE", "the embedded rules' license\n")

	tests := []struct {
		name string
		ok   bool
	}{
		{name: "the notices are exactly the expected text", ok: text == want},
		{name: "Go's LICENSE comes first", ok: strings.Index(text, rule) == strings.Index(text, rule+goPart+": LICENSE")},
		{name: "a license beside a linked package of a module is in", ok: strings.Contains(text, "internal/vendored/LICENSE.md")},
		{name: "a license beside a package the binary doesn't link is left out", ok: !strings.Contains(text, "a package the binary doesn't link")},
		{name: "Go source named like a notice is left out", ok: !strings.Contains(text, "notice.go")},
		{name: "an Apache NOTICE file is in", ok: strings.Contains(text, "b's Apache NOTICE")},
		{name: "an extra for a linked package is in", ok: strings.Contains(text, "the embedded rules' license")},
		{name: "an extra for a package the binary doesn't link is left out", ok: !strings.Contains(text, "material only an unlinked package embeds")},
		{name: "a file without a final newline gets one", ok: strings.Contains(text, "no final newline\n")},
		{name: "no directory on the build machine is named", ok: !strings.Contains(text, w.dir)},
		{name: "the same inputs give the same bytes", ok: string(again.render()) == text},
		{name: "the counts say two modules", ok: n.modules == 2 && len(n.parts) == 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("notices:\n%s", text)
			}
		})
	}
}

func TestBuildNoticesRefuses(t *testing.T) {
	tests := []struct {
		name    string
		edit    func(t *testing.T, w *noticeWorld)
		wantErr string
	}{
		{
			name:    "a binary built with another Go version",
			edit:    func(t *testing.T, w *noticeWorld) { w.env.GOVERSION = "go1.26.2" },
			wantErr: "built with go1.26.3, but the go command here is go1.26.2",
		},
		{
			name:    "build info without a Go version",
			edit:    func(t *testing.T, w *noticeWorld) { w.info.GoVersion = "" },
			wantErr: "names no Go version",
		},
		{
			name: "Go's LICENSE missing",
			edit: func(t *testing.T, w *noticeWorld) {
				if err := os.Remove(filepath.Join(w.env.GOROOT, "LICENSE")); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "reading Go's LICENSE",
		},
		{
			name: "a module with only a NOTICE at its root",
			edit: func(t *testing.T, w *noticeWorld) {
				if err := os.Remove(filepath.Join(w.dir, "mod", "example.com", "b@v0.3.1", "COPYING")); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "the module example.com/b v0.3.1 has no license file at its root",
		},
		{
			name: "a module the binary links that go list doesn't find",
			edit: func(t *testing.T, w *noticeWorld) {
				w.info.Deps = append(w.info.Deps, &debug.Module{Path: "example.com/c", Version: "v1.0.0"})
			},
			wantErr: "(only go list: none; only the build info: example.com/c@v1.0.0)",
		},
		{
			name:    "a module go list finds that the binary doesn't list",
			edit:    func(t *testing.T, w *noticeWorld) { w.info.Deps = w.info.Deps[:1] },
			wantErr: "(only go list: example.com/a@v1.2.0; only the build info: none)",
		},
		{
			name:    "a module at another version than the binary's",
			edit:    func(t *testing.T, w *noticeWorld) { w.info.Deps[0].Version = "v0.3.2" },
			wantErr: "(only go list: example.com/b@v0.3.1; only the build info: example.com/b@v0.3.2)",
		},
		{
			name:    "a module go list gives no directory",
			edit:    func(t *testing.T, w *noticeWorld) { w.pkgs[3].Module.Dir = "" },
			wantErr: "go list gave the module example.com/b v0.3.1 no directory",
		},
		{
			name:    "a package outside its module's directory",
			edit:    func(t *testing.T, w *noticeWorld) { w.pkgs[1].Dir = filepath.Join(w.dir, "elsewhere") },
			wantErr: "outside its module example.com/a v1.2.0",
		},
		{
			name:    "a package go list gives no module",
			edit:    func(t *testing.T, w *noticeWorld) { w.pkgs[2].Module = nil },
			wantErr: "go list gave the package example.com/a no module",
		},
		{
			name:    "go list without the binary's main package",
			edit:    func(t *testing.T, w *noticeWorld) { w.pkgs = w.pkgs[:len(w.pkgs)-1] },
			wantErr: "didn't list the binary's main package example.com/repo/cmd/x",
		},
		{
			name:    "a main module other than the binary's",
			edit:    func(t *testing.T, w *noticeWorld) { w.info.Main.Path = "example.com/other" },
			wantErr: "go list's main module is example.com/repo, but the binary's is example.com/other",
		},
		{
			name: "a license that is a symbolic link",
			edit: func(t *testing.T, w *noticeWorld) {
				p := filepath.Join(w.dir, "mod", "example.com", "a@v1.2.0", "LICENSE")
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(w.env.GOROOT, "LICENSE"), p); err != nil {
					t.Skipf("can't make a symbolic link here: %v", err)
				}
			},
			wantErr: "LICENSE in the module example.com/a v1.2.0 is a symbolic link",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newNoticeWorld(t)
			tt.edit(t, w)
			_, err := buildNotices(w.info, w.env, w.pkgs, w.extras)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestModuleKeysMatch(t *testing.T) {
	tests := []struct {
		name   string
		binary *debug.Module
		listed *listedModule
		title  string
	}{
		{
			name:   "a module",
			binary: &debug.Module{Path: "example.com/a", Version: "v1.0.0"},
			listed: &listedModule{Path: "example.com/a", Version: "v1.0.0"},
			title:  "example.com/a v1.0.0",
		},
		{
			name:   "a module replaced by another module",
			binary: &debug.Module{Path: "example.com/a", Version: "v1.0.0", Replace: &debug.Module{Path: "example.com/fork", Version: "v1.0.1"}},
			listed: &listedModule{Path: "example.com/a", Version: "v1.0.0", Replace: &listedModule{Path: "example.com/fork", Version: "v1.0.1"}},
			title:  "example.com/a v1.0.0 => example.com/fork v1.0.1",
		},
		{
			name:   "a module replaced by a directory, which build info gives the version (devel)",
			binary: &debug.Module{Path: "example.com/a", Version: "v1.0.0", Replace: &debug.Module{Path: "../a", Version: "(devel)"}},
			listed: &listedModule{Path: "example.com/a", Version: "v1.0.0", Replace: &listedModule{Path: "../a"}},
			title:  "example.com/a v1.0.0 => ../a",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := moduleKey(tt.binary), tt.listed.key(); got != want {
				t.Errorf("build info key %q, go list key %q", got, want)
			}
			if got := tt.listed.title(); got != tt.title {
				t.Errorf("title %q, want %q", got, tt.title)
			}
		})
	}
}

func TestIsNoticeFile(t *testing.T) {
	tests := []struct {
		name          string
		file          string
		notice, legal bool // a notice file; a license file
	}{
		{name: "LICENSE is a license", file: "LICENSE", notice: true, legal: true},
		{name: "case doesn't matter", file: "License.txt", notice: true, legal: true},
		{name: "a suffix after a hyphen counts", file: "LICENSE-3RD-PARTY.md", notice: true, legal: true},
		{name: "a suffix after an underscore counts", file: "LICENSE_APACHE", notice: true, legal: true},
		{name: "COPYING is a license", file: "COPYING", notice: true, legal: true},
		{name: "the British spelling counts", file: "LICENCE", notice: true, legal: true},
		{name: "MIT-LICENSE is a license", file: "MIT-LICENSE.txt", notice: true, legal: true},
		{name: "UNLICENSE is a license", file: "UNLICENSE", notice: true, legal: true},
		{name: "NOTICE is a notice but not a license", file: "NOTICE", notice: true},
		{name: "PATENTS is a notice but not a license", file: "PATENTS", notice: true},
		{name: "Go source named notice.go is code", file: "notice.go"},
		{name: "a Go test named license_test.go is code", file: "license_test.go"},
		{name: "assembly named license.s is code", file: "license.s"},
		{name: "a name that only starts with the stem isn't one", file: "licensed.txt"},
		{name: "LICENSES, plural, isn't one", file: "LICENSES"},
		{name: "README.md isn't one", file: "README.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNoticeFile(tt.file); got != tt.notice {
				t.Errorf("isNoticeFile(%q) = %v", tt.file, got)
			}
			if got := isLicenseFile(tt.file); got != tt.legal {
				t.Errorf("isLicenseFile(%q) = %v", tt.file, got)
			}
		})
	}
}

func TestBuildEnv(t *testing.T) {
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "-buildmode", Value: "exe"},
		{Key: "-ldflags", Value: "-s -w"},
		{Key: "-tags", Value: "desktop,netgo"},
		{Key: "-trimpath", Value: "true"},
		{Key: "CGO_ENABLED", Value: "0"},
		{Key: "GOARCH", Value: "amd64"},
		{Key: "GOOS", Value: "windows"},
		{Key: "GOAMD64", Value: "v1"},
		{Key: "DefaultGODEBUG", Value: "x509sha1=1"},
		{Key: "vcs", Value: "git"},
	}}
	env, tags := buildEnv(info)
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "GOOS, GOARCH and GOAMD64 come from the binary", ok: slices.Contains(env, "GOOS=windows") && slices.Contains(env, "GOARCH=amd64") && slices.Contains(env, "GOAMD64=v1")},
		{name: "cgo is set the way the binary was built", ok: slices.Contains(env, "CGO_ENABLED=0")},
		{name: "GOFLAGS is cleared and go.work ignored", ok: slices.Contains(env, "GOFLAGS=") && slices.Contains(env, "GOWORK=off")},
		{name: "the tags come from the binary", ok: tags == "desktop,netgo"},
		{name: "flags, VCS and GODEBUG settings stay out of the environment", ok: !slices.ContainsFunc(env, func(s string) bool {
			return strings.HasPrefix(s, "-") || strings.HasPrefix(s, "vcs") || strings.HasPrefix(s, "DefaultGODEBUG")
		})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("env %q, tags %q", env, tags)
			}
		})
	}
}

func TestReadExtraNotices(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "root.go"), "package root\n")
	write(t, filepath.Join(root, "pkg", "embeds", "embeds.go"), "package embeds\n")
	write(t, filepath.Join(root, "pkg", "embeds", "LICENSE"), "MIT\n")
	write(t, filepath.Join(root, "pkg", "embeds", "rules", "rules.json"), "{}\n")
	write(t, filepath.Join(root, "pkg", "tested", "tested_test.go"), "package tested\n")
	write(t, filepath.Join(root, "pkg", "ignored", "_ignored.go"), "package ignored\n")
	tests := []struct {
		name    string
		list    string
		want    []string // titles
		wantErr string
	}{
		{name: "comments and blank lines are skipped", list: "# a comment\n\n   # an indented one\n"},
		{name: "a line names a package, a notice file and a title", list: "pkg/embeds pkg/embeds/LICENSE Some rules, v2\n", want: []string{"Some rules, v2"}},
		{name: "the repository root is a package directory", list: ". pkg/embeds/LICENSE Root rules\n", want: []string{"Root rules"}},
		{name: "a line without a title is refused", list: "pkg/embeds pkg/embeds/LICENSE\n", wantErr: "line 1: want a package directory, a notice file and a title"},
		{name: "a path out of the repository is refused", list: "pkg/embeds ../LICENSE Rules\n", wantErr: `"../LICENSE" isn't a clean path inside the repository`},
		{name: "an absolute path is refused", list: "/etc pkg/embeds/LICENSE Rules\n", wantErr: `"/etc" isn't a clean path`},
		{name: "a path that isn't clean is refused", list: "./pkg/embeds pkg/embeds/LICENSE Rules\n", wantErr: `"./pkg/embeds" isn't a clean path`},
		{name: "a backslash is refused", list: `pkg\embeds pkg/embeds/LICENSE Rules` + "\n", wantErr: "isn't a clean path"},
		{name: "a package directory that doesn't exist is refused", list: "pkg/gone pkg/embeds/LICENSE Rules\n", wantErr: "the package directory pkg/gone doesn't exist"},
		{name: "a directory with no Go file, which no binary can link, is refused", list: "pkg/embeds/rules pkg/embeds/LICENSE Rules\n", wantErr: "line 1: pkg/embeds/rules holds no Go file that a build compiles"},
		{name: "a directory with only tests is refused", list: "pkg/tested pkg/embeds/LICENSE Rules\n", wantErr: "pkg/tested holds no Go file that a build compiles"},
		{name: "a directory whose only Go file the go command ignores is refused", list: "pkg/ignored pkg/embeds/LICENSE Rules\n", wantErr: "pkg/ignored holds no Go file that a build compiles"},
		{name: "a notice file that doesn't exist is refused", list: "# x\npkg/embeds pkg/embeds/NOTICE Rules\n", wantErr: "line 2:"},
		{name: "a line listed twice is refused", list: "pkg/embeds pkg/embeds/LICENSE A\npkg/embeds pkg/embeds/LICENSE B\n", wantErr: "line 2: pkg/embeds/LICENSE for pkg/embeds is listed twice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list := filepath.Join(t.TempDir(), "extra-notices.txt")
			write(t, list, tt.list)
			extras, err := readExtraNotices(list, root)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var titles []string
			for _, e := range extras {
				titles = append(titles, e.title)
				if string(e.data) != "MIT\n" {
					t.Errorf("%s: data %q", e.title, e.data)
				}
			}
			if !slices.Equal(titles, tt.want) {
				t.Fatalf("titles %q, want %q", titles, tt.want)
			}
		})
	}

	// release.yml reads the checked-in list for every target, but only on
	// PRs that touch the release code. This checks every line on every PR.
	t.Run("the checked-in extra-notices.txt names files that exist", func(t *testing.T) {
		if _, err := readExtraNotices("extra-notices.txt", filepath.Join("..", "..")); err != nil {
			t.Fatal(err)
		}
	})
}

func TestNoticesCommand(t *testing.T) {
	notBinary := filepath.Join(t.TempDir(), "pairtime")
	write(t, notBinary, "#!/bin/sh\necho not a Go binary\n")
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantErr  string
	}{
		{name: "-bin is required", args: []string{"notices", "-out", "x"}, wantCode: 2, wantErr: "-bin is required"},
		{name: "-out is required", args: []string{"notices", "-bin", notBinary}, wantCode: 2, wantErr: "-out is required"},
		{name: "a file that isn't a Go binary is refused", args: []string{"notices", "-bin", notBinary, "-out", filepath.Join(t.TempDir(), noticesName)}, wantCode: 1, wantErr: "reading its build info"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, errOut := cli(t, tt.args...)
			if code != tt.wantCode || !strings.Contains(errOut, tt.wantErr) {
				t.Fatalf("exit %d, stderr %q; want exit %d and %q", code, errOut, tt.wantCode, tt.wantErr)
			}
		})
	}
}
