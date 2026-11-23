// SPDX-License-Identifier: AGPL-3.0-or-later

package vault

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/some-du6e/pairtime/pkg/ledger"
)

// The device keys the tests seal under, and their ids.
const (
	keyA = "0123456789abcdef0123456789abcdef"
	keyB = "fedcba9876543210fedcba9876543210"
)

// testKeys derives the ledger keys of a K_dev made of b repeated.
func testKeys(t *testing.T, b byte) *ledger.Keys {
	t.Helper()
	k, err := ledger.DeriveKeys(bytes.Repeat([]byte{b}, ledger.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// problems collects what a vault reports.
type problems struct {
	mu   sync.Mutex
	msgs []string
}

func (p *problems) add(msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.msgs = append(p.msgs, msg)
}

func (p *problems) all() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.msgs)
}

// open opens the vault in dir under K_dev byte b with key id id, and
// reconciles it with next.
func open(t *testing.T, dir string, b byte, id string, next uint64, keepText bool) (*Vault, *problems) {
	t.Helper()
	p := &problems{}
	v, err := Open(Options{Dir: dir, Keys: testKeys(t, b), KeyID: id, KeepText: keepText, Problem: p.add})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Reconcile(next); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	return v, p
}

// commit is record s's commitment to text under K_dev byte b.
func commit(t *testing.T, b byte, s uint64, text string) ledger.Commitment {
	t.Helper()
	c, err := testKeys(t, b).Commit(s, []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// text reads record s's text, checked against its commitment to want.
func text(t *testing.T, v *Vault, s uint64, want string) (string, error) {
	t.Helper()
	got, err := v.Text(s, commit(t, 1, s, want))
	return string(got), err
}

// rawFiles returns every file under dir, by path relative to it, with its
// bytes.
func rawFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

const (
	day1 = "2026-11-20"
	day2 = "2026-11-21"
	day3 = "2026-11-22"
)

func TestKeep(t *testing.T) {
	tests := []struct {
		name string
		// off opens the vault with "keep prompt text" off.
		off bool
		run func(t *testing.T, v *Vault, p *problems)
	}{
		{
			name: "a kept text reads back as kept, checked against its record's commitment",
			run: func(t *testing.T, v *Vault, p *problems) {
				v.Keep(day1, 1, []byte("fix the nav bar"), nil)
				if got, err := text(t, v, 1, "fix the nav bar"); err != nil || got != "fix the nav bar" {
					t.Errorf("Text(1) = %q, %v", got, err)
				}
			},
		},
		{
			name: "the empty text a record commits is kept and reads back empty",
			run: func(t *testing.T, v *Vault, p *problems) {
				v.Keep(day1, 1, []byte{}, nil)
				if got, err := text(t, v, 1, ""); err != nil || got != "" {
					t.Errorf("Text(1) = %q, %v", got, err)
				}
			},
		},
		{
			name: "a text doesn't read back against a commitment to another text",
			run: func(t *testing.T, v *Vault, p *problems) {
				v.Keep(day1, 1, []byte("fix the nav bar"), nil)
				if _, err := v.Text(1, commit(t, 1, 1, "something else")); !errors.Is(err, ErrNotKept) {
					t.Errorf("Text(1) against another commitment: %v, want ErrNotKept", err)
				}
			},
		},
		{
			name: "a record with no entry isn't kept",
			run: func(t *testing.T, v *Vault, p *problems) {
				v.Keep(day1, 1, []byte("one"), nil)
				v.Keep(day1, 3, []byte("three"), nil)
				if _, err := text(t, v, 2, "two"); !errors.Is(err, ErrNotKept) {
					t.Errorf("Text(2): %v, want ErrNotKept", err)
				}
				if _, err := v.Paths(1); !errors.Is(err, ErrNotKept) {
					t.Errorf("Paths(1): %v, want ErrNotKept", err)
				}
			},
		},
		{
			name: "paths read back in order, without repeats or empty ones",
			run: func(t *testing.T, v *Vault, p *problems) {
				v.Keep(day1, 1, nil, []string{"src/nav.js", "", "src/app.css", "src/nav.js"})
				got, err := v.Paths(1)
				if want := []string{"src/nav.js", "src/app.css"}; err != nil || !slices.Equal(got, want) {
					t.Errorf("Paths(1) = %q, %v; want %q", got, err, want)
				}
			},
		},
		{
			name: "with keep prompt text off no text is kept, and paths still are",
			off:  true,
			run: func(t *testing.T, v *Vault, p *problems) {
				v.Keep(day1, 1, []byte("fix the nav bar"), []string{"src/nav.js"})
				if _, err := text(t, v, 1, "fix the nav bar"); !errors.Is(err, ErrNotKept) {
					t.Errorf("Text(1): %v, want ErrNotKept", err)
				}
				if got, err := v.Paths(1); err != nil || !slices.Equal(got, []string{"src/nav.js"}) {
					t.Errorf("Paths(1) = %q, %v", got, err)
				}
			},
		},
		{
			name: "turning keep prompt text off keeps no text from then on, and the text kept before stays",
			run: func(t *testing.T, v *Vault, p *problems) {
				v.Keep(day1, 1, []byte("one"), nil)
				v.SetKeepText(false)
				v.Keep(day1, 2, []byte("two"), nil)
				if _, err := text(t, v, 1, "one"); err != nil {
					t.Errorf("Text(1): %v", err)
				}
				if _, err := text(t, v, 2, "two"); !errors.Is(err, ErrNotKept) {
					t.Errorf("Text(2): %v, want ErrNotKept", err)
				}
			},
		},
		{
			name: "a record's entries go in the file of the day the writer names",
			run: func(t *testing.T, v *Vault, p *problems) {
				v.Keep(day1, 1, []byte("one"), nil)
				v.Keep(day2, 2, []byte("two"), nil)
				files := rawFiles(t, v.dir)
				if len(files) != 2 || files[day1+".seg"] == nil || files[day2+".seg"] == nil {
					t.Errorf("files %v, want %s.seg and %s.seg", keysOf(files), day1, day2)
				}
				for s, want := range map[uint64]string{1: "one", 2: "two"} {
					if got, err := text(t, v, s, want); err != nil || got != want {
						t.Errorf("Text(%d) = %q, %v", s, got, err)
					}
				}
			},
		},
		{
			name: "the files never go back to an older day, as the ledger's segments don't",
			run: func(t *testing.T, v *Vault, p *problems) {
				v.Keep(day2, 1, []byte("one"), nil)
				v.Keep(day1, 2, []byte("two"), nil)
				if files := rawFiles(t, v.dir); len(files) != 1 || files[day2+".seg"] == nil {
					t.Errorf("files %v, want %s.seg alone", keysOf(files), day2)
				}
				if got, err := text(t, v, 2, "two"); err != nil || got != "two" {
					t.Errorf("Text(2) = %q, %v", got, err)
				}
			},
		},
		{
			name: "an entry for a number at or below the last one kept is refused and reported",
			run: func(t *testing.T, v *Vault, p *problems) {
				v.Keep(day1, 2, []byte("two"), nil)
				v.Keep(day1, 2, []byte("again"), nil)
				v.Keep(day1, 1, []byte("one"), nil)
				if got, err := text(t, v, 2, "two"); err != nil || got != "two" {
					t.Errorf("Text(2) = %q, %v", got, err)
				}
				if _, err := text(t, v, 1, "one"); !errors.Is(err, ErrNotKept) {
					t.Errorf("Text(1): %v, want ErrNotKept", err)
				}
				if msgs := p.all(); len(msgs) != 2 || !strings.Contains(msgs[0], "didn't keep the content of record 2") || !strings.Contains(msgs[1], "didn't keep the content of record 1") {
					t.Errorf("reported %q", msgs)
				}
			},
		},
		{
			name: "no file holds a kept text or path in the clear",
			run: func(t *testing.T, v *Vault, p *problems) {
				v.Keep(day1, 1, []byte("planted-3c1f prompt"), []string{"planted-3c1f/secret.go"})
				for name, b := range rawFiles(t, v.dir) {
					if bytes.Contains(b, []byte("planted-3c1f")) {
						t.Errorf("%s holds the planted string in the clear", name)
					}
				}
			},
		},
		{
			name: "the same text kept twice is sealed under two nonces",
			run: func(t *testing.T, v *Vault, p *problems) {
				v.Keep(day1, 1, []byte("same"), nil)
				v.Keep(day1, 2, []byte("same"), nil)
				f, err := readFile(segPath(v.dir, day1), day1, keyA)
				if err != nil || len(f.entries) != 2 {
					t.Fatalf("read %d entries, %v", len(f.entries), err)
				}
				if bytes.Equal(f.entries[0].nonce, f.entries[1].nonce) || bytes.Equal(f.entries[0].sealed, f.entries[1].sealed) {
					t.Error("two entries share a nonce or a ciphertext")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, p := open(t, t.TempDir(), 1, keyA, 1, !tt.off)
			tt.run(t, v, p)
		})
	}
}

func keysOf(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// tamper changes the first entry of day1's file in dir with fn, fixing up
// the frame's CRC, so only the vault's own check can catch it.
func tamper(t *testing.T, dir string, fn func(payload []byte)) {
	t.Helper()
	name := segPath(dir, day1)
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	f, err := readFile(name, day1, keyA)
	if err != nil || len(f.entries) == 0 {
		t.Fatalf("read %d entries, %v", len(f.entries), err)
	}
	start := f.headEnd
	size := binary.BigEndian.Uint32(b[start+1:])
	payload := b[start+frameHeadSize : start+frameHeadSize+int64(size)]
	fn(payload)
	fixed := appendFrame(nil, kindEntry, payload)
	copy(b[start:], fixed)
	if err := os.WriteFile(name, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSealed(t *testing.T) {
	tests := []struct {
		name   string
		change func(payload []byte)
	}{
		{"an entry whose ciphertext changed doesn't open", func(p []byte) { p[len(p)-tagSize-1] ^= 1 }},
		{"an entry whose tag changed doesn't open", func(p []byte) { p[len(p)-1] ^= 1 }},
		{"an entry moved to another record's number doesn't open", func(p []byte) { binary.BigEndian.PutUint64(p, 2) }},
		{"an entry whose nonce changed doesn't open", func(p []byte) { p[9] ^= 1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			v, _ := open(t, dir, 1, keyA, 1, true)
			v.Keep(day1, 1, []byte("fix the nav bar"), nil)
			v.Close()
			tamper(t, dir, tt.change)
			v, _ = open(t, dir, 1, keyA, 3, true)
			for s := uint64(1); s <= 2; s++ {
				if got, err := v.read(s, whatText); err == nil {
					t.Errorf("record %d's text opened to %q", s, got)
				}
			}
		})
	}
	t.Run("an entry whose kind changed doesn't open as the other kind", func(t *testing.T) {
		dir := t.TempDir()
		v, _ := open(t, dir, 1, keyA, 1, true)
		v.Keep(day1, 1, []byte(`["src/nav.js"]`), nil)
		v.Close()
		tamper(t, dir, func(p []byte) { p[8] = whatPaths })
		v, _ = open(t, dir, 1, keyA, 2, true)
		if got, err := v.Paths(1); err == nil {
			t.Errorf("a text opened as paths %q", got)
		}
	})
	t.Run("another device key's entries don't open with this key", func(t *testing.T) {
		dir := t.TempDir()
		v, _ := open(t, dir, 1, keyA, 1, true)
		v.Keep(day1, 1, []byte("fix the nav bar"), nil)
		v.Close()
		other, err := Open(Options{Dir: dir, Keys: testKeys(t, 2), KeyID: keyA})
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		if err := other.Reconcile(2); err != nil {
			t.Fatal(err)
		}
		if got, err := other.read(1, whatText); !errors.Is(err, errSealed) {
			t.Errorf("read under another K_vault = %q, %v; want errSealed", got, err)
		}
	})
}

// keepAll keeps texts for records from..to in day's file.
func keepAll(v *Vault, day string, from, to uint64) {
	for s := from; s <= to; s++ {
		v.Keep(day, s, []byte(textOf(s)), nil)
	}
}

func textOf(s uint64) string { return strings.Repeat("x", int(s)) + " prompt" }

func TestReconcile(t *testing.T) {
	tests := []struct {
		name string
		// fill keeps entries in a vault reconciled with next = 1, and may
		// change its files after it is closed.
		fill func(t *testing.T, dir string)
		next uint64
		// kept are the records whose text reads back, gone those that don't.
		kept, gone []uint64
		files      []string // the day files left
	}{
		{
			name: "entries from the next record's number on are cut, and the ones before stay",
			fill: func(t *testing.T, dir string) {
				v, _ := open(t, dir, 1, keyA, 1, true)
				keepAll(v, day1, 1, 5)
				v.Close()
			},
			next:  4,
			kept:  []uint64{1, 2, 3},
			gone:  []uint64{4, 5},
			files: []string{day1},
		},
		{
			name: "a newest file holding only cut entries goes, and the cut goes on into the file before",
			fill: func(t *testing.T, dir string) {
				v, _ := open(t, dir, 1, keyA, 1, true)
				keepAll(v, day1, 1, 3)
				keepAll(v, day2, 4, 6)
				v.Close()
			},
			next:  3,
			kept:  []uint64{1, 2},
			gone:  []uint64{3, 4, 5, 6},
			files: []string{day1},
		},
		{
			name: "a vault whose every entry is cut is left with no file",
			fill: func(t *testing.T, dir string) {
				v, _ := open(t, dir, 1, keyA, 1, true)
				keepAll(v, day1, 1, 2)
				v.Close()
			},
			next:  1,
			gone:  []uint64{1, 2},
			files: nil,
		},
		{
			name: "nothing is cut when every entry's record survived",
			fill: func(t *testing.T, dir string) {
				v, _ := open(t, dir, 1, keyA, 1, true)
				keepAll(v, day1, 1, 2)
				keepAll(v, day2, 3, 4)
				v.Close()
			},
			next:  5,
			kept:  []uint64{1, 2, 3, 4},
			files: []string{day1, day2},
		},
		{
			name: "a frame a crash left half written is cut, and the whole entries before it stay",
			fill: func(t *testing.T, dir string) {
				v, _ := open(t, dir, 1, keyA, 1, true)
				keepAll(v, day1, 1, 3)
				v.Close()
				name := segPath(dir, day1)
				info, err := os.Stat(name)
				if err != nil {
					t.Fatal(err)
				}
				// The last entry loses its last 3 bytes.
				if err := os.Truncate(name, info.Size()-3); err != nil {
					t.Fatal(err)
				}
			},
			next:  4,
			kept:  []uint64{1, 2},
			gone:  []uint64{3},
			files: []string{day1},
		},
		{
			name: "the zeros a power cut leaves after the last frame are cut",
			fill: func(t *testing.T, dir string) {
				v, _ := open(t, dir, 1, keyA, 1, true)
				keepAll(v, day1, 1, 2)
				v.Close()
				f, err := os.OpenFile(segPath(dir, day1), os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					t.Fatal(err)
				}
				f.Write(make([]byte, 512))
				f.Close()
			},
			next:  3,
			kept:  []uint64{1, 2},
			files: []string{day1},
		},
		{
			name: "a file whose first write a crash cut short holds nothing, and goes",
			fill: func(t *testing.T, dir string) {
				v, _ := open(t, dir, 1, keyA, 1, true)
				keepAll(v, day1, 1, 2)
				v.Close()
				// The head and the first entry go in one write, cut short here.
				if err := os.WriteFile(segPath(dir, day2), []byte{kindHead, 0, 0}, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			next:  3,
			kept:  []uint64{1, 2},
			files: []string{day1},
		},
		{
			name: "an empty file a crash left goes",
			fill: func(t *testing.T, dir string) {
				if err := os.WriteFile(segPath(dir, day1), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			next:  1,
			files: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.fill(t, dir)
			v, p := open(t, dir, 1, keyA, tt.next, true)
			for _, s := range tt.kept {
				if got, err := text(t, v, s, textOf(s)); err != nil || got != textOf(s) {
					t.Errorf("Text(%d) = %q, %v", s, got, err)
				}
			}
			for _, s := range tt.gone {
				if _, err := v.read(s, whatText); !errors.Is(err, ErrNotKept) {
					t.Errorf("record %d's text is still there: %v", s, err)
				}
			}
			days, err := segments(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(days, tt.files) {
				t.Errorf("files %q, want %q", days, tt.files)
			}
			// The numbers cut are free for the records that take them next.
			v.Keep(day3, tt.next, []byte("the next one"), nil)
			if got, err := text(t, v, tt.next, "the next one"); err != nil || got != "the next one" {
				t.Errorf("after the cut, Text(%d) = %q, %v", tt.next, got, err)
			}
			for _, msg := range p.all() {
				if msg != "" && !strings.Contains(msg, "deleted") {
					t.Errorf("reported %q", msg)
				}
			}
		})
	}
}

func TestReconcileRules(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, dir string)
	}{
		{
			name: "nothing is kept before Reconcile",
			run: func(t *testing.T, dir string) {
				v, err := Open(Options{Dir: dir, Keys: testKeys(t, 1), KeyID: keyA, KeepText: true})
				if err != nil {
					t.Fatal(err)
				}
				defer v.Close()
				v.Keep(day1, 1, []byte("too early"), nil)
				if days, _ := segments(dir); len(days) != 0 {
					t.Errorf("files %q before Reconcile", days)
				}
			},
		},
		{
			name: "Reconcile runs once",
			run: func(t *testing.T, dir string) {
				v, _ := open(t, dir, 1, keyA, 1, true)
				if err := v.Reconcile(1); err == nil {
					t.Error("a second Reconcile succeeded")
				}
			},
		},
		{
			name: "nothing is kept after Close",
			run: func(t *testing.T, dir string) {
				v, _ := open(t, dir, 1, keyA, 1, true)
				v.Close()
				v.Keep(day1, 1, []byte("too late"), nil)
				if days, _ := segments(dir); len(days) != 0 {
					t.Errorf("files %q after Close", days)
				}
				if _, err := v.DropText(); !errors.Is(err, ErrClosed) {
					t.Errorf("DropText after Close: %v, want ErrClosed", err)
				}
			},
		},
		{
			name: "another device key's files go to old/<its key id>, where this key's vault doesn't read them",
			run: func(t *testing.T, dir string) {
				v, _ := open(t, dir, 2, keyB, 1, true)
				keepAll(v, day1, 1, 2)
				v.Close()
				v, _ = open(t, dir, 1, keyA, 1, true)
				if days, _ := segments(dir); len(days) != 0 {
					t.Errorf("files %q left in the vault", days)
				}
				if days, _ := segments(filepath.Join(dir, oldDir, keyB)); !slices.Equal(days, []string{day1}) {
					t.Errorf("old/%s holds %q, want %q", keyB, days, day1)
				}
				// This key's entry 1 is its own, not the other key's.
				v.Keep(day1, 1, []byte("mine"), nil)
				if got, err := text(t, v, 1, "mine"); err != nil || got != "mine" {
					t.Errorf("Text(1) = %q, %v", got, err)
				}
			},
		},
		{
			name: "a vault whose Reconcile fails keeps nothing more, so no entry it couldn't cut is followed by another of its number",
			run: func(t *testing.T, dir string) {
				if runtime.GOOS == "windows" || os.Geteuid() == 0 {
					t.Skip("needs a file this user can't read")
				}
				v, _ := open(t, dir, 1, keyA, 1, true)
				keepAll(v, day1, 1, 3)
				v.Close()
				name := segPath(dir, day1)
				p := &problems{}
				v, err := Open(Options{Dir: dir, Keys: testKeys(t, 1), KeyID: keyA, KeepText: true, Problem: p.add})
				if err != nil {
					t.Fatal(err)
				}
				defer v.Close()
				// After Open, which reads each file's head, and before
				// Reconcile reads the newest file through.
				if err := os.Chmod(name, 0); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(name, 0o600)
				if err := v.Reconcile(2); err == nil {
					t.Fatal("Reconcile read a file it can't read")
				}
				if msgs := p.all(); len(msgs) != 1 || !strings.Contains(msgs[0], "keeps nothing until the agent starts again") {
					t.Errorf("reported %q", msgs)
				}
				os.Chmod(name, 0o600)
				v.Keep(day1, 2, []byte("two again"), nil)
				f, err := readFile(name, day1, keyA)
				if err != nil {
					t.Fatal(err)
				}
				if len(f.entries) != 3 {
					t.Errorf("the file holds %d entries, want the 3 it had", len(f.entries))
				}
			},
		},
		{
			name: "a rewrite a crash cut short leaves no temporary file behind",
			run: func(t *testing.T, dir string) {
				tmp := filepath.Join(dir, "."+day1+segSuffix+".tmp-123")
				if err := os.WriteFile(tmp, []byte("sealed bytes"), 0o600); err != nil {
					t.Fatal(err)
				}
				open(t, dir, 1, keyA, 1, true)
				if _, err := os.Stat(tmp); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("the temporary file is still there: %v", err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.run(t, t.TempDir()) })
	}
}

func TestDropText(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, dir string, v *Vault)
	}{
		{
			name: "every text goes, the paths stay, and a file left with nothing goes",
			run: func(t *testing.T, dir string, v *Vault) {
				v.Keep(day1, 1, []byte("one"), nil)
				v.Keep(day2, 2, []byte("two"), []string{"src/nav.js"})
				n, err := v.DropText()
				if err != nil || n != 2 {
					t.Fatalf("DropText() = %d, %v; want 2", n, err)
				}
				for s, want := range map[uint64]string{1: "one", 2: "two"} {
					if _, err := text(t, v, s, want); !errors.Is(err, ErrNotKept) {
						t.Errorf("Text(%d): %v, want ErrNotKept", s, err)
					}
				}
				if got, err := v.Paths(2); err != nil || !slices.Equal(got, []string{"src/nav.js"}) {
					t.Errorf("Paths(2) = %q, %v", got, err)
				}
				if days, _ := segments(dir); !slices.Equal(days, []string{day2}) {
					t.Errorf("files %q, want %q", days, day2)
				}
			},
		},
		{
			name: "the file open for appending is written again, and records go on being kept in it",
			run: func(t *testing.T, dir string, v *Vault) {
				v.Keep(day1, 1, []byte("one"), []string{"a.go"})
				if _, err := v.DropText(); err != nil {
					t.Fatal(err)
				}
				v.Keep(day1, 2, []byte("two"), nil)
				if got, err := text(t, v, 2, "two"); err != nil || got != "two" {
					t.Errorf("Text(2) = %q, %v", got, err)
				}
				if got, err := v.Paths(1); err != nil || !slices.Equal(got, []string{"a.go"}) {
					t.Errorf("Paths(1) = %q, %v", got, err)
				}
			},
		},
		{
			name: "no deleted text is left in any file's bytes",
			run: func(t *testing.T, dir string, v *Vault) {
				v.Keep(day1, 1, []byte("planted-77aa"), []string{"a.go"})
				f, err := readFile(segPath(dir, day1), day1, keyA)
				if err != nil {
					t.Fatal(err)
				}
				sealed := f.entries[0].sealed
				if _, err := v.DropText(); err != nil {
					t.Fatal(err)
				}
				for name, b := range rawFiles(t, dir) {
					if bytes.Contains(b, sealed) {
						t.Errorf("%s still holds the deleted text's ciphertext", name)
					}
				}
			},
		},
		{
			name: "a vault with no text is left as it was",
			run: func(t *testing.T, dir string, v *Vault) {
				v.Keep(day1, 1, nil, []string{"a.go"})
				before := rawFiles(t, dir)
				if n, err := v.DropText(); err != nil || n != 0 {
					t.Fatalf("DropText() = %d, %v", n, err)
				}
				after := rawFiles(t, dir)
				if len(before) != len(after) || !bytes.Equal(before[day1+".seg"], after[day1+".seg"]) {
					t.Error("the files changed")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			v, _ := open(t, dir, 1, keyA, 1, true)
			tt.run(t, dir, v)
		})
	}
}

func TestPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps the files private with the data directory's ACL (internal/agent/home)")
	}
	tests := []struct {
		name string
		path func(dir string) string
		want fs.FileMode
	}{
		{"the vault directory is this user's alone", func(dir string) string { return filepath.Join(dir, "vault") }, 0o700},
		{"a day's file is this user's alone", func(dir string) string { return segPath(filepath.Join(dir, "vault"), day1) }, 0o600},
		{"a file written again by DropText is this user's alone", func(dir string) string { return segPath(filepath.Join(dir, "vault"), day2) }, 0o600},
	}
	dir := t.TempDir()
	v, _ := open(t, filepath.Join(dir, "vault"), 1, keyA, 1, true)
	v.Keep(day1, 1, nil, []string{"a.go"})
	v.Keep(day2, 2, []byte("two"), []string{"b.go"})
	if _, err := v.DropText(); err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := os.Stat(tt.path(dir))
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != tt.want {
				t.Errorf("mode %v, want %v", got, tt.want)
			}
		})
	}
}
