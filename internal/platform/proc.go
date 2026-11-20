// SPDX-License-Identifier: AGPL-3.0-or-later

// The process table, read by the OS packages (internal/platform/windows,
// darwin and linux) and walked by the classifier in classify.go. A Proc
// holds only what classification needs: ids, the executable's name, the
// start time, the owner and, where the OS has them, the process group and
// whether a terminal is attached. Command lines, environments and memory of
// other processes are never read, because they can hold prompt text and
// secrets.

package platform

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"time"
)

// TTY says whether a process has a controlling terminal.
type TTY uint8

const (
	// TTYUnknown means the OS doesn't say, as on Windows.
	TTYUnknown TTY = iota
	// TTYNone means the process has no controlling terminal.
	TTYNone
	// TTYAttached means it has one.
	TTYAttached
)

func (t TTY) String() string {
	switch t {
	case TTYNone:
		return "none"
	case TTYAttached:
		return "attached"
	}
	return "unknown"
}

// MarshalText writes the String form, so recorded tables read as words.
func (t TTY) MarshalText() ([]byte, error) { return []byte(t.String()), nil }

// UnmarshalText reads the String form.
func (t *TTY) UnmarshalText(b []byte) error {
	switch string(b) {
	case "unknown":
		*t = TTYUnknown
	case "none":
		*t = TTYNone
	case "attached":
		*t = TTYAttached
	default:
		return fmt.Errorf("tty: want unknown, none or attached, got %q", b)
	}
	return nil
}

// Proc is one process.
type Proc struct {
	PID  int `json:"pid"`
	PPID int `json:"ppid"`
	// Name is the executable's name, from ExeName: for a script started
	// through a #! line it is the interpreter's.
	Name string `json:"name"`
	// Alias is the name the process goes by where that differs from Name:
	// Linux's comm and macOS's p_comm, which a script's own file name or a
	// program's chosen title sets (a Node app that sets process.title). It
	// is empty where the OS has no such name or it equals Name.
	Alias string `json:"alias,omitempty"`
	// Start is when the process started, in wall-clock time, zero when the
	// OS wouldn't say. Windows and macOS record it when the process starts.
	// Linux keeps the start as Ticks after boot, and the reader adds the
	// boot time, which moves whenever the wall clock is stepped, so on Linux
	// two reads of one process can give different Starts. ID, not Start,
	// names a process.
	Start time.Time `json:"start,omitzero"`
	// Ticks is the start in clock ticks after boot, where the OS keeps it
	// that way: Linux's starttime (proc(5)). It is 0 elsewhere. No clock
	// step changes it, but it counts from boot, so it names a process only
	// together with the boot (BootID in internal/platform/linux).
	Ticks uint64 `json:"ticks,omitempty"`
	// PGID is the process group, 0 where the OS has none (Windows).
	PGID int `json:"pgid,omitempty"`
	TTY  TTY `json:"tty,omitempty"`
	// TPGID is the foreground process group of the process's controlling
	// terminal: the group that reads what is typed there. It is 0 when the
	// process has no terminal or the OS doesn't say (Windows).
	TPGID int `json:"tpgid,omitempty"`
	// Owner is whose process it is, from UIDOwner or SessionOwner: on Linux
	// and macOS the user it runs as (its effective uid), on Windows the
	// session it runs in, the Remote Desktop Services session that each
	// signed-in user has. It is empty when the reader couldn't tell, which
	// on Windows includes a harness process the reader couldn't open.
	// Owners compare as plain strings, and never leave the device.
	Owner string `json:"owner,omitempty"`
}

// UIDOwner is the Owner of a process that runs as uid, on Linux and macOS.
func UIDOwner(uid uint32) string { return "uid:" + strconv.FormatUint(uint64(uid), 10) }

// SessionOwner is the Owner of a process that runs in the Windows session
// id (ProcessIdToSessionId's session).
func SessionOwner(id uint32) string { return "session:" + strconv.FormatUint(uint64(id), 10) }

