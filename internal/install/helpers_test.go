// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// testExe is the binary's path on each OS, where CP 7 puts it. It is only
// a string in the files install writes, so a test can plan for any OS.
var testExe = map[string]string{
	"linux":   "/home/teen/.local/bin/pairtime",
	"darwin":  "/Users/teen/.local/bin/pairtime",
	"windows": `C:\Users\teen\AppData\Local\pairtime\bin\pairtime.exe`,
}

// testEnv returns an Env for goos in a new, empty home directory.
func testEnv(t *testing.T, goos string, vars map[string]string) Env {
	t.Helper()
	return Env{GOOS: goos, Home: t.TempDir(), Getenv: func(k string) string { return vars[k] }, Exe: testExe[goos]}
}

// newInstaller returns an installer whose directory is in a new temporary
// directory, with a registry in memory.
func newInstaller(t *testing.T) *Installer {
	t.Helper()
	return &Installer{Dir: filepath.Join(t.TempDir(), "pairtime", "install"), Registry: fakeRegistry{}}
}

// fakeRegistry is HKEY_CURRENT_USER in a map, keyed by key and value name.
type fakeRegistry map[[2]string]regValue

type regValue struct {
	value string
	typ   uint32
}

func (r fakeRegistry) Get(key, name string) (string, uint32, bool, error) {
	v, ok := r[[2]string{key, name}]
	return v.value, v.typ, ok, nil
}

func (r fakeRegistry) Set(key, name, value string, typ uint32) error {
	r[[2]string{key, name}] = regValue{value: value, typ: typ}
	return nil
}

func (r fakeRegistry) Delete(key, name string) error {
	delete(r, [2]string{key, name})
	return nil
}

// install plans items and applies the plan, failing the test on an error.
func install(t *testing.T, in *Installer, items ...Item) *Plan {
	t.Helper()
	p, err := in.PlanInstall(items)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	if err := p.Apply(); err != nil {
		t.Fatalf("Apply of the install plan: %v", err)
	}
	return p
}

// uninstall plans uninstall for items and applies it, failing the test on
// an error or a problem.
func uninstall(t *testing.T, in *Installer, items ...Item) *Plan {
	t.Helper()
	p, err := in.PlanUninstall(items...)
	if err != nil {
		t.Fatalf("PlanUninstall: %v", err)
	}
	if len(p.Problems) > 0 {
		t.Fatalf("uninstall has problems: %q", p.Problems)
	}
	if err := p.Apply(); err != nil {
		t.Fatalf("Apply of the uninstall plan: %v", err)
	}
	return p
}

// readOrNil returns the file's bytes, or nil when it isn't there.
func readOrNil(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// checkGone fails the test if path exists.
func checkGone(t *testing.T, path, what string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s %s is still there after uninstall (%v)", what, path, err)
	}
}

// decode decodes JSON data, after a byte order mark, as encoding/json does
// with UseNumber.
func decode(t *testing.T, data []byte) any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		t.Fatalf("encoding/json can't decode %q: %v", data, err)
	}
	return v
}
