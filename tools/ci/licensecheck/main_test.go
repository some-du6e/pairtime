// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	read := func(name string) string {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	mit := read("testdata/spdx-MIT.txt")
	apache := read("testdata/spdx-Apache-2.0.txt")
	gpl := read("../../../testdata/licensecheck/gpl/LICENSE")

	// Each case lays out files under a fresh directory and lists packages
	// whose Dir and Module paths are relative to it.
	tests := []struct {
		name  string
		files map[string]string
		pkgs  []goPackage
		want  []verdict
	}{
		{
			name: "standard library and main-module packages are skipped",
			pkgs: []goPackage{
				{ImportPath: "fmt", Standard: true},
				{ImportPath: "example.com/me", Dir: "me", Module: &goModule{Path: "example.com/me", Dir: "me", Main: true}},
			},
			want: []verdict{},
		},
		{
			name:  "a module whose root LICENSE is MIT passes",
			files: map[string]string{"dep/LICENSE": mit},
			pkgs:  []goPackage{{ImportPath: "example.com/dep", Dir: "dep", Module: &goModule{Path: "example.com/dep", Version: "v1.0.0", Dir: "dep"}}},
			want:  []verdict{{module: "example.com/dep@v1.0.0", licenses: []string{"MIT"}}},
		},
		{
			name:  "a module without a license file fails",
			files: map[string]string{"dep/README.md": "no license here"},
			pkgs:  []goPackage{{ImportPath: "example.com/dep", Dir: "dep", Module: &goModule{Path: "example.com/dep", Version: "v1.0.0", Dir: "dep"}}},
			want:  []verdict{{module: "example.com/dep@v1.0.0", problems: []string{"no license file at the module root, so its license is unknown"}}},
		},
		{
			name:  "a GPL module fails, naming the file",
			files: map[string]string{"dep/COPYING": gpl},
			pkgs:  []goPackage{{ImportPath: "example.com/dep", Dir: "dep", Module: &goModule{Path: "example.com/dep", Version: "v1.0.0", Dir: "dep"}}},
			want: []verdict{{module: "example.com/dep@v1.0.0", problems: []string{
				`COPYING: it mentions "general public license", so it isn't only MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0 or ISC`,
			}}},
		},
		{
			name:  "a replaced module is read from its replacement",
			files: map[string]string{"orig/LICENSE": mit, "fork/LICENSE": gpl},
			pkgs: []goPackage{{ImportPath: "example.com/dep", Dir: "fork", Module: &goModule{
				Path: "example.com/dep", Version: "v1.0.0", Dir: "fork",
				Replace: &goModule{Path: "../fork", Dir: "fork"},
			}}},
			want: []verdict{{module: "example.com/dep@v1.0.0 => ../fork", problems: []string{
				`LICENSE: it mentions "general public license", so it isn't only MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0 or ISC`,
			}}},
		},
		{
			name: "a GPL license in a package directory below an MIT root fails",
			files: map[string]string{
				"dep/LICENSE":             mit,
				"dep/third_party/LICENSE": gpl,
				"dep/third_party/x/x.go":  "package x",
			},
			pkgs: []goPackage{{ImportPath: "example.com/dep/third_party/x", Dir: "dep/third_party/x", Module: &goModule{Path: "example.com/dep", Version: "v1.0.0", Dir: "dep"}}},
			want: []verdict{{module: "example.com/dep@v1.0.0", licenses: []string{"MIT"}, problems: []string{
				`third_party/LICENSE: it mentions "general public license", so it isn't only MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0 or ISC`,
			}}},
		},
		{
			name: "license files outside the module are ignored",
			files: map[string]string{
				"LICENSE":     gpl,
				"dep/LICENSE": mit,
			},
			pkgs: []goPackage{{ImportPath: "example.com/dep", Dir: "dep", Module: &goModule{Path: "example.com/dep", Version: "v1.0.0", Dir: "dep"}}},
			want: []verdict{{module: "example.com/dep@v1.0.0", licenses: []string{"MIT"}}},
		},
		{
			name: "every license file at the root must be allowed",
			files: map[string]string{
				"dep/LICENSE":        mit,
				"dep/COPYING.txt":    gpl,
				"dep/LICENSE-3RD.md": "not a license file name, so not read",
			},
			pkgs: []goPackage{{ImportPath: "example.com/dep", Dir: "dep", Module: &goModule{Path: "example.com/dep", Version: "v1.0.0", Dir: "dep"}}},
			want: []verdict{{module: "example.com/dep@v1.0.0", licenses: []string{"MIT"}, problems: []string{
				`COPYING.txt: it mentions "general public license", so it isn't only MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0 or ISC`,
			}}},
		},
		{
			name: "a dual MIT and Apache-2.0 module passes",
			files: map[string]string{
				"dep/LICENSE-MIT":    mit,
				"dep/LICENSE-APACHE": apache,
			},
			pkgs: []goPackage{{ImportPath: "example.com/dep", Dir: "dep", Module: &goModule{Path: "example.com/dep", Version: "v1.0.0", Dir: "dep"}}},
			want: []verdict{{module: "example.com/dep@v1.0.0", licenses: []string{"Apache-2.0", "MIT"}}},
		},
		{
			name:  "packages listed for several OSes count once",
			files: map[string]string{"dep/LICENSE": mit, "dep/sub/s.go": "package sub"},
			pkgs: []goPackage{
				{ImportPath: "example.com/dep", Dir: "dep", Module: &goModule{Path: "example.com/dep", Version: "v1.0.0", Dir: "dep"}},
				{ImportPath: "example.com/dep/sub", Dir: "dep/sub", Module: &goModule{Path: "example.com/dep", Version: "v1.0.0", Dir: "dep"}},
				{ImportPath: "example.com/dep", Dir: "dep", Module: &goModule{Path: "example.com/dep", Version: "v1.0.0", Dir: "dep"}},
			},
			want: []verdict{{module: "example.com/dep@v1.0.0", licenses: []string{"MIT"}}},
		},
		{
			name:  "two versions of a module are judged apart",
			files: map[string]string{"v1/LICENSE": mit, "v2/LICENSE": gpl},
			pkgs: []goPackage{
				{ImportPath: "example.com/dep", Dir: "v1", Module: &goModule{Path: "example.com/dep", Version: "v1.0.0", Dir: "v1"}},
				{ImportPath: "example.com/dep", Dir: "v2", Module: &goModule{Path: "example.com/dep", Version: "v1.1.0", Dir: "v2"}},
			},
			want: []verdict{
				{module: "example.com/dep@v1.0.0", licenses: []string{"MIT"}},
				{module: "example.com/dep@v1.1.0", problems: []string{
					`LICENSE: it mentions "general public license", so it isn't only MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0 or ISC`,
				}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for name, body := range tt.files {
				p := filepath.Join(root, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			abs := func(rel string) string {
				if rel == "" {
					return ""
				}
				return filepath.Join(root, filepath.FromSlash(rel))
			}
			for i := range tt.pkgs {
				p := &tt.pkgs[i]
				p.Dir = abs(p.Dir)
				if m := p.Module; m != nil {
					m.Dir = abs(m.Dir)
					if m.Replace != nil {
						m.Replace.Dir = abs(m.Replace.Dir)
					}
				}
			}
			got := check(tt.pkgs)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("check() =\n%s\nwant\n%s", show(got), show(tt.want))
			}
		})
	}
}

func show(vs []verdict) string {
	var b strings.Builder
	for _, v := range vs {
		b.WriteString("  " + v.module + " licenses=" + strings.Join(v.licenses, ",") + "\n")
		for _, p := range v.problems {
			b.WriteString("    problem: " + p + "\n")
		}
	}
	return b.String()
}
