// SPDX-License-Identifier: AGPL-3.0-or-later

package linux

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// backgroundEnv, set, makes the test binary the child TestBackground reads
// the priority of: it calls Background, starts threads of its own after it,
// says so on stdout and waits for stdin to close.
const backgroundEnv = "PAIRTIME_TEST_BACKGROUND_CHILD"

// maxLater bounds the threads the child starts after Background.
const maxLater = 16

// backgroundChild is the child's side of TestBackground. It reports the
// threads it started after Background: "ready" and their ids.
func backgroundChild() {
	if err := Background(); err != nil {
		fmt.Printf("error %v\n", err)
		return
	}
	before := map[int]bool{}
	for _, p := range mustPriorities() {
		before[p.TID] = true
	}
	// Each locked goroutine holds a thread of its own, so the runtime
	// starts new ones, from threads Background set, until one appears.
	hold := make(chan struct{})
	var later []string
	for i := 0; i < maxLater && len(later) == 0; i++ {
		started := make(chan struct{})
		go func() {
			runtime.LockOSThread()
			close(started)
			<-hold
		}()
		<-started
		for _, p := range mustPriorities() {
			if !before[p.TID] {
				later = append(later, strconv.Itoa(p.TID))
			}
		}
	}
	fmt.Printf("ready %s\n", strings.Join(later, ","))
	bufio.NewReader(os.Stdin).ReadString('\n')
	close(hold)
}

func mustPriorities() []ThreadPriority {
	ps, err := priorities("/proc/self/task")
	if err != nil {
		panic(err)
	}
	return ps
}

// TestBackground checks Background from outside the process it lowers, as
// WP-AD3 asks: the kernel's own record of each thread of a child that
// called it, its stat line's nice value and ioprio_get(2), reads back the
// lowest priority, for the threads that started after the call too.
func TestBackground(t *testing.T) {
	if os.Getenv(backgroundEnv) != "" {
		backgroundChild()
		return
	}
	before, autogroups := AutogroupNice(os.Getpid())
	cmd := exec.Command(os.Args[0], "-test.run=^TestBackground$")
	cmd.Env = append(os.Environ(), backgroundEnv+"=1")
	// A session of its own, as the agent starts lookback's reader, whose
	// autogroup Background may lower.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stdin.Close()
		cmd.Wait()
	}()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("the child said nothing: %v", err)
	}
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), "ready")
	if !ok {
		t.Fatalf("the child said %q", line)
	}
	later := map[int]bool{}
	for _, f := range strings.FieldsFunc(rest, func(r rune) bool { return r == ',' || r == ' ' }) {
		tid, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("the child said %q", line)
		}
		later[tid] = true
	}
	ps, err := Priorities(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) == 0 {
		t.Fatal("read no thread of the child")
	}
	every := func(ok func(ThreadPriority) bool) func(t *testing.T) {
		return func(t *testing.T) {
			for _, p := range ps {
				if !ok(p) {
					t.Errorf("thread %d: %+v", p.TID, p)
				}
			}
		}
	}
	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"every thread runs at nice 19", every(func(p ThreadPriority) bool { return p.Nice == LowestNice })},
		{"every thread runs in the idle scheduling policy", every(func(p ThreadPriority) bool { return p.Policy == unix.SCHED_IDLE })},
		{"every thread is in the idle I/O class", every(func(p ThreadPriority) bool { return p.IOClass == IOClassIdle })},
		{"the child's session's autogroup is at nice 19, and the test's own is as it was", func(t *testing.T) {
			if !autogroups {
				t.Skip("this kernel keeps no autogroups")
			}
			child, ok := AutogroupNice(cmd.Process.Pid)
			after, _ := AutogroupNice(os.Getpid())
			if !ok || child != LowestNice || after != before {
				t.Errorf("the child's autogroup is at %d (%v), the test's at %d, was %d", child, ok, after, before)
			}
		}},
		{"the threads started after Background are among them", func(t *testing.T) {
			found := 0
			for _, p := range ps {
				if later[p.TID] {
					found++
				}
			}
			if len(later) == 0 || found != len(later) {
				t.Errorf("the child started threads %v after Background, and %d of them are among the %d read", later, found, len(ps))
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.check)
	}
}

// TestStatNice checks the nice value read from a stat line, whose comm may
// hold spaces and parentheses.
func TestStatNice(t *testing.T) {
	fields := func(nice string) string {
		f := make([]string, 20)
		for i := range f {
			f[i] = "0"
		}
		f[0] = "S"
		f[16] = nice
		return strings.Join(f, " ")
	}
	cases := []struct {
		name, line string
		want       int
		bad        bool
	}{
		{"field 19 is the nice value", "42 (pairtime) " + fields("19"), 19, false},
		{"a negative nice value reads as one", "42 (pairtime) " + fields("-5"), -5, false},
		{"a comm with spaces and parentheses ends at the last parenthesis", "42 (a) b (c)) " + fields("7"), 7, false},
		{"a line cut short is an error", "42 (pairtime) S 1 2", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := statNice([]byte(tc.line))
			if (err != nil) != tc.bad || got != tc.want {
				t.Errorf("statNice = %d, %v; want %d, error %v", got, err, tc.want, tc.bad)
			}
		})
	}
}
