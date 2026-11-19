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
	bsd2 := read("spdx-BSD-2-Clause.txt")
	bsd3 := read("spdx-BSD-3-Clause.txt")
	gpl, err := os.ReadFile("../../../testdata/licensecheck/gpl/LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	// CodeRabbit's example of a condition that uses none of conditionRE's
	// words.
	const france = "Redistribution outside France constitutes copyright infringement."
	mustReplace := func(s, old, new string) string {
		if !strings.Contains(s, old) {
			t.Fatalf("test text lacks %q", old)
		}
		return strings.Replace(s, old, new, 1)
	}
	// reflow puts each word from the first from onward on its own line.
	reflow := func(s, from string) string {
		i := strings.Index(s, from)
		if i < 0 {
			t.Fatalf("test text lacks %q", from)
		}
		return s[:i] + strings.Join(strings.Fields(s[i:]), "\n")
	}
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "CRLF line endings don't matter", text: strings.ReplaceAll(mit, "\n", "\r\n"), want: "MIT"},
		{name: "reflowed lines in the license text don't matter", text: reflow(mit, "Permission is hereby"), want: "MIT"},
		{name: "a copyright line split in two fails, since only notices and titles may stand above the text", text: mustReplace(mit, "Copyright (c) <year> <copyright holders>", "Copyright (c) <year>\n<copyright holders>")},
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
		{name: "a restriction after MIT's copyright line fails", text: mustReplace(mit, "Copyright (c) <year> <copyright holders>", "Copyright (c) <year> <copyright holders>\n\nRedistribution is prohibited without written permission.")},
		{name: "a restriction after ISC's copyright line fails", text: mustReplace(isc, "Copyright <year> <owner>", "Copyright <year> <owner>\nCommercial use is prohibited.")},
		{name: "a restriction after BSD-2-Clause's copyright line fails", text: mustReplace(bsd2, "Copyright (c) <year> <owner>", "Copyright (c) <year> <owner>\nRedistribution is prohibited without written permission.")},
		{name: "a restriction after BSD-3-Clause's copyright line fails", text: mustReplace(bsd3, "Copyright (c) <year> <owner>.", "Copyright (c) <year> <owner>.\nThis software may not be sold.")},
		{name: "a restriction in the holder's name fails", text: mustReplace(mit, "EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS", "EVENT SHALL THE AUTHORS, WHO FORBID COMMERCIAL USE,")},
		{name: "a restriction in place of a bullet fails", text: mustReplace(bsd2, "1. Redistributions of source", "No sale. Redistributions of source")},
		{name: "a restriction in Apache-2.0's appendix fails", text: mustReplace(apache, "Copyright [yyyy] [name of copyright owner]", "Copyright 2024 Foo Inc. Not for commercial use.")},
		{name: "a sentence on its own line under MIT's copyright fails", text: mustReplace(mit, "Copyright (c) <year> <copyright holders>", "Copyright (c) <year> <copyright holders>\n\n"+france)},
		{name: "a sentence on its own line under ISC's copyright fails", text: mustReplace(isc, "Copyright <year> <owner>", "Copyright <year> <owner>\n"+france)},
		{name: "a sentence on its own line under BSD-2-Clause's copyright fails", text: mustReplace(bsd2, "Copyright (c) <year> <owner>", "Copyright (c) <year> <owner>\n"+france)},
		{name: "a sentence on its own line under BSD-3-Clause's copyright fails", text: mustReplace(bsd3, "Copyright (c) <year> <owner>.", "Copyright (c) <year> <owner>.\n"+france)},
		{name: "a sentence above the copyright line fails", text: mustReplace(bsd2, "Copyright (c) <year> <owner>", france+"\nCopyright (c) <year> <owner>")},
		{name: "a title and a rule above the copyright line are fine", text: mustReplace(bsd3, "Copyright (c) <year> <owner>.", "BSD 3-Clause License\n====================\n\nCopyright (c) 2020, Example Corp.\nAll rights reserved."), want: "BSD-3-Clause"},
		{name: "a title on the copyright line is fine", text: mustReplace(mit, "MIT License\n\nCopyright (c) <year> <copyright holders>", "MIT License Copyright (c) 2020 Example Corp."), want: "MIT"},
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
		{name: "a bad text regexp is an error", template: "a <<text:(>> b"},
		{name: "two heads are an error", template: "<<head:.*>> a <<head:.*>> b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := compileTemplate(tt.template); err == nil {
				t.Errorf("compileTemplate(%q) succeeded, want an error", tt.template)
			}
		})
	}
}

func TestHeadLineOK(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{name: "a copyright line is fine", line: "Copyright (c) 2016 Zalando SE", want: true},
		{name: "a line starting with (c) is fine", line: "(c) 2016 Example Corp.", want: true},
		{name: "a Markdown bullet before Copyright is fine", line: "* Copyright 2009 The Go Authors.", want: true},
		{name: "all rights reserved is fine", line: "All rights reserved.", want: true},
		{name: "a license title is fine", line: "The MIT License (MIT)", want: true},
		{name: "a title naming the variant is fine", line: "BSD 2-Clause 'Simplified' License", want: true},
		{name: "a rule of punctuation is fine", line: "====================", want: true},
		{name: "a sentence fails", line: "Redistribution is limited to schools.", want: false},
		{name: "a sentence that mentions copyright fails", line: "Redistribution outside France constitutes copyright infringement.", want: false},
		{name: "a title with other words fails", line: "The MIT License applies only in France", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := headLineOK(tt.line); got != tt.want {
				t.Errorf("headLineOK(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestHeadLines(t *testing.T) {
	tests := []struct {
		name     string
		original string
		head     string
		want     []string
	}{
		{
			name:     "each line of the head comes back normalized",
			original: "Copyright \u00a9 2012 The Go Authors.\nCopyright \u00a9 fsnotify Authors.\n\nRedistribution and use...",
			head:     "Copyright (c) 2012 The Go Authors. Copyright (c) fsnotify Authors.",
			want:     []string{"Copyright (c) 2012 The Go Authors.", "Copyright (c) fsnotify Authors."},
		},
		{
			name:     "lines before the head are skipped",
			original: "MIT License\n\nCopyright (c) 2020 A\n\nPermission is hereby granted...",
			head:     "Copyright (c) 2020 A",
			want:     []string{"Copyright (c) 2020 A"},
		},
		{
			name:     "a head that ends mid-line keeps only its part",
			original: "Copyright (c) 2020 A Permission is hereby granted...",
			head:     "Copyright (c) 2020 A",
			want:     []string{"Copyright (c) 2020 A"},
		},
		{
			name:     "lines that don't add up give the head as one line",
			original: "Copyright (c) 2020 A\nsomething else",
			head:     "Copyright (c) 2020 A B",
			want:     []string{"Copyright (c) 2020 A B"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := headLines(tt.original, tt.head)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("headLines() = %q, want %q", got, tt.want)
			}
		})
	}
}
