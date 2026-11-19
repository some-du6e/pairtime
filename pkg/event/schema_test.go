// SPDX-License-Identifier: AGPL-3.0-or-later

package event

// These tests fail when docs/schema/event.schema.json and the Go types
// drift apart. The schema is written by hand; the Go types are the
// reference. A property the schema lists and Go lacks, a required list
// that doesn't match the fields Go always writes, a missing kind, an enum
// value on one side only, a local field marked on one side only, or a
// kind rule that differs all fail here.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const schemaPath = "../../docs/schema/event.schema.json"

// enumInfo is an enum with its type erased.
type enumInfo struct {
	name   string
	typ    reflect.Type
	values []string
}

func infoOf[T ~string](e enum[T]) enumInfo {
	values := make([]string, len(e.values))
	for i, v := range e.values {
		values[i] = string(v)
	}
	return enumInfo{name: e.name, typ: reflect.TypeFor[T](), values: values}
}

// schemaEnums lists every enum the event schema describes under $defs.
func schemaEnums() []enumInfo {
	return []enumInfo{
		infoOf(kinds), infoOf(sources), infoOf(harnesses), infoOf(startKinds), infoOf(entrypoints),
		infoOf(parentClasses), infoOf(declaredOrigins), infoOf(toolClasses), infoOf(decisions),
		infoOf(decidedBys), infoOf(stopReasons), infoOf(subagentPhases), infoOf(editorActs),
		infoOf(editSources), infoOf(cmdClasses), infoOf(gitOps), infoOf(actors), infoOf(appClasses),
		infoOf(powerOps), infoOf(activeTimeTypes),
	}
}

// field is one JSON field of a Go struct.
type field struct {
	name      string
	omitempty bool
	local     bool
	typ       reflect.Type // nil for the synthetic "data" field
}

// jsonFields lists the JSON fields of struct type t, flattening embedded
// structs the way encoding/json does.
func jsonFields(t reflect.Type) []field {
	var out []field
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag, hasTag := f.Tag.Lookup("json")
		if f.Anonymous && !hasTag && f.Type.Kind() == reflect.Struct {
			out = append(out, jsonFields(f.Type)...)
			continue
		}
		if !f.IsExported() || tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		out = append(out, field{
			name:      name,
			omitempty: slices.Contains(strings.Split(opts, ","), "omitempty"),
			local:     f.Tag.Get("pairtime") == "local",
			typ:       f.Type,
		})
	}
	return out
}

type schemaDoc struct {
	root map[string]any
	defs map[string]any
}

func loadSchema(t *testing.T) schemaDoc {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(schemaPath))
	if err != nil {
		t.Fatalf("reading the schema: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("%s isn't valid JSON: %v", schemaPath, err)
	}
	return schemaDoc{root: root, defs: object(t, root["$defs"], "$defs")}
}

func object(t *testing.T, v any, path string) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("schema: %s is %T, want an object", path, v)
	}
	return m
}

func stringList(t *testing.T, v any, path string) []string {
	t.Helper()
	if v == nil {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		t.Fatalf("schema: %s is %T, want an array", path, v)
	}
	out := make([]string, len(list))
	for i, x := range list {
		s, ok := x.(string)
		if !ok {
			t.Fatalf("schema: %s[%d] is %T, want a string", path, i, x)
		}
		out[i] = s
	}
	return out
}

func (s schemaDoc) isEnum(name string) bool {
	def, _ := s.defs[name].(map[string]any)
	_, ok := def["enum"]
	return ok
}

// refName returns the $defs name a property refers to (through "items"
// for a list), or "".
func refName(prop map[string]any, list bool) string {
	if list {
		prop, _ = prop["items"].(map[string]any)
	}
	ref, _ := prop["$ref"].(string)
	return strings.TrimPrefix(ref, "#/$defs/")
}

