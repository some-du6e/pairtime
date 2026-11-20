// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The expected identifiers come from outside this package: the SPDX License
// List's own texts (spdx-*, from github.com/spdx/license-list-data), Debian's
// copies in /usr/share/common-licenses (debian-*), and the LICENSE files of
// modules on DESIGN 3.12's list and their dependencies (real-*), with the
// identifier GitHub's license detection gives each repository. GitHub gives
// none for go-humanize because of the URL after its MIT text; pkg.go.dev
// calls it MIT. modernc.org/libc lives on GitLab, and pkg.go.dev calls it
// BSD-3-Clause.
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
		{name: "modernc.org/libc is BSD-3-Clause, naming the authors and the contributors (mathutil and memory share the text)", file: "testdata/real-modernc-libc.txt", want: "BSD-3-Clause"},
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
	// franceMIT is MIT with that sentence on a line of its own under the
	// copyright line.
	franceMIT := mustReplace(mit, "Copyright (c) <year> <copyright holders>", "Copyright (c) <year> <copyright holders>\n\n"+france)
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
		{name: "a sentence on its own line under MIT's copyright fails", text: franceMIT},
		{name: "a sentence on its own line under ISC's copyright fails", text: mustReplace(isc, "Copyright <year> <owner>", "Copyright <year> <owner>\n"+france)},
		{name: "a sentence on its own line under BSD-2-Clause's copyright fails", text: mustReplace(bsd2, "Copyright (c) <year> <owner>", "Copyright (c) <year> <owner>\n"+france)},
		{name: "a sentence on its own line under BSD-3-Clause's copyright fails", text: mustReplace(bsd3, "Copyright (c) <year> <owner>.", "Copyright (c) <year> <owner>.\n"+france)},
		{name: "a sentence above the copyright line fails", text: mustReplace(bsd2, "Copyright (c) <year> <owner>", france+"\nCopyright (c) <year> <owner>")},
		{name: "a title and a rule above the copyright line are fine", text: mustReplace(bsd3, "Copyright (c) <year> <owner>.", "BSD 3-Clause License\n====================\n\nCopyright (c) 2020, Example Corp.\nAll rights reserved."), want: "BSD-3-Clause"},
		{name: "a title on the copyright line is fine", text: mustReplace(mit, "MIT License\n\nCopyright (c) <year> <copyright holders>", "MIT License Copyright (c) 2020 Example Corp."), want: "MIT"},
		{name: "a sentence under a title and copyright sharing a line fails", text: mustReplace(mit, "MIT License\n\nCopyright (c) <year> <copyright holders>", "MIT License Copyright (c) 2020 Foo\n"+france)},
		{name: "a holder named PayPal passes", text: mustReplace(mit, "Copyright (c) <year> <copyright holders>", "Copyright (c) 2024 PayPal, Inc."), want: "MIT"},
		{name: "a holder named May passes", text: mustReplace(mit, "Copyright (c) <year> <copyright holders>", "Copyright (c) 2024 Sally May"), want: "MIT"},
		{name: "a holder whose name starts with No passes", text: mustReplace(mit, "Copyright (c) <year> <copyright holders>", "Copyright (c) 2024 No Starch Press"), want: "MIT"},
		{name: "a condition on the copyright line itself fails", text: mustReplace(mit, "Copyright (c) <year> <copyright holders>", "Copyright (c) 2020 Foo. Commercial use is prohibited.")},
		{name: "a may-not condition on the copyright line itself fails", text: mustReplace(mit, "Copyright (c) <year> <copyright holders>", "Copyright (c) 2020 Foo. This software may not be sold.")},
		{name: "a condition inside BSD-3-Clause's endorsement clause fails", text: mustReplace(bsd3, "Neither the name of the copyright holder nor the names of its contributors may", "Neither the name of Foo nor the names of its contributors may be used to endorse this software. Commercial use is prohibited. Neither the name of Foo nor the names of its contributors may")},
		{name: "BSD-3-Clause's 'the name of X may not' wording is BSD-3-Clause", text: mustReplace(bsd3, "Neither the name of the copyright holder nor the names of its contributors may", "The name of the author may not"), want: "BSD-3-Clause"},
		{name: "BSD-3-Clause naming their contributors is BSD-3-Clause", text: mustReplace(bsd3, "nor the names of its contributors", "nor the names of their contributors"), want: "BSD-3-Clause"},
		{name: "a GPL mention in the copyright lines fails", text: mustReplace(mit, "Copyright (c) <year> <copyright holders>", "Copyright (c) 2016 A. Some files are under the GNU General Public License."), want: ""},
		{name: "BSD-3-Clause without its third clause is BSD-2-Clause", text: bsd3[:strings.Index(bsd3, "3. Neither")] + bsd3[strings.Index(bsd3, "THIS SOFTWARE"):], want: "BSD-2-Clause"},
		{name: "MIT and GPL texts in one file fail", text: mit + "\n" + string(gpl)},
		{name: "an empty file fails", text: ""},
		{name: "a URL that ends a line stops at the line break, even a vertical tab", text: mustReplace(mit, "Copyright (c) <year> <copyright holders>", "Copyright (c) 2020 Foo https://example.com/foo\vCopyright (c) 2021 Bar"), want: "MIT"},
	}
	// Every line break normalize turns into a space also ends a line for
	// the head rule, so the license still matches whichever one a file
	// uses, and a sentence under the copyright line still fails. The
	// separators past \v are built from their code points.
	for _, lb := range []struct{ name, sep string }{
		{name: "CR-only", sep: "\r"},
		{name: "form feed", sep: "\f"},
		{name: "vertical tab", sep: "\v"},
		{name: "NEL (U+0085)", sep: string(rune(0x85))},
		{name: "U+2028 line separator", sep: string(rune(0x2028))},
		{name: "U+2029 paragraph separator", sep: string(rune(0x2029))},
	} {
		tests = append(tests,
			struct{ name, text, want string }{name: lb.name + " line breaks don't matter", text: strings.ReplaceAll(mit, "\n", lb.sep), want: "MIT"},
			struct{ name, text, want string }{name: "a sentence on its own line under MIT's copyright fails with " + lb.name + " line breaks", text: strings.ReplaceAll(franceMIT, "\n", lb.sep)},
		)
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

// TestShortLicenseNames puts the short name of a license outside Allowed,
// or a link to its text, on MIT's copyright line, where the head rule
// accepts anything after "Copyright". Each must fail, and the reason must
// name it, so no case passes on another rule.
func TestShortLicenseNames(t *testing.T) {
	data, err := os.ReadFile("testdata/spdx-MIT.txt")
	if err != nil {
		t.Fatal(err)
	}
	mit := string(data)
	const line = "Copyright (c) <year> <copyright holders>"
	if !strings.Contains(mit, line) {
		t.Fatalf("spdx-MIT.txt lacks %q", line)
	}
	tests := []struct {
		name    string
		holder  string // what follows "Copyright (c) 2020 "
		mention string // what identify says the text mentions; "" means it stays MIT
	}{
		{name: "GPLv2 fails", holder: "Foo. Files under asm/: GPLv2", mention: "GPLv2"},
		{name: "GPL-3.0-or-later fails", holder: "Foo. Files under asm/: GPL-3.0-or-later", mention: "GPL-3.0"},
		{name: "LGPL-2.1 fails", holder: "Foo. Files under asm/: LGPL-2.1", mention: "LGPL-2.1"},
		{name: "AGPLv3 fails", holder: "Foo. Files under asm/: AGPLv3", mention: "AGPLv3"},
		{name: "MPL-2.0 fails", holder: "Foo. Files under asm/: MPL-2.0", mention: "MPL-2.0"},
		{name: "EPL-2.0 fails", holder: "Foo. Files under asm/: EPL-2.0", mention: "EPL-2.0"},
		{name: "EUPL-1.2 fails", holder: "Foo. Files under asm/: EUPL-1.2", mention: "EUPL-1.2"},
		{name: "CDDL-1.0 fails", holder: "Foo. Files under asm/: CDDL-1.0", mention: "CDDL-1.0"},
		{name: "SSPL-1.0 fails", holder: "Foo. Files under asm/: SSPL-1.0", mention: "SSPL-1.0"},
		{name: "BUSL-1.1 fails", holder: "Foo. Files under asm/: BUSL-1.1", mention: "BUSL-1.1"},
		{name: "BSL, the Business Source License's other short name, fails", holder: "Foo. Files under asm/: BSL 1.1", mention: "BSL"},
		{name: "ELv2 fails", holder: "Foo. Files under asm/: ELv2", mention: "ELv2"},
		{name: "Elastic-2.0 fails", holder: "Foo. Files under asm/: Elastic-2.0", mention: "Elastic-2.0"},
		{name: "CC-BY-NC-4.0 fails", holder: "Foo. Files under asm/: CC-BY-NC-4.0", mention: "CC-BY"},
		{name: "CC0-1.0 fails", holder: "Foo. Files under asm/: CC0-1.0", mention: "CC0-1.0"},
		{name: "a link to the GPL's text fails, though normalize drops URLs", holder: "Foo. Files under asm/: https://www.gnu.org/licenses/gpl-3.0.html", mention: "gpl-3.0"},
		{name: "a link to the MPL's text fails", holder: "Foo. Files under asm/: https://www.mozilla.org/MPL/2.0/", mention: "MPL"},
		{name: "GPL_v2, joined by an underscore, fails", holder: "Foo. Files under asm/ are GPL_v2", mention: "GPL"},
		{name: "a license file named COPYING_LGPL fails", holder: "Foo. Files under asm/ are covered by COPYING_LGPL", mention: "LGPL"},
		{name: "LICENSE_GPL fails", holder: "Foo. See LICENSE_GPL for asm/", mention: "GPL"},
		{name: "a holder with underscores and no license name passes", holder: "The foo_bar Authors"},
		{name: "a holder whose name starts with GPL's letters passes", holder: "Gplot Authors"},
		{name: "a URL whose path starts with GPL's letters passes", holder: "Foo, https://github.com/foo/gplot"},
		{name: "a holder named Elastic passes without a version after it", holder: "Elastic N.V."},
		{name: "a holder whose name starts with EPL's letters passes", holder: "Eplus Ltd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := identify(strings.Replace(mit, line, "Copyright (c) 2020 "+tt.holder, 1))
			if tt.mention == "" {
				if got != "MIT" {
					t.Errorf("identify() = %q (%s), want MIT", got, reason)
				}
				return
			}
			if want := `it mentions "` + tt.mention + `"`; got != "" || !strings.HasPrefix(reason, want) {
				t.Errorf("identify() = %q (%s), want \"\" with a reason starting %s", got, reason, want)
			}
		})
	}
}

