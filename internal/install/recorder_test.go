// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// keyedEdit adds the line "key = <key>" to a file, the way WP-IN3 adds its
// line to ~/.wakatime.cfg, and records the key, so that its undo can tell
// its line from a line the teen added after install.
type keyedEdit struct{ path, key string }

const kindKeyed = "test.keyed"

func (e keyedEdit) Target() Target { return File(e.path) }

func (e keyedEdit) Kind() string { return kindKeyed }

func (e keyedEdit) line() string { return "key = " + e.key + "\n" }

func (e keyedEdit) Apply(cur Content) (Content, error) {
	if bytes.Contains(cur.Data, []byte(e.line())) {
		return cur, nil
	}
	return Content{Exists: true, Data: append(append([]byte(nil), cur.Data...), e.line()...), Mode: cur.Mode}, nil
}

func (e keyedEdit) Record() json.RawMessage {
	data, _ := json.Marshal(map[string]string{"key": e.key})
	return data
}

// undoKeyed takes out the line of the key data names, and only that line.
func undoKeyed(cur, _ Content, data json.RawMessage) (Content, error) {
	var rec struct{ Key string }
	if err := json.Unmarshal(data, &rec); err != nil || rec.Key == "" {
		return Content{}, err
	}
	cur.Data = bytes.Replace(cur.Data, []byte(keyedEdit{key: rec.Key}.line()), nil, 1)
	return cur, nil
}

func TestEditsRecordWhatTheirUndoNeeds(t *testing.T) {
	undoers[kindKeyed] = quiet(undoKeyed)
	t.Cleanup(func() { delete(undoers, kindKeyed) })
	tests := []struct {
		name string
		// keys are the keys of successive installs.
		keys []string
		// after is a line the teen adds after the installs, so that
		// uninstall takes out only pairtime's line by its key.
		after string
		want  string // what uninstall leaves
	}{
		{"uninstall takes out the line whose key the manifest recorded, and a line added since stays",
			[]string{"k1"}, "key = other\n", "a = 1\nkey = other\n"},
		{"a reinstall with a new key records it in place of the old one, and uninstall takes out the new key's line",
			[]string{"k1", "k2"}, "key = other\n", "a = 1\nkey = k1\nkey = other\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "app.cfg")
			writeFile(t, path, []byte("a = 1\n"))
			in := newInstaller(t)
			for _, k := range tt.keys {
				install(t, in, Item{ID: "keyed", Edits: []Edit{keyedEdit{path: path, key: k}}})
			}
			last := tt.keys[len(tt.keys)-1]
			if m := string(readOrNil(t, in.manifestPath())); !strings.Contains(m, `"data": {`) || !strings.Contains(m, `"key": "`+last+`"`) {
				t.Errorf("the manifest doesn't record the last install's key %q:\n%s", last, m)
			}
			if tt.after != "" {
				writeFile(t, path, append(readOrNil(t, path), tt.after...))
			}
			uninstall(t, in)
			if got := string(readOrNil(t, path)); got != tt.want {
				t.Errorf("after uninstall the file holds %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPlanInstallRefusesDataThatIsntJSON(t *testing.T) {
	tests := []struct {
		name string
		data json.RawMessage
	}{
		{"an edit that records data that isn't JSON is refused", json.RawMessage("{key")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			undoers[kindKeyed] = quiet(undoKeyed)
			t.Cleanup(func() { delete(undoers, kindKeyed) })
			item := Item{ID: "bad", Edits: []Edit{badRecord{keyedEdit{path: filepath.Join(t.TempDir(), "x"), key: "k"}, tt.data}}}
			if _, err := newInstaller(t).PlanInstall([]Item{item}); err == nil || !strings.Contains(err.Error(), "isn't JSON") {
				t.Errorf("PlanInstall = %v, want an error about the data", err)
			}
		})
	}
}

// badRecord is a keyedEdit that records data of its own.
type badRecord struct {
	keyedEdit
	data json.RawMessage
}

func (e badRecord) Record() json.RawMessage { return e.data }

func TestPlanInstallRefusesTwoEditsOfAKindForOneTarget(t *testing.T) {
	undoers[kindKeyed] = quiet(undoKeyed)
	t.Cleanup(func() { delete(undoers, kindKeyed) })
	tests := []struct {
		name  string
		edits func(dir string) []Edit
		ok    bool
	}{
		{"an item with two edits of one kind for one file is refused, since the manifest keeps one record for both", func(dir string) []Edit {
			return []Edit{keyedEdit{filepath.Join(dir, "app.cfg"), "k1"}, keyedEdit{filepath.Join(dir, "app.cfg"), "k2"}}
		}, false},
		{"an item with edits of one kind for two files is planned", func(dir string) []Edit {
			return []Edit{keyedEdit{filepath.Join(dir, "a.cfg"), "k1"}, keyedEdit{filepath.Join(dir, "b.cfg"), "k2"}}
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := newInstaller(t).PlanInstall([]Item{{ID: "keyed", Edits: tt.edits(t.TempDir())}})
			if p != nil {
				p.Close()
			}
			if (err == nil) != tt.ok {
				t.Errorf("PlanInstall = %v, want it to plan: %v", err, tt.ok)
			}
		})
	}
}
