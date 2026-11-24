// SPDX-License-Identifier: AGPL-3.0-or-later

// Package jsonschema checks JSON against a JSON Schema (draft 2020-12),
// for the keywords pairtime's schemas in docs/schema use, with the
// standard library alone. A keyword it doesn't know is an error, not a
// pass, so a schema can't outgrow it unnoticed. Parse looks at every
// subschema once, those under not and if included, and refuses a keyword
// it doesn't support, a $ref that doesn't resolve, a pattern that doesn't
// compile and a keyword whose value has the wrong type. So validating
// only ever reports how an instance breaks the schema, and not and if
// can't turn a broken subschema into a silent pass.
//
// Tests use it to check what a command prints against the schema that
// describes it, such as `pairtime today --json` against
// docs/schema/cli-today.schema.json; the client binary doesn't link it.
// It started as the validator in internal/timelines' tests, which now
// check fixture events against event.schema.json with it too.
package jsonschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// annotations are keywords that never make an instance invalid.
var annotations = []string{
	"$schema", "$id", "$defs", "$comment", "title", "description", "x-local", "contentEncoding",
	"default", "examples",
}

// Schema is a parsed schema.
type Schema struct {
	root     any
	patterns map[string]*regexp.Regexp
}

// Parse decodes a schema and checks every subschema in it (checkSchema).
func Parse(data []byte) (*Schema, error) {
	root, err := Decode(data)
	if err != nil {
		return nil, err
	}
	s := &Schema{root: root, patterns: map[string]*regexp.Regexp{}}
	if err := s.checkSchema("#", root); err != nil {
		return nil, err
	}
	return s, nil
}

// Decode decodes one JSON value, keeping numbers as json.Number, which is
// what Validate expects. Anything after the value but white space is an
// error, a stray '}' or ']' included, which Decoder.More doesn't report.
func Decode(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("jsonschema: more follows the JSON value")
	}
	return v, nil
}

// typeNames are the names "type" may use.
var typeNames = []string{"null", "boolean", "object", "array", "number", "integer", "string"}

// checkSchema checks schema, found at the JSON pointer at, and every
// subschema inside it: $defs, properties, additionalProperties, items,
// allOf, not, if, then and else. It returns the first problem: a schema
// that is neither an object nor a boolean, a keyword this validator
// doesn't support, a $ref that doesn't resolve, a pattern that doesn't
// compile, or a keyword whose value has the wrong type.
func (s *Schema) checkSchema(at string, schema any) error {
	sc, ok := schema.(map[string]any)
	if !ok {
		if _, ok := schema.(bool); ok {
			return nil
		}
		return fmt.Errorf("jsonschema: %s: a schema is an object or a boolean, got %s", at, jsonType(schema))
	}
	for _, k := range sortedKeys(sc) {
		kw, where := sc[k], at+"/"+pointerToken(k)
		bad := func(want string) error {
			return fmt.Errorf("jsonschema: %s: %s must be %s, got %s", at, k, want, jsonType(kw))
		}
		switch k {
		case "$ref":
			ref, ok := kw.(string)
			if !ok {
				return bad("a string")
			}
			if _, err := s.resolve(ref); err != nil {
				return fmt.Errorf("jsonschema: %s: %w", at, err)
			}
		case "$defs", "properties":
			subs, ok := kw.(map[string]any)
			if !ok {
				return bad("an object")
			}
			for _, name := range sortedKeys(subs) {
				if err := s.checkSchema(where+"/"+pointerToken(name), subs[name]); err != nil {
					return err
				}
			}
		case "additionalProperties", "items", "not", "if", "then", "else":
			if err := s.checkSchema(where, kw); err != nil {
				return err
			}
		case "allOf":
			list, ok := kw.([]any)
			if !ok || len(list) == 0 {
				return bad("a non-empty array")
			}
			for i, sub := range list {
				if err := s.checkSchema(fmt.Sprintf("%s/%d", where, i), sub); err != nil {
					return err
				}
			}
		case "type":
			if !validType(kw) {
				return bad(fmt.Sprintf("one of %q or a list of them", typeNames))
			}
		case "enum":
			if _, ok := kw.([]any); !ok {
				return bad("an array")
			}
		case "required":
			list, ok := kw.([]any)
			if !ok {
				return bad("an array of strings")
			}
			for _, r := range list {
				if _, ok := r.(string); !ok {
					return bad("an array of strings")
				}
			}
		case "minItems", "maxItems", "minLength", "maxLength", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum":
			if _, ok := asRat(kw); !ok {
				return bad("a number")
			}
		case "uniqueItems":
			if _, ok := kw.(bool); !ok {
				return bad("a boolean")
			}
		case "pattern":
			src, ok := kw.(string)
			if !ok {
				return bad("a string")
			}
			if _, err := s.pattern(src); err != nil {
				return fmt.Errorf("jsonschema: %s: pattern %q: %w", at, src, err)
			}
		case "const":
			// Any value.
		default:
			if !slices.Contains(annotations, k) {
				return fmt.Errorf("jsonschema: %s: schema keyword %q isn't supported by this validator", at, k)
			}
		}
	}
	return nil
}

