// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/objstore"
	"github.com/some-du6e/pairtime/internal/server/ops"
)

// buckets are the two buckets of DESIGN 10.5 item 4.
type buckets struct {
	// main holds Litestream's replica and the evidence store.
	main *objstore.Bucket
	// snapshots holds the nightly snapshots.
	snapshots *objstore.Bucket
}

// openBuckets returns cfg's buckets, or nil when object storage is off.
// It makes no request; /readyz checks that they answer.
func openBuckets(cfg *config.Config) (*buckets, error) {
	oc, on := cfg.ObjectStore()
	if !on {
		return nil, nil
	}
	c, err := objstore.New(oc)
	if err != nil {
		return nil, err
	}
	main, err := c.Bucket(cfg.ObjectStorage.Bucket)
	if err != nil {
		return nil, err
	}
	snapshots, err := c.Bucket(cfg.ObjectStorage.SnapshotBucket)
	if err != nil {
		return nil, err
	}
	return &buckets{main: main, snapshots: snapshots}, nil
}

// checks are /readyz's object storage checks, one per bucket and named
// after its key in pairtime.toml (DESIGN 10.4).
func (b *buckets) checks() []ops.Check {
	return []ops.Check{
		{Name: "object_storage.bucket", Run: b.main.Check},
		{Name: "object_storage.snapshot_bucket", Run: b.snapshots.Check},
	}
}
