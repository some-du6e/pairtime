// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/store"
)

// maxCommits is how many commit records one request carries at most
// (ingest-v1 2.4).
const maxCommits = 500

// The observed_at the server takes, in Unix ms: from 2020 to before 2100
// (ingest-v1 2.3.3).
const (
	minObservedAt = 1_577_836_800_000
	maxObservedAt = 4_102_444_800_000
)

// commitEntry is a line of commits/ a plan decided: its day, the mark just
// past it, and the record it sends, nil for a line withheld for good or
// passed over. An entry with the zero mark reads its day again from its
// first line.
type commitEntry struct {
	day    string
	mark   store.Mark
	commit *Commit
}

// commitLine is a whole line of a day's file, without its newline, and
// where it ends, just past its newline.
type commitLine struct {
	line []byte
	end  int64
}

// commitVerdict is what a plan does with the first line of a sha.
type commitVerdict int

const (
	// commitGoes: the record goes in the request.
	commitGoes commitVerdict = iota
	// commitWaits: the line waits for a later plan, and the rest of its
	// day with it, since a mark only ever passes lines that were decided.
	commitWaits
	// commitWithheld: the line never goes, nor its sha in a later line.
	commitWithheld
)

// commitsOf reads commits/ for the next request: up to n records that go,
// and the lines decided before them, in the order commits/ holds them. It
// also returns the days the folder has, the days whose file it couldn't
// read, and whether a record that could go waits behind the limit. It only
// reads: the agent's git host appends to the files, one whole line at a
// time, each with a newline before it as well as after it.
//
// A sha goes once per device, as the first line commits/ holds for it
// (D-ag5-D2). Each plan reads every kept day's file whole. The shas of the
// lines before each day's mark were decided already. The new lines, read
// oldest day first and then in file order, each add their sha, those after
// a line that waits included. The first line of a sha is judged by
// judgeCommit. One that waits holds back the rest of its day: their marks
// wait with it, as a mark passes only decided lines, and they are read
// again next time. A new line whose sha is already among them is passed
// over when the sha's first line went or was withheld. When the first line
// still waits, or was held back with its day, the new line waits too,
// holding back its own day: no mark passes a line of a sha whose first
// line is undecided, so the next plan judges the first line first, rather
// than find its sha before a later line's mark and pass it over too. A day
// whose file can't be read, for a reason other than being gone, stops every
// line in the plan, whatever its day: none goes and no mark moves, since
// the file may hold the first line of a sha that another day's line has
// too. Its day is in unread for doctor.
func commitsOf(in planInput, n int) (entries []commitEntry, days map[string]bool, unread []string, more bool) {
	if in.commitDir == "" {
		return nil, nil, nil, false
	}
	files, err := store.BucketDays(in.commitDir)
	if err != nil {
		return nil, nil, []string{"any day"}, false
	}
	type dayFile struct {
		day   string
		lines []commitLine
		// from is the first line after the day's mark, and restart says the
		// mark wasn't at the end of the line it names, so the day is read
		// from its first line.
		from    int
		restart bool
	}
	var read []dayFile
	days = make(map[string]bool, len(files))
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(in.commitDir, f.Day+".jsonl"))
		if errors.Is(err, fs.ErrNotExist) {
			continue // pruned since the folder was read
		}
		if err != nil {
			unread = append(unread, f.Day)
			continue
		}
		d := dayFile{day: f.Day, lines: wholeLines(data)}
		d.from, d.restart = afterMark(d.lines, in.st.CommitMarks[f.Day])
		days[f.Day] = true
		read = append(read, d)
	}
	if len(unread) > 0 {
		return nil, nil, unread, false
	}
	if in.rows.behind || slices.ContainsFunc(in.rows.stretches, func(s ledger.Stretch) bool { return s.Open }) {
		// The rows don't hold every stretch the writer has: one goes on, so
		// its end isn't recorded yet, or an older version's rows saw
		// config.toml local-only past the list the rows hold. Every line
		// waits, and no mark moves, until a seal frame records them
		// (D-ag5-D1).
		return nil, nil, nil, false
	}
	// seen holds the shas of the lines read so far, and undecided those
	// whose first line waits in this plan, or was held back with its day.
	seen, undecided := map[string]bool{}, map[string]bool{}
	for _, d := range read {
		for _, l := range d.lines[:d.from] {
			if c, ok := commitOf(l.line); ok {
				seen[c.SHA] = true
			}
		}
	}
	count := 0
	for _, d := range read {
		held, took := false, 0
		for _, l := range d.lines[d.from:] {
			if len(l.line) == 0 {
				continue // the newline before a line
			}
			c, ok := commitOf(l.line)
			if held {
				if ok && !seen[c.SHA] {
					seen[c.SHA], undecided[c.SHA] = true, true
				}
				continue
			}
			if ok && undecided[c.SHA] {
				// A later line of a sha whose first line waits waits with it.
				held = true
				continue
			}
			mark := store.Mark{Off: l.end, Line: commitDigest(l.line)}
			if !ok || seen[c.SHA] {
				// A line that doesn't read as a record, as one a power cut cut
				// short, or a sha whose first line came before it and went or
				// was withheld, as after a crash's second scan or another
				// clone's pull, is passed over.
				entries = append(entries, commitEntry{day: d.day, mark: mark})
				took++
				continue
			}
			seen[c.SHA] = true
			switch judgeCommit(in, c.ObservedAt) {
			case commitWaits:
				undecided[c.SHA] = true
				held = true
				continue
			case commitWithheld:
				entries = append(entries, commitEntry{day: d.day, mark: mark})
			case commitGoes:
				if count == n {
					// It goes in the next request, and the lines after it
					// are decided then.
					return entries, days, nil, true
				}
				count++
				entries = append(entries, commitEntry{day: d.day, mark: mark, commit: &c})
			}
			took++
		}
		if d.restart && took == 0 {
			entries = append(entries, commitEntry{day: d.day})
		}
	}
	return entries, days, nil, false
}

