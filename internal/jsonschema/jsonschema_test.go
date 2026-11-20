// SPDX-License-Identifier: AGPL-3.0-or-later

package jsonschema

import "testing"

// bs is one backslash, so a pattern with a backslash-u escape can be
// written without one in this file.
const bs = `\`

func TestValidate(t *testing.T) {
	controls := `{"pattern": "^[^` + bs + bs + `u0000-` + bs + bs + `u001f]*$"}`
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
		{"a pattern with backslash-u escapes rejects a control character", controls, `"a` + bs + `u0007b"`, false},
		{"a pattern with backslash-u escapes accepts plain text", controls, `"a b"`, true},
		{"minimum rejects a smaller number", `{"minimum": 1}`, `0`, false},
		{"exclusiveMaximum rejects the bound itself", `{"exclusiveMaximum": 5}`, `5`, false},
		{"maximum accepts the bound itself", `{"maximum": 5}`, `5`, true},
		{"if and then apply then when if matches", `{"if": {"const": 1}, "then": {"const": 2}}`, `1`, false},
		{"if without a match skips then", `{"if": {"const": 1}, "then": {"const": 2}}`, `3`, true},
		{"if without a match applies else", `{"if": {"const": 1}, "else": {"const": 2}}`, `3`, false},
		{"allOf needs every schema", `{"allOf": [{"type": "integer"}, {"minimum": 5}]}`, `3`, false},
		{"$ref applies the referenced schema", `{"$defs": {"s": {"type": "string"}}, "$ref": "#/$defs/s"}`, `1`, false},
		{"$ref and its siblings both apply", `{"$defs": {"s": {"type": "string"}}, "$ref": "#/$defs/s", "minLength": 2}`, `"a"`, false},
		{"a $ref that doesn't resolve is an error", `{"$ref": "#/$defs/missing"}`, `1`, false},
		{"a $ref to another document is an error", `{"$ref": "event.schema.json#/$defs/key"}`, `1`, false},
		{"an unsupported keyword is an error", `{"patternProperties": {}}`, `{}`, false},
		{"format is unsupported, so a schema can't lean on it unchecked", `{"format": "date"}`, `"2026-11-24"`, false},
		{"annotations never fail", `{"description": "x", "x-local": true, "contentEncoding": "base64"}`, `"y"`, true},
		{"the true schema accepts anything", `true`, `{"a": [1]}`, true},
		{"the false schema rejects everything", `false`, `null`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Parse([]byte(tc.schema))
			if err != nil {
				t.Fatalf("schema: %v", err)
			}
			errs, err := s.ValidateJSON([]byte(tc.value))
			if err != nil {
				t.Fatalf("value: %v", err)
			}
			if got := len(errs) == 0; got != tc.valid {
				t.Errorf("valid = %v, want %v; errors: %q", got, tc.valid, errs)
			}
		})
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		name    string
		data    string
		wantErr bool
	}{
		{"an object is a schema", `{"type": "string"}`, false},
		{"a boolean is a schema", `true`, false},
		{"a number isn't a schema", `3`, true},
		{"two values aren't one schema", `{} {}`, true},
		{"broken JSON isn't a schema", `{"type":`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.data))
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, want an error: %v", err, tc.wantErr)
			}
		})
	}
}
