// SPDX-License-Identifier: AGPL-3.0-or-later

package ci

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// spdxJS is the first line of every hand-written script under
// internal/server.
const spdxJS = "// SPDX-License-Identifier: AGPL-3.0-or-later"

var (
	// moduleStatement finds an import or export statement, at the start
	// of a line or after another statement, as a bundle has them.
	moduleStatement = regexp.MustCompile(`(?m)(^|[;}])[ \t]*(import|export)\b`)
	// scriptSinks are what a server page's script may not do (D-server-ui
	// point 2): load or make code, or turn a string into HTML.
	scriptSinks = map[string]*regexp.Regexp{
		"import(":                  regexp.MustCompile(`\bimport\s*\(`),
		"require(":                 regexp.MustCompile(`\brequire\s*\(`),
		"eval(":                    regexp.MustCompile(`\beval\s*\(`),
		"new Function":             regexp.MustCompile(`\bnew\s+Function\b`),
		"innerHTML":                regexp.MustCompile(`\binnerHTML\b`),
		"outerHTML":                regexp.MustCompile(`\bouterHTML\b`),
		"insertAdjacentHTML":       regexp.MustCompile(`\binsertAdjacentHTML\b`),
		"document.write":           regexp.MustCompile(`\bdocument\s*\.\s*write\b`),
		"writeln":                  regexp.MustCompile(`\bwriteln\b`),
		"DOMParser":                regexp.MustCompile(`\bDOMParser\b`),
		"parseFromString":          regexp.MustCompile(`\bparseFromString\b`),
		"createContextualFragment": regexp.MustCompile(`\bcreateContextualFragment\b`),
		"setHTMLUnsafe":            regexp.MustCompile(`\bsetHTMLUnsafe\b`),
		"parseHTMLUnsafe":          regexp.MustCompile(`\bparseHTMLUnsafe\b`),
		"srcdoc":                   regexp.MustCompile(`\bsrcdoc\b`),
	}
	// scriptExceptions are the uses of a sink a script may make, by file
	// and sink, each with its count. There is one: dashboard.js checks that
	// the browser has DOMParser (dashboard.js:14) and parses with it the
	// server's own /dashboard page, which it fetched, never teen text
	// (dashboard.js:183).
	scriptExceptions = map[string]map[string]int{
		"internal/server/pages/dashboard.js": {"DOMParser": 2, "parseFromString": 1},
	}
)

// scriptProblems returns what is wrong with src, a script a server page
// carries inline, given the uses of a sink it may make, or nothing.
func scriptProblems(src string, allowed map[string]int) []string {
	var out []string
	if first, _, _ := strings.Cut(src, "\n"); strings.TrimRight(first, "\r") != spdxJS {
		out = append(out, "its first line isn't "+spdxJS+", so it isn't one of ours: a bundle copied in, or a file without its license")
	}
	if m := moduleStatement.FindString(src); m != "" {
		out = append(out, "it has an import or export statement, "+strings.TrimSpace(m)+", as a module or a bundle does")
	}
	names := make([]string, 0, len(scriptSinks))
	for name := range scriptSinks {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if n := len(scriptSinks[name].FindAllStringIndex(src, -1)); n != allowed[name] {
			out = append(out, "it has "+name+" "+times(n)+", where it may have it "+times(allowed[name]))
		}
	}
	return out
}

// times says n as a count of uses.
func times(n int) string {
	switch n {
	case 0:
		return "nowhere"
	case 1:
		return "once"
	case 2:
		return "twice"
	}
	return strconv.Itoa(n) + " times"
}