// checkObject compares one object schema with the Go fields it describes.
func (s schemaDoc) checkObject(t *testing.T, where string, obj map[string]any, fields []field) {
	t.Helper()
	byType := map[reflect.Type]string{}
	for _, e := range schemaEnums() {
		byType[e.typ] = e.name
	}
	if obj["additionalProperties"] != false {
		t.Errorf("%s: the schema must set \"additionalProperties\": false", where)
	}
	props := object(t, obj["properties"], where+".properties")
	var names, required []string
	for _, f := range fields {
		names = append(names, f.name)
		if !f.omitempty {
			required = append(required, f.name)
		}
		p, ok := props[f.name]
		if !ok {
			t.Errorf("%s: the schema has no property %q", where, f.name)
			continue
		}
		prop := object(t, p, where+"."+f.name)
		if local, _ := prop["x-local"].(bool); local != f.local {
			t.Errorf("%s.%s: schema \"x-local\" is %v, but the Go pairtime:\"local\" tag says %v", where, f.name, local, f.local)
		}
		if f.typ == nil {
			continue
		}
		typ, list := f.typ, false
		if typ.Kind() == reflect.Slice {
			if _, isEnum := byType[typ.Elem()]; isEnum {
				typ, list = typ.Elem(), true
			}
		}
		want, isEnum := byType[typ]
		got := refName(prop, list)
		switch {
		case isEnum && got != want:
			t.Errorf("%s.%s: the Go type is enum %q, but the schema refers to %q", where, f.name, want, got)
		case !isEnum && s.isEnum(got):
			t.Errorf("%s.%s: the schema refers to enum %q, but the Go type is %s", where, f.name, got, f.typ)
		}
	}
	for name := range props {
		if !slices.Contains(names, name) {
			t.Errorf("%s: schema property %q has no Go field", where, name)
		}
	}
	got := stringList(t, obj["required"], where+".required")
	slices.Sort(got)
	slices.Sort(required)
	if !slices.Equal(got, required) {
		t.Errorf("%s: the schema requires %q, but Go always writes %q (the fields without omitempty)", where, got, required)
	}
}

func TestSchemaEnvelope(t *testing.T) {
	s := loadSchema(t)
	fields := append(jsonFields(reflect.TypeFor[Envelope]()),
		field{name: "kind", typ: reflect.TypeFor[Kind]()},
		field{name: "data"})
	s.checkObject(t, "event", s.root, fields)
}

func TestSchemaPayloads(t *testing.T) {
	s := loadSchema(t)
	for _, k := range Kinds() {
		def, ok := s.defs[string(k)]
		if !ok {
			t.Errorf("the schema has no $defs entry for kind %q", k)
			continue
		}
		s.checkObject(t, string(k), object(t, def, "$defs/"+string(k)), jsonFields(reflect.TypeOf(newPayload(k)).Elem()))
	}
	s.checkObject(t, "pending_cron", object(t, s.defs["pending_cron"], "$defs/pending_cron"), jsonFields(reflect.TypeFor[PendingCron]()))
}

func TestSchemaEnums(t *testing.T) {
	s := loadSchema(t)
	registered := map[string]bool{}
	for _, e := range schemaEnums() {
		registered[e.name] = true
		def, ok := s.defs[e.name]
		if !ok {
			t.Errorf("the schema has no $defs entry %q for Go enum %s", e.name, e.typ)
			continue
		}
		if got := stringList(t, object(t, def, "$defs/"+e.name)["enum"], "$defs/"+e.name+".enum"); !slices.Equal(got, e.values) {
			t.Errorf("$defs/%s lists %q, but Go %s has %q (same order)", e.name, got, e.typ, e.values)
		}
	}
	for name := range s.defs {
		if s.isEnum(name) && !registered[name] {
			t.Errorf("schema enum $defs/%s has no Go enum; add it to schemaEnums", name)
		}
	}
}

