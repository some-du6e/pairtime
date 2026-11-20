// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/some-du6e/pairtime/internal/server/objstore"
)

// MaxLag is how far behind the database Litestream's replica may be
// before /readyz fails (DESIGN 10.4).
const MaxLag = 60 * time.Second

// ltxName is an LTX file's name, the first and last transaction ids it
// holds as 16 hex digits each (Litestream v0.5, superfly/ltx).
var ltxName = regexp.MustCompile(`^([0-9a-f]{16})-([0-9a-f]{16})\.ltx$`)

// maxWaiting bounds the commits Replication remembers. It keeps one entry
// for each second that saw commits, so this is about 17 minutes of a busy
// database that Litestream stopped reading. Past it, later commits join the
// newest entry, which can only make the lag Lag reports longer, never
// shorter.
const maxWaiting = 1024

// waiting is the commits of one second, or of more once maxWaiting is
// reached, that no local LTX file has followed yet: the first and the last
// one's times, in Unix nanoseconds.
type waiting struct {
	first, last int64
}

// Replication measures how far Litestream's replica is behind the
// database, from what Litestream v0.5 leaves on both sides. Litestream
// turns each change into an LTX file in the database's directory, under
// .<database name>-litestream/ltx/0/, and uploads it to the replica under
// <path>/0000/, each named for the transaction ids it holds. The replica
// lacks the local files whose last transaction id is above the highest it
// has. Litestream deletes old level-0 files on both sides together and
// keeps the newest on each, so a database that hasn't changed reads as
// caught up. Matching files alone don't show that Litestream still reads
// the database, though, so serve also tells Replication about each commit
// (Wrote), and a commit that no newer local file follows counts as waiting
// too.
//
// It reads the directory and lists one prefix of the bucket: one class A
// request on R2 a call. restore-drill.yml runs it against the real
// Litestream, so a Litestream release that moves these files fails there,
// before Renovate's bump merges.
type Replication struct {
	// DBPath is the database Litestream replicates.
	DBPath string
	// Bucket is the main bucket, and Path the replica's path in it, as
	// litestream.yml's replica sets them.
	Bucket *objstore.Bucket
	Path   string
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	// lastLag holds the last lag measured, in milliseconds, or -1.
	lastLag atomic.Int64
	// mu guards pending, the commits that no local LTX file has followed
	// yet, oldest first.
	mu      sync.Mutex
	pending []waiting
}

// Wrote records a commit made at t. serve calls it from the writer's
// OnCommit for each transaction that changed rows (writer.Commit.Changed),
// since only those write WAL for Litestream to read. Lag then treats the
// commit as waiting until Litestream writes a local LTX file after it,
// which tells a database Litestream stopped reading from one that hasn't
// changed. Every commit counts until a file follows it, however many come
// first; commits within a second of each other share an entry.
func (r *Replication) Wrote(t time.Time) {
	ns := t.UnixNano()
	r.mu.Lock()
	defer r.mu.Unlock()
	if n := len(r.pending); n > 0 {
		w := &r.pending[n-1]
		if ns-w.first < int64(time.Second) || n >= maxWaiting {
			w.last = max(w.last, ns)
			return
		}
	}
	r.pending = append(r.pending, waiting{first: ns, last: ns})
}

// captureLag forgets the commits a local LTX file has followed, the newest
// of which Litestream wrote at newest, and returns how long the oldest
// commit left has waited; 0 when none is left. A file follows an entry
// only once it is newer than the entry's last commit.
func (r *Replication) captureLag(newest, now time.Time) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	followed := 0
	if !newest.IsZero() {
		for followed < len(r.pending) && r.pending[followed].last <= newest.UnixNano() {
			followed++
		}
	}
	r.pending = slices.Delete(r.pending, 0, followed)
	if len(r.pending) == 0 {
		return 0
	}
	return max(now.Sub(time.Unix(0, r.pending[0].first)), 0)
}

