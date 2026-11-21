// SPDX-License-Identifier: AGPL-3.0-or-later

package ci

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// imagePaths returns restore_drill.sh's image_paths, the paths that make a
// pull request's restore drill build its image from source, and
// server-image.yml's push paths, the paths a push to main builds a new
// server image for, the workflow itself left out. Both name what goes into
// the server image, in git's pathspec form: server-image.yml's dir/** is
// the script's dir.
func imagePaths(t *testing.T) (drill, built []string) {
	t.Helper()
	script, err := os.ReadFile("restore_drill.sh")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)\nimage_paths=\(\n(.*?)\n\)\n`).FindSubmatch(script)
	if m == nil {
		t.Fatal("restore_drill.sh has no image_paths=( ... ) list")
	}
	for _, l := range strings.Split(string(m[1]), "\n") {
		if p := strings.Trim(strings.TrimSpace(l), "'"); p != "" {
			drill = append(drill, p)
		}
	}
	workflow, err := os.ReadFile("../../.github/workflows/server-image.yml")
	if err != nil {
		t.Fatal(err)
	}
	m = regexp.MustCompile(`(?s)\n  push:\n.*?\n    paths:\n(.*?)\n  workflow_dispatch:`).FindSubmatch(workflow)
	if m == nil {
		t.Fatal("server-image.yml has no push paths followed by workflow_dispatch")
	}
	for _, l := range strings.Split(string(m[1]), "\n") {
		p, ok := strings.CutPrefix(strings.TrimSpace(l), "- ")
		if !ok || p == ".github/workflows/server-image.yml" {
			continue
		}
		built = append(built, strings.TrimSuffix(p, "/**"))
	}
	return drill, built
}

// TestDrillImagePaths checks that restore-drill.yml's choice of image and
// server-image.yml's builds agree on what goes into the server image: a
// path only the drill names would drill a source build that no image on
// main ever holds, and a path only server-image.yml names would drill the
// pinned image on a PR that changes the image.
func TestDrillImagePaths(t *testing.T) {
	drill, built := imagePaths(t)
	if len(drill) == 0 || len(built) == 0 {
		t.Fatalf("restore_drill.sh names %d paths and server-image.yml %d", len(drill), len(built))
	}
	var tests []struct {
		name string
		path string
	}
	for _, p := range slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(drill), built...)))) {
		tests = append(tests, struct {
			name string
			path string
		}{name: p + " is in both restore_drill.sh's image_paths and server-image.yml's push paths", path: p})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !slices.Contains(drill, tt.path) {
				t.Errorf("restore_drill.sh's image_paths lacks %s", tt.path)
			}
			if !slices.Contains(built, tt.path) {
				t.Errorf("server-image.yml's push paths lack %s", tt.path)
			}
		})
	}
}