// TestSchemaKindRules checks the schema's allOf: one if/then rule per kind,
// sending "data" to that kind's $defs entry and restricting source,
// harness and session_key exactly as the Go kind rule does.
func TestSchemaKindRules(t *testing.T) {
	s := loadSchema(t)
	rules := map[Kind]map[string]any{}
	allOf, ok := s.root["allOf"].([]any)
	if !ok {
		t.Fatal("the schema has no allOf list of kind rules")
	}
	for i, r := range allOf {
		where := fmt.Sprintf("allOf[%d]", i)
		rule := object(t, r, where)
		cond := object(t, object(t, object(t, rule["if"], where+".if")["properties"], where+".if.properties")["kind"], where+".if.properties.kind")
		k, _ := cond["const"].(string)
		if newPayload(Kind(k)) == nil {
			t.Errorf("%s: its if names %q, which isn't a kind", where, k)
			continue
		}
		if !slices.Contains(stringList(t, object(t, rule["if"], where+".if")["required"], where+".if.required"), "kind") {
			t.Errorf("%s: its if must require \"kind\", or it also matches events without one", where)
		}
		if rules[Kind(k)] != nil {
			t.Errorf("%s: a second rule for kind %q", where, k)
		}
		rules[Kind(k)] = object(t, object(t, rule["then"], where+".then")["properties"], where+".then.properties")
	}
	for _, k := range Kinds() {
		props, ok := rules[k]
		if !ok {
			t.Errorf("the schema's allOf has no rule for kind %q", k)
			continue
		}
		if got := refName(object(t, props["data"], string(k)+" rule data"), false); got != string(k) {
			t.Errorf("%s rule: data refers to %q, want %q", k, got, k)
		}
		r := ruleFor(k)
		var want []string
		for _, src := range r.sources {
			want = append(want, string(src))
		}
		if got := allowedValues(t, props["source"]); !slices.Equal(got, want) {
			t.Errorf("%s rule: the schema allows sources %q, Go allows %q (empty means any)", k, got, want)
		}
		wantMode := "any"
		switch {
		case r.noSession:
			wantMode = "none"
		case r.inSession:
			wantMode = "not none"
		}
		for _, prop := range []string{"harness", "session_key"} {
			if got := noneMode(t, props[prop]); got != wantMode {
				t.Errorf("%s rule: the schema's %s is %q, Go's rule is %q", k, prop, got, wantMode)
			}
		}
		for name := range props {
			if !slices.Contains([]string{"data", "source", "harness", "session_key"}, name) {
				t.Errorf("%s rule: unexpected property %q; this test only knows data, source, harness and session_key", k, name)
			}
		}
	}
}

// allowedValues reads a {"const": x} or {"enum": [...]} restriction; nil
// means the property isn't restricted.
func allowedValues(t *testing.T, v any) []string {
	t.Helper()
	if v == nil {
		return nil
	}
	p := object(t, v, "source rule")
	if c, ok := p["const"].(string); ok {
		return []string{c}
	}
	return stringList(t, p["enum"], "source rule enum")
}

// noneMode reads how a rule restricts harness or session_key: "none" for
// {"const": "none"}, "not none" for {"not": {"const": "none"}}, "any" when
// absent.
func noneMode(t *testing.T, v any) string {
	t.Helper()
	if v == nil {
		return "any"
	}
	p := object(t, v, "harness or session_key rule")
	if p["const"] == NoSession {
		return "none"
	}
	if not, ok := p["not"].(map[string]any); ok && not["const"] == NoSession {
		return "not none"
	}
	t.Errorf("unexpected harness or session_key rule %v", p)
	return "?"
}

// TestSchemaRefs checks that every $ref in the schema points at an entry
// that exists.
func TestSchemaRefs(t *testing.T) {
	s := loadSchema(t)
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch v := v.(type) {
		case map[string]any:
			for key, child := range v {
				if key == "$ref" {
					ref, _ := child.(string)
					name, ok := strings.CutPrefix(ref, "#/$defs/")
					if _, exists := s.defs[name]; !ok || !exists {
						t.Errorf("%s: $ref %q doesn't resolve", path, ref)
					}
					continue
				}
				walk(path+"/"+key, child)
			}
		case []any:
			for i, child := range v {
				walk(path+"/"+strconv.Itoa(i), child)
			}
		}
	}
	walk("#", s.root)
}
