// SPDX-License-Identifier: AGPL-3.0-or-later

package objstore

import (
	"context"
	"io"
	"strings"
	"time"
)

// SnapshotPrefix is where the nightly snapshots go in the snapshot bucket.
const SnapshotPrefix = "snapshots/"

// snapshotTime is the time format in a snapshot's key: UTC to the second,
// so keys sort in the order the snapshots were taken.
const snapshotTime = "20060102T150405Z"

// Snapshots is the second bucket (DESIGN 10.3): each night's VACUUM INTO
// copy of the database, encrypted with age to Karim's offline key, written
// once under the time it was taken. The bucket's own 30-day lifecycle rule
// deletes old snapshots; it is set on the bucket when it is created
// (DESIGN 10.5 item 4), not by pairtimed. WP-OP1's nightly job makes the
// encrypted file and hands it to Put.
//
// It must not be the main bucket: that lifecycle rule would delete the
// evidence and Litestream's replica too. The configuration refuses one
// name for both.
type Snapshots struct {
	b *Bucket
}

// NewSnapshots returns the snapshot store in bucket b.
func NewSnapshots(b *Bucket) *Snapshots { return &Snapshots{b: b} }

// SnapshotKey is the key of the snapshot taken at t, such as
// snapshots/pairtime-20261119T031000Z.db.age.
func SnapshotKey(t time.Time) string {
	return SnapshotPrefix + "pairtime-" + t.UTC().Format(snapshotTime) + ".db.age"
}

// isSnapshotKey reports whether key is one SnapshotKey makes.
func isSnapshotKey(key string) bool {
	ts, ok := strings.CutPrefix(key, SnapshotPrefix+"pairtime-")
	if !ok {
		return false
	}
	ts, ok = strings.CutSuffix(ts, ".db.age")
	if !ok {
		return false
	}
	_, err := time.Parse(snapshotTime, ts)
	return err == nil
}

// Put uploads one encrypted snapshot under SnapshotKey(takenAt), from the
// file's current offset to its end. It is write-once: a second snapshot
// with the same time fails with ErrExists and leaves the first.
func (s *Snapshots) Put(ctx context.Context, takenAt time.Time, file io.ReadSeeker) (ObjectInfo, error) {
	return s.b.Put(ctx, SnapshotKey(takenAt), file, PutOptions{IfNoneMatch: true})
}

// Latest returns the newest snapshot, or an error wrapping ErrNotFound
// when there is none. Keys under SnapshotPrefix that SnapshotKey didn't
// make are skipped.
func (s *Snapshots) Latest(ctx context.Context) (ObjectInfo, error) {
	var latest ObjectInfo
	for o, err := range s.b.List(ctx, SnapshotPrefix) {
		if err != nil {
			return ObjectInfo{}, err
		}
		if isSnapshotKey(o.Key) && o.Key > latest.Key {
			latest = o
		}
	}
	if latest.Key == "" {
		return ObjectInfo{}, &Error{Op: "ListObjectsV2", Bucket: s.b.name, Key: SnapshotPrefix, Err: ErrNotFound}
	}
	return latest, nil
}

// Get returns a snapshot's body, which the caller reads and closes.
func (s *Snapshots) Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	return s.b.Get(ctx, key)
}
