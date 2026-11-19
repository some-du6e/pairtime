// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"strings"
	"testing"
)

// The expected identifiers come from outside this package: the SPDX License
// List's own texts (spdx-*, from github.com/spdx/license-list-data), Debian's
// copies in /usr/share/common-licenses (debian-*), and the LICENSE files of
// modules on DESIGN 3.12's list and their dependencies (real-*), with the
// identifier GitHub's license detection gives each repository. GitHub gives
// none for go-humanize because of the URL after its MIT text; pkg.go.dev
// calls it MIT.
func TestIdentify(t *testing.T) {
	tests := []struct {
		name string
		file string
		want string // "" means not allowed
	}{
		{name: "SPDX MIT text is MIT", file: "testdata/spdx-MIT.txt", want: "MIT"},
		{name: "SPDX ISC text is ISC", file: "testdata/spdx-ISC.txt", want: "ISC"},
		{name: "SPDX BSD-2-Clause text is BSD-2-Clause", file: "testdata/spdx-BSD-2-Clause.txt", want: "BSD-2-Clause"},
		{name: "SPDX BSD-3-Clause text is BSD-3-Clause", file: "testdata/spdx-BSD-3-Clause.txt", want: "BSD-3-Clause"},
		{name: "SPDX Apache-2.0 text is Apache-2.0", file: "testdata/spdx-Apache-2.0.txt", want: "Apache-2.0"},
		{name: "golang.org/x/sys is BSD-3-Clause", file: "testdata/real-x-sys.txt", want: "BSD-3-Clause"},
		{name: "purego is Apache-2.0, with an unfilled appendix in braces", file: "testdata/real-purego.txt", want: "Apache-2.0"},
		{name: "godbus is BSD-2-Clause", file: "testdata/real-godbus-dbus.txt", want: "BSD-2-Clause"},
		{name: "fsnotify is BSD-3-Clause, with two copyright lines using the symbol", file: "testdata/real-fsnotify.txt", want: "BSD-3-Clause"},
		{name: "go-keyring is MIT with a title before the copyright", file: "testdata/real-go-keyring.txt", want: "MIT"},
		{name: "systray is Apache-2.0, with a filled-in appendix", file: "testdata/real-systray.txt", want: "Apache-2.0"},
		{name: "wincred is MIT", file: "testdata/real-wincred.txt", want: "MIT"},
		{name: "go-humanize is MIT followed by its URL", file: "testdata/real-go-humanize.txt", want: "MIT"},
		{name: "google/uuid is BSD-3-Clause", file: "testdata/real-google-uuid.txt", want: "BSD-3-Clause"},
		{name: "go-isatty is MIT with the title after the copyright", file: "testdata/real-go-isatty.txt", want: "MIT"},
		{name: "GPL-3.0 is not allowed", file: "../../../testdata/licensecheck/gpl/LICENSE"},
		{name: "AGPL-3.0, pairtime's own license, is not allowed for a dependency", file: "../../../LICENSE"},
		{name: "LGPL-3.0 is not allowed", file: "testdata/debian-LGPL-3.txt"},
		{name: "MPL-2.0 is not allowed", file: "testdata/debian-MPL-2.0.txt"},
		{name: "BSD-4-Clause, with its advertising clause, is not allowed", file: "testdata/spdx-BSD-4-Clause.txt"},
		{name: "the JSON license, MIT plus a use restriction, is not allowed", file: "testdata/spdx-JSON.txt"},
		{name: "0BSD is permissive but not on the list", file: "testdata/spdx-0BSD.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(tt.file)
			if err != nil {
				t.Fatal(err)
			}
			got, reason := identify(string(data))
			if got != tt.want {
				t.Errorf("identify(%s) = %q (%s), want %q", tt.file, got, reason, tt.want)
			}
			if got == "" && reason == "" {
				t.Errorf("identify(%s) gave no reason for rejecting it", tt.file)
			}
		})
	}
}

