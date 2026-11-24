// SPDX-License-Identifier: AGPL-3.0-or-later

package publisher

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/some-du6e/pairtime/pkg/ledger"
)

// FileFormat is a day file's format member (ledger-v1 8).
const FileFormat = "pairtime-day-root-v1"

// FileName returns day d's file under the directory, with slashes:
// <YYYY>/<YYYY-MM-DD>.json, by the day's UTC date (ledger-v1 8).
func FileName(d uint64) string {
	date := Date(d)
	return date[:4] + "/" + date + ".json"
}

// fileJSON is a day file, in its members' order.
type fileJSON struct {
	Format    string      `json:"format"`
	Day       uint64      `json:"day"`
	Date      string      `json:"date"`
	Prev      ledger.Hash `json:"prev"`
	Count     uint64      `json:"count"`
	Root      ledger.Hash `json:"root"`
	Head      ledger.Hash `json:"head"`
	KeyID     string      `json:"key_id"`
	Signature string      `json:"signature"`
}

// File returns the day's file (ledger-v1 8): d, its date, G_(d-1),
// count_d, T_d, G_d, the id of the key that signed and the signature, and
// nothing else.
func (h Day) File() ([]byte, error) {
	b, err := json.MarshalIndent(fileJSON{
		Format:    FileFormat,
		Day:       h.D,
		Date:      Date(h.D),
		Prev:      h.Prev,
		Count:     h.Count,
		Root:      h.Root,
		Head:      h.Head,
		KeyID:     h.KeyID,
		Signature: hex.EncodeToString(h.Signature[:]),
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("publisher: writing day %s's file: %w", Date(h.D), err)
	}
	return append(b, '\n'), nil
}

// file is what a day file is written through: an *os.File, or a test's
// stand-in.
type file interface {
	Write(p []byte) (int, error)
	Sync() error
	Close() error
}

// writeFiles writes the file of every stored head that has none under
// o.Dir, oldest first, and returns their days. A file that is there stays
// as it is. One that doesn't hold its stored head is reported and never
// replaced, since the sidecar may have published it already.
func writeFiles(ctx context.Context, o Options) ([]uint64, error) {
	heads, err := Heads(ctx, o.Read)
	if err != nil {
		return nil, err
	}
	var written []uint64
	var errs []error
	for _, h := range heads {
		data, err := h.File()
		if err != nil {
			return written, err
		}
		path := filepath.Join(o.Dir, filepath.FromSlash(FileName(h.D)))
		have, err := os.ReadFile(path)
		switch {
		case err == nil:
			if !bytes.Equal(have, data) {
				errs = append(errs, fmt.Errorf("publisher: %s doesn't hold day %s's stored head; it stays as it is, since the sidecar may have published it", path, Date(h.D)))
			}
			continue
		case !errors.Is(err, fs.ErrNotExist):
			return written, fmt.Errorf("publisher: %w", err)
		}
		if err := o.writeOnce(path, data); err != nil {
			return written, err
		}
		written = append(written, h.D)
	}
	return written, errors.Join(errs...)
}

// writeOnce writes data to path, which isn't there, through a dot-name in
// path's directory: the dot-file is written and synced, renamed to path,
// and the directory synced. So path never holds part of data, and a crash
// leaves at most the dot-file, which the sidecar leaves out and the next
// try replaces (deploy/witness/push.sh).
func (o Options) writeOnce(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := ensureDir(dir); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+filepath.Base(path)+".tmp")
	create := o.create
	if create == nil {
		create = func(name string) (file, error) {
			return os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		}
	}
	f, err := create(tmp)
	if err != nil {
		return fmt.Errorf("publisher: %w", err)
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("publisher: writing %s: %w", path, err)
	}
	return syncDir(dir)
}

// ensureDir makes dir when it is missing, and syncs its parent so the new
// entry lasts.
func ensureDir(dir string) error {
	fi, err := os.Stat(dir)
	switch {
	case err == nil && fi.IsDir():
		return nil
	case err == nil:
		return fmt.Errorf("publisher: %s isn't a directory", dir)
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("publisher: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("publisher: %w", err)
	}
	return syncDir(filepath.Dir(dir))
}

// syncDir syncs dir, so the names in it last. Windows can't sync a
// directory, and pairtimed doesn't publish there.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("publisher: %w", err)
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("publisher: syncing %s: %w", dir, err)
	}
	return nil
}