// validType reports whether kw is a type name or a non-empty list of them.
func validType(kw any) bool {
	switch t := kw.(type) {
	case string:
		return slices.Contains(typeNames, t)
	case []any:
		for _, x := range t {
			name, ok := x.(string)
			if !ok || !slices.Contains(typeNames, name) {
				return false
			}
		}
		return len(t) > 0
	}
	return false
}

// pointerToken escapes a name as one token of a JSON pointer (RFC 6901).
func pointerToken(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
}

// ValidateJSON decodes data and returns every way it breaks the schema.
func (s *Schema) ValidateJSON(data []byte) ([]string, error) {
	v, err := Decode(data)
	if err != nil {
		return nil, err
	}
	return s.Validate(v), nil
}

// Validate returns every way v, a value from Decode, breaks the schema.
func (s *Schema) Validate(v any) []string {
	var errs []string
	s.check(&errs, "", s.root, v)
	return errs
}

// valid reports whether v passes schema, collecting nothing.
func (s *Schema) valid(schema, v any) bool {
	var errs []string
	s.check(&errs, "", schema, v)
	return len(errs) == 0
}

func (s *Schema) check(errs *[]string, path string, schema, v any) {
	addf := func(format string, args ...any) {
		where := path
		if where == "" {
			where = "(root)"
		}
		*errs = append(*errs, where+": "+fmt.Sprintf(format, args...))
	}
	switch sc := schema.(type) {
	case bool:
		if !sc {
			addf("the false schema matches nothing")
		}
	case map[string]any:
		s.checkObject(errs, path, sc, v, addf)
	default:
		addf("schema is %T, want an object or a boolean", schema)
	}
}

func (s *Schema) checkObject(errs *[]string, path string, sc map[string]any, v any, addf func(string, ...any)) {
	for _, k := range sortedKeys(sc) {
		kw := sc[k]
		switch k {
		case "$ref":
			ref, _ := kw.(string)
			target, err := s.resolve(ref)
			if err != nil {
				addf("%v", err)
				continue
			}
			s.check(errs, path, target, v)
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
			if s.valid(kw, v) {
				addf("%s matches a schema it must not match", short(v))
			}
		case "allOf":
			list, _ := kw.([]any)
			for i, sub := range list {
				var subErrs []string
				s.check(&subErrs, path, sub, v)
				for _, e := range subErrs {
					*errs = append(*errs, fmt.Sprintf("%s (allOf[%d])", e, i))
				}
			}
		case "if":
			if s.valid(kw, v) {
				if then, ok := sc["then"]; ok {
					s.check(errs, path, then, v)
				}
			} else if els, ok := sc["else"]; ok {
				s.check(errs, path, els, v)
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
					s.check(errs, path+"/"+name, sub, obj[name])
				}
			}
		case "additionalProperties":
			obj, ok := v.(map[string]any)
			if !ok {
				continue
			}
			props, _ := sc["properties"].(map[string]any)
			for _, name := range sortedKeys(obj) {
				if _, known := props[name]; !known {
					var sub []string
					s.check(&sub, path+"/"+name, kw, obj[name])
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
				s.check(errs, fmt.Sprintf("%s/%d", path, i), kw, x)
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
			re, err := s.pattern(src)
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
func (s *Schema) resolve(ref string) (any, error) {
	if !strings.HasPrefix(ref, "#") {
		return nil, fmt.Errorf("$ref %q: only references into this document are supported", ref)
	}
	node := s.root
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

// pattern compiles an ECMA-262 pattern as Go's RE2. Patterns in pairtime's
// schemas may use backslash-u escapes of four hex digits, which RE2 spells
// \x{XXXX}; nothing else they use differs between the two.
func (s *Schema) pattern(src string) (*regexp.Regexp, error) {
	if re, ok := s.patterns[src]; ok {
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
	s.patterns[src] = re
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
