// SPDX-License-Identifier: AGPL-3.0-or-later

// The process table, read by the OS packages (internal/platform/windows,
// darwin and linux) and walked by the classifier in classify.go. A Proc
// holds only what classification needs: ids, the executable's name, the
// start time and, where the OS has them, the process group and whether a
// terminal is attached. Command lines, environments and memory of other
// processes are never read, because they can hold prompt text and secrets.

package platform

import (
	"cmp"
	"fmt"
	"slices"
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
	// Start is when the process started, zero when the OS wouldn't say.
	// With PID it names one process even after the PID is reused.
	Start time.Time `json:"start,omitzero"`
	// PGID is the process group, 0 where the OS has none (Windows).
	PGID int `json:"pgid,omitempty"`
	TTY  TTY `json:"tty,omitempty"`
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
// parent exits, and the PID can be reused (CP 3.3).
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
