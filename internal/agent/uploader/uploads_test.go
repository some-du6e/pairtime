// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPastUploads(t *testing.T) {
	t.Run("an answered request is kept with what it carried, its codes and its body's SHA-256, never its body", func(t *testing.T) {
		minutes := make([]int, 45)
		for i := range minutes {
			minutes[i] = i
		}
		r := newRig(t, 3, minutes, -time.Minute)
		r.run(t, "every minute", func(f *fakeIngest) bool { return len(f.buckets) == len(minutes) })
		past, err := PastUploads(r.dirs.Data)
		if err != nil {
			t.Fatal(err)
		}
		r.f.mu.Lock()
		bodies := append([][]byte(nil), r.f.bodies...)
		r.f.mu.Unlock()
		if len(past) != len(bodies) || len(past) == 0 {
			t.Fatalf("%d past uploads for %d requests", len(past), len(bodies))
		}
		roots, buckets := 0, 0
		for i, e := range past {
			sum := sha256.Sum256(bodies[i])
			if e.BodySHA256 != hex.EncodeToString(sum[:]) || e.Server != r.creds.Server || e.Status != 200 || e.Results == nil {
				t.Errorf("past upload %d is %+v", i, e)
				continue
			}
			roots += e.Results.Roots["accepted"]
			buckets += e.Results.Minutes["accepted"]
		}
		if first := past[0]; first.Roots != 1 || !first.Labels || first.Results.Labels != "accepted" {
			t.Errorf("the first upload is %+v, want the link's first root alone with the labels", first)
		}
		if roots != 3 || buckets != len(minutes) {
			t.Errorf("the past uploads count %d roots and %d minutes accepted, want 3 and %d", roots, buckets, len(minutes))
		}
		raw, err := os.ReadFile(filepath.Join(r.dirs.Data, UploadsName))
		if err != nil {
			t.Fatal(err)
		}
		for _, member := range []string{`"credited_s"`, `"H_b"`, `"slots"`} {
			if bytes.Contains(raw, []byte(member)) {
				t.Errorf("%s holds %s, from a body it should only hash", UploadsName, member)
			}
		}
	})
	t.Run("past uploads older than 90 days go, with a line a crash cut short, and the rest stay", func(t *testing.T) {
		dir := t.TempDir()
		now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
		for _, at := range []time.Time{now.Add(-91 * 24 * time.Hour), now.Add(-24 * time.Hour)} {
			if err := appendUpload(dir, PastUpload{At: at, Server: "https://pairtime.example"}); err != nil {
				t.Fatal(err)
			}
		}
		f, err := os.OpenFile(filepath.Join(dir, UploadsName), os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(`{"at":"2026-11`)); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if err := pruneUploads(dir, now); err != nil {
			t.Fatal(err)
		}
		past, err := PastUploads(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(past) != 1 || !past[0].At.Equal(now.Add(-24*time.Hour)) {
			t.Errorf("past uploads %+v, want the one a day old", past)
		}
	})
}
