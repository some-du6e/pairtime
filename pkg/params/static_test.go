// SPDX-License-Identifier: AGPL-3.0-or-later

package params

// TE WP0.6's static check: every parameter pkg/score reads through Int,
// Rat or Bool is in the public table the agent scores with, the newest
// one. The parameter types are opaque (names.go), so a params.NAME selector
// is the only way another package can name a parameter to those three.
// Table.Text takes a string and isn't checked; it's for code that shows a
// whole table. paramReads finds those selectors with go/ast, without
// building the package, and TestScoreReadsOnlyTableParams compares them
// with the newest embedded table. An older table can lack a parameter a
// later version added, and pkg/score's NewState refuses such a table.
// pkg/ledger's draw runs under the table frozen with its r, which can be
// any table (DESIGN 7.5 step 4), so its reads are compared with every
// table.

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
	"reflect"
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
		// every compares the reads with every table, not just the newest.
		every bool
	}{
		{"pkg/score reads only parameters the newest table, the one the agent scores with, has", filepath.Join("..", "score"), false},
		{"pkg/ledger reads only parameters every table has", filepath.Join("..", "ledger"), true},
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
			tables := Tables()
			if !c.every {
				tables = tables[len(tables)-1:]
			}
			for _, m := range missing(reads, tables) {
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

// TestAdded checks added against all and the embedded tables: each entry
// names a parameter once, with the version of an embedded table, and that
// table is the first to have the parameter, so Parse's rule and the
// published tables agree.
func TestAdded(t *testing.T) {
	seen := make(map[string]bool)
	for _, a := range added {
		t.Run(a.p.String()+" is first in table "+a.version, func(t *testing.T) {
			if seen[a.p.String()] {
				t.Errorf("added lists %s twice", a.p)
			}
			seen[a.p.String()] = true
			if !slices.Contains(all, a.p) {
				t.Errorf("added lists %s, which all doesn't", a.p)
			}
			if _, ok := parseVersion(a.version); !ok {
				t.Fatalf("added gives %s the version %q, which isn't MAJOR.MINOR.PATCH", a.p, a.version)
			}
			if _, err := ByVersion(a.version); err != nil {
				t.Errorf("added gives %s the version %s, which no embedded table has: %v", a.p, a.version, err)
			}
			for _, tab := range Tables() {
				_, has := tab.Text(a.p.String())
				if want := compareVersions(tab.Version(), a.version) >= 0; has != want {
					t.Errorf("table %s has %s: %v, want %v", tab.Version(), a.p, has, want)
				}
			}
		})
	}
	t.Run("every other parameter is in every table", func(t *testing.T) {
		for _, p := range all {
			if since(p) != "" {
				continue
			}
			for _, tab := range Tables() {
				if _, ok := tab.Text(p.String()); !ok {
					t.Errorf("table %s has no %s", tab.Version(), p)
				}
			}
		}
	})
}

// paramTypes maps the name of each parameter type to its kind.
var paramTypes = map[string]kind{"IntParam": kindInt, "RatParam": kindRat, "BoolParam": kindBool}

// paramDecl is one parameter variable as the source declares it.
type paramDecl struct {
	kind kind
	// value is the name the variable's composite literal gives it, or ""
	// when there is no literal of the form Type{"NAME"}.
	value string
	pos   token.Position
}

// declaredParams lists the package-level variables of type IntParam,
// RatParam or BoolParam in the non-test Go files of dir: each variable
// declared with one of those types, given a composite literal of one, or
// both.
func declaredParams(dir string) (map[string]paramDecl, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	out := make(map[string]paramDecl)
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
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, id := range vs.Names {
					d := paramDecl{kind: paramKind(vs.Type), pos: fset.Position(id.Pos())}
					if i < len(vs.Values) {
						if lit, ok := vs.Values[i].(*ast.CompositeLit); ok && paramKind(lit.Type) != 0 {
							d.kind = paramKind(lit.Type)
							d.value = literalName(lit)
						}
					}
					if d.kind != 0 {
						out[id.Name] = d
					}
				}
			}
		}
	}
	return out, nil
}

// paramKind returns the kind of the parameter type typ names, or 0 when
// typ, which may be nil, names none.
func paramKind(typ ast.Expr) kind {
	if id, ok := typ.(*ast.Ident); ok {
		return paramTypes[id.Name]
	}
	return 0
}

