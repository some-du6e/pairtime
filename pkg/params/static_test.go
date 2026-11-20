// SPDX-License-Identifier: AGPL-3.0-or-later

package params

// TE WP0.6's static check: every parameter pkg/score reads is in the
// public table. paramReads finds the reads with go/ast, without building
// the package, and TestScoreReadsOnlyTableParams compares them with every
// embedded table.

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const importPath = "github.com/some-du6e/pairtime/pkg/params"

// isParamName reports whether s is written the way every parameter is: a
// capital letter, then capitals, digits and underscores. Other exported
// names, such as ByVersion and Table, are the package's API.
func isParamName(s string) bool {
	if s == "" || s[0] < 'A' || s[0] > 'Z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if c := s[i]; !('A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

// paramReads lists the parameters the Go files in dir read, test files
// included: the NAME of every params.NAME selector whose left side is the
// name the file imports this package as, with the position of its first
// use. It refuses a dot import, which would hide the reads.
func paramReads(dir string) (map[string]token.Position, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	reads := make(map[string]token.Position)
	for _, file := range files {
		f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		local := ""
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p != importPath {
				continue
			}
			local = "params"
			if imp.Name != nil {
				local = imp.Name.Name
			}
		}
		switch local {
		case "", "_":
			continue
		case ".":
			return nil, fmt.Errorf("%s dot-imports %s, which hides its parameter reads from this check", file, importPath)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == local && isParamName(sel.Sel.Name) {
				if _, seen := reads[sel.Sel.Name]; !seen {
					reads[sel.Sel.Name] = fset.Position(sel.Sel.Pos())
				}
			}
			return true
		})
	}
	return reads, nil
}

// treeReads runs paramReads on root and every directory below it that the
// go command would build: not testdata, and not names starting with . or _.
func treeReads(root string) (map[string]token.Position, error) {
	reads := make(map[string]token.Position)
	err := filepath.WalkDir(root, func(dir string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		if name := d.Name(); dir != root && (name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
			return filepath.SkipDir
		}
		found, err := paramReads(dir)
		for name, pos := range found {
			if _, seen := reads[name]; !seen {
				reads[name] = pos
			}
		}
		return err
	})
	return reads, err
}

// missing lists each read that a table lacks, sorted by table and name.
func missing(reads map[string]token.Position, tables []*Table) []string {
	var out []string
	for _, tab := range tables {
		for _, name := range slices.Sorted(maps.Keys(reads)) {
			if _, ok := tab.Text(name); !ok {
				out = append(out, fmt.Sprintf("%s: params.%s isn't in table %s", reads[name], name, tab.Version()))
			}
		}
	}
	return out
}

func TestScoreReadsOnlyTableParams(t *testing.T) {
	cases := []struct {
		name string
		dir  string
	}{
		{"pkg/score reads only parameters every table has", filepath.Join("..", "score")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := os.Stat(c.dir); errors.Is(err, fs.ErrNotExist) {
				t.Skipf("%s doesn't exist yet; the check runs once it does", c.dir)
			} else if err != nil {
				t.Fatal(err)
			}
			reads, err := treeReads(c.dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range missing(reads, Tables()) {
				t.Error(m)
			}
			t.Logf("%s reads %d parameters", c.dir, len(reads))
		})
	}
}

// TestParamReads runs the static check's helpers on testdata/reads, a
// package the go command never builds, so it can read a name no table has.
func TestParamReads(t *testing.T) {
	reads, err := treeReads(filepath.Join("testdata", "reads"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		param string
		read  bool
	}{
		{"a selector on the params import is a read", "THINK_LEAD_S", true},
		{"a name no table has is listed too", "NOT_A_PARAM_S", true},
		{"a renamed import is followed", "COMPOSE_CPS", true},
		{"test files count", "BATCH_S", true},
		{"packages below the root count", "SAMPLE_S", true},
		{"a local value named params isn't the package", "LOCAL_S", false},
		{"testdata below the root is skipped, as the go command skips it", "SKIPPED_S", false},
		{"API names such as ByVersion aren't parameters", "ByVersion", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, got := reads[c.param]; got != c.read {
				t.Errorf("%s listed = %v, want %v; listed: %v", c.param, got, c.read, slices.Sorted(maps.Keys(reads)))
			}
		})
	}
	t.Run("only the name no table has is reported, once per table", func(t *testing.T) {
		got := missing(reads, Tables())
		if len(got) != len(Tables()) {
			t.Fatalf("missing reported %q, want one line per table", got)
		}
		for _, m := range got {
			if !strings.Contains(m, "params.NOT_A_PARAM_S isn't in table") {
				t.Errorf("missing reported %q, want only NOT_A_PARAM_S", m)
			}
		}
	})
	t.Run("a dot import is refused", func(t *testing.T) {
		if _, err := paramReads(filepath.Join("testdata", "dotimport")); err == nil {
			t.Error("paramReads accepted a dot import")
		}
	})
}

// constDecl is one parameter constant as the source declares it.
type constDecl struct {
	kind  kind
	value string
	pos   token.Position
}

// declaredParams lists the constants of type IntParam, RatParam or
// BoolParam in the non-test Go files of dir.
func declaredParams(dir string) (map[string]constDecl, error) {
	types := map[string]kind{"IntParam": kindInt, "RatParam": kindRat, "BoolParam": kindBool}
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	out := make(map[string]constDecl)
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				typ, ok := vs.Type.(*ast.Ident)
				if !ok {
					continue
				}
				k, ok := types[typ.Name]
				if !ok {
					continue
				}
				for i, id := range vs.Names {
					d := constDecl{kind: k, pos: fset.Position(id.Pos())}
					if i < len(vs.Values) {
						if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							d.value, _ = strconv.Unquote(lit.Value)
						}
					}
					out[id.Name] = d
				}
			}
		}
	}
	return out, nil
}

// TestDeclaredParams checks the constants against all, the list Parse
// checks tables against. Each constant is spelled as its value, so a
// selector's name is the table's name, and all holds each one once with
// its type, so no table can lack a parameter rule code can name.
func TestDeclaredParams(t *testing.T) {
	decls, err := declaredParams(".")
	if err != nil {
		t.Fatal(err)
	}
	listed := make(map[string][]kind)
	for _, p := range all {
		listed[p.name()] = append(listed[p.name()], p.kind())
	}
	for _, name := range slices.Sorted(maps.Keys(decls)) {
		d := decls[name]
		t.Run(name+" is spelled as its value and listed once in all with its type", func(t *testing.T) {
			if d.value != name {
				t.Errorf("%s: %s = %q; a parameter constant's value is its own name", d.pos, name, d.value)
			}
			if got := listed[name]; len(got) != 1 || got[0] != d.kind {
				t.Errorf("%s: all lists %s as %v, want it once as %s", d.pos, name, got, d.kind)
			}
		})
	}
	t.Run("all lists only declared constants", func(t *testing.T) {
		for name := range listed {
			if _, ok := decls[name]; !ok {
				t.Errorf("all lists %s, which no constant declares", name)
			}
		}
	})
}

func TestIsParamName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"capitals with an underscore are a parameter", "THINK_LEAD_S", true},
		{"digits are allowed after the first letter", "R2_S", true},
		{"MixedCaps is API", "ByVersion", false},
		{"a leading underscore isn't a parameter", "_S", false},
		{"a leading digit isn't a parameter", "2S", false},
		{"lowercase isn't a parameter", "regular_timing", false},
		{"the empty string isn't a parameter", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isParamName(c.in); got != c.want {
				t.Errorf("isParamName(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
