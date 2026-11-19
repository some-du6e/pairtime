// SPDX-License-Identifier: AGPL-3.0-or-later

package timelines

// A JSON Schema (draft 2020-12) validator for the keywords that
// docs/schema/event.schema.json uses, so every fixture event can be checked
// against the schema with the standard library alone. A keyword it doesn't
// know is an error, not a pass, so the schema can't outgrow it unnoticed.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// annotations are keywords that never make an instance invalid.
var annotations = []string{
	"$schema", "$id", "$defs", "$comment", "title", "description", "x-local", "contentEncoding",
	"default", "examples",
}

type schemaValidator struct {
	root     any
	patterns map[string]*regexp.Regexp
}

func newSchemaValidator(root any) *schemaValidator {
	return &schemaValidator{root: root, patterns: map[string]*regexp.Regexp{}}
}

// decodeJSON decodes b keeping numbers as json.Number, which is what the
// validator expects for instances and schemas alike.
func decodeJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("more than one JSON value")
	}
	return v, nil
}

func loadEventSchema(t *testing.T) *schemaValidator {
	t.Helper()
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "..", "..", "docs", "schema", "event.schema.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := decodeJSON(b)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return newSchemaValidator(s)
}

// validate returns every way v breaks the root schema.
func (sv *schemaValidator) validate(v any) []string {
	var errs []string
	sv.check(&errs, "", sv.root, v)
	return errs
}

// valid reports whether v passes schema, collecting nothing.
func (sv *schemaValidator) valid(schema, v any) bool {
	var errs []string
	sv.check(&errs, "", schema, v)
	return len(errs) == 0
}

func (sv *schemaValidator) check(errs *[]string, path string, schema, v any) {
	addf := func(format string, args ...any) {
		where := path
		if where == "" {
			where = "(root)"
		}
		*errs = append(*errs, where+": "+fmt.Sprintf(format, args...))
	}
	switch s := schema.(type) {
	case bool:
		if !s {
			addf("the false schema matches nothing")
		}
		return
	case map[string]any:
		sv.checkObject(errs, path, s, v, addf)
	default:
		addf("schema is %T, want an object or a boolean", schema)
	}
}

