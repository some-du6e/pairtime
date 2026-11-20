// SPDX-License-Identifier: AGPL-3.0-or-later

package udiff

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// numbered returns lines "1\n" to "n\n".
func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%d\n", i)
	}
	return b.String()
}

func TestUnified(t *testing.T) {
	twenty := numbered(20)
	tests := []struct {
		name     string
		from, to string // header names
		a, b     string
		want     string
	}{
		{name: "equal inputs give no diff", from: "a", to: "b", a: "x\n", b: "x\n", want: ""},
		{name: "an added line shows with three lines of context on each side",
			from: "a", to: "b", a: numbered(8), b: strings.Replace(numbered(8), "4\n", "4\nx\n", 1),
			want: "--- a\n+++ b\n@@ -2,6 +2,7 @@\n 2\n 3\n 4\n+x\n 5\n 6\n 7\n"},
		{name: "a new file is all additions from line 0",
			from: "/dev/null", to: "b", a: "", b: "x\ny\n",
			want: "--- /dev/null\n+++ b\n@@ -0,0 +1,2 @@\n+x\n+y\n"},
		{name: "a deleted file is all deletions, and a range of one line is just its number",
			from: "a", to: "/dev/null", a: "x\n", b: "",
			want: "--- a\n+++ /dev/null\n@@ -1 +0,0 @@\n-x\n"},
		{name: "a last line without a newline is marked",
			from: "a", to: "b", a: "a\nb", b: "a\nc",
			want: "--- a\n+++ b\n@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+c\n\\ No newline at end of file\n"},
		{name: "adding the final newline is a change to the last line",
			from: "a", to: "b", a: "a", b: "a\n",
			want: "--- a\n+++ b\n@@ -1 +1 @@\n-a\n\\ No newline at end of file\n+a\n"},
		{name: "CRLF lines keep their CR",
			from: "a", to: "b", a: "1\r\n2\r\n", b: "1\r\n2\r\n3\r\n",
			want: "--- a\n+++ b\n@@ -1,2 +1,3 @@\n 1\r\n 2\r\n+3\r\n"},
		{name: "changes more than six lines apart make two hunks",
			from: "a", to: "b", a: twenty, b: strings.Replace(strings.Replace(twenty, "\n2\n", "\ntwo\n", 1), "\n19\n", "\nnineteen\n", 1),
			want: "--- a\n+++ b\n@@ -1,5 +1,5 @@\n 1\n-2\n+two\n 3\n 4\n 5\n@@ -16,5 +16,5 @@\n 16\n 17\n 18\n-19\n+nineteen\n 20\n"},
		{name: "changes up to six lines apart share a hunk",
			from: "a", to: "b", a: twenty, b: strings.Replace(strings.Replace(twenty, "\n5\n", "\nfive\n", 1), "\n11\n", "\neleven\n", 1),
			want: "--- a\n+++ b\n@@ -2,13 +2,13 @@\n 2\n 3\n 4\n-5\n+five\n 6\n 7\n 8\n 9\n 10\n-11\n+eleven\n 12\n 13\n 14\n"},
		{name: "a replaced run shows its deletions before its insertions",
			from: "a", to: "b", a: "k\n1\n2\nk\n", b: "k\nx\ny\nk\n",
			want: "--- a\n+++ b\n@@ -1,4 +1,4 @@\n k\n-1\n-2\n+x\n+y\n k\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Unified(tt.from, tt.to, []byte(tt.a), []byte(tt.b)); got != tt.want {
				t.Errorf("Unified gives\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

// TestGitAppliesTheDiff checks every diff against git, a diff reader this
// package didn't write: `git apply` must turn a into exactly b. The pairs
// are the cases above, random edits, and the installer's golden
// settings.json files in both directions. It runs on Linux, where CI has
// git; elsewhere it is skipped.
func TestGitAppliesTheDiff(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("git apply is checked on Linux only")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git isn't installed, so the diffs can't be checked: %v", err)
	}
	type pair struct {
		name string
		a, b []byte // nil for a file that isn't there
	}
	pairs := []pair{
		{"a line added in the middle", []byte(numbered(8)), []byte(strings.Replace(numbered(8), "4\n", "4\nx\n", 1))},
		{"a file created", nil, []byte("x\ny\n")},
		{"a file deleted", []byte("x\ny\n"), nil},
		{"a last line without a newline", []byte("a\nb"), []byte("a\nc")},
		{"a newline added at the end", []byte("a"), []byte("a\n")},
		{"CRLF lines", []byte("1\r\n2\r\n"), []byte("1\r\n2\r\n3\r\n")},
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 40 {
		a := randomLines(rng, 1+rng.IntN(60))
		pairs = append(pairs, pair{fmt.Sprintf("random edit %d", i), a, mutate(rng, a)})
	}
	goldens, err := filepath.Glob(filepath.Join("..", "testdata", "claudecode", "*", "after.json"))
	if err != nil || len(goldens) == 0 {
		t.Fatalf("no golden files found: %v", err)
	}
	for _, after := range goldens {
		dir := filepath.Dir(after)
		a, err := os.ReadFile(filepath.Join(dir, "before.json"))
		if errors.Is(err, fs.ErrNotExist) {
			a = nil
		} else if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(after)
		if err != nil {
			t.Fatal(err)
		}
		pairs = append(pairs, pair{"install " + filepath.Base(dir), a, b}, pair{"uninstall " + filepath.Base(dir), b, a})
	}
	for _, p := range pairs {
		t.Run("git apply turns a into b: "+p.name, func(t *testing.T) {
			dir := t.TempDir()
			f := filepath.Join(dir, "f")
			from, to := "a/f", "b/f"
			if p.a == nil {
				from = "/dev/null"
			} else if err := os.WriteFile(f, p.a, 0o644); err != nil {
				t.Fatal(err)
			}
			if p.b == nil {
				to = "/dev/null"
			}
			patch := Unified(from, to, p.a, p.b)
			if err := os.WriteFile(filepath.Join(dir, "p.diff"), []byte(patch), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(git, "apply", "--whitespace=nowarn", "p.diff")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CEILING_DIRECTORIES="+filepath.Dir(dir))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git apply refused the diff: %v\n%s\nthe diff:\n%s", err, out, patch)
			}
			got, err := os.ReadFile(f)
			switch {
			case p.b == nil:
				if !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("the file is still there after a diff that deletes it")
				}
			case err != nil:
				t.Fatal(err)
			case !bytes.Equal(got, p.b):
				t.Errorf("git apply gives\n%q\nwant\n%q\nthe diff:\n%s", got, p.b, patch)
			}
		})
	}
}

// randomLines returns n lines drawn from a small set, so that equal lines
// recur and the diff has choices to make; the last may lack its newline.
func randomLines(rng *rand.Rand, n int) []byte {
	words := []string{"{", "}", "  \"a\": 1,", "x", "y", "", "\t[", "]"}
	var b bytes.Buffer
	for i := 0; i < n; i++ {
		b.WriteString(words[rng.IntN(len(words))])
		if i < n-1 || rng.IntN(4) > 0 {
			b.WriteByte('\n')
		}
	}
	return b.Bytes()
}

// mutate deletes, inserts and replaces a few random lines of a.
func mutate(rng *rand.Rand, a []byte) []byte {
	lines := Lines(a)
	for range 1 + rng.IntN(6) {
		i := rng.IntN(len(lines) + 1)
		switch rng.IntN(3) {
		case 0:
			if i < len(lines) {
				lines = append(lines[:i], lines[i+1:]...)
			}
		case 1:
			lines = append(lines[:i], append([]string{"new\n"}, lines[i:]...)...)
		default:
			if i < len(lines) {
				lines[i] = "changed\n"
			}
		}
	}
	b := []byte(strings.Join(lines, ""))
	if len(b) == 0 {
		return []byte("new\n")
	}
	return b
}
