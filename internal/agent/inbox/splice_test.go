// SPDX-License-Identifier: AGPL-3.0-or-later

package inbox

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSplice checks Splice, which rewrites a private file while a writer
// goes on appending to it: on Windows too, which renames nothing over an
// open file, as platform.yml's run of this package checks.
func TestSplice(t *testing.T) {
	const text = "AAAA\nBBBB\nCCCC\n"
	size := int64(len(text))
	tests := []struct {
		name       string
		size       int64 // what StartSplice is given
		keep       []Span
		appended   string // what the writer appends once the splice has started
		writerOpen bool   // the writer's handle is still open at Finish
		windows    bool   // the case is Windows' alone
		abort      bool
		startErr   bool
		finishErr  bool
		want       string // the file afterwards
	}{
		{name: "the new file holds the parts kept, in the order given, and then what the writer appended meanwhile",
			size: size, keep: []Span{{10, 5}, {0, 5}}, appended: "DDDD\n", want: "CCCC\nAAAA\nDDDD\n"},
		{name: "a splice that keeps nothing and finds nothing appended leaves the file empty, for the writer to go on with",
			size: size, want: ""},
		{name: "a part past the size given is refused, and the file stays as it was",
			size: size, keep: []Span{{10, 10}}, startErr: true, want: text},
		{name: "a file that ends before a part to keep is an error, and stays as it was",
			size: 2 * size, keep: []Span{{0, 2 * size}}, startErr: true, want: text},
		{name: "an aborted splice leaves the file as it was, with what the writer appended",
			size: size, keep: []Span{{5, 5}}, appended: "DDDD\n", abort: true, want: text + "DDDD\n"},
		{name: "a writer's handle left open makes Finish fail on Windows, and the file stays as it was, with what the writer appended",
			size: size, keep: []Span{{5, 5}}, appended: "DDDD\n", writerOpen: true, windows: true, finishErr: true, want: text + "DDDD\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.windows && runtime.GOOS != "windows" {
				t.Skip("only Windows refuses to rename over an open file")
			}
			dir := filepath.Join(t.TempDir(), "data")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(dir, "held.jsonl.pending")
			if err := os.WriteFile(name, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			// The writer, as the agent's pend keeps its handle: open, at the end.
			w, err := OpenPrivate(name, os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Seek(0, io.SeekEnd); err != nil {
				t.Fatal(err)
			}
			open := true
			closeWriter := func() {
				if open {
					open = false
					w.Close()
				}
			}
			defer closeWriter()

			copied := 0
			s, err := StartSplice(name, ".held.jsonl.tmp-", tt.size, tt.keep, func() { copied++ })
			if (err != nil) != tt.startErr {
				t.Fatalf("StartSplice: %v, want an error: %v", err, tt.startErr)
			}
			// copied runs once a splice has copied its parts, and not for one
			// refused before that.
			want := 1
			if tt.startErr {
				want = 0
			}
			if copied != want {
				t.Errorf("copied ran %d times, want %d", copied, want)
			}
			if err == nil {
				// A test that fails before Finish leaves no handle open,
				// which Windows' TempDir cleanup couldn't delete around.
				defer s.Abort()
				if _, err := w.Write([]byte(tt.appended)); err != nil {
					t.Fatal(err)
				}
				if !tt.writerOpen {
					closeWriter()
				}
				if tt.abort {
					s.Abort()
				} else {
					n, err := s.Finish()
					if (err != nil) != tt.finishErr {
						t.Errorf("Finish: %v, want an error: %v", err, tt.finishErr)
					}
					if err == nil && n != int64(len(tt.want)) {
						t.Errorf("Finish says the file holds %d bytes, want %d", n, len(tt.want))
					}
				}
			}
			closeWriter()

			if got, err := os.ReadFile(name); err != nil || string(got) != tt.want {
				t.Errorf("the file holds %q (%v), want %q", got, err, tt.want)
			}
			if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
				var names []string
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Errorf("the folder holds %q (%v), want the file alone", names, err)
			}
			if f, err := OpenPrivate(name, os.O_RDONLY, 0); err != nil {
				t.Errorf("the file isn't private any more: %v", err)
			} else {
				f.Close()
			}
		})
	}
}