func (sv *schemaValidator) checkObject(errs *[]string, path string, s map[string]any, v any, addf func(string, ...any)) {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		kw := s[k]
		switch k {
		case "$ref":
			ref, _ := kw.(string)
			target, err := sv.resolve(ref)
			if err != nil {
				addf("%v", err)
				continue
			}
			sv.check(errs, path, target, v)
		case "type":
			if !matchesType(kw, v) {
				addf("is %s, want type %v", jsonType(v), kw)
			}
		case "enum":
			list, _ := kw.([]any)
			if !slices.ContainsFunc(list, func(x any) bool { return jsonEqual(x, v) }) {
				addf("%s is not one of the enum values", short(v))
			}
		case "const":
			if !jsonEqual(kw, v) {
				addf("%s is not %s", short(v), short(kw))
			}
		case "not":
			if sv.valid(kw, v) {
				addf("%s matches a schema it must not match", short(v))
			}
		case "allOf":
			list, _ := kw.([]any)
			for i, sub := range list {
				var sub2 []string
				sv.check(&sub2, path, sub, v)
				for _, e := range sub2 {
					*errs = append(*errs, fmt.Sprintf("%s (allOf[%d])", e, i))
				}
			}
		case "if":
			if sv.valid(kw, v) {
				if then, ok := s["then"]; ok {
					sv.check(errs, path, then, v)
				}
			} else if els, ok := s["else"]; ok {
				sv.check(errs, path, els, v)
			}
		case "then", "else":
			// Applied with "if".
		case "properties":
			obj, ok := v.(map[string]any)
			if !ok {
				continue
			}
			props, _ := kw.(map[string]any)
			for _, name := range sortedKeys(obj) {
				if sub, ok := props[name]; ok {
					sv.check(errs, path+"/"+name, sub, obj[name])
				}
			}
		case "additionalProperties":
			obj, ok := v.(map[string]any)
			if !ok {
				continue
			}
			props, _ := s["properties"].(map[string]any)
			for _, name := range sortedKeys(obj) {
				if _, known := props[name]; !known {
					var sub []string
					sv.check(&sub, path+"/"+name, kw, obj[name])
					if len(sub) > 0 {
						addf("has a property the schema doesn't allow: %q", name)
					}
				}
			}
		case "required":
			obj, ok := v.(map[string]any)
			if !ok {
				continue
			}
			list, _ := kw.([]any)
			for _, r := range list {
				name, _ := r.(string)
				if _, ok := obj[name]; !ok {
					addf("is missing required property %q", name)
				}
			}
		case "items":
			arr, ok := v.([]any)
			if !ok {
				continue
			}
			for i, x := range arr {
				sv.check(errs, fmt.Sprintf("%s/%d", path, i), kw, x)
			}
		case "minItems", "maxItems":
			arr, ok := v.([]any)
			if !ok {
				continue
			}
			n, ok := asRat(kw)
			if !ok {
				addf("%s is not a number", k)
				continue
			}
			got := big.NewRat(int64(len(arr)), 1)
			if k == "minItems" && got.Cmp(n) < 0 || k == "maxItems" && got.Cmp(n) > 0 {
				addf("has %d items, breaking %s %s", len(arr), k, n.RatString())
			}
		case "uniqueItems":
			arr, ok := v.([]any)
			if !ok || kw != true {
				continue
			}
			for i := range arr {
				for j := range i {
					if jsonEqual(arr[i], arr[j]) {
						addf("items %d and %d are equal", j, i)
					}
				}
			}
		case "minLength", "maxLength":
			str, ok := v.(string)
			if !ok {
				continue
			}
			n, ok := asRat(kw)
			if !ok {
				addf("%s is not a number", k)
				continue
			}
			got := big.NewRat(int64(utf8.RuneCountInString(str)), 1)
			if k == "minLength" && got.Cmp(n) < 0 || k == "maxLength" && got.Cmp(n) > 0 {
				addf("has %s characters, breaking %s %s", got.RatString(), k, n.RatString())
			}
		case "pattern":
			str, ok := v.(string)
			if !ok {
				continue
			}
			src, _ := kw.(string)
			re, err := sv.pattern(src)
			if err != nil {
				addf("pattern %q: %v", src, err)
				continue
			}
			if !re.MatchString(str) {
				addf("%s doesn't match pattern %q", short(v), src)
			}
		case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum":
			got, ok := asRat(v)
			if !ok {
				continue
			}
			bound, ok := asRat(kw)
			if !ok {
				addf("%s is not a number", k)
				continue
			}
			c := got.Cmp(bound)
			bad := k == "minimum" && c < 0 || k == "maximum" && c > 0 ||
				k == "exclusiveMinimum" && c <= 0 || k == "exclusiveMaximum" && c >= 0
			if bad {
				addf("%s breaks %s %s", got.RatString(), k, bound.RatString())
			}
		default:
			if !slices.Contains(annotations, k) {
				addf("schema keyword %q isn't supported by this validator", k)
			}
		}
	}
}

// resolve follows a "#/..." JSON pointer into the root schema.
func (sv *schemaValidator) resolve(ref string) (any, error) {
	if !strings.HasPrefix(ref, "#") {
		return nil, fmt.Errorf("$ref %q: only references into this document are supported", ref)
	}
	node := sv.root
	ptr := strings.TrimPrefix(ref, "#")
	if ptr == "" {
		return node, nil
	}
	for _, tok := range strings.Split(strings.TrimPrefix(ptr, "/"), "/") {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		obj, ok := node.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("$ref %q: %q is not inside an object", ref, tok)
		}
		if node, ok = obj[tok]; !ok {
			return nil, fmt.Errorf("$ref %q doesn't resolve", ref)
		}
	}
	return node, nil
}

