// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/ledger"
)

// copyDir copies the regular files under dir to a new folder, as a kill -9
// of the agent leaves them: what was written is there, flushed or not. A
// file that goes while it copies, as a temporary file an atomic write
// renames, is left out, as the kill would have left the rename's target.
func copyDir(t *testing.T, dir string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(to, 0o700)
		case !d.Type().IsRegular():
			return nil
		}
		b, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// TestKillKeepsModes is a kill -9 of the agent right after it sealed a
// batch in local-only mode, at each point before the uploader could save
// the batch's mode in state.json: while a request is in flight, which
// keeps the loop from taking the batch for up to a minute; after the seal
// frame's flush, before the batch's row is in batches.seg; and after the
// row, before the uploader hears of the batch. The next start, with
// config.toml online again and no new sign-in, sends neither the batch's
// root nor its minutes, and no root after it, and doctor hears why: the
// seal frame holds the batch's mode, on disk with the seal (DESIGN 4.2).
func TestKillKeepsModes(t *testing.T) {
	cases := []struct {
		name string
		// kill seals batch 4 in local-only mode, once batches 1 and 2 went,
		// and returns the data directory as a kill -9 then leaves it.
		kill func(t *testing.T, r *liveRig) string
	}{
		{"a kill while batch 3's request is in flight", func(t *testing.T, r *liveRig) string {
			g := r.hold(t)
			r.seal(t, 3)
			r.u.Wake()
			select {
			case <-g.arrived:
			case <-time.After(time.Minute):
				t.Fatal("batch 3's request never came")
			}
			r.local.Store(true)
			r.seal(t, 4)
			return copyDir(t, r.dirs.Data)
		}},
		{"a kill after the seal frame's flush, before the row is in batches.seg", func(t *testing.T, r *liveRig) string {
			r.seal(t, 3)
			r.wait(t, "batch 3 and its minutes", func(f *fakeIngest) bool { return f.head.b == 3 && len(f.buckets) == 45 })
			r.drain()
			batches := filepath.Join(r.dirs.Ledger(), "batches.seg")
			fi, err := os.Stat(batches)
			if err != nil {
				t.Fatal(err)
			}
			var (
				killed string
				cut    error
			)
			r.sealing = func(b ledger.Batch) {
				if b.Row.B == 4 {
					killed = copyDir(t, r.dirs.Data)
					// The row is all the seal wrote to batches.seg.
					cut = os.Truncate(filepath.Join(killed, "ledger", "batches.seg"), fi.Size())
				}
			}
			r.local.Store(true)
			r.seal(t, 4)
			if cut != nil {
				t.Fatal(cut)
			}
			return killed
		}},
		{"a kill after the row is in batches.seg, before the uploader heard of the batch", func(t *testing.T, r *liveRig) string {
			r.seal(t, 3)
			r.wait(t, "batch 3 and its minutes", func(f *fakeIngest) bool { return f.head.b == 3 && len(f.buckets) == 45 })
			r.drain()
			var killed string
			r.sealing = func(b ledger.Batch) {
				if b.Row.B == 4 {
					killed = copyDir(t, r.dirs.Data)
				}
			}
			r.local.Store(true)
			r.seal(t, 4)
			return killed
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newLiveRig(t, 90)
			r.u.Start()
			r.seal(t, 1)
			r.seal(t, 2)
			r.wait(t, "batches 1 and 2 and their minutes", func(f *fakeIngest) bool { return f.head != nil && f.head.b == 2 && len(f.buckets) == 30 })
			killed := c.kill(t, r)
			// The next start, from what the kill left, with config.toml
			// online again and the same sign-in.
			r.local.Store(false)
			r.restart(t, killed)
			r.u.Start()
			r.wait(t, "batch 3 and its minutes", func(f *fakeIngest) bool { return f.head.b == 3 && r.minutesIn(f, 30, 44) == 15 })
			waitFor(t, "doctor to hear that uploads stopped", func() bool {
				r.u.Wake()
				return strings.Contains(r.doctor(), "stopped uploading")
			})
			r.drain()
			r.f.mu.Lock()
			defer r.f.mu.Unlock()
			checks := []struct {
				name string
				ok   bool
			}{
				{"no root of the batch sealed in local-only mode went, nor any after it", r.f.head.b == 3 && r.f.rows[4].b == 0},
				{"no minute of that batch went", r.minutesIn(r.f, 45, 59) == 0},
				{"the uploader says the roots stop at it", r.u.Status().Stopped == 4},
			}
			for _, ch := range checks {
				t.Run(ch.name, func(t *testing.T) {
					if !ch.ok {
						t.Errorf("no: the server holds head %d and %d minutes; the uploader says %+v", r.f.head.b, len(r.f.buckets), r.u.Status())
					}
				})
			}
		})
	}
}
