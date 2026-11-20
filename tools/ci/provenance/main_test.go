// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// good is a complete provenance file.
const good = `---
harness: claude_code
harness_version: 2.0.14
os: Windows 11 23H2, build 22631
captured_on: 2026-11-21
captured_by: Karim
synthetic: false
scrubbed_by: tools/capture/scrub.sh, every replacement read by eye
---

Captured by following tools/capture/SCRIPTS.md.
`

// without returns good with the line for key removed.
func without(key string) string {
	var out []string
	for _, line := range strings.Split(good, "\n") {
		if !strings.HasPrefix(line, key+":") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// with returns good with the value of key replaced.
func with(key, value string) string {
	var out []string
	for _, line := range strings.Split(good, "\n") {
		if strings.HasPrefix(line, key+":") {
			line = key + ": " + value
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

// moved returns text, a variant of good, for another harness and version.
func moved(text, harness, version string) string {
	return strings.NewReplacer("harness: claude_code", "harness: "+harness, "harness_version: 2.0.14", "harness_version: "+version).Replace(text)
}

func TestCheckTree(t *testing.T) {
	const dir = "claude_code/2.0.14/windows"
	const stray = "not inside a <harness>/<version>/<os>/ fixture directory, so no PROVENANCE.md covers it"
	tests := []struct {
		name     string
		files    fstest.MapFS
		want     []problem
		wantDirs int
	}{
		{
			name:  "an empty fixtures tree passes",
			files: fstest.MapFS{},
		},
		{
			name: "README notes and the root's template above the os level pass",
			files: fstest.MapFS{
				"README.md":                    file("how fixtures are laid out"),
				"PROVENANCE.template.md":       file("---\nharness: <harness>\n---\n"),
				"claude_code/README.md":        file("notes"),
				"claude_code/2.0.14/README.md": file("notes on this version"),
			},
		},
		{
			name: "a file above the os level fails",
			files: fstest.MapFS{
				"claude_code/2.0.14/x.txt": file("a stray file is not a fixture directory"),
			},
			want: []problem{{path: "claude_code/2.0.14/x.txt", msg: stray}},
		},
		{
			name: "a capture saved without its os level fails, even beside a fixture directory",
			files: fstest.MapFS{
				dir + "/PROVENANCE.md":                file(good),
				"claude_code/2.0.14/transcript.jsonl": file("{}"),
			},
			want:     []problem{{path: "claude_code/2.0.14/transcript.jsonl", msg: stray}},
			wantDirs: 1,
		},
		{
			name: "files directly under the root or a harness directory fail",
			files: fstest.MapFS{
				"notes.txt":             file("not a README"),
				"codex/rollout-1.jsonl": file("{}"),
			},
			want: []problem{
				{path: "codex/rollout-1.jsonl", msg: stray},
				{path: "notes.txt", msg: stray},
			},
		},
		{
			name: "the provenance template is a note only at the root",
			files: fstest.MapFS{
				"claude_code/PROVENANCE.template.md": file("---\nharness: <harness>\n---\n"),
			},
			want: []problem{{path: "claude_code/PROVENANCE.template.md", msg: stray}},
		},
		{
			name: "a PROVENANCE.md above the os level covers nothing",
			files: fstest.MapFS{
				"claude_code/2.0.14/PROVENANCE.md":  file(good),
				"claude_code/2.0.14/hooks/001.json": file("{}"),
			},
			want: []problem{
				{path: "claude_code/2.0.14/PROVENANCE.md", msg: stray},
				{path: "claude_code/2.0.14/hooks", msg: badOS},
			},
		},
		{
			name: "a run directory saved without its os level fails, even with a PROVENANCE.md in it",
			files: fstest.MapFS{
				"claude_code/2.1.328/cli/PROVENANCE.md":      file(moved(good, "claude_code", "2.1.328")),
				"claude_code/2.1.328/cli/hooks/000001/stdin": file("{}"),
			},
			want: []problem{{path: "claude_code/2.1.328/cli", msg: badOS}},
		},
		{
			name: "an os directory takes the README's name for macOS, not Go's",
			files: fstest.MapFS{
				"claude_code/2.0.14/darwin/PROVENANCE.md": file(good),
			},
			want: []problem{{path: "claude_code/2.0.14/darwin", msg: badOS}},
		},
		{
			name: "an os directory name is lower case",
			files: fstest.MapFS{
				"claude_code/2.0.14/Windows/PROVENANCE.md": file(good),
			},
			want: []problem{{path: "claude_code/2.0.14/Windows", msg: badOS}},
		},
		{
			name: "the front matter's harness must be the directory's",
			files: fstest.MapFS{
				"codex/2.0.14/linux/PROVENANCE.md": file(good),
			},
			want:     []problem{{path: "codex/2.0.14/linux/PROVENANCE.md", msg: `harness is "claude_code", but the fixture directory says "codex"`}},
			wantDirs: 1,
		},
		{
			name: "the front matter's harness_version must be the directory's",
			files: fstest.MapFS{
				"claude_code/2.0.15/macos/PROVENANCE.md": file(good),
			},
			want:     []problem{{path: "claude_code/2.0.15/macos/PROVENANCE.md", msg: `harness_version is "2.0.14", but the fixture directory says "2.0.15"`}},
			wantDirs: 1,
		},
		{
			name: "a symbolic link in place of an os directory fails",
			files: fstest.MapFS{
				"claude_code/2.0.13/windows/PROVENANCE.md": file(moved(good, "claude_code", "2.0.13")),
				dir: &fstest.MapFile{Mode: fs.ModeSymlink, Data: []byte("../2.0.13/windows")},
			},
			want:     []problem{{path: dir, msg: stray}},
			wantDirs: 1,
		},
		{
			name: "a fixture directory with complete provenance passes",
			files: fstest.MapFS{
				dir + "/PROVENANCE.md":      file(good),
				dir + "/hooks/001.json":     file("{}"),
				dir + "/transcript.jsonl":   file("{}"),
				dir + "/deeper/still/x.txt": file("covered by the provenance above"),
			},
			wantDirs: 1,
		},
		{
			name: "a fixture directory without PROVENANCE.md fails",
			files: fstest.MapFS{
				dir + "/hooks/001.json": file("{}"),
			},
			want:     []problem{{path: dir, msg: "fixture directory has no PROVENANCE.md"}},
			wantDirs: 1,
		},
		{
			name: "each fixture directory is checked on its own",
			files: fstest.MapFS{
				dir + "/PROVENANCE.md":             file(good),
				"claude_code/2.0.14/macos/x.json":  file("{}"),
				"codex/0.63.0/linux/PROVENANCE.md": file(moved(without("os"), "codex", "0.63.0")),
			},
			want: []problem{
				{path: "claude_code/2.0.14/macos", msg: "fixture directory has no PROVENANCE.md"},
				{path: "codex/0.63.0/linux/PROVENANCE.md", msg: "front matter has no os field"},
			},
			wantDirs: 3,
		},
		{
			name: "a PROVENANCE.md in a subdirectory doesn't count for the fixture directory",
			files: fstest.MapFS{
				dir + "/hooks/PROVENANCE.md": file(good),
			},
			want:     []problem{{path: dir, msg: "fixture directory has no PROVENANCE.md"}},
			wantDirs: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, dirs, err := checkTree(tt.files)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("problems = %q, want %q", got, tt.want)
			}
			if dirs != tt.wantDirs {
				t.Errorf("checked %d fixture directories, want %d", dirs, tt.wantDirs)
			}
		})
	}
}

func TestCheckFile(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{name: "complete front matter passes", text: good},
		{name: "synthetic may be true", text: with("synthetic", "true")},
		{name: "quoted values are accepted", text: with("harness_version", `"2.0.14"`)},
		{name: "extra fields are allowed", text: strings.Replace(good, "---\n\n", "notes: second capture\n---\n\n", 1)},
		{name: "CRLF line endings are accepted", text: strings.ReplaceAll(good, "\n", "\r\n")},
		{name: "a byte order mark is accepted", text: "\ufeff" + good},
		{name: "blank lines and comments in front matter are skipped", text: strings.Replace(good, "harness:", "\n# who and what\nharness:", 1)},
		{name: "missing harness fails", text: without("harness"), want: []string{"front matter has no harness field"}},
		{name: "missing harness_version fails", text: without("harness_version"), want: []string{"front matter has no harness_version field"}},
		{name: "missing os fails", text: without("os"), want: []string{"front matter has no os field"}},
		{name: "missing captured_on fails", text: without("captured_on"), want: []string{"front matter has no captured_on field"}},
		{name: "missing captured_by fails", text: without("captured_by"), want: []string{"front matter has no captured_by field"}},
		{name: "missing synthetic fails", text: without("synthetic"), want: []string{"front matter has no synthetic field"}},
		{name: "missing scrubbed_by fails", text: without("scrubbed_by"), want: []string{"front matter has no scrubbed_by field"}},
		{name: "an empty value fails", text: with("captured_by", ""), want: []string{"field captured_by is empty"}},
		{name: "an empty quoted value fails", text: with("scrubbed_by", `""`), want: []string{"field scrubbed_by is empty"}},
		{name: "synthetic must be true or false", text: with("synthetic", "no"), want: []string{`synthetic is "no", want true or false`}},
		{name: "captured_on must be a date", text: with("captured_on", "last tuesday"), want: []string{`captured_on is "last tuesday", want a date like 2026-11-21`}},
		{name: "captured_on must be a real day", text: with("captured_on", "2026-02-30"), want: []string{`captured_on is "2026-02-30", want a date like 2026-11-21`}},
		{
			name: "every missing field is reported",
			text: "---\nharness: claude_code\n---\n",
			want: []string{
				"front matter has no harness_version field",
				"front matter has no os field",
				"front matter has no captured_on field",
				"front matter has no captured_by field",
				"front matter has no synthetic field",
				"front matter has no scrubbed_by field",
			},
		},
		{
			name: "front matter must open the file",
			text: "# Provenance\n\n" + good,
			want: []string{"file must open with front matter: a --- line, the fields as key: value lines, and another --- line"},
		},
		{
			name: "fields in the body don't count",
			text: "---\n---\n" + strings.TrimPrefix(good, "---\n"),
			want: []string{
				"front matter has no harness field",
				"front matter has no harness_version field",
				"front matter has no os field",
				"front matter has no captured_on field",
				"front matter has no captured_by field",
				"front matter has no synthetic field",
				"front matter has no scrubbed_by field",
			},
		},
		{
			name: "front matter must be closed",
			text: strings.TrimSuffix(strings.SplitAfter(good, "scrubbed_by: tools/capture/scrub.sh, every replacement read by eye\n")[0], "\n"),
			want: []string{"front matter has no closing --- line"},
		},
		{
			name: "a field may be set only once",
			text: strings.Replace(good, "os:", "harness: codex\nos:", 1),
			want: []string{"front matter line 4 sets harness a second time"},
		},
		{
			name: "front matter lines must be key: value",
			text: strings.Replace(good, "os:", "just some words\nos:", 1),
			want: []string{`front matter line 4 is "just some words", want key: value`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkFile([]byte(tt.text), "")
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("checkFile() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRun(t *testing.T) {
	root := t.TempDir()
	fixtures := filepath.Join(root, "fixtures")
	gooddir := filepath.Join(fixtures, "claude_code", "2.0.14", "linux")
	baddir := filepath.Join(fixtures, "codex", "0.63.0", "linux")
	for _, d := range []string{gooddir, baddir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(gooddir, FileName), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	template := filepath.Join(root, "PROVENANCE.template.md")
	if err := os.WriteFile(template, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
	}{
		{name: "a fixtures tree that doesn't exist yet passes", args: []string{filepath.Join(root, "missing")}, wantCode: 0, wantStdout: "no fixtures to check"},
		{name: "a single provenance file can be checked", args: []string{template}, wantCode: 0},
		{name: "a tree with a fixture directory lacking provenance fails", args: []string{fixtures}, wantCode: 1, wantStdout: "::error file=" + filepath.ToSlash(baddir) + "::fixture directory has no PROVENANCE.md"},
		{name: "flags are a usage error", args: []string{"-v"}, wantCode: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d; stdout:\n%s\nstderr:\n%s", code, tt.wantCode, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
		})
	}
}
