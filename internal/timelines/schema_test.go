// SPDX-License-Identifier: AGPL-3.0-or-later

package timelines

// Fixture events are checked against docs/schema/event.schema.json with
// internal/jsonschema. Its Parse refuses a keyword it doesn't support
// anywhere in the schema, under not and if included, and a $ref that
// doesn't resolve, so the schema can't outgrow the check unnoticed, and a
// broken subschema under not or if can't turn into a silent pass.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/jsonschema"
)

// loadEventSchema parses docs/schema/event.schema.json.
func loadEventSchema(t *testing.T) *jsonschema.Schema {
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
	s, err := jsonschema.Parse(b)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return s
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
			v, err := jsonschema.Decode([]byte(tc.edit(good)))
			if err != nil {
				t.Fatal(err)
			}
			errs := sv.Validate(v)
			if got := len(errs) == 0; got != tc.valid {
				t.Errorf("valid = %v, want %v; errors: %q", got, tc.valid, errs)
			}
		})
	}
}