// TestServerScripts holds every .js file under internal/server, the
// scripts pairtimed's pages carry inline, to D-server-ui's test 2: each is
// hand-written, opens with its SPDX line, and loads no code and makes no
// HTML from a string, but for dashboard.js's one exception. It reads each
// file with os.ReadFile, so go test's cache keys on the scripts.
func TestServerScripts(t *testing.T) {
	spdx := spdxJS + "\n"
	tests := []struct {
		name    string
		src     string
		allowed map[string]int
		// want is part of a problem scriptProblems must find, "" for none.
		want string
	}{
		{name: "a hand-written script with its SPDX line that sets only text passes", src: spdx + "'use strict';\n(() => {\n\tdocument.getElementById('live').textContent = 'Saved.';\n})();\n"},
		{name: "a bundle copied into the tree fails", src: `var e=Object.defineProperty;import{a as b}from"./chunk-ab12cd.js";const c=()=>b();export{c as default};` + "\n", want: "its first line isn't"},
		{name: "a bundle with an SPDX line still fails for its imports and exports", src: spdx + `import{a as b}from"./chunk-ab12cd.js";const c=()=>b();export{c as default};` + "\n", want: "import or export statement"},
		{name: "a module's export fails", src: spdx + "export function draw() {}\n", want: "import or export statement"},
		{name: "a dynamic import fails", src: spdx + "import('./chart.js').then((m) => m.draw());\n", want: "import("},
		{name: "require fails", src: spdx + "const chart = require('chart.js');\n", want: "require("},
		{name: "eval fails", src: spdx + "eval(text);\n", want: "eval("},
		{name: "new Function fails", src: spdx + "new Function('return 1')();\n", want: "new Function"},
		{name: "innerHTML fails", src: spdx + "el.innerHTML = turn.text;\n", want: "innerHTML"},
		{name: "outerHTML fails", src: spdx + "el.outerHTML = html;\n", want: "outerHTML"},
		{name: "insertAdjacentHTML fails", src: spdx + "el.insertAdjacentHTML('beforeend', html);\n", want: "insertAdjacentHTML"},
		{name: "document.write fails", src: spdx + "document.write(html);\n", want: "document.write"},
		{name: "writeln fails", src: spdx + "doc.writeln(html);\n", want: "writeln"},
		{name: "createContextualFragment fails", src: spdx + "document.createRange().createContextualFragment(html);\n", want: "createContextualFragment"},
		{name: "setHTMLUnsafe fails", src: spdx + "el.setHTMLUnsafe(html);\n", want: "setHTMLUnsafe"},
		{name: "parseHTMLUnsafe fails", src: spdx + "Document.parseHTMLUnsafe(html);\n", want: "parseHTMLUnsafe"},
		{name: "srcdoc fails", src: spdx + "frame.srcdoc = html;\n", want: "srcdoc"},
		{name: "DOMParser without an exception fails", src: spdx + "new DOMParser().parseFromString(html, 'text/html');\n", want: "DOMParser"},
		{name: "an exception allows only the uses it counts", src: spdx + "new DOMParser().parseFromString(a, 'text/html');\nnew DOMParser().parseFromString(b, 'text/html');\n", allowed: map[string]int{"DOMParser": 1, "parseFromString": 1}, want: "DOMParser twice"},
	}
	for _, tt := range tests {
		t.Run("the check: "+tt.name, func(t *testing.T) {
			got := scriptProblems(tt.src, tt.allowed)
			if tt.want == "" {
				if len(got) != 0 {
					t.Errorf("scriptProblems found %q, want nothing", got)
				}
				return
			}
			if !slices.ContainsFunc(got, func(p string) bool { return strings.Contains(p, tt.want) }) {
				t.Errorf("scriptProblems found %q, want a problem with %q", got, tt.want)
			}
		})
	}

	var scripts []string
	err := filepath.WalkDir(filepath.Join(repoRoot, "internal", "server"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".js") {
			rel, err := filepath.Rel(repoRoot, p)
			if err != nil {
				return err
			}
			scripts = append(scripts, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for file := range scriptExceptions {
		if !slices.Contains(scripts, file) {
			t.Errorf("the walk didn't reach %s, so it can't be trusted with the rest", file)
		}
	}
	for _, file := range scripts {
		t.Run(file+" is hand-written, and loads no code and makes no HTML from a string", func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(file)))
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range scriptProblems(string(b), scriptExceptions[file]) {
				t.Errorf("%s: %s", file, p)
			}
		})
	}
}

// nodeBuild finds what in a file of the server image's build would set up
// Node or build with npm or Vite.
var nodeBuild = regexp.MustCompile(`(?i)\b(setup-node|npm|npx|vite|node_modules)\b`)

// viteApps finds the body of vite.config.ts's apps, and viteServer a server
// entry in it.
var (
	viteApps   = regexp.MustCompile(`(?s)\nconst apps = \{(.*?)\n\} as const;`)
	viteServer = regexp.MustCompile(`(?m)^\s*['"]?server['"]?\s*:`)
)