// TestShortLicenseNamesInAllowedTexts checks that no allowed license's own
// text, nor any real-* LICENSE file, uses a short name otherLicenseIDRE
// looks for, so the screen can't turn an allowed module away.
func TestShortLicenseNamesInAllowedTexts(t *testing.T) {
	files, err := filepath.Glob("testdata/real-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range Allowed {
		files = append(files, "testdata/spdx-"+id+".txt")
	}
	tests := []struct{ name, file string }{}
	for _, f := range files {
		tests = append(tests, struct{ name, file string }{name: filepath.Base(f) + " names no license outside Allowed", file: f})
	}
	if len(tests) < 2*len(Allowed) {
		t.Fatalf("found only %d license texts", len(tests))
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(tt.file)
			if err != nil {
				t.Fatal(err)
			}
			if m := shortLicenseName(string(data)); m != "" {
				t.Errorf("otherLicenseIDRE finds %q in %s", m, tt.file)
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
		{name: "a wildcard in a var is an error, since only text and head are screened", template: "a <<var:foo .{1,20} bar>> b"},
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
	nel, ls, ps := string(rune(0x85)), string(rune(0x2028)), string(rune(0x2029))
	tests := []struct {
		name     string
		original string
		head     string
		want     []string
		wantOK   bool
	}{
		{
			name:     "each line of the head comes back normalized",
			original: "Copyright \u00a9 2012 The Go Authors.\nCopyright \u00a9 fsnotify Authors.\n\nRedistribution and use...",
			head:     "Copyright (c) 2012 The Go Authors. Copyright (c) fsnotify Authors.",
			want:     []string{"Copyright (c) 2012 The Go Authors.", "Copyright (c) fsnotify Authors."},
			wantOK:   true,
		},
		{
			name:     "lines before the head are skipped",
			original: "MIT License\n\nCopyright (c) 2020 A\n\nPermission is hereby granted...",
			head:     "Copyright (c) 2020 A",
			want:     []string{"Copyright (c) 2020 A"},
			wantOK:   true,
		},
		{
			name:     "a head that starts mid-line, after a title, keeps its own lines",
			original: "MIT License Copyright (c) 2020 Foo\nRedistribution outside France constitutes copyright infringement.\n\nPermission is hereby granted...",
			head:     "Copyright (c) 2020 Foo Redistribution outside France constitutes copyright infringement.",
			want:     []string{"Copyright (c) 2020 Foo", "Redistribution outside France constitutes copyright infringement."},
			wantOK:   true,
		},
		{
			name:     "a head that ends mid-line keeps only its part",
			original: "Copyright (c) 2020 A Permission is hereby granted...",
			head:     "Copyright (c) 2020 A",
			want:     []string{"Copyright (c) 2020 A"},
			wantOK:   true,
		},
		{
			name:     "every line break normalize removes ends a line",
			original: "Copyright (c) 2020 A\rCopyright (c) 2021 B\fCopyright (c) 2022 C\vCopyright (c) 2023 D" + nel + "Copyright (c) 2024 E" + ls + "Copyright (c) 2025 F" + ps + "\r\n\r\nPermission is hereby granted...",
			head:     "Copyright (c) 2020 A Copyright (c) 2021 B Copyright (c) 2022 C Copyright (c) 2023 D Copyright (c) 2024 E Copyright (c) 2025 F",
			want:     []string{"Copyright (c) 2020 A", "Copyright (c) 2021 B", "Copyright (c) 2022 C", "Copyright (c) 2023 D", "Copyright (c) 2024 E", "Copyright (c) 2025 F"},
			wantOK:   true,
		},
		{
			name:     "lines that don't add up to the head are not ok",
			original: "Copyright (c) 2020 A\nsomething else",
			head:     "Copyright (c) 2020 A B",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := headLines(tt.original, tt.head)
			if ok != tt.wantOK || strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("headLines() = %q, %v, want %q, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