// ID names one process for as long as it runs: its PID, and its start in
// the reader's own terms, so a reused PID gets a new ID and a wall-clock
// step changes none. Two reads of a process on one boot give equal IDs.
type ID struct {
	PID int `json:"pid"`
	// Start is Ticks where the reader sets them (Linux), else Start in Unix
	// nanoseconds, and 0 when the start is unknown.
	Start int64 `json:"start"`
}

// ID returns p's ID.
func (p Proc) ID() ID {
	switch {
	case p.Ticks != 0:
		return ID{PID: p.PID, Start: int64(p.Ticks)}
	case !p.Start.IsZero():
		return ID{PID: p.PID, Start: p.Start.UnixNano()}
	}
	return ID{PID: p.PID}
}

// MaxChain caps a parent chain. Real chains are under 20 long; the cap
// only stops a damaged table from looping.
const MaxChain = 64

// Table is one snapshot of the process table, for one OS's names.
type Table struct {
	goos  string
	procs map[int]Proc
}

// NewTable indexes procs, read on goos. A later entry for a PID replaces
// an earlier one.
func NewTable(goos string, procs []Proc) *Table {
	t := &Table{goos: goos, procs: make(map[int]Proc, len(procs))}
	for _, p := range procs {
		t.procs[p.PID] = p
	}
	return t
}

// OS returns the operating system the table was read on.
func (t *Table) OS() string { return t.goos }

// Len returns the number of processes.
func (t *Table) Len() int { return len(t.procs) }

// Get returns the process with the given PID.
func (t *Table) Get(pid int) (Proc, bool) {
	p, ok := t.procs[pid]
	return p, ok
}

// Procs returns every process, by PID.
func (t *Table) Procs() []Proc {
	out := make([]Proc, 0, len(t.procs))
	for _, p := range t.procs {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b Proc) int { return cmp.Compare(a.PID, b.PID) })
	return out
}

// Parent returns p's parent. It reports false at the root, when the parent
// has exited, and when the PID p names now belongs to a process that
// started after p did: Windows keeps a process's parent PID after the
// parent exits, and the PID can be reused (CP 3.3). The check needs both
// start times, so the readers give every process one, the Windows reader
// without opening the process.
func (t *Table) Parent(p Proc) (Proc, bool) {
	if p.PPID <= 0 || p.PPID == p.PID {
		return Proc{}, false
	}
	q, ok := t.procs[p.PPID]
	if !ok {
		return Proc{}, false
	}
	if !p.Start.IsZero() && !q.Start.IsZero() && q.Start.After(p.Start) {
		return Proc{}, false
	}
	return q, true
}

// Chain returns the process pid and its ancestors, nearest first, so
// Chain(pid)[0] is pid itself. It is empty when pid isn't in the table.
func (t *Table) Chain(pid int) []Proc {
	p, ok := t.procs[pid]
	if !ok {
		return nil
	}
	chain := []Proc{p}
	seen := map[int]bool{pid: true}
	for len(chain) < MaxChain {
		q, ok := t.Parent(p)
		if !ok || seen[q.PID] {
			break
		}
		seen[q.PID] = true
		chain = append(chain, q)
		p = q
	}
	return chain
}

// Descendants returns every process whose chain passes through pid, not
// counting pid itself, by PID. Focus uses it to find the harness sessions
// a window hosts (SM 3.2).
func (t *Table) Descendants(pid int) []Proc {
	root, ok := t.procs[pid]
	if !ok {
		return nil
	}
	children := make(map[int][]Proc)
	for _, p := range t.procs {
		if q, ok := t.Parent(p); ok {
			children[q.PID] = append(children[q.PID], p)
		}
	}
	var out []Proc
	seen := map[int]bool{root.PID: true}
	queue := []int{root.PID}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, c := range children[next] {
			if seen[c.PID] {
				continue
			}
			seen[c.PID] = true
			out = append(out, c)
			queue = append(queue, c.PID)
		}
	}
	slices.SortFunc(out, func(a, b Proc) int { return cmp.Compare(a.PID, b.PID) })
	return out
}