// TestIdentifyVariants edits the SPDX texts the way real copies differ, and
// the way a restricted license would.
func TestIdentifyVariants(t *testing.T) {
	read := func(name string) string {
		data, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	mit := read("spdx-MIT.txt")
	isc := read("spdx-ISC.txt")
	apache := read("spdx-Apache-2.0.txt")
	bsd3 := read("spdx-BSD-3-Clause.txt")
	gpl, err := os.ReadFile("../../../testdata/licensecheck/gpl/LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	mustReplace := func(s, old, new string) string {
		if !strings.Contains(s, old) {
			t.Fatalf("test text lacks %q", old)
		}
		return strings.Replace(s, old, new, 1)
	}
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "CRLF line endings don't matter", text: strings.ReplaceAll(mit, "\n", "\r\n"), want: "MIT"},
		{name: "reflowed lines don't matter", text: strings.Join(strings.Fields(mit), "\n"), want: "MIT"},
		{name: "case doesn't matter", text: strings.ToLower(mit), want: "MIT"},
		{name: "curly quotes count as straight ones", text: strings.NewReplacer(`"Software"`, "\u201cSoftware\u201d", `"AS IS"`, "\u201cAS IS\u201d").Replace(mit), want: "MIT"},
		{name: "a byte order mark is ignored", text: "\ufeff" + mit, want: "MIT"},
		{name: "several copyright lines are fine", text: mustReplace(mit, "Copyright (c) <year> <copyright holders>", "Copyright (c) 2014 A\nCopyright (c) 2015 B\nCopyright (c) 2016 C"), want: "MIT"},
		{name: "the old ISC wording without and/or is ISC", text: mustReplace(isc, "and/or distribute", "and distribute"), want: "ISC"},
		{name: "Apache-2.0 without its appendix is Apache-2.0", text: apache[:strings.Index(apache, "APPENDIX:")], want: "Apache-2.0"},
		{name: "https instead of http doesn't matter", text: strings.ReplaceAll(apache, "http://", "https://"), want: "Apache-2.0"},
		{name: "BSD-3-Clause naming a company is BSD-3-Clause", text: mustReplace(bsd3, "Neither the name of the copyright holder nor", "Neither the name of Example Corp. nor"), want: "BSD-3-Clause"},
		{name: "a restriction added to MIT fails", text: mustReplace(mit, "following conditions:", "following conditions:\n\nThe Software may not be used for commercial purposes.\n")},
		{name: "text after MIT fails", text: mit + "\nExcept for the files in vendor/, which are proprietary.\n"},
		{name: "text before Apache-2.0 fails", text: "Parts of this module may not be redistributed.\n\n" + apache},
		{name: "the Commons Clause after Apache-2.0 fails", text: apache + "\n\"Commons Clause\" License Condition v1.0\n\nThe Software is provided to you by the Licensor under the License, as defined below, subject to the following condition.\n"},
		{name: "a GPL mention in the copyright lines fails", text: mustReplace(mit, "Copyright (c) <year> <copyright holders>", "Copyright (c) 2016 A. Some files are under the GNU General Public License."), want: ""},
		{name: "BSD-3-Clause without its third clause is BSD-2-Clause", text: bsd3[:strings.Index(bsd3, "3. Neither")] + bsd3[strings.Index(bsd3, "THIS SOFTWARE"):], want: "BSD-2-Clause"},
		{name: "MIT and GPL texts in one file fail", text: mit + "\n" + string(gpl)},
		{name: "an empty file fails", text: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := identify(tt.text)
			if got != tt.want {
				t.Errorf("identify() = %q (%s), want %q", got, reason, tt.want)
			}
		})
	}
}

// TestTemplatesCompile checks each allowed license has a template that
// matches its own SPDX text and no other license's.
func TestTemplatesCompile(t *testing.T) {
	if len(templates) != len(Allowed) {
		t.Fatalf("%d templates for %d allowed licenses", len(templates), len(Allowed))
	}
	for _, tmpl := range templates {
		t.Run(tmpl.id+" matches only its own text", func(t *testing.T) {
			for _, id := range Allowed {
				data, err := os.ReadFile("testdata/spdx-" + id + ".txt")
				if err != nil {
					t.Fatal(err)
				}
				if got, want := tmpl.re.MatchString(normalize(string(data))), id == tmpl.id; got != want {
					t.Errorf("template %s matching the %s text = %v, want %v", tmpl.id, id, got, want)
				}
			}
		})
	}
}

func TestCompileTemplateErrors(t *testing.T) {
	tests := []struct {
		name     string
		template string
	}{
		{name: "an unclosed optional part is an error", template: "a <<begin>> b"},
		{name: "an unopened optional part is an error", template: "a <<end>> b"},
		{name: "a bad variable regexp is an error", template: "a <<var:(>> b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := compileTemplate(tt.template); err == nil {
				t.Errorf("compileTemplate(%q) succeeded, want an error", tt.template)
			}
		})
	}
}