// literalName returns NAME for a composite literal Type{"NAME"}, and "" for
// any other literal.
func literalName(lit *ast.CompositeLit) string {
	if len(lit.Elts) != 1 {
		return ""
	}
	s, ok := lit.Elts[0].(*ast.BasicLit)
	if !ok || s.Kind != token.STRING {
		return ""
	}
	name, _ := strconv.Unquote(s.Value)
	return name
}

// TestDeclaredParams checks the parameter variables against all, the list
// Parse checks tables against. Each is declared as NAME = Type{"NAME"}, so
// a selector's name is the table's name, and all holds each one once with
// its type, so no table can lack a parameter rule code can name.
func TestDeclaredParams(t *testing.T) {
	decls, err := declaredParams(".")
	if err != nil {
		t.Fatal(err)
	}
	listed := make(map[string][]kind)
	for _, p := range all {
		listed[p.String()] = append(listed[p.String()], p.kind())
	}
	for _, name := range slices.Sorted(maps.Keys(decls)) {
		d := decls[name]
		t.Run(name+" is declared with its own name and listed once in all with its type", func(t *testing.T) {
			if d.value != name {
				t.Errorf("%s: %s names %q; declare a parameter as NAME = Type{\"NAME\"}", d.pos, name, d.value)
			}
			if got := listed[name]; len(got) != 1 || got[0] != d.kind {
				t.Errorf("%s: all lists %s as %v, want it once as %s", d.pos, name, got, d.kind)
			}
		})
	}
	t.Run("all lists only declared variables", func(t *testing.T) {
		for name := range listed {
			if _, ok := decls[name]; !ok {
				t.Errorf("all lists %s, which no variable declares", name)
			}
		}
	})
}

// TestParamDecls runs declaredParams on testdata/decls, a package the go
// command never builds, which declares parameters the right way and in
// each wrong way TestDeclaredParams has to catch.
func TestParamDecls(t *testing.T) {
	decls, err := declaredParams(filepath.Join("testdata", "decls"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		param string
		// kind is 0 for a variable that isn't listed.
		kind  kind
		value string
	}{
		{"an IntParam literal is listed with its name", "GOOD_S", kindInt, "GOOD_S"},
		{"a RatParam literal is listed as a rational", "GOOD_RATIO", kindRat, "GOOD_RATIO"},
		{"a BoolParam literal is listed as a flag", "GOOD_FLAG", kindBool, "GOOD_FLAG"},
		{"a literal under a declared type is read the same way", "TYPED_S", kindInt, "TYPED_S"},
		{"a literal that gives another name is listed with that name", "MISNAMED_S", kindInt, "OTHER_S"},
		{"a keyed literal is listed with no name", "KEYED_S", kindInt, ""},
		{"an empty literal is listed with no name", "EMPTY_S", kindInt, ""},
		{"a variable with a type and no value is listed with no name", "BARE_S", kindInt, ""},
		{"each variable of a list gets its own literal", "PAIR_B_S", kindInt, "WRONG_S"},
		{"a slice of parameters isn't a parameter", "list", 0, ""},
		{"a string isn't a parameter", "text", 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, ok := decls[c.param]
			if ok != (c.kind != 0) || d.kind != c.kind || d.value != c.value {
				t.Errorf("declaredParams lists %s: %v, kind %v, name %q; want kind %v, name %q", c.param, ok, d.kind, d.value, c.kind, c.value)
			}
		})
	}
}

// TestParamTypesAreOpaque checks what leaves other packages no names but
// the variables in names.go: a string or number type would take any
// constant, as in tab.Int("THINK_LEAD_S"); an exported field would let any
// package write a literal with a name; and a conversion between two
// parameter types would change a name's type.
func TestParamTypesAreOpaque(t *testing.T) {
	types := []reflect.Type{reflect.TypeFor[IntParam](), reflect.TypeFor[RatParam](), reflect.TypeFor[BoolParam]()}
	for _, typ := range types {
		t.Run(typ.Name()+" is a struct with no exported field", func(t *testing.T) {
			if typ.Kind() != reflect.Struct {
				t.Fatalf("%s is a %s, which takes a constant of its kind", typ, typ.Kind())
			}
			for i := range typ.NumField() {
				if f := typ.Field(i); f.IsExported() {
					t.Errorf("%s has exported field %s, which any package can set", typ, f.Name)
				}
			}
		})
		for _, other := range types {
			if other == typ {
				continue
			}
			t.Run(typ.Name()+" doesn't convert to "+other.Name(), func(t *testing.T) {
				if typ.ConvertibleTo(other) {
					t.Errorf("%s converts to %s", typ, other)
				}
			})
		}
	}
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
