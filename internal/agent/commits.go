// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters/git"
	"github.com/some-du6e/pairtime/internal/agent/home"
)

// commitsKeep is how long commits/ keeps a day's file: 180 days, as the
// minute buckets are kept (DESIGN 3.5, 4.4; D-commit-records-local).
const commitsKeep = 180 * 24 * time.Hour

// commitStore is commits/ in the data directory, where the commit records
// of DESIGN 4.3 wait for the uploader, which sends them from here (PLAN,
// WP-AG5's follow-up; D-commit-records-local). The git host's sink appends
// each change's records once the ledger has taken its git.change, and
// before the watcher's cursor can move past the change, one line each, in
// the file of the UTC day the record's observed_at falls on,
// YYYY-MM-DD.jsonl. A line is a git.Commit's JSON, which holds only fields
// DESIGN 4.3 lets a client upload: no message, no path, and nothing of
// git.CommitLocal. The reflog that a record's rewrites come from expires,
// so a record dropped before the uploader takes it can't be made again. A
// day's file goes once it is commitsKeep old.
//
// Each line has a newline before it as well as after it, as the inbox's
// spool has, so a line a power cut left short can't take the next with it.
// A crash after the records went and before the cursor did writes them
// again at the next start, so a sha can have more than one line.
type commitStore struct {
	dir string

	mu     sync.Mutex
	pruned string // the UTC day the last prune ran on
}

// add appends commits to the files of their days, each file's lines with
// one write and a flush, and has prune drop the files past commitsKeep.
func (s *commitStore) add(commits []git.Commit, now time.Time) error {
	if len(commits) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.dir); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(s.dir, 0o700); err != nil {
			return err
		}
		home.SyncDir(filepath.Dir(s.dir))
	}
	s.pruneLocked(now)
	lines := make(map[string][]byte)
	var days []string
	for _, c := range commits {
		b, err := json.Marshal(c)
		if err != nil {
			return err
		}
		at := now
		if c.ObservedAt > 0 {
			at = time.UnixMilli(c.ObservedAt)
		}
		day := at.UTC().Format(time.DateOnly)
		if _, ok := lines[day]; !ok {
			days = append(days, day)
		}
		l := append(lines[day], '\n')
		l = append(l, b...)
		lines[day] = append(l, '\n')
	}
	var errs []error
	for _, day := range days {
		errs = append(errs, appendSynced(filepath.Join(s.dir, day+".jsonl"), lines[day]))
	}
	return errors.Join(errs...)
}

// appendSynced appends b to the file name with one write, and flushes it,
// and its folder too when the file is new, so its name outlives a power cut
// as its lines do.
func appendSynced(name string, b []byte) error {
	_, statErr := os.Stat(name)
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if errors.Is(statErr, fs.ErrNotExist) {
		defer home.SyncDir(filepath.Dir(name))
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// prune deletes the files of the days commitsKeep before now, at most once
// a UTC day. The git host runs it at its start and with every poll.
func (s *commitStore) prune(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
}

func (s *commitStore) pruneLocked(now time.Time) {
	today := now.UTC().Format(time.DateOnly)
	if s.pruned == today {
		return
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return // none yet: the next add or poll looks again
	}
	s.pruned = today
	oldest := now.UTC().Add(-commitsKeep).Format(time.DateOnly)
	for _, e := range entries {
		day, ok := strings.CutSuffix(e.Name(), ".jsonl")
		if !ok || e.IsDir() {
			continue
		}
		if _, err := time.Parse(time.DateOnly, day); err == nil && day < oldest {
			os.Remove(filepath.Join(s.dir, e.Name()))
		}
	}
}
