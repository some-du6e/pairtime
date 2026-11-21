// SPDX-License-Identifier: AGPL-3.0-or-later

package ci

import (
	"fmt"
	"go/build"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// repoRoot is the repository's root, from this package's directory.
const repoRoot = "../.."

// imageTargets are the platforms server-image.yml builds pairtimed for.
var imageTargets = []string{"linux/amd64", "linux/arm64"}

// TestServerImagePaths checks that server-image.yml's paths filter covers
// every file the image is built from, so a change to any of them pushes a
// new image. It walks pairtimed's imports the way go build does for each
// of the image's targets, and those of the notices tool, which writes the
// image's THIRD_PARTY_NOTICES.txt.
func TestServerImagePaths(t *testing.T) {
	const workflow = ".github/workflows/server-image.yml"
	patterns := workflowPaths(t, workflow)
	var server buildFiles
	for _, target := range imageTargets {
		server.add(sources(t, target, "cmd/pairtimed"))
	}
	notices := sources(t, "linux/amd64", "tools/release")
	tests := []struct {
		name  string
		files []string
		// must are files the walk has to reach, so a walk that finds
		// nothing can't pass.
		must []string
	}{
		{
			name:  "a change to a Go file pairtimed is built from, for either Linux target, rebuilds the image",
			files: server.goFiles,
			must:  []string{"cmd/pairtimed/main.go", "docs/embed.go", "pkg/params/table.go"},
		},
		{
			name:  "a change to a file pairtimed embeds, such as docs/retention.json for /privacy, rebuilds the image",
			files: server.embedded,
			must:  []string{"docs/retention.json", "internal/server/pages/privacy.md", "deploy/pairtime.toml.tmpl"},
		},
		{
			name:  "a change to the notices tool, which writes the image's THIRD_PARTY_NOTICES.txt, or to the list it reads rebuilds the image",
			files: slices.Concat(notices.goFiles, notices.embedded, []string{"tools/release/extra-notices.txt"}),
			must:  []string{"tools/release/notices.go"},
		},
		{
			name:  "a change to the module's requirements, the Dockerfile, the license the image carries or the workflow rebuilds the image",
			files: []string{"go.mod", "go.sum", "Dockerfile.pairtimed", "LICENSE", workflow},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, f := range tt.must {
				if !slices.Contains(tt.files, f) {
					t.Errorf("the walk didn't reach %s, so it can't be trusted with the rest", f)
				}
			}
			for _, f := range tt.files {
				if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(f))); err != nil {
					t.Errorf("%s: %v", f, err)
				}
				ok, err := anyMatch(patterns, f)
				if err != nil {
					t.Fatal(err)
				}
				if !ok {
					t.Errorf("%s is under none of %s's paths, so a change to it doesn't rebuild the image; add it there", f, workflow)
				}
			}
		})
	}
}