// pattern compiles an ECMA-262 pattern as Go's RE2. The schema's patterns
// use \uXXXX escapes, which RE2 spells \x{XXXX}; nothing else they use
// differs between the two.
func (sv *schemaValidator) pattern(src string) (*regexp.Regexp, error) {
	if re, ok := sv.patterns[src]; ok {
		return re, nil
	}
	var b strings.Builder
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c != '\\' || i+1 >= len(src) {
			b.WriteByte(c)
			continue
		}
		if src[i+1] == 'u' && i+6 <= len(src) && isHex4(src[i+2:i+6]) {
			b.WriteString(`\x{` + src[i+2:i+6] + `}`)
			i += 5
			continue
		}
		b.WriteByte(c)
		b.WriteByte(src[i+1])
		i++
	}
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, err
	}
	sv.patterns[src] = re
	return re, nil
}

func isHex4(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return false
		}
	}
	return len(s) == 4
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func asRat(v any) (*big.Rat, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return nil, false
	}
	r, ok := new(big.Rat).SetString(string(n))
	return r, ok
}

func jsonType(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	case json.Number:
		if r, ok := asRat(x); ok && r.IsInt() {
			return "integer"
		}
		return "number"
	}
	return fmt.Sprintf("%T", v)
}

// matchesType applies "type", a name or a list of names. An integer is
// also a number.
func matchesType(kw, v any) bool {
	var names []string
	switch t := kw.(type) {
	case string:
		names = []string{t}
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok {
				names = append(names, s)
			}
		}
	}
	got := jsonType(v)
	for _, n := range names {
		if n == got || n == "number" && got == "integer" {
			return true
		}
	}
	return false
}

