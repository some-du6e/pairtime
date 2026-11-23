// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DefaultTimeout caps one sqlite3 run. Every read is a rowid range or an
// index lookup and takes milliseconds, so one that takes this long is
// stuck, or waits on t3 doctor's compaction, which holds the store
// exclusively, and the reader gives up on it.
const DefaultTimeout = 5 * time.Second

// maxOutput caps what one run may print. The biggest read is a message's
// text, which the text query cuts at maxTextChars characters, hex-encoded.
const maxOutput = 8 << 20

// maxStderr is how much of a failed run's stderr the runner reads.
const maxStderr = 2 << 10

// ErrNoSQLite3 means no sqlite3 executable was found on PATH. The reader is
// off until one is: DESIGN 5.9's doctor line says how to install it.
var ErrNoSQLite3 = errors.New("sqlite3 isn't installed or isn't on PATH")

// ErrBusy means T3 Code held its store exclusively for longer than the
// read waits, as t3 doctor's compaction does. The read can't answer, and the
// next one tries again.
var ErrBusy = errors.New("T3 Code's store was busy")

// ErrOutputTooLong means a run printed more than the reader reads.
var ErrOutputTooLong = errors.New("sqlite3 printed more than the reader reads")

// ErrOutput means sqlite3 printed something that isn't the hex fields the
// reader asked for.
var ErrOutput = errors.New("sqlite3 printed something the reader didn't ask for")

// QueryError is a sqlite3 run that exited with a non-zero status.
type QueryError struct {
	Code int
	// Detail is the first line sqlite3 wrote to stderr, with every quoted
	// string and hex literal in it cut out, since the SQL names the folders
	// pairtime tracks.
	Detail string
}

func (e *QueryError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("sqlite3 exited with status %d", e.Code)
	}
	return fmt.Sprintf("sqlite3 exited with status %d: %s", e.Code, e.Detail)
}

// Runner runs the sqlite3 command-line tool, read-only, on one store at a
// time (DESIGN 5.1): `sqlite3 -readonly -batch -bail -init <null device>
// <store>`, so no .sqliterc runs, with the SQL on stdin, never as an
// argument, since it names the folders pairtime tracks and other accounts
// on the machine can read a process's arguments. Every field comes back
// hex-encoded behind a fixed separator, since -json needs sqlite3 3.33 and
// Ubuntu 20.04 ships 3.31. Each read is one short process, at low CPU
// priority and under a timeout. Its zero value runs "sqlite3" from PATH. It
// is safe for concurrent use.
type Runner struct {
	// Path is the sqlite3 executable. Empty means "sqlite3", looked up in
	// PATH.
	Path string
	// Timeout caps one run whose context has no deadline. Zero means
	// DefaultTimeout.
	Timeout time.Duration

	// mu guards exe. Only a lookup that worked is kept, so a sqlite3
	// installed later is found on the next read.
	mu  sync.Mutex
	exe string
}

// Exe returns the sqlite3 executable, or ErrNoSQLite3.
func (r *Runner) Exe() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.exe != "" {
		return r.exe, nil
	}
	name := r.Path
	if name == "" {
		name = "sqlite3"
	}
	exe, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoSQLite3, err)
	}
	r.exe = exe
	return exe, nil
}

// preamble starts every run's SQL: list mode, with a separator no hex field
// holds and N for NULL, which no hex field is either, and a busy timeout of
// 2 s, well under DefaultTimeout, for a store t3 doctor's compaction holds.
// hex of the empty string prints nothing, so a field that can be NULL is
// written with hx, which keeps NULL apart from the empty string.
const preamble = ".mode list\n.headers off\n.separator |\n.nullvalue N\n.timeout 2000\n"

// field is one value of an output row: NULL, or the bytes hex() encoded.
type field struct {
	null bool
	s    string
}

// row is one output line, every field decoded.
type row []field

// Query runs sql on the store at db and returns its rows. Every field of
// every row must be hex or N, as hx writes them.
func (r *Runner) Query(ctx context.Context, db, sql string) ([]row, error) {
	exe, err := r.Exe()
	if err != nil {
		return nil, err
	}
	if _, ok := ctx.Deadline(); !ok {
		timeout := r.Timeout
		if timeout <= 0 {
			timeout = DefaultTimeout
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	// The store's path is absolute (Locate), and nothing else in the run
	// names a file, so sqlite3's working directory doesn't matter.
	cmd := exec.CommandContext(ctx, exe, "-readonly", "-batch", "-bail", "-init", os.DevNull, db)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out := &capped{max: maxOutput}
	errOut := &capped{max: maxStderr, quiet: true}
	cmd.Stdout = out
	cmd.Stderr = errOut
	prepare(cmd)
	if err := cmd.Start(); err != nil {
		stdin.Close()
		// Start's error names the executable, which can be in the home
		// folder, and the reader's words name no path (Status).
		var pe *fs.PathError
		if errors.As(err, &pe) {
			return nil, fmt.Errorf("sqlite3 didn't start: %w", pe.Err)
		}
		return nil, fmt.Errorf("sqlite3: %w", err)
	}
	// sqlite3 waits for its SQL on stdin, so lowering its priority now,
	// before the SQL goes in, comes before any of its work.
	lower(cmd.Process)
	// A write fails only when sqlite3 has already exited, and Wait says why.
	_, _ = io.WriteString(stdin, preamble+sql)
	stdin.Close()
	err = cmd.Wait()
	if out.over {
		return nil, ErrOutputTooLong
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("sqlite3: %w", ctx.Err())
		}
		detail := firstLine(errOut.buf.String())
		if strings.Contains(detail, "database is locked") || strings.Contains(detail, "database table is locked") {
			return nil, ErrBusy
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, &QueryError{Code: exit.ExitCode(), Detail: detail}
		}
		return nil, fmt.Errorf("sqlite3: %w", err)
	}
	return parseRows(out.buf.Bytes())
}

// literals are what firstLine cuts out of an error: quoted strings and hex
// literals, which can be pieces of the SQL, and so of folder paths. It is
// compiled on first use, since every client package's init runs in every
// hook call.
var literals = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`(?i)x'[0-9a-f]*'|'[^']*'|"[^"]*"`)
})

// firstLine returns the first line of s, trimmed, with its literals cut
// out and at most 200 bytes long.
func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	s = literals().ReplaceAllString(strings.TrimSpace(s), "…")
	if len(s) > 200 {
		s = strings.ToValidUTF8(s[:200], "")
	}
	return s
}

// parseRows reads sqlite3's list-mode output: one line per row, its fields
// behind "|", each N or hex.
func parseRows(out []byte) ([]row, error) {
	var rows []row
	for _, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if len(line) == 0 {
			continue
		}
		var r row
		for _, f := range bytes.Split(line, []byte("|")) {
			if string(f) == "N" {
				r = append(r, field{null: true})
				continue
			}
			b := make([]byte, hex.DecodedLen(len(f)))
			if _, err := hex.Decode(b, f); err != nil {
				return nil, ErrOutput
			}
			r = append(r, field{s: string(b)})
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// capped is a writer that keeps the first max bytes and notes whether
// there were more. A quiet one drops the rest without failing the run.
type capped struct {
	buf   bytes.Buffer
	max   int
	over  bool
	quiet bool
}

func (c *capped) Write(p []byte) (int, error) {
	n := len(p)
	if room := c.max - c.buf.Len(); len(p) > room {
		p = p[:max(room, 0)]
		if !c.quiet {
			c.over = true
		}
	}
	c.buf.Write(p)
	if c.over {
		return 0, io.ErrShortWrite
	}
	return n, nil
}