// judgeCommit decides the first line of a sha by its observed_at, m, in
// Unix ms on the device's clock (D-ag5-D1, D3). The record is withheld for
// good when m is outside the server's range; before the chain began, or,
// once a retention sweep dropped the ledger's oldest batches, before the
// oldest one kept was sealed, as a commit whose first batch after it may be
// one dropped; when B*, the first batch sealed at or after m, as
// rowIndex.sealedAfter finds it, was sealed in local-only mode
// (saved.localOnly), which takes in the batches before a link, a link's gap
// and the runs; when m falls where such a run ended (saved.endsRun); and
// when a local-only stretch the rows hold has m in it by the wall clock
// alone, passed or not, both edges included (ledger.Stretch.Holds), as it
// would a record of it with no monotonic reading. It waits while the rows
// hold no B* yet, while the link's first batch isn't
// stored (Linking), and until B*'s root has been answered, stored with its
// receipt or passed as gone (B* < Next), so it never goes in the request
// that carries B*'s own root. Otherwise it goes.
func judgeCommit(in planInput, m int64) commitVerdict {
	ix := in.rows
	switch {
	case m < minObservedAt || m >= maxObservedAt:
		return commitWithheld
	case ix.created > 0 && m < ix.created:
		return commitWithheld
	}
	if ix.base > 1 {
		if t, ok := ix.sealedAt(ix.start()); ok && m < t {
			return commitWithheld
		}
	}
	b, ok := ix.sealedAfter(m)
	if !ok {
		return commitWaits
	}
	if in.st.localOnly(b) || in.st.endsRun(ix, m) || slices.ContainsFunc(ix.stretches, func(s ledger.Stretch) bool { return s.Holds(m) }) {
		return commitWithheld
	}
	if in.st.Linking || b >= in.st.Next {
		return commitWaits
	}
	return commitGoes
}

// commitOf reads a line of commits/ as a commit record, through the
// uploader's own type, and reports whether it is one with a commit id for
// its sha.
func commitOf(line []byte) (Commit, bool) {
	var c Commit
	if err := json.Unmarshal(line, &c); err != nil || !isCommitID(c.SHA) {
		return Commit{}, false
	}
	return c, true
}

// isCommitID reports whether s is a commit id as the server takes it: 40 or
// 64 lowercase hex digits (ingest-v1 2.3.3). It is written out rather than
// a regexp, so nothing is compiled when a hook process starts.
func isCommitID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// wholeLines splits a day's file into its whole lines, each without its
// newline and with where it ends. A last line with no newline yet, as one
// the git host is still writing, is left for a later read.
func wholeLines(data []byte) []commitLine {
	var out []commitLine
	off := int64(0)
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			return out
		}
		off += int64(i + 1)
		out = append(out, commitLine{line: data[:i], end: off})
		data = data[i+1:]
	}
}

// afterMark returns the place in lines just past the line the mark m
// names, 0 for the zero mark, and restart when m names no line that ends
// where it says, as after the file was cut and written again.
func afterMark(lines []commitLine, m store.Mark) (from int, restart bool) {
	if m.Off == 0 {
		return 0, false
	}
	for i, l := range lines {
		if l.end == m.Off {
			if commitDigest(l.line) == m.Line {
				return i + 1, false
			}
			break
		}
	}
	return 0, true
}

// commitDigest is a commit mark's digest of its line, without the newline:
// 16 hex digits of its SHA-256, as the bucket store's marks have.
func commitDigest(line []byte) string {
	sum := sha256.Sum256(line)
	return hex.EncodeToString(sum[:8])
}

// commitsIn counts the entries that send a record.
func commitsIn(entries []commitEntry) int {
	n := 0
	for _, e := range entries {
		if e.commit != nil {
			n++
		}
	}
	return n
}

// firstCommits returns the entries before the one that sends the
// (k+1)-th record: those up to and including the k-th, and the lines
// decided after it up to the next one that goes.
func firstCommits(entries []commitEntry, k int) []commitEntry {
	n := 0
	for i, e := range entries {
		if e.commit == nil {
			continue
		}
		if n == k {
			return entries[:i]
		}
		n++
	}
	return entries
}
