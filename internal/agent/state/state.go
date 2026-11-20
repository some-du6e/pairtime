// SPDX-License-Identifier: AGPL-3.0-or-later

// Package state keeps state.json, what the agent resumes from after a
// restart: scorer state, tailer cursors, the upload cursor, the project map
// and consent answers, as the work packages that own them arrive
// (DESIGN 3.5).
//
// Each component owns one top-level key and keeps its own JSON there, so
// adding a component changes nobody else's. Everything in state.json can be
// rebuilt from the ledger and the harness logs, so a state.json this
// release can't read is set aside and the agent starts afresh.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"sync"

	"github.com/some-du6e/pairtime/internal/agent/home"
)

// version is the file's format, stored under the key "v".
const version = 1

// Store is state.json in memory. Its methods are safe to call from several
// goroutines.
type Store struct {
	path string

	mu   sync.Mutex
	keys map[string]json.RawMessage
}

// Open reads the state file name. A missing file opens an empty store. A
// file that isn't a state.json this release reads is renamed to name+".bad",
// replacing an older one, and the store opens empty; problem says so.
func Open(name string) (s *Store, problem string, err error) {
	s = &Store{path: name, keys: make(map[string]json.RawMessage)}
	data, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return s, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	keys, why := decode(data)
	if why != "" {
		if err := os.Rename(name, name+".bad"); err != nil {
			return nil, "", err
		}
		return s, fmt.Sprintf("state.json %s, so it was moved to state.json.bad and the agent started afresh", why), nil
	}
	s.keys = keys
	return s, "", nil
}

// decode parses a state file, or says why it can't.
func decode(data []byte) (map[string]json.RawMessage, string) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, "isn't a JSON object"
	}
	var v int
	if raw, ok := keys["v"]; !ok || json.Unmarshal(raw, &v) != nil || v != version {
		return nil, fmt.Sprintf("isn't format version %d", version)
	}
	delete(keys, "v")
	return keys, ""
}

// Get decodes the value stored under key into v, and reports whether there
// was one.
func (s *Store) Get(key string, v any) (bool, error) {
	s.mu.Lock()
	raw, ok := s.keys[key]
	s.mu.Unlock()
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, v)
}

// Put stores v under key, in memory. Save writes it to the file.
func (s *Store) Put(key string, v any) error {
	if key == "v" {
		return errors.New(`state: the key "v" holds the format version`)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.keys[key] = raw
	s.mu.Unlock()
	return nil
}

// Save writes every key to the file at once, with home.WriteFile.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]json.RawMessage, len(s.keys)+1)
	for k, raw := range s.keys {
		out[k] = raw
	}
	out["v"] = json.RawMessage(strconv.Itoa(version))
	data, err := json.Marshal(out)
	if err != nil {
		return err
	}
	return home.WriteFile(s.path, append(data, '\n'))
}