// jsonEqual compares two decoded JSON values, numbers by value.
func jsonEqual(a, b any) bool {
	switch x := a.(type) {
	case json.Number:
		ra, ok1 := asRat(x)
		rb, ok2 := asRat(b)
		return ok1 && ok2 && ra.Cmp(rb) == 0
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !jsonEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, xv := range x {
			yv, ok := y[k]
			if !ok || !jsonEqual(xv, yv) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

func short(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	if len(b) > 60 {
		return string(b[:57]) + "..."
	}
	return string(b)
}

func TestSchemaValidator(t *testing.T) {
	cases := []struct {
		name   string
		schema string
		value  string
		valid  bool
	}{
		{"type accepts a matching type", `{"type": "string"}`, `"x"`, true},
		{"type rejects another type", `{"type": "string"}`, `1`, false},
		{"type integer rejects a fraction", `{"type": "integer"}`, `1.5`, false},
		{"type number accepts an integer", `{"type": "number"}`, `3`, true},
		{"type list accepts null", `{"type": ["number", "null"]}`, `null`, true},
		{"enum accepts a listed value", `{"enum": ["a", "b"]}`, `"b"`, true},
		{"enum rejects an unlisted value", `{"enum": ["a", "b"]}`, `"c"`, false},
		{"const compares numbers by value", `{"const": 10}`, `1e1`, true},
		{"const rejects a different value", `{"const": "os"}`, `"hook"`, false},
		{"not rejects what its schema matches", `{"not": {"const": "none"}}`, `"none"`, false},
		{"not accepts what its schema rejects", `{"not": {"const": "none"}}`, `"x"`, true},
		{"required rejects a missing property", `{"required": ["a"]}`, `{"b": 1}`, false},
		{"properties check each present property", `{"properties": {"a": {"type": "string"}}}`, `{"a": 1}`, false},
		{"additionalProperties false rejects an unknown property", `{"properties": {"a": {}}, "additionalProperties": false}`, `{"a": 1, "b": 2}`, false},
		{"additionalProperties false accepts known properties", `{"properties": {"a": {}}, "additionalProperties": false}`, `{"a": 1}`, true},
		{"items check every element", `{"items": {"type": "integer"}}`, `[1, "x"]`, false},
		{"maxItems rejects a long array", `{"maxItems": 1}`, `[1, 2]`, false},
		{"minItems rejects a short array", `{"minItems": 2}`, `[1]`, false},
		{"uniqueItems rejects a repeat", `{"uniqueItems": true}`, `["a", "a"]`, false},
		{"maxLength counts characters, not bytes", `{"maxLength": 2}`, `"éé"`, true},
		{"minLength rejects a short string", `{"minLength": 1}`, `""`, false},
		{"pattern with \\u escapes rejects a control character", `{"pattern": "^[^\\u0000-\\u001f]*$"}`, `"a\u0007b"`, false},
		{"pattern with \\u escapes accepts plain text", `{"pattern": "^[^\\u0000-\\u001f]*$"}`, `"a b"`, true},
		{"minimum rejects a smaller number", `{"minimum": 1}`, `0`, false},
		{"exclusiveMaximum rejects the bound itself", `{"exclusiveMaximum": 5}`, `5`, false},
		{"maximum accepts the bound itself", `{"maximum": 5}`, `5`, true},
		{"if and then apply then when if matches", `{"if": {"const": 1}, "then": {"const": 2}}`, `1`, false},
		{"if without a match skips then", `{"if": {"const": 1}, "then": {"const": 2}}`, `3`, true},
		{"allOf needs every schema", `{"allOf": [{"type": "integer"}, {"minimum": 5}]}`, `3`, false},
		{"$ref applies the referenced schema", `{"$defs": {"s": {"type": "string"}}, "$ref": "#/$defs/s"}`, `1`, false},
		{"$ref and its siblings both apply", `{"$defs": {"s": {"type": "string"}}, "$ref": "#/$defs/s", "minLength": 2}`, `"a"`, false},
		{"an unsupported keyword is an error", `{"patternProperties": {}}`, `{}`, false},
		{"annotations never fail", `{"description": "x", "x-local": true, "contentEncoding": "base64"}`, `"y"`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema, err := decodeJSON([]byte(tc.schema))
			if err != nil {
				t.Fatalf("schema: %v", err)
			}
			value, err := decodeJSON([]byte(tc.value))
			if err != nil {
				t.Fatalf("value: %v", err)
			}
			errs := newSchemaValidator(schema).validate(value)
			if got := len(errs) == 0; got != tc.valid {
				t.Errorf("valid = %v, want %v; errors: %q", got, tc.valid, errs)
			}
		})
	}
}

// TestSchemaRejectsBrokenEvents checks the validator against the real
// schema: an event from a fixture passes, and each way of breaking it
// fails, so a passing fixture means something.
func TestSchemaRejectsBrokenEvents(t *testing.T) {
	sv := loadEventSchema(t)
	good := `{"event_id":"019aa0b8-6000-7000-8000-000000000001","device_id":"d_1","seq":1,"t":1795524000000,"mono":5,"boot_id":"b_1","source":"os","via":"os","harness":"none","session_key":"none","kind":"os.presence","data":{"idle_any_s":3.5,"idle_keys_s":null,"locked":false}}`
	cases := []struct {
		name  string
		edit  func(string) string
		valid bool
	}{
		{"a well-formed presence sample passes", func(s string) string { return s }, true},
		{"an unknown envelope field fails", func(s string) string { return strings.Replace(s, `"seq":1,`, `"seq":1,"chain_hash":"x",`, 1) }, false},
		{"an unknown data field fails", func(s string) string { return strings.Replace(s, `"locked":false`, `"locked":false,"lock":true`, 1) }, false},
		{"a missing data field fails", func(s string) string { return strings.Replace(s, `,"locked":false`, ``, 1) }, false},
		{"an os event from a harness fails", func(s string) string { return strings.Replace(s, `"harness":"none"`, `"harness":"cursor"`, 1) }, false},
		{"an os event that didn't arrive via os fails", func(s string) string { return strings.Replace(s, `"via":"os"`, `"via":"plugin"`, 1) }, false},
		{"an unknown kind fails", func(s string) string { return strings.Replace(s, `"os.presence"`, `"os.idle"`, 1) }, false},
		{"a time in seconds fails", func(s string) string { return strings.Replace(s, `"t":1795524000000`, `"t":1795524000`, 1) }, false},
		{"a key with a dot fails", func(s string) string { return strings.Replace(s, `"d_1"`, `"d.1"`, 1) }, false},
		{"a negative idle time fails", func(s string) string { return strings.Replace(s, `3.5`, `-3.5`, 1) }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := decodeJSON([]byte(tc.edit(good)))
			if err != nil {
				t.Fatal(err)
			}
			errs := sv.validate(v)
			if got := len(errs) == 0; got != tc.valid {
				t.Errorf("valid = %v, want %v; errors: %q", got, tc.valid, errs)
			}
		})
	}
}
