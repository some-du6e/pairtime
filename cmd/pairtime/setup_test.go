// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
	"github.com/some-du6e/pairtime/internal/install"
)

// TestAgentCall checks that install and uninstall send the agent's bearer
// token only while an agent holds the data directory's lock, so that the
// port.json an agent left when it crashed hands the token to nothing that
// took its port since (#124).
func TestAgentCall(t *testing.T) {
	tests := []struct {
		name    string
		locked  bool // an agent holds the data directory's lock
		wantErr error
	}{
		{"with no agent holding the lock, port.json's port gets no token", false, errAgentDown},
		{"while an agent holds the lock, its port gets the token", true, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			got := make(chan string, 1)
			srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case got <- r.Header.Get("Authorization"):
				default:
				}
				fmt.Fprint(w, `{"pid": 4242, "version": {"version": "test"}}`)
			})}
			go srv.Serve(ln)
			t.Cleanup(func() { srv.Close() })
			token := localhttp.NewToken()
			pf := localhttp.PortFile{Port: ln.Addr().(*net.TCPAddr).Port, Token: token, PID: 4242}
			if err := localhttp.WritePortFile(dirs.PortFile(), pf); err != nil {
				t.Fatal(err)
			}
			if tt.locked {
				l, err := home.Acquire(dir)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { l.Release() })
			}
			st, err := queryAgent(dirs)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("queryAgent = %+v, %v; want error %v", st, err, tt.wantErr)
			}
			select {
			case auth := <-got:
				if !tt.locked {
					t.Errorf("with no agent holding the lock, the port got %q", auth)
				} else if auth != "Bearer "+token {
					t.Errorf("the agent got Authorization %q, want the token from port.json", auth)
				}
			default:
				if tt.locked {
					t.Error("the agent holding the lock never heard from queryAgent")
				}
			}
		})
	}
}

// TestTidy checks that a command that changed nothing takes away the data
// directory that planning made, but never a lock file that someone holds:
// the agent's and the installer's locks are on their files, so deleting
// one would let a second agent or install lock a new file beside it (#124).
func TestTidy(t *testing.T) {
	tests := []struct {
		name string
		hold string // the lock another process holds: "", "agent" or "install"
		gone bool   // the data directory is gone afterwards
	}{
		{"with no lock held, both lock files go, and so does the data directory", "", true},
		{"the agent's lock file stays while an agent holds it, and so does the data directory", "agent", false},
		{"the installer's lock file stays while another install holds it, and so does the data directory", "install", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := filepath.Join(t.TempDir(), "data")
			dirs := home.Dirs{Data: data, Config: data, Runtime: data}
			in := &install.Installer{Dir: filepath.Join(data, "install")}
			missing := missingDirs(data)
			// What planning and looking for an agent leave behind: the data
			// directory with both lock files.
			p, err := in.PlanInstall(nil)
			if err != nil {
				t.Fatal(err)
			}
			p.Close()
			agentLocked(dirs)
			held := ""
			switch tt.hold {
			case "agent":
				l, err := home.Acquire(data)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { l.Release() })
				held = filepath.Join(data, "agent.lock")
			case "install":
				p, err := in.PlanInstall(nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { p.Close() })
				held = filepath.Join(data, "install.lock")
			}
			tidy(in, dirs, missing)
			_, err = os.Stat(data)
			if gone := errors.Is(err, fs.ErrNotExist); gone != tt.gone {
				t.Errorf("after tidy the data directory is gone: %v, want %v (%v)", gone, tt.gone, err)
			}
			if held != "" {
				if _, err := os.Stat(held); err != nil {
					t.Errorf("tidy deleted %s while its lock was held: %v", held, err)
				}
			}
		})
	}
}

// TestPairtimeBinaries checks that uninstall counts each pairtime binary
// once, however its path is spelled, and knows which it runs from before
// it deletes any (#124).
func TestPairtimeBinaries(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	pt := filepath.Join(root, "home", "bin", "pairtime")
	other := filepath.Join(root, "other", "pairtime")
	script := filepath.Join(root, "script", "pairtime")
	for p, b := range map[string][]byte{pt: data, other: data, script: []byte("#!/bin/sh\n")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The home directory under another name, as /var/home is /home on
	// Fedora Atomic.
	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "home"), link); err != nil {
		t.Skipf("making a link: %v", err)
	}
	viaLink := filepath.Join(link, "bin", "pairtime")
	tests := []struct {
		name  string
		src   string
		paths []string
		want  []string
		self  []bool
	}{
		{"a binary named through two spellings of its folder counts once, and is the one that runs",
			pt, []string{pt, viaLink}, []string{pt}, []bool{true}},
		{"the same binary through the other spelling first counts once too",
			viaLink, []string{viaLink, pt}, []string{viaLink}, []bool{true}},
		{"another copy counts on its own, and isn't the one that runs",
			pt, []string{pt, other}, []string{pt, other}, []bool{true, false}},
		{"an empty path and a file that isn't a pairtime binary don't count",
			pt, []string{"", script, pt}, []string{pt}, []bool{true}},
		{"when the binary that runs is gone, none of them is it",
			filepath.Join(root, "gone"), []string{pt, other}, []string{pt, other}, []bool{false, false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var paths []string
			var self []bool
			for _, b := range pairtimeBinaries(tt.src, tt.paths) {
				paths, self = append(paths, b.path), append(self, b.self)
			}
			if !slices.Equal(paths, tt.want) || !slices.Equal(self, tt.self) {
				t.Errorf("pairtimeBinaries = %q, self %v; want %q, self %v", paths, self, tt.want, tt.self)
			}
		})
	}
}

