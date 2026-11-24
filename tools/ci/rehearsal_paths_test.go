// SPDX-License-Identifier: AGPL-3.0-or-later

package ci

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// rehearsalTarget is the platform of rehearsal.yml's runner, ubuntu-latest,
// where the rehearsal builds pairtime and pairtimed and go test builds the
// rehearsal itself.
const rehearsalTarget = "linux/amd64"

// rehearsalFile is the rehearsal's one test file.
const rehearsalFile = "internal/rehearsal/rehearsal_desktop_test.go"

// TestRehearsalPaths checks that rehearsal.yml's paths filter covers
// everything the rehearsal builds or reads, so a PR that could break it
// runs it. It walks the imports of pairtime and pairtimed, which the
// rehearsal builds, and of the rehearsal's own test binary, built with
// -tags desktop, which is also the stand-in claude. Then the Claude Code
// capture the stand-in replays, and the files the workflow's steps run or
// mount. While the rehearsal ran in platform.yml's Linux job, #123 broke it
// through cmd/pairtimed, which platform.yml's paths leave out. The same job
// runs the live harness's Playwright specs, so it walks what they import
// too.
func TestRehearsalPaths(t *testing.T) {
	const workflow = ".github/workflows/rehearsal.yml"
	patterns := workflowPaths(t, workflow)
	var binaries buildFiles
	for _, pkg := range []string{"cmd/pairtime", "cmd/pairtimed"} {
		binaries.add(sources(t, rehearsalTarget, pkg))
	}
	rehearsal := testSources(t, rehearsalTarget, []string{"desktop"}, "internal/rehearsal")
	capture := rehearsalConst(t, "captureDir")
	tests := []struct {
		name  string
		files []string
		// must are files the walk has to reach, so a walk that finds
		// nothing can't pass.
		must []string
	}{
		{
			name:  "a change to a Go file pairtime or pairtimed is built from runs the rehearsal, which builds both",
			files: binaries.goFiles,
			must:  []string{"cmd/pairtime/main.go", "cmd/pairtimed/init.go", "deploy/embed.go", "third_party/burntsushi-toml/decode.go"},
		},
		{
			name:  "a change to a file pairtime or pairtimed embeds, such as the template pairtimed init writes pairtime.toml from, runs the rehearsal",
			files: binaries.embedded,
			must:  []string{"deploy/pairtime.toml.tmpl", "docs/retention.json", "internal/server/pages/dashboard.html"},
		},
		{
			name:  "a change to the rehearsal's test binary, the stand-in claude, or to a file it embeds runs the rehearsal",
			files: slices.Concat(rehearsal.goFiles, rehearsal.embedded),
			must:  []string{rehearsalFile, "internal/rehearsal/doc.go", "internal/agent/ledger/writer.go", "pkg/ledger/batch.go"},
		},
		{
			name:  "a change to the Claude Code capture the stand-in claude replays runs the rehearsal",
			files: filesUnder(t, capture),
			must:  []string{path.Join(capture, "000001", "stdin")},
		},
		{
			name:  "a change to a live spec, or to a file one imports by a relative path, such as the style helpers a11y.live.ts reads looks with, runs the rehearsal, which runs the live specs",
			files: liveImports(t),
			must:  []string{"web/tests/live/a11y.live.ts", "web/tests/support/style.ts"},
		},
		{
			name:  "a change to a script the workflow runs, a directory it mounts into the mock OIDC server, the module's requirements or the workflow runs the rehearsal",
			files: slices.Concat(stepFiles(t, workflow), []string{"go.mod", "go.sum", workflow}),
			must:  []string{"tools/ci/desktop_session.sh", "internal/server/auth/testdata/mock-oauth2-server.json"},
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
					t.Errorf("%s is under none of %s's paths, so a PR that changes it doesn't run the rehearsal; add it there", f, workflow)
				}
			}
		})
	}
}

// relImport finds an import or export of a relative path in TypeScript,
// "import { x } from './y'", "import type { T } from '../z'" and
// "import './w'" alike, over lines if it must.
var relImport = regexp.MustCompile(`(?:^|[\s;{}])(?:import|export)\b[^'"]*?['"](\.{1,2}/[^'"]+)['"]`)

// liveImports returns web/tests/live's specs and every file they import by
// a relative path, and every file those import, read with os.ReadFile, so
// go test's cache keys on them.
func liveImports(t *testing.T) []string {
	t.Helper()
	specs, err := filepath.Glob(filepath.Join(repoRoot, "web", "tests", "live", "*.live.ts"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var out []string
	var walk func(file string)
	walk = func(file string) {
		if seen[file] {
			return
		}
		seen[file] = true
		out = append(out, file)
		b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(file)))
		if err != nil {
			// The case's os.Stat says which.
			return
		}
		for _, m := range relImport.FindAllStringSubmatch(string(b), -1) {
			walk(resolveTS(path.Join(path.Dir(file), m[1])))
		}
	}
	for _, spec := range specs {
		rel, err := filepath.Rel(repoRoot, spec)
		if err != nil {
			t.Fatal(err)
		}
		walk(filepath.ToSlash(rel))
	}
	slices.Sort(out)
	return out
}

// resolveTS returns the file an import's path names: the path itself, or
// with .ts or /index.ts after it, as TypeScript resolves one.
func resolveTS(p string) string {
	for _, c := range []string{p, p + ".ts", p + "/index.ts"} {
		if fi, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(c))); err == nil && !fi.IsDir() {
			return c
		}
	}
	return p
}

// rehearsalConst returns the value of the string constant name in the
// rehearsal's test file. The file builds only with -tags desktop, and in a
// test package, so this package reads its source instead of importing it.
func rehearsalConst(t *testing.T, name string) string {
	t.Helper()
	file := filepath.Join(repoRoot, filepath.FromSlash(rehearsalFile))
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range f.Decls {
		g, ok := decl.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, spec := range g.Specs {
			v := spec.(*ast.ValueSpec)
			for i, n := range v.Names {
				if n.Name != name {
					continue
				}
				var lit *ast.BasicLit
				if i < len(v.Values) {
					lit, _ = v.Values[i].(*ast.BasicLit)
				}
				if lit == nil || lit.Kind != token.STRING {
					t.Fatalf("%s's %s isn't a string literal", rehearsalFile, name)
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				return s
			}
		}
	}
	t.Fatalf("%s declares no constant %s", rehearsalFile, name)
	return ""
}

// filesUnder returns the files under dir, a directory in the repository,
// as paths from the repository's root.
func filesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := fs.WalkDir(os.DirFS(repoRoot), dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("%s holds no files", dir)
	}
	return files
}

// stepFiles returns the files in the repository that the steps of the
// workflow name outside comments: the scripts under tools/ they run, and
// the files under each directory they mount into a container with
// docker run -v "$PWD/<dir>:...".
func stepFiles(t *testing.T, workflow string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(workflow)))
	if err != nil {
		t.Fatal(err)
	}
	_, jobs, ok := strings.Cut(string(b), "\njobs:\n")
	if !ok {
		t.Fatalf("%s has no jobs", workflow)
	}
	script := regexp.MustCompile(`\btools/[\w./-]+\.(?:sh|py)\b`)
	mount := regexp.MustCompile(`"\$PWD/([^":]+):`)
	var files []string
	for _, line := range strings.Split(jobs, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		files = append(files, script.FindAllString(line, -1)...)
		for _, m := range mount.FindAllStringSubmatch(line, -1) {
			files = append(files, filesUnder(t, m[1])...)
		}
	}
	return sortedUnique(files)
}