// serverUI returns what in vite.config.ts and package.json would build a
// Svelte server UI, or nothing: a server entry in apps, or a build script
// with --mode server. It fails when either file isn't as the check reads it.
func serverUI(viteConfig string, packageJSON []byte) ([]string, error) {
	var out []string
	m := viteApps.FindStringSubmatch(viteConfig)
	if m == nil {
		return nil, errors.New("vite.config.ts has no `const apps = { ... } as const;`")
	}
	if viteServer.MatchString(m[1]) {
		out = append(out, "vite.config.ts's apps has a server entry")
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(packageJSON, &pkg); err != nil {
		return nil, err
	}
	build := pkg.Scripts["build"]
	if !strings.Contains(build, "--mode agent") {
		return nil, errors.New("package.json's build script doesn't build the local dashboard, so the check can't read it: " + build)
	}
	if regexp.MustCompile(`--mode[ =]server\b`).MatchString(build) {
		out = append(out, "package.json's build script builds --mode server")
	}
	return out, nil
}

// TestServerBuildNoNode holds pairtimed's image to D-server-ui's test 3:
// nothing that builds it sets up Node, runs npm or Vite or copies
// node_modules, and nothing in web/ builds a server UI, so the image stays
// Go alone. It fails the moment anyone builds a Svelte server UI into it.
func TestServerBuildNoNode(t *testing.T) {
	vite, err := os.ReadFile(filepath.Join(repoRoot, "web", "vite.config.ts"))
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := os.ReadFile(filepath.Join(repoRoot, "web", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	serverEntry := strings.Replace(string(vite), "\n} as const;", "\n\tserver: { root: 'src/server', outDir: '../internal/server/dist' },\n} as const;", 1)
	tests := []struct {
		name string
		run  func(t *testing.T) []string
		// want is part of a problem the check must find, "" for none.
		want string
	}{
		{name: "a workflow step that sets up Node is found", run: func(t *testing.T) []string {
			return problemsOf(nodeBuild.FindString("      - uses: actions/setup-node@39370e3970a6d050c480ffad4ff0ed4d3fdee5af # v4.1.0\n"))
		}, want: "setup-node"},
		{name: "a Dockerfile that runs npm is found", run: func(t *testing.T) []string {
			return problemsOf(nodeBuild.FindString("RUN cd web && npm ci && npm run build\n"))
		}, want: "npm"},
		{name: "a Dockerfile that copies node_modules is found", run: func(t *testing.T) []string {
			return problemsOf(nodeBuild.FindString("COPY --from=ui /web/node_modules /web/node_modules\n"))
		}, want: "node_modules"},
		{name: "vite.config.ts with a server entry in apps is found", run: func(t *testing.T) []string {
			got, err := serverUI(serverEntry, pkg)
			if err != nil {
				t.Fatal(err)
			}
			return got
		}, want: "a server entry"},
		{name: "a build script with --mode server is found", run: func(t *testing.T) []string {
			got, err := serverUI(string(vite), []byte(`{"scripts": {"build": "vite build --mode agent && vite build --mode server"}}`))
			if err != nil {
				t.Fatal(err)
			}
			return got
		}, want: "--mode server"},
		{name: "today's vite.config.ts and package.json build no server UI", run: func(t *testing.T) []string {
			got, err := serverUI(string(vite), pkg)
			if err != nil {
				t.Fatal(err)
			}
			return got
		}},
		{name: "no file is under web/src/server", run: func(t *testing.T) []string {
			var got []string
			err := filepath.WalkDir(filepath.Join(repoRoot, "web", "src", "server"), func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !d.IsDir() {
					got = append(got, p+" is a file of a server UI")
				}
				return nil
			})
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				t.Fatal(err)
			}
			return got
		}},
	}
	for _, f := range []string{".github/workflows/server-image.yml", ".github/workflows/restore-drill.yml", "tools/ci/restore_drill.sh", "Dockerfile.pairtimed"} {
		tests = append(tests, struct {
			name string
			run  func(t *testing.T) []string
			want string
		}{name: f + " sets up no Node, and runs no npm or Vite", run: func(t *testing.T) []string {
			b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(f)))
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, m := range nodeBuild.FindAllString(string(b), -1) {
				got = append(got, f+" names "+m)
			}
			return got
		}})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.run(t)
			if tt.want == "" {
				if len(got) != 0 {
					t.Errorf("found %q, want nothing", got)
				}
				return
			}
			if !slices.ContainsFunc(got, func(p string) bool { return strings.Contains(p, tt.want) }) {
				t.Errorf("found %q, want a problem with %q", got, tt.want)
			}
		})
	}
}

// problemsOf turns a match into the problem it is, or nothing for "".
func problemsOf(m string) []string {
	if m == "" {
		return nil
	}
	return []string{"names " + m}
}
