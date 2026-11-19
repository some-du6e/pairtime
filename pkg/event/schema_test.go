// SPDX-License-Identifier: AGPL-3.0-or-later

package event

// These tests fail when docs/schema/event.schema.json and the Go types
// drift apart. The schema is written by hand and the Go types are the
// reference. checkSchema lists every disagreement it finds: a missing kind,
// a property on one side only, a required list that doesn't match the
// fields Go always writes, an enum value on one side only, a local field
// marked on one side only, a kind rule that differs, or a $ref that doesn't
// resolve. TestSchemaDriftIsCaught breaks the schema in each of those ways
// and checks that checkSchema notices.

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
		infoOf(kinds), infoOf(sources), infoOf(vias), infoOf(harnesses), infoOf(startKinds), infoOf(entrypoints),
		infoOf(parentClasses), infoOf(declaredOrigins), infoOf(toolClasses), infoOf(decisions),
		infoOf(decidedBys), infoOf(stopReasons), infoOf(subagentPhases), infoOf(editorActs),
		infoOf(editSources), infoOf(cmdClasses), infoOf(gitOps), infoOf(actors), infoOf(appClasses),
		infoOf(powerOps), infoOf(activeTimeTypes), infoOf(logMatches),
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

// loadSchema decodes the schema afresh, so a test may modify the result.
func loadSchema(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(schemaPath))
	if err != nil {
		t.Fatalf("reading the schema: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("%s isn't valid JSON: %v", schemaPath, err)
	}
	return root
}

// schemaCheck collects every way a decoded schema disagrees with Go.
type schemaCheck struct {
	root, defs map[string]any
	byType     map[reflect.Type]string
	problems   []string
}

func checkSchema(root map[string]any) []string {
	c := &schemaCheck{root: root, byType: map[reflect.Type]string{}}
	for _, e := range schemaEnums() {
		c.byType[e.typ] = e.name
	}
	c.defs = c.object(root["$defs"], "$defs")
	c.envelope()
	c.payloads()
	c.enums()
	c.kindRules()
	c.refs("#", root)
	return c.problems
}

func (c *schemaCheck) addf(format string, args ...any) {
	c.problems = append(c.problems, fmt.Sprintf(format, args...))
}

// object returns v as an object, or records a problem and returns nil,
// which reads as empty.
func (c *schemaCheck) object(v any, path string) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		c.addf("%s is %T, want an object", path, v)
	}
	return m
}

func (c *schemaCheck) stringList(v any, path string) []string {
	if v == nil {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		c.addf("%s is %T, want an array", path, v)
		return nil
	}
	out := make([]string, 0, len(list))
	for i, x := range list {
		s, ok := x.(string)
		if !ok {
			c.addf("%s[%d] is %T, want a string", path, i, x)
			continue
		}
		out = append(out, s)
	}
	return out
}

