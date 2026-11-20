// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// dirInfo is a directory's FileInfo that knows only its mode, which is all
// closedDir reads.
type dirInfo struct {
	fs.FileInfo
	mode fs.FileMode
}

func (d dirInfo) Mode() fs.FileMode { return fs.ModeDir | d.mode }

func TestClosedDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory modes are a Unix check")
	}
	tests := []struct {
		name    string
		modes   map[string]fs.FileMode
		dir     string
		want    string
		wantErr bool
	}{
		{name: "a 0700 database directory shuts group and others out", modes: map[string]fs.FileMode{"/data": 0o700, "/": 0o755}, dir: "/data", want: "/data"},
		{
			name:  "a directory others can enter is closed by one above it, as t.TempDir() makes it",
			modes: map[string]fs.FileMode{"/tmp/TestX/001": 0o755, "/tmp/TestX": 0o700, "/tmp": 0o777 | fs.ModeSticky, "/": 0o755},
			dir:   "/tmp/TestX/001",
			want:  "/tmp/TestX",
		},
		{name: "0755 all the way up lets everyone in, as /data is when Docker creates it for a bind mount", modes: map[string]fs.FileMode{"/data": 0o755, "/": 0o755}, dir: "/data"},
		{name: "a shared directory such as /tmp lets everyone in", modes: map[string]fs.FileMode{"/tmp": 0o777 | fs.ModeSticky, "/": 0o755}, dir: "/tmp"},
		{name: "directories the group can enter let the group read the copies", modes: map[string]fs.FileMode{"/data": 0o750, "/": 0o750}, dir: "/data"},
		{name: "a directory others can enter but not list still lets them open a file by its name", modes: map[string]fs.FileMode{"/data": 0o711, "/": 0o755}, dir: "/data"},
		{name: "a directory group and others can list but not enter keeps its files private", modes: map[string]fs.FileMode{"/data": 0o744, "/": 0o755}, dir: "/data", want: "/data"},
		{name: "a directory that can't be read is an error, not a pass", modes: map[string]fs.FileMode{"/": 0o755}, dir: "/data", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stat := func(name string) (fs.FileInfo, error) {
				m, ok := tt.modes[name]
				if !ok {
					return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
				}
				return dirInfo{mode: m}, nil
			}
			got, err := closedDir(tt.dir, stat)
			if (err != nil) != tt.wantErr {
				t.Fatalf("closedDir(%q) error = %v, want an error: %v", tt.dir, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("closedDir(%q) = %q, want %q", tt.dir, got, tt.want)
			}
		})
	}
}

func TestOpenRefusesOpenDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory modes are a Unix check")
	}
	tests := []struct {
		name     string
		open     bool // let group and others into the test's own 0700 directory too
		noCreate bool
		wantErr  string // substring; "" means Open must succeed
	}{
		{name: "a database directory others can enter is fine under a directory that shuts them out"},
		{name: "a database that group and others can reach through every directory is refused, and no file is created", open: true, wantErr: "chmod 700 "},
		{name: "with NoCreate, an existing database they can reach is refused too", open: true, noCreate: true, wantErr: "chmod 700 "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			// t.TempDir makes dir with mode 0777 less the umask, inside a 0700
			// directory of the test's own.
			dir := t.TempDir()
			path := filepath.Join(dir, "pairtime.db")
			if tt.noCreate {
				db, err := Open(ctx, Options{Engine: SQLite, Path: path})
				if err != nil {
					t.Fatal(err)
				}
				db.Close()
			}
			chmod := []string{dir}
			if tt.open {
				chmod = append(chmod, filepath.Dir(dir))
			}
			for _, d := range chmod {
				if err := os.Chmod(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if tt.open {
				resolved, err := filepath.EvalSymlinks(dir)
				if err != nil {
					t.Fatal(err)
				}
				if closed, err := closedDir(resolved, os.Stat); err != nil || closed != "" {
					t.Skipf("%q, above the test's directory, already shuts group and others out (stat: %v)", closed, err)
				}
			}

			db, err := Open(ctx, Options{Engine: SQLite, Path: path, NoCreate: tt.noCreate})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Open: %v", err)
				}
				db.Close()
				return
			}
			if err == nil {
				db.Close()
				t.Fatal("Open succeeded")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Open error = %v, want one containing %q", err, tt.wantErr)
			}
			if !tt.noCreate {
				if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("the refused Open left %s behind (stat: %v)", path, err)
				}
			}
		})
	}
}