// TestClearData checks what uninstall deletes in pairtime's directories:
// everything but what the teen keeps and the lock files, which go later,
// and through a link only what pairtime puts there (#124).
func TestClearData(t *testing.T) {
	tests := []struct {
		name  string
		files []string // in the folder, a name ending in / a folder
		link  bool     // the data directory is a link to the folder
		keep  []string
		want  []string // what is left in the folder afterwards
	}{
		{
			name:  "everything goes but what is kept and the two lock files",
			files: []string{"agent.lock", "install.lock", "ledger/2026-11-20.seg", "keys/keystore.json", "state.json", "inbox/", "days/2026-11-20.json", "coach/recaps.jsonl"},
			keep:  []string{"days", "coach"},
			want:  []string{"agent.lock", "coach", "coach/recaps.jsonl", "days", "days/2026-11-20.json", "install.lock"},
		},
		{
			name:  "with nothing kept, only the lock files stay for later",
			files: []string{"agent.lock", "ledger/2026-11-20.seg", "days/2026-11-20.json", "notes.txt"},
			want:  []string{"agent.lock"},
		},
		{
			name:  "through a link, only what pairtime puts in its directories goes, and what else the folder holds stays",
			files: []string{"ledger/2026-11-20.seg", "state.json", "state.json.bad", ".state.json.tmp-123", "keys/keystore.json", "account.json", "account/agent.lock", "outbox.json", "uploads.jsonl", "notes.txt", "photos/cat.jpg"},
			link:  true,
			want:  []string{"notes.txt", "photos", "photos/cat.jpg"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			folder := filepath.Join(root, "folder")
			for _, f := range tt.files {
				p := filepath.Join(folder, filepath.FromSlash(f))
				if strings.HasSuffix(f, "/") {
					if err := os.MkdirAll(p, 0o700); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(f), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			dir := folder
			if tt.link {
				dir = filepath.Join(root, "link")
				if err := os.Symlink(folder, dir); err != nil {
					t.Skipf("making a link: %v", err)
				}
			}
			if err := clearData(dir, tt.keep, nil); err != nil {
				t.Fatalf("clearData: %v", err)
			}
			if got := treeOf(t, folder); !slices.Equal(got, tt.want) {
				t.Errorf("after clearData the folder holds %q, want %q", got, tt.want)
			}
		})
	}
}

// treeOf returns the paths in dir, slash-separated and sorted.
func treeOf(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if rel, _ := filepath.Rel(dir, p); rel != "." {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

// TestOnlyHolds checks when uninstall on Windows may leave a folder of the
// data directory in place for now: when all it holds is pairtime's
// binaries, such as the one uninstall runs from, which PowerShell deletes
// once uninstall has exited, and the folders that hold them (#124).
func TestOnlyHolds(t *testing.T) {
	tests := []struct {
		name     string
		files    []string // in the folder, a name ending in / a folder
		binaries []string // pairtime's binaries, in the folder
		want     bool
	}{
		{"a folder holding only the running binary, in folders of its own, may stay", []string{"x/pairtime.exe"}, []string{"x/pairtime.exe"}, true},
		{"a folder holding two binaries, one moved aside by an install, may stay", []string{"pairtime.exe", "pairtime.old.exe"}, []string{"pairtime.exe", "pairtime.old.exe"}, true},
		{"a folder holding another file beside the binary may not", []string{"pairtime.exe", "draft.txt"}, []string{"pairtime.exe"}, false},
		{"a folder holding another, empty folder beside the binary may not", []string{"pairtime.exe", "empty/"}, []string{"pairtime.exe"}, false},
		{"with no binaries, no folder that holds anything may stay", []string{"pairtime.exe"}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			folder := filepath.Join(t.TempDir(), "bin")
			for _, f := range tt.files {
				p := filepath.Join(folder, filepath.FromSlash(f))
				if strings.HasSuffix(f, "/") {
					if err := os.MkdirAll(p, 0o700); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var binaries []string
			for _, b := range tt.binaries {
				binaries = append(binaries, filepath.Join(folder, filepath.FromSlash(b)))
			}
			if got := onlyHolds(folder, binaries); got != tt.want {
				t.Errorf("onlyHolds = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestOnPath checks when install says that the binary's folder isn't on
// the PATH: on Windows too, where paths are compared in any case (#124).
func TestOnPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bin")
	tests := []struct {
		name string
		path string // the PATH entry beside one that is elsewhere
		want bool
	}{
		{"the folder is on the PATH", dir, true},
		{"the folder with a separator after it is on the PATH", dir + string(filepath.Separator), true},
		{"another folder isn't the folder", filepath.Join(filepath.Dir(dir), "other"), false},
		{"a relative entry isn't the folder", "bin", false},
	}
	if runtime.GOOS == "windows" {
		tests = append(tests, struct {
			name string
			path string
			want bool
		}{"on Windows the folder spelled in capitals is on the PATH", strings.ToUpper(dir), true})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PATH", strings.Join([]string{filepath.Join(filepath.Dir(dir), "elsewhere"), tt.path}, string(filepath.ListSeparator)))
			if got := onPath(dir); got != tt.want {
				t.Errorf("onPath(%q) with PATH %q = %v, want %v", dir, os.Getenv("PATH"), got, tt.want)
			}
		})
	}
}