func TestMatchPath(t *testing.T) {
	tests := []struct {
		name, pattern, file string
		want, wantErr       bool
	}{
		{name: "a directory's /** matches any file under it", pattern: "internal/server/**", file: "internal/server/pages/privacy.md", want: true},
		{name: "a directory's /** doesn't match a sibling whose name starts the same", pattern: "cmd/pairtimed/**", file: "cmd/pairtime/main.go"},
		{name: "a file's path matches that file", pattern: "docs/retention.json", file: "docs/retention.json", want: true},
		{name: "a file's path matches no other file in its directory", pattern: "docs/retention.json", file: "docs/DESIGN.md"},
		{name: "* matches within one directory", pattern: "docs/*.json", file: "docs/retention.json", want: true},
		{name: "* doesn't cross a slash", pattern: "docs/*.json", file: "docs/schema/event.schema.json"},
		{name: "a dot is a dot, not any character", pattern: "go.mod", file: "goxmod"},
		{name: "a negated pattern is refused, since the check doesn't read negation", pattern: "!docs/**", file: "docs/DESIGN.md", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := matchPath(tt.pattern, tt.file)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("matchPath(%q, %q) = %v, %v; want %v, error %v", tt.pattern, tt.file, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

// buildFiles are the files in the repository a build reads: the Go files
// it compiles, and the files they embed. Both are paths from the
// repository's root, with slashes.
type buildFiles struct {
	goFiles, embedded []string
}

// add merges o into b, each file once.
func (b *buildFiles) add(o buildFiles) {
	b.goFiles = sortedUnique(append(b.goFiles, o.goFiles...))
	b.embedded = sortedUnique(append(b.embedded, o.embedded...))
}

func sortedUnique(s []string) []string {
	slices.Sort(s)
	return slices.Compact(s)
}

// sources returns the files in the repository that go build reads to build
// the package in pkg for target, GOOS/GOARCH, with cgo off as AGENTS.md
// has it: the Go files of every package in its import graph that lives
// here, in the module or in a module go.mod replaces with a local
// directory, and the files they embed. The standard library and the module
// cache are left out, since go.sum pins what the build takes from them.
func sources(t *testing.T, target, pkg string) buildFiles {
	t.Helper()
	return walkSources(t, target, nil, pkg, false)
}

// testSources returns the files in the repository that go test -tags
// reads to build the test binary of the package in pkg for target: what
// sources returns with those build tags, and the package's own _test.go
// files, the files they embed and the packages they import.
func testSources(t *testing.T, target string, tags []string, pkg string) buildFiles {
	t.Helper()
	return walkSources(t, target, tags, pkg, true)
}

// walkSources walks the import graph of the package in pkg for sources and
// testSources, with the build tags in tags, and with the package's own
// test files when tests is set.
func walkSources(t *testing.T, target string, tags []string, pkg string, tests bool) buildFiles {
	t.Helper()
	mod, replaced := goMod(t)
	goos, goarch, ok := strings.Cut(target, "/")
	if !ok {
		t.Fatalf("the target %q isn't GOOS/GOARCH", target)
	}
	ctx := build.Default
	ctx.GOOS, ctx.GOARCH, ctx.CgoEnabled = goos, goarch, false
	ctx.BuildTags = append(slices.Clone(ctx.BuildTags), tags...)
	var out buildFiles
	seen := make(map[string]bool)
	var visit func(dir string)
	visit = func(dir string) {
		if seen[dir] {
			return
		}
		seen[dir] = true
		// ImportDir reads the directory itself, so it never runs the go
		// command.
		p, err := ctx.ImportDir(filepath.Join(repoRoot, filepath.FromSlash(dir)), 0)
		if err != nil {
			t.Fatalf("%s for %s: %v", dir, target, err)
		}
		files, embeds, imports := p.GoFiles, p.EmbedPatterns, p.Imports
		// go test compiles the test files of the package it tests alone;
		// the packages they import are built as go build builds them.
		if tests && dir == pkg {
			files = slices.Concat(files, p.TestGoFiles, p.XTestGoFiles)
			embeds = slices.Concat(embeds, p.TestEmbedPatterns, p.XTestEmbedPatterns)
			imports = slices.Concat(imports, p.TestImports, p.XTestImports)
		}
		for _, f := range files {
			out.goFiles = append(out.goFiles, path.Join(dir, f))
		}
		for _, pattern := range embeds {
			out.embedded = append(out.embedded, embeddedFiles(t, dir, pattern)...)
		}
		for _, imp := range imports {
			if d, ok := localDir(imp, mod, replaced); ok {
				visit(d)
			}
		}
	}
	visit(pkg)
	out.goFiles = sortedUnique(out.goFiles)
	out.embedded = sortedUnique(out.embedded)
	return out
}

// embeddedFiles returns the files a //go:embed pattern of the package in
// dir names, as go build reads it: a directory names the files under it,
// apart from those whose names start with "." or "_", unless the pattern
// starts with "all:".
func embeddedFiles(t *testing.T, dir, pattern string) []string {
	t.Helper()
	root := os.DirFS(repoRoot)
	rest, all := strings.CutPrefix(pattern, "all:")
	matches, err := fs.Glob(root, path.Join(dir, rest))
	if err != nil || len(matches) == 0 {
		t.Fatalf("%s: //go:embed %s names no file (%v)", dir, pattern, err)
	}
	var files []string
	for _, m := range matches {
		err := fs.WalkDir(root, m, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if p != m && !all && (strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_")) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if !d.IsDir() {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// localDir returns the directory, from the repository's root, of the
// package imp when it lives in the repository: in the module mod, or in a
// module that replaced maps to a local directory.
func localDir(imp, mod string, replaced map[string]string) (string, bool) {
	if rest, ok := strings.CutPrefix(imp, mod); ok && (rest == "" || rest[0] == '/') {
		return path.Join(".", rest), true
	}
	for module, dir := range replaced {
		if rest, ok := strings.CutPrefix(imp, module); ok && (rest == "" || rest[0] == '/') {
			return path.Join(dir, rest), true
		}
	}
	return "", false
}

// goMod returns go.mod's module path, and the directory of each replace
// directive whose target is a local directory, by the module it replaces.
func goMod(t *testing.T) (string, map[string]string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	var mod string
	replaced := make(map[string]string)
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) == 2 && f[0] == "module":
			mod = f[1]
		case len(f) >= 2 && f[0] == "replace" && f[1] == "(":
			t.Fatal("go.mod has a replace block, which this test doesn't read yet")
		case len(f) > 0 && f[0] == "replace":
			// replace old [version] => new [version]
			i := slices.Index(f, "=>")
			if i < 2 || i+1 >= len(f) {
				t.Fatalf("go.mod: can't read %q", line)
			}
			if to := f[i+1]; strings.HasPrefix(to, "./") || strings.HasPrefix(to, "../") {
				replaced[f[1]] = path.Clean(to)
			}
		}
	}
	if mod == "" {
		t.Fatal("go.mod names no module")
	}
	return mod, replaced
}

// workflowPaths returns the patterns of the one paths filter in the
// workflow file name.
func workflowPaths(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	var patterns []string
	filters, in := 0, false
	for _, line := range strings.Split(string(b), "\n") {
		s := strings.TrimSpace(line)
		switch {
		case s == "paths:":
			filters++
			in = true
		case !in, s == "", strings.HasPrefix(s, "#"):
		case strings.HasPrefix(s, "- "):
			patterns = append(patterns, strings.Trim(strings.TrimSpace(s[2:]), `"'`))
		default:
			in = false
		}
	}
	if filters != 1 || len(patterns) == 0 {
		t.Fatalf("%s has %d paths filters, with %d patterns in all; this test reads exactly one", name, filters, len(patterns))
	}
	return patterns
}

// matchPath reports whether file matches pattern, a workflow paths filter,
// as GitHub reads one: ** matches any characters and * any but a slash. It
// refuses the filter syntax it doesn't read: ?, +, [], {} and a leading !.
func matchPath(pattern, file string) (bool, error) {
	if strings.ContainsAny(pattern, "?+[]{}!") {
		return false, fmt.Errorf("the paths filter %q uses syntax this test doesn't read", pattern)
	}
	var re strings.Builder
	re.WriteString("^")
	for i, part := range strings.Split(pattern, "**") {
		if i > 0 {
			re.WriteString(".*")
		}
		re.WriteString(strings.ReplaceAll(regexp.QuoteMeta(part), `\*`, "[^/]*"))
	}
	re.WriteString("$")
	return regexp.MatchString(re.String(), file)
}

// anyMatch reports whether file matches one of patterns.
func anyMatch(patterns []string, file string) (bool, error) {
	for _, p := range patterns {
		ok, err := matchPath(p, file)
		if err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}