// NewReplication returns the measure of the replica at path in bucket of
// the database at dbPath.
func NewReplication(dbPath string, bucket *objstore.Bucket, path string) *Replication {
	r := &Replication{DBPath: dbPath, Bucket: bucket, Path: path}
	r.lastLag.Store(-1)
	return r
}

// LocalDir is the directory Litestream writes the database's level-0 LTX
// files to.
func (r *Replication) LocalDir() string {
	dir, base := filepath.Split(r.DBPath)
	return filepath.Join(dir, "."+base+"-litestream", "ltx", "0")
}

// prefix is where the replica's level-0 files are in the bucket.
func (r *Replication) prefix() string {
	return path.Join(r.Path, fmt.Sprintf("%04x", 0)) + "/"
}

// ltxFile is one LTX file's transaction range.
type ltxFile struct {
	min, max uint64
	modified time.Time
}

func parseLTX(name string) (ltxFile, bool) {
	m := ltxName.FindStringSubmatch(name)
	if m == nil {
		return ltxFile{}, false
	}
	lo, err1 := strconv.ParseUint(m[1], 16, 64)
	hi, err2 := strconv.ParseUint(m[2], 16, 64)
	if err1 != nil || err2 != nil || lo > hi {
		return ltxFile{}, false
	}
	return ltxFile{min: lo, max: hi}, true
}

// Lag returns how long the oldest change the replica lacks has waited for
// it: the age of the oldest local level-0 file above the replica's highest
// transaction id, or of the oldest commit no local file has followed
// (Wrote), whichever is older; 0 when there is neither. It fails when
// Litestream has left no state next to the database, which means it isn't
// replicating this database.
func (r *Replication) Lag(ctx context.Context) (time.Duration, error) {
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	entries, err := os.ReadDir(r.LocalDir())
	if errors.Is(err, fs.ErrNotExist) {
		return 0, fmt.Errorf("backup: Litestream has no state at %s, so it isn't replicating %s (yet): litestream.yml must name this database, and Litestream writes the directory soon after it starts", r.LocalDir(), r.DBPath)
	}
	if err != nil {
		return 0, fmt.Errorf("backup: %w", err)
	}
	var local []ltxFile
	var newest time.Time
	for _, e := range entries {
		f, ok := parseLTX(e.Name())
		if !ok || !e.Type().IsRegular() {
			continue
		}
		fi, err := e.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue // Litestream deleted it meanwhile
		}
		if err != nil {
			return 0, fmt.Errorf("backup: %w", err)
		}
		f.modified = fi.ModTime()
		local = append(local, f)
		if f.modified.After(newest) {
			newest = f.modified
		}
	}
	capture := r.captureLag(newest, now())
	if len(local) == 0 {
		r.lastLag.Store(capture.Milliseconds())
		return capture, nil
	}
	var replica uint64
	for o, err := range r.Bucket.List(ctx, r.prefix()) {
		if err != nil {
			return 0, err
		}
		if f, ok := parseLTX(path.Base(o.Key)); ok && f.max > replica {
			replica = f.max
		}
	}
	var oldest time.Time
	for _, f := range local {
		if f.max > replica && (oldest.IsZero() || f.modified.Before(oldest)) {
			oldest = f.modified
		}
	}
	lag := capture
	if !oldest.IsZero() {
		lag = max(lag, now().Sub(oldest))
	}
	r.lastLag.Store(lag.Milliseconds())
	return lag, nil
}

// Check is /readyz's replication check: it fails when the replica is more
// than MaxLag behind, or the lag can't be measured.
func (r *Replication) Check(ctx context.Context) error {
	lag, err := r.Lag(ctx)
	if err != nil {
		return err
	}
	if lag > MaxLag {
		return fmt.Errorf("the replica is %s behind the database, over %s: Litestream isn't reading or uploading changes (DESIGN 10.4)", lag.Round(time.Second), MaxLag)
	}
	return nil
}

// LastLag is the lag the last call measured, in seconds, or -1 before
// any did, for /metrics.
func (r *Replication) LastLag() float64 {
	ms := r.lastLag.Load()
	if ms < 0 {
		return -1
	}
	return float64(ms) / 1000
}
