// SPDX-License-Identifier: AGPL-3.0-or-later

package inbox

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Rewrite keeps the lines of name, a private file of lines such as the
// agent's held.jsonl, for which keep returns true, in the order they come,
// and reports how many it dropped. It writes the lines it keeps to a new
// file beside name, named tmpPrefix and a random suffix, flushes it and
// renames it over name, so a kill at any moment leaves one of the two files
// whole. It opens name as OpenPrivate does and closes it before the rename,
// which Windows refuses while any handle to name is open, so the caller
// must have none open either. keep gets each line that isn't blank, with
// its newline, and the offset it starts at; blank lines stay as they are.
// A rewrite that would drop nothing leaves name as it is, and one that
// would keep no line deletes it.
func Rewrite(name, tmpPrefix string, keep func(line []byte, at int64) bool) (dropped int, err error) {
	src, err := OpenPrivate(name, os.O_RDONLY, 0)
	if err != nil {
		return 0, err
	}
	srcOpen := true
	defer func() {
		if srcOpen {
			src.Close()
		}
	}()
	tmp, err := os.CreateTemp(filepath.Dir(name), tmpPrefix)
	if err != nil {
		return 0, err
	}
	renamed := false
	defer func() {
		if !renamed {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	w := bufio.NewWriter(tmp)
	kept := 0
	at := int64(0)
	for r := bufio.NewReader(src); ; {
		line, rerr := r.ReadBytes('\n')
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return 0, rerr
		}
		switch {
		case len(bytes.TrimSpace(line)) == 0:
			w.Write(line)
		case keep(line, at):
			w.Write(line)
			kept++
		default:
			dropped++
		}
		at += int64(len(line))
		if rerr != nil {
			break // EOF
		}
	}
	if dropped == 0 {
		return 0, nil
	}
	srcOpen = false
	if err := src.Close(); err != nil {
		return 0, err
	}
	if kept == 0 {
		if err := os.Remove(name); err != nil {
			return 0, err
		}
		return dropped, nil
	}
	if err := w.Flush(); err != nil {
		return 0, err
	}
	if err := tmp.Sync(); err != nil {
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp.Name(), name); err != nil {
		return 0, err
	}
	renamed = true
	return dropped, nil
}
