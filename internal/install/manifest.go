// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// manifestVersion is the format of manifest.json.
const manifestVersion = 1

// manifest is manifest.json: every target pairtime wrote outside its own
// directories, what it wrote there, and where the copy of what was there
// before is. Uninstall works from it alone.
type manifest struct {
	V       int      `json:"v"`
	Targets []record `json:"targets"`
}

// record is what the manifest keeps about one target.
type record struct {
	Target Target `json:"target"`
	// Base is what the target held before pairtime, which uninstall puts
	// back when the target still holds what pairtime wrote.
	Base saved `json:"base"`
	// Written is the digest of what pairtime last wrote there.
	Written string `json:"written"`
	// Dirs are the directories pairtime made for the target, outermost
	// first.
	Dirs []string `json:"dirs,omitempty"`
	// Edits are the edits pairtime made there, in order.
	Edits []editRecord `json:"edits"`
}

// saved says where the copy of a target's base is.
type saved struct {
	// Exists is false when the target wasn't there before pairtime.
	Exists bool `json:"exists"`
	// Backup is the copy's file name in the backups directory.
	Backup string `json:"backup,omitempty"`
	// Digest is the base's digest, which the copy is checked against.
	Digest string `json:"digest,omitempty"`
	Mode   uint32 `json:"mode,omitempty"`
	Type   uint32 `json:"type,omitempty"`
}

// editRecord names one edit: the item it belongs to and its kind.
type editRecord struct {
	Item string `json:"item"`
	Kind string `json:"kind"`
}

// pendingBackup is a copy Apply writes before it changes anything.
type pendingBackup struct {
	name string
	data []byte
}

// find returns the record of t, or nil.
func (m *manifest) find(t Target) *record {
	for i := range m.Targets {
		if m.Targets[i].Target == t {
			return &m.Targets[i]
		}
	}
	return nil
}

// clone returns a deep copy of m, for a plan to change.
func (m *manifest) clone() *manifest {
	c := &manifest{V: m.V}
	for _, r := range m.Targets {
		r.Dirs = append([]string(nil), r.Dirs...)
		r.Edits = append([]editRecord(nil), r.Edits...)
		c.Targets = append(c.Targets, r)
	}
	return c
}

// loadManifest reads manifest.json, and returns with it the digest of its
// bytes, which Apply checks to see that nothing changed it in between. A
// missing manifest is an empty one with digest "".
func (in *Installer) loadManifest() (*manifest, string, error) {
	data, err := os.ReadFile(in.manifestPath())
	if errors.Is(err, fs.ErrNotExist) {
		return &manifest{V: manifestVersion}, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, "", fmt.Errorf("%s is damaged (%v). It records what pairtime install changed, so install and uninstall change nothing until it is repaired", in.manifestPath(), err)
	}
	if m.V != manifestVersion {
		return nil, "", fmt.Errorf("%s has format %d, which this version of pairtime can't read", in.manifestPath(), m.V)
	}
	return &m, sum(data), nil
}

// encodeManifest returns m as manifest.json's bytes, or nil when m records
// nothing, which means there should be no manifest.json.
func encodeManifest(m *manifest) ([]byte, error) {
	if len(m.Targets) == 0 {
		return nil, nil
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// save names the copy of base for target t, and returns what the manifest
// keeps about it and the backup to write, nil when there is nothing to
// copy.
func save(t Target, base Content) (saved, *pendingBackup) {
	if !base.Exists {
		return saved{}, nil
	}
	name := backupName(t)
	s := saved{Exists: true, Backup: name, Digest: base.digest(), Mode: uint32(base.Mode), Type: base.Type}
	return s, &pendingBackup{name: name, data: append([]byte(nil), base.Data...)}
}

// backupName returns a new, unique name for a copy of what t holds, which
// says where it came from.
func backupName(t Target) string {
	what := "registry-" + t.Name
	if !t.isRegistry() {
		what = filepath.Base(t.Path)
	}
	clean := strings.Map(func(r rune) rune {
		if ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, what)
	return strings.ToLower(rand.Text()[:12]) + "-" + clean
}

// loadSaved returns the base that s describes, checked against its digest.
func (in *Installer) loadSaved(s saved) (Content, error) {
	if !s.Exists {
		return Content{}, nil
	}
	if s.Backup == "" || s.Backup != filepath.Base(s.Backup) {
		return Content{}, fmt.Errorf("the manifest names no usable backup (%q)", s.Backup)
	}
	data, err := os.ReadFile(filepath.Join(in.backupsDir(), s.Backup))
	if err != nil {
		return Content{}, fmt.Errorf("the copy of what was there before pairtime can't be read: %w", err)
	}
	c := Content{Exists: true, Data: data, Mode: fs.FileMode(s.Mode), Type: s.Type}
	if c.digest() != s.Digest {
		return Content{}, fmt.Errorf("the copy of what was there before pairtime, %s, has changed", filepath.Join(in.backupsDir(), s.Backup))
	}
	return c, nil
}
