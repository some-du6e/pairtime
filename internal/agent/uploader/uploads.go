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
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
)

// UploadsName is the file in the data directory where the uploader keeps
// its record of past uploads: a JSON line for each request the server
// answered, which the dashboard's Privacy page lists in plain words
// (DESIGN 8.8). It holds no body, only what went and the SHA-256 of the
// body `pairtime privacy show` printed, and never leaves the device
// (docs/errata/2026-11-21-upload-log.md).
const UploadsName = "uploads.jsonl"

// uploadsKeep is how long a past upload is kept, and uploadsMax how many
// at most, past which the oldest go.
const (
	uploadsKeep = 90 * 24 * time.Hour
	uploadsMax  = 200_000
)

// PastUpload is a request the server answered, as uploads.jsonl keeps it.
type PastUpload struct {
	// At is when the answer came, and Server where the request went.
	At     time.Time `json:"at"`
	Server string    `json:"server"`
	// Roots and Minutes count the batch roots and the minute bucket
	// revisions the request carried, and Labels says whether it carried
	// the labels.
	Roots   int  `json:"roots"`
	Minutes int  `json:"minutes"`
	Labels  bool `json:"labels"`
	// Commits counts the commit records it carried.
	Commits int `json:"commits"`
	// Status is the answer's HTTP status. Refused is the error code of an
	// answer that refused the request as a whole, and Results the result
	// codes of an answer that took it, each with how many of the
	// request's items got it.
	Status  int      `json:"status"`
	Refused string   `json:"refused,omitempty"`
	Results *Results `json:"results,omitempty"`
	// Replay is set when the answer was the one the server kept for a
	// request it had already taken, whose first answer was lost (ingest-v1
	// 2.10).
	Replay bool `json:"replay,omitempty"`
	// BodySHA256 is the SHA-256 of the request's body before gzip, in
	// hex: of the bytes `pairtime privacy show` printed for it.
	BodySHA256 string `json:"body_sha256"`
}

// Results are the result codes of an answer, by kind of item.
type Results struct {
	Roots   map[string]int `json:"roots,omitempty"`
	Minutes map[string]int `json:"minutes,omitempty"`
	Commits map[string]int `json:"commits,omitempty"`
	Labels  string         `json:"labels,omitempty"`
}

// pastUpload describes the request p, which went to server and got the
// answer a at at.
func pastUpload(at time.Time, server string, p plan, a answer) PastUpload {
	sum := sha256.Sum256(p.js)
	return PastUpload{
		At: at, Server: server,
		Roots: len(p.body.Batches), Minutes: len(p.body.Buckets), Labels: p.body.Labels != nil,
		Status: a.status, Replay: a.header.Get("X-Pairtime-Replay") == "true",
		BodySHA256: hex.EncodeToString(sum[:]),
	}
}

// resultsOf counts the result codes of resp.
func resultsOf(resp Response) *Results {
	count := func(items []ItemResult) map[string]int {
		if len(items) == 0 {
			return nil
		}
		m := map[string]int{}
		for _, it := range items {
			m[it.Code]++
		}
		return m
	}
	r := &Results{Roots: count(resp.Batches), Minutes: count(resp.Buckets), Commits: count(resp.Commits)}
	if resp.Labels != nil {
		r.Labels = resp.Labels.Code
	}
	return r
}

// recordUpload appends a past upload to uploads.jsonl, and once a day lets
// go of those older than uploadsKeep. A record that doesn't write is
// logged and changes nothing else: the upload went all the same.
func (u *Uploader) recordUpload(e PastUpload) {
	if err := appendUpload(u.opts.DataDir, e); err != nil {
		u.log.Warn("couldn't record a past upload in "+UploadsName, "err", err)
	}
	if now := u.now(); now.Sub(u.pruned) >= 24*time.Hour {
		u.pruned = now
		if err := pruneUploads(u.opts.DataDir, now); err != nil {
			u.log.Warn("couldn't let go of the past uploads older than 90 days", "err", err)
		}
	}
}

// appendUpload appends e to dir's uploads.jsonl, readable by this user
// only.
func appendUpload(dir string, e PastUpload) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, UploadsName), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// pruneUploads lets go of the past uploads in dir older than uploadsKeep
// before now, and of the oldest past uploadsMax, by writing the rest again
// with an atomic rename. A line that doesn't read, as one a crash cut
// short, goes too.
func pruneUploads(dir string, now time.Time) error {
	name := filepath.Join(dir, UploadsName)
	data, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	cut := now.Add(-uploadsKeep)
	var keep [][]byte
	changed := false
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var e PastUpload
		if json.Unmarshal(line, &e) != nil || e.At.Before(cut) {
			changed = true
			continue
		}
		keep = append(keep, line)
	}
	if len(keep) > uploadsMax {
		keep, changed = keep[len(keep)-uploadsMax:], true
	}
	if !changed {
		return nil
	}
	var out []byte
	for _, line := range keep {
		out = append(append(out, line...), '\n')
	}
	return home.WritePrivate(name, out)
}

// PastUploads returns the past uploads the data directory dataDir's
// uploads.jsonl keeps, oldest first, passing over a line that doesn't
// read.
func PastUploads(dataDir string) ([]PastUpload, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, UploadsName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []PastUpload
	for _, line := range bytes.Split(data, []byte("\n")) {
		var e PastUpload
		if len(line) > 0 && json.Unmarshal(line, &e) == nil {
			out = append(out, e)
		}
	}
	return out, nil
}