func (c *schemaCheck) isEnum(name string) bool {
	def, _ := c.defs[name].(map[string]any)
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

// objectFields compares one object schema with the Go fields it describes.
func (c *schemaCheck) objectFields(where string, obj map[string]any, fields []field) {
	if obj["additionalProperties"] != false {
		c.addf("%s: the schema must set \"additionalProperties\": false", where)
	}
	props := c.object(obj["properties"], where+".properties")
	var names, required []string
	for _, f := range fields {
		names = append(names, f.name)
		if !f.omitempty {
			required = append(required, f.name)
		}
		p, ok := props[f.name]
		if !ok {
			c.addf("%s: the schema has no property %q", where, f.name)
			continue
		}
		prop := c.object(p, where+"."+f.name)
		if local, _ := prop["x-local"].(bool); local != f.local {
			c.addf("%s.%s: schema \"x-local\" is %v, but the Go pairtime:\"local\" tag says %v", where, f.name, local, f.local)
		}
		if f.typ == nil {
			continue
		}
		typ, list := f.typ, false
		if typ.Kind() == reflect.Slice {
			if _, isEnum := c.byType[typ.Elem()]; isEnum {
				typ, list = typ.Elem(), true
			}
		}
		want, isEnum := c.byType[typ]
		got := refName(prop, list)
		switch {
		case isEnum && got != want:
			c.addf("%s.%s: the Go type is enum %q, but the schema refers to %q", where, f.name, want, got)
		case !isEnum && c.isEnum(got):
			c.addf("%s.%s: the schema refers to enum %q, but the Go type is %s", where, f.name, got, f.typ)
		}
	}
	for name := range props {
		if !slices.Contains(names, name) {
			c.addf("%s: schema property %q has no Go field", where, name)
		}
	}
	got := c.stringList(obj["required"], where+".required")
	slices.Sort(got)
	slices.Sort(required)
	if !slices.Equal(got, required) {
		c.addf("%s: the schema requires %q, but Go always writes %q (the fields without omitempty)", where, got, required)
	}
}

func (c *schemaCheck) envelope() {
	fields := append(jsonFields(reflect.TypeFor[Envelope]()),
		field{name: "kind", typ: reflect.TypeFor[Kind]()},
		field{name: "data"})
	c.objectFields("event", c.root, fields)
}

func (c *schemaCheck) payloads() {
	for _, k := range Kinds() {
		def, ok := c.defs[string(k)]
		if !ok {
			c.addf("the schema has no $defs entry for kind %q", k)
			continue
		}
		c.objectFields(string(k), c.object(def, "$defs/"+string(k)), jsonFields(reflect.TypeOf(newPayload(k)).Elem()))
	}
	c.objectFields("pending_cron", c.object(c.defs["pending_cron"], "$defs/pending_cron"), jsonFields(reflect.TypeFor[PendingCron]()))
}

func (c *schemaCheck) enums() {
	registered := map[string]bool{}
	for _, e := range schemaEnums() {
		registered[e.name] = true
		def, ok := c.defs[e.name]
		if !ok {
			c.addf("the schema has no $defs entry %q for Go enum %s", e.name, e.typ)
			continue
		}
		if got := c.stringList(c.object(def, "$defs/"+e.name)["enum"], "$defs/"+e.name+".enum"); !slices.Equal(got, e.values) {
			c.addf("$defs/%s lists %q, but Go %s has %q (same order)", e.name, got, e.typ, e.values)
		}
	}
	for name := range c.defs {
		if c.isEnum(name) && !registered[name] {
			c.addf("schema enum $defs/%s has no Go enum; add it to schemaEnums", name)
		}
	}
}

// kindRules checks the schema's allOf: one if/then rule per kind, sending
// "data" to that kind's $defs entry and restricting source, via, harness
// and session_key exactly as the Go kind rule does.
func (c *schemaCheck) kindRules() {
	rules := map[Kind]map[string]any{}
	allOf, ok := c.root["allOf"].([]any)
	if !ok {
		c.addf("the schema has no allOf list of kind rules")
	}
	for i, r := range allOf {
		where := fmt.Sprintf("allOf[%d]", i)
		rule := c.object(r, where)
		cond := c.object(rule["if"], where+".if")
		k, _ := c.object(c.object(cond["properties"], where+".if.properties")["kind"], where+".if.properties.kind")["const"].(string)
		if newPayload(Kind(k)) == nil {
			c.addf("%s: its if names %q, which isn't a kind", where, k)
			continue
		}
		if !slices.Contains(c.stringList(cond["required"], where+".if.required"), "kind") {
			c.addf("%s: its if must require \"kind\", or it also matches events without one", where)
		}
		if rules[Kind(k)] != nil {
			c.addf("%s: a second rule for kind %q", where, k)
		}
		rules[Kind(k)] = c.object(c.object(rule["then"], where+".then")["properties"], where+".then.properties")
	}
	for _, k := range Kinds() {
		props, ok := rules[k]
		if !ok {
			c.addf("the schema's allOf has no rule for kind %q", k)
			continue
		}
		data, _ := props["data"].(map[string]any)
		if got := refName(data, false); got != string(k) {
			c.addf("%s rule: data refers to %q, want %q", k, got, k)
		}
		r := ruleFor(k)
		var want []string
		for _, src := range r.sources {
			want = append(want, string(src))
		}
		if got := c.allowed(props["source"]); !slices.Equal(got, want) {
			c.addf("%s rule: the schema allows sources %q, Go allows %q (empty means any)", k, got, want)
		}
		var wantVias []string
		for _, v := range r.vias {
			wantVias = append(wantVias, string(v))
		}
		if got := c.allowed(props["via"]); !slices.Equal(got, wantVias) {
			c.addf("%s rule: the schema allows vias %q, Go allows %q (empty means any)", k, got, wantVias)
		}
		wantMode := "any"
		switch {
		case r.noSession:
			wantMode = "none"
		case r.inSession:
			wantMode = "not none"
		}
		for _, prop := range []string{"harness", "session_key"} {
			if got := c.noneMode(props[prop]); got != wantMode {
				c.addf("%s rule: the schema's %s is %q, Go's rule is %q", k, prop, got, wantMode)
			}
		}
		for name := range props {
			if !slices.Contains([]string{"data", "source", "via", "harness", "session_key"}, name) {
				c.addf("%s rule: unexpected property %q; checkSchema only knows data, source, via, harness and session_key", k, name)
			}
		}
	}
}

// allowed reads a {"const": x} or {"enum": [...]} restriction on source
// or via; nil means the property isn't restricted.
func (c *schemaCheck) allowed(v any) []string {
	if v == nil {
		return nil
	}
	p := c.object(v, "source or via rule")
	if s, ok := p["const"].(string); ok {
		return []string{s}
	}
	return c.stringList(p["enum"], "source or via rule enum")
}

// noneMode reads how a rule restricts harness or session_key: "none" for
// {"const": "none"}, "not none" for {"not": {"const": "none"}}, "any" when
// absent.
func (c *schemaCheck) noneMode(v any) string {
	if v == nil {
		return "any"
	}
	p := c.object(v, "harness or session_key rule")
	if p["const"] == NoSession {
		return "none"
	}
	if not, ok := p["not"].(map[string]any); ok && not["const"] == NoSession {
		return "not none"
	}
	return fmt.Sprintf("unrecognized %v", p)
}

// refs checks that every $ref under v points at a $defs entry that exists.
func (c *schemaCheck) refs(path string, v any) {
	switch v := v.(type) {
	case map[string]any:
		for key, child := range v {
			if key == "$ref" {
				ref, _ := child.(string)
				name, ok := strings.CutPrefix(ref, "#/$defs/")
				if _, exists := c.defs[name]; !ok || !exists {
					c.addf("%s: $ref %q doesn't resolve", path, ref)
				}
				continue
			}
			c.refs(path+"/"+key, child)
		}
	case []any:
		for i, child := range v {
			c.refs(path+"/"+strconv.Itoa(i), child)
		}
	}
}

func TestSchemaMatchesGo(t *testing.T) {
	for _, p := range checkSchema(loadSchema(t)) {
		t.Error(p)
	}
}

// at walks a decoded schema by object keys and returns the object there.
func at(t *testing.T, root map[string]any, keys ...string) map[string]any {
	t.Helper()
	m := root
	for i, k := range keys {
		next, ok := m[k].(map[string]any)
		if !ok {
			t.Fatalf("schema has no object at %q", strings.Join(keys[:i+1], "/"))
		}
		m = next
	}
	return m
}

// kindRuleAt returns the then.properties of kind k's allOf rule, and its
// index in allOf.
func kindRuleAt(t *testing.T, root map[string]any, k Kind) (map[string]any, int) {
	t.Helper()
	for i, r := range root["allOf"].([]any) {
		rule := r.(map[string]any)
		if at(t, rule, "if", "properties", "kind")["const"] == string(k) {
			return at(t, rule, "then", "properties"), i
		}
	}
	t.Fatalf("schema has no allOf rule for %q", k)
	return nil, -1
}

func TestSchemaDriftIsCaught(t *testing.T) {
	tests := []struct {
		name   string
		breaks func(t *testing.T, root map[string]any)
		want   string // a substring of one reported problem
	}{
		{"a kind missing from the kind enum", func(t *testing.T, root map[string]any) {
			def := at(t, root, "$defs", "kind")
			list := def["enum"].([]any)
			def["enum"] = list[:len(list)-1]
		}, "$defs/kind lists"},
		{"a kind with no $defs entry", func(t *testing.T, root map[string]any) {
			delete(at(t, root, "$defs"), "os.power")
		}, `no $defs entry for kind "os.power"`},
		{"a property Go doesn't have", func(t *testing.T, root map[string]any) {
			at(t, root, "$defs", "agent.queue", "properties")["prompt"] = map[string]any{"type": "string"}
		}, `property "prompt" has no Go field`},
		{"a Go field the schema doesn't have", func(t *testing.T, root map[string]any) {
			delete(at(t, root, "$defs", "agent.queue", "properties"), "len_chars")
		}, `no property "len_chars"`},
		{"an envelope field the schema doesn't have", func(t *testing.T, root map[string]any) {
			delete(at(t, root, "properties"), "boot_id")
		}, `event: the schema has no property "boot_id"`},
		{"an envelope field Go dropped", func(t *testing.T, root map[string]any) {
			at(t, root, "properties")["chain_hash"] = map[string]any{"type": "string"}
		}, `event: schema property "chain_hash" has no Go field`},
		{"a required list out of step", func(t *testing.T, root map[string]any) {
			at(t, root, "$defs", "agent.queue")["required"] = []any{"turn_key"}
		}, "agent.queue: the schema requires"},
		{"extra properties allowed", func(t *testing.T, root map[string]any) {
			at(t, root, "$defs", "os.power")["additionalProperties"] = true
		}, `os.power: the schema must set "additionalProperties": false`},
		{"a local field the schema doesn't mark", func(t *testing.T, root map[string]any) {
			delete(at(t, root, "$defs", "prompt.submit", "properties", "text"), "x-local")
		}, `prompt.submit.text: schema "x-local" is false`},
		{"a shared field the schema marks local", func(t *testing.T, root map[string]any) {
			at(t, root, "$defs", "agent.tool", "properties", "error_sig")["x-local"] = true
		}, `agent.tool.error_sig: schema "x-local" is true`},
		{"an enum value only the schema has", func(t *testing.T, root map[string]any) {
			def := at(t, root, "$defs", "power_op")
			def["enum"] = append(def["enum"].([]any), "hibernate")
		}, "$defs/power_op lists"},
		{"an enum Go doesn't have", func(t *testing.T, root map[string]any) {
			at(t, root, "$defs")["mood"] = map[string]any{"enum": []any{"calm", "stuck"}}
		}, "$defs/mood has no Go enum"},
		{"a field pointing at the wrong enum", func(t *testing.T, root map[string]any) {
			at(t, root, "$defs", "os.power", "properties", "op")["$ref"] = "#/$defs/git_op"
		}, `os.power.op: the Go type is enum "power_op"`},
		{"a kind rule that lets any source through", func(t *testing.T, root map[string]any) {
			props, _ := kindRuleAt(t, root, KindOSPresence)
			delete(props, "source")
		}, "os.presence rule: the schema allows sources"},
		{"a kind rule that lets any via through", func(t *testing.T, root map[string]any) {
			props, _ := kindRuleAt(t, root, KindCLITrack)
			delete(props, "via")
		}, "cli.track rule: the schema allows vias"},
		{"a kind rule with a via Go doesn't allow", func(t *testing.T, root map[string]any) {
			props, _ := kindRuleAt(t, root, KindGitChange)
			props["via"] = map[string]any{"const": "plugin"}
		}, "git.change rule: the schema allows vias"},
		{"a via value only Go has", func(t *testing.T, root map[string]any) {
			def := at(t, root, "$defs", "via")
			list := def["enum"].([]any)
			def["enum"] = slices.DeleteFunc(slices.Clone(list), func(v any) bool { return v == "bridge_unverified" })
		}, "$defs/via lists"},
		{"a kind rule that drops the session check", func(t *testing.T, root map[string]any) {
			props, _ := kindRuleAt(t, root, KindPromptSubmit)
			delete(props, "session_key")
		}, "prompt.submit rule: the schema's session_key"},
		{"a kind with no rule", func(t *testing.T, root map[string]any) {
			_, i := kindRuleAt(t, root, KindCLITrack)
			allOf := root["allOf"].([]any)
			root["allOf"] = append(allOf[:i:i], allOf[i+1:]...)
		}, `no rule for kind "cli.track"`},
		{"a rule that sends data to another kind", func(t *testing.T, root map[string]any) {
			props, _ := kindRuleAt(t, root, KindAgentQueue)
			props["data"] = map[string]any{"$ref": "#/$defs/agent.stop"}
		}, `agent.queue rule: data refers to "agent.stop"`},
		{"a dangling $ref", func(t *testing.T, root map[string]any) {
			at(t, root, "$defs", "agent.queue", "properties", "turn_key")["$ref"] = "#/$defs/turn"
		}, `$ref "#/$defs/turn" doesn't resolve`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := loadSchema(t)
			tt.breaks(t, root)
			problems := checkSchema(root)
			for _, p := range problems {
				if strings.Contains(p, tt.want) {
					return
				}
			}
			t.Errorf("checkSchema didn't report %q; it reported %q", tt.want, problems)
		})
	}
}
