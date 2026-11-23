// SPDX-License-Identifier: AGPL-3.0-or-later

package publisher

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// watched writes through to a real file and, before each write, looks at
// every name in the root's directory: none but the dot-name may hold any
// of the root yet.
type watched struct {
	*os.File
	t          *testing.T
	dir, final string
	// failAfter, when not negative, fails the write once that many bytes
	// are in, as a full disk would.
	failAfter int
	written   int
}

func (w *watched) Write(p []byte) (int, error) {
	w.t.Helper()
	if _, err := os.Lstat(w.final); err == nil {
		w.t.Errorf("%s exists while its dot-file is written", w.final)
	}
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		w.t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name()[0] != '.' {
			w.t.Errorf("%s holds %s while a root is written under its dot-name", w.dir, e.Name())
		}
	}
	if w.failAfter >= 0 && w.written+len(p) > w.failAfter {
		n, _ := w.File.Write(p[:w.failAfter-w.written])
		w.written += n
		return n, errors.New("no space left on device")
	}
	n, err := w.File.Write(p)
	w.written += n
	return n, err
}

func TestWriteOnce(t *testing.T) {
	data := []byte("{\n  \"format\": \"pairtime-day-root-v1\",\n  \"day\": 20776\n}\n")
	tests := []struct {
		name string
		// stale, when set, is a dot-file a crash left behind.
		stale     []byte
		failAfter int
		wantErr   bool
	}{
		{name: "a root is written under its dot-name and renamed, so its own name never holds part of it, and no dot-file is left", failAfter: -1},
		{name: "a write that fails leaves neither the root's name nor its dot-file, and the next try writes it whole", failAfter: 10, wantErr: true},
		{name: "a dot-file a crash left behind is written over and renamed, never published as it was", stale: []byte("{\"da"), failAfter: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "witness", "2026")
			final := filepath.Join(dir, "2026-11-19.json")
			dot := filepath.Join(dir, ".2026-11-19.json.tmp")
			if tt.stale != nil {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(dot, tt.stale, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var opened []string
			o := Options{create: func(name string) (file, error) {
				opened = append(opened, name)
				f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
				if err != nil {
					return nil, err
				}
				return &watched{File: f, t: t, dir: dir, final: final, failAfter: tt.failAfter}, nil
			}}
			err := o.writeOnce(final, data)
			if (err != nil) != tt.wantErr {
				t.Fatalf("writeOnce error %v, want an error %v", err, tt.wantErr)
			}
			if !slices.Equal(opened, []string{dot}) {
				t.Errorf("writeOnce wrote through %v, want only %s", opened, dot)
			}
			if tt.wantErr {
				for _, p := range []string{final, dot} {
					if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
						t.Errorf("%s is there after a failed write: %v", p, err)
					}
				}
				o.create = nil
				if err := o.writeOnce(final, data); err != nil {
					t.Fatalf("the next try: %v", err)
				}
			}
			got, err := os.ReadFile(final)
			if err != nil || !bytes.Equal(got, data) {
				t.Errorf("%s holds %q (%v), want %q", final, got, err, data)
			}
			if _, err := os.Lstat(dot); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the dot-file is still there: %v", err)
			}
		})
	}
}
