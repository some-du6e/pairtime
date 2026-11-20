// SPDX-License-Identifier: AGPL-3.0-or-later

package ipc_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
)

// The test binary doubles as the processes these tests need. childEnv
// names the part it plays; the rest of its settings come in other
// environment variables.
const (
	childEnv = "PAIRTIME_TEST_CHILD"
	dataEnv  = "PAIRTIME_TEST_DATA"
	pipeEnv  = "PAIRTIME_TEST_PIPE"
)

// children are the parts the test binary can play, by name.
var children = map[string]func() int{
	"agent": runAgent,
}

func TestMain(m *testing.M) {
	if part := os.Getenv(childEnv); part != "" {
		run, ok := children[part]
		if !ok {
			fmt.Fprintf(os.Stderr, "no child part %q\n", part)
			os.Exit(2)
		}
		os.Exit(run())
	}
	os.Exit(m.Run())
}

// runAgent is a real agent, run by agent.Run in the data directory that
// dataEnv names, draining the inbox every 100 ms. It prints "ready" once it
// takes messages, and stops cleanly when its stdin closes.
func runAgent() int {
	data := os.Getenv(dataEnv)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		io.Copy(io.Discard, os.Stdin)
		cancel()
	}()
	err := agent.Run(ctx, agent.Options{
		Dirs:       home.Dirs{Data: data, Config: data, Runtime: data},
		Log:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Ports:      []int{0},
		ForcePipe:  os.Getenv(pipeEnv) == "1",
		DrainEvery: 100 * time.Millisecond,
		Ready:      func() { fmt.Println("ready") },
		// The device key goes in a key file here, not the OS's store.
		KeyStores: []keys.Store{},
	})
	switch {
	case errors.Is(err, agent.ErrAlreadyRunning):
		return 3
	case err != nil:
		fmt.Fprintln(os.Stderr, "agent:", err)
		return 1
	}
	return 0
}

// shortDir makes a directory whose path leaves room for the socket's name.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// agentProc is an agent process started by startAgent.
type agentProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	waited bool
}

// startAgent starts an agent process on data and waits until it takes
// messages the way it should: on the socket, or on the pipe when pipe is
// set. An agent killed a moment ago may still hold the data directory's
// lock or its pipe, so a start that comes up without them is tried again.
func startAgent(t *testing.T, data string, pipe bool) *agentProc {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := inbox.ViaSocket
	if pipe {
		want = inbox.ViaPipe
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		p, err := tryStart(exe, data, pipe)
		if err == nil {
			st, serr := agentStatus(data)
			if serr == nil && st.IPC.Via == want {
				t.Cleanup(func() { p.kill() })
				return p
			}
			err = fmt.Errorf("it takes messages by %q, want %q (status: %v; problems: %v)", st.IPC.Via, want, serr, st.Problems)
			p.stop()
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent didn't come up within 60 s: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func tryStart(exe, data string, pipe bool) (*agentProc, error) {
	cmd := exec.Command(exe, "-test.run=^$")
	usePipe := "0"
	if pipe {
		usePipe = "1"
	}
	cmd.Env = append(os.Environ(), childEnv+"=agent", dataEnv+"="+data, pipeEnv+"="+usePipe)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &agentProc{cmd: cmd, stdin: stdin}
	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(stdout).ReadString('\n')
		line <- s
	}()
	select {
	case s := <-line:
		if s == "ready\n" {
			return p, nil
		}
		p.kill()
		return nil, fmt.Errorf("the agent exited before it was ready: %v", cmd.ProcessState)
	case <-time.After(30 * time.Second):
		p.kill()
		return nil, errors.New("the agent wasn't ready within 30 s")
	}
}

// kill kills the agent the way kill -9 does, with no chance to clean up,
// and waits for it to be gone.
func (p *agentProc) kill() {
	if p.waited {
		return
	}
	p.cmd.Process.Kill()
	p.cmd.Wait()
	p.waited = true
}

// stop closes the agent's stdin, which makes it stop cleanly, and waits.
func (p *agentProc) stop() error {
	if p.waited {
		return nil
	}
	p.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		p.waited = true
		return err
	case <-time.After(30 * time.Second):
		p.cmd.Process.Kill()
		<-done
		p.waited = true
		return errors.New("the agent didn't stop within 30 s, so it was killed")
	}
}

// agentReport is the part of a running agent's GET /v1/status that these
// tests read.
type agentReport struct {
	IPC struct {
		// Via is how the agent takes messages.
		Via inbox.Via `json:"via"`
		// Duplicates counts the second copies it dropped since it started.
		Duplicates uint64 `json:"duplicates"`
	} `json:"ipc"`
	Problems map[string]string `json:"problems"`
}

// agentStatus asks the agent that runs on data for its status.
func agentStatus(data string) (agentReport, error) {
	pf, err := localhttp.ReadPortFile(home.Dirs{Data: data}.PortFile())
	if err != nil {
		return agentReport{}, err
	}
	req, err := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/v1/status", pf.Port), nil)
	if err != nil {
		return agentReport{}, err
	}
	req.Header.Set("Authorization", "Bearer "+pf.Token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return agentReport{}, err
	}
	defer resp.Body.Close()
	var r agentReport
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return agentReport{}, err
	}
	return r, nil
}

// stored is one line of the agent's journal, read the way a test that
// trusts nothing in package inbox would.
type stored struct {
	ID  string    `json:"id"`
	Via inbox.Via `json:"via"`
}

func readJournal(t *testing.T, data string) []stored {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(data, "received", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var all []stored
	for _, name := range names {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			var s stored
			if json.Unmarshal([]byte(line), &s) == nil && s.ID != "" {
				all = append(all, s)
			}
		}
	}
	return all
}

// spoolLeft lists the spool files still in the inbox.
func spoolLeft(t *testing.T, data string) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(data, "inbox", "*"))
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// TestAgentKilledMidStream is WP-AG2's oracle: the senders' count of
// messages against the agent's, across real kills of a real agent. Four
// senders hand 1,000 messages to the agent the way hooks do, with
// ipc.Send and its 100 ms timeout. The agent is killed twice while they
// send, and they spool while it is down.
//
// A kill seldom lands between a store and its ack, so while the agent is
// down the test makes the duplicates itself: copies of acked messages in
// the spool, as senders whose ack came too late leave them, and a taken
// file of other copies, as a drain killed before its delete leaves it.
// Every message must be stored exactly once, and the last agent must count
// the copies made while it was down among the duplicates it dropped.
func TestAgentKilledMidStream(t *testing.T) {
	tests := []struct {
		name string
		pipe bool
	}{
		{name: "1,000 messages sent across two kills of the agent are each stored exactly once"},
		{name: "the same holds on the named pipe when it is forced", pipe: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.pipe && runtime.GOOS != "windows" {
				t.Skip("the named pipe is the Windows fallback")
			}
			const total, senders = 1000, 4
			data := shortDir(t)
			ep, err := ipc.EndpointFor(home.Dirs{Data: data, Config: data, Runtime: data})
			if err != nil {
				t.Fatal(err)
			}
			proc := startAgent(t, data, tt.pipe)

			// gate holds the senders back while the agent restarts, so
			// that they don't spool everything left while it comes up.
			// Kills don't wait for it: they land mid-stream.
			var gate sync.RWMutex
			var next, done atomic.Int64
			sent := make([][]string, senders)
			// acked holds the messages the agent acked, in the order it
			// did, for duplicate to copy.
			var ackedMu sync.Mutex
			var acked []inbox.Message
			var wg sync.WaitGroup
			// However the test ends, the senders stop first: they report
			// through t, which must not outlive the test.
			defer func() {
				next.Store(total)
				wg.Wait()
			}()
			for s := range senders {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						gate.RLock()
						n := next.Add(1) - 1
						if n >= total {
							gate.RUnlock()
							return
						}
						m := inbox.New("test", json.RawMessage(fmt.Sprintf(`{"n":%d}`, n)))
						via, err := ipc.Send(ep, m, ipc.HookTimeout)
						gate.RUnlock()
						if err != nil {
							t.Errorf("message %d went nowhere: %v", n, err)
							return
						}
						sent[s] = append(sent[s], m.ID)
						if via != inbox.ViaInbox {
							ackedMu.Lock()
							acked = append(acked, m)
							ackedMu.Unlock()
						}
						done.Add(1)
					}
				}()
			}
			waitFor := func(n int64) {
				t.Helper()
				deadline := time.Now().Add(2 * time.Minute)
				for done.Load() < n {
					if time.Now().After(deadline) {
						t.Fatalf("only %d messages were sent in 2 minutes", done.Load())
					}
					time.Sleep(time.Millisecond)
				}
			}
			restart := func() {
				t.Helper()
				gate.Lock()
				defer gate.Unlock()
				proc = startAgent(t, data, tt.pipe)
			}
			// duplicate makes, while the agent is down, both kinds of copy
			// that the next agent must drop when it drains them. It appends
			// copies of the last messages acked to the spool, as senders
			// whose ack came too late do, and writes copies of the ones
			// acked before those to a taken file, as a drain killed before
			// it deleted the file leaves it. It returns how many copies it
			// made.
			duplicate := func() int {
				t.Helper()
				const each = 5
				ackedMu.Lock()
				n := len(acked)
				var last []inbox.Message
				if n >= 2*each {
					last = slices.Clone(acked[n-2*each:])
				}
				ackedMu.Unlock()
				if last == nil {
					t.Fatalf("the agent acked %d messages before it was killed, too few to copy", n)
				}
				for _, m := range last[each:] {
					if err := inbox.Append(ep.Inbox, m); err != nil {
						t.Fatal(err)
					}
				}
				var b []byte
				for _, m := range last[:each] {
					line, err := m.Encode()
					if err != nil {
						t.Fatal(err)
					}
					b = append(b, '\n')
					b = append(b, line...)
					b = append(b, '\n')
				}
				// Named the way a drain names the spool it takes. Append made
				// the inbox directory, if no sender had.
				name := filepath.Join(ep.Inbox, fmt.Sprintf("spool-%020d-%s.drain", time.Now().UnixNano(), inbox.NewID()[:8]))
				if err := os.WriteFile(name, b, 0o600); err != nil {
					t.Fatal(err)
				}
				return len(last)
			}
			waitFor(250)
			proc.kill()
			duplicate()
			waitFor(350)
			restart()
			waitFor(650)
			proc.kill()
			// The last agent's status counts only what it dropped, so these
			// are the copies it must report.
			copies := duplicate()
			waitFor(750)
			restart()
			wg.Wait()
			if t.Failed() {
				return
			}

			want := make(map[string]bool)
			for _, list := range sent {
				for _, id := range list {
					want[id] = true
				}
			}
			if len(want) != total {
				t.Fatalf("the senders have %d distinct ids, want %d", len(want), total)
			}
			// The agent drains the spool every 100 ms. Wait until it holds
			// every message and the spool is empty, then stop it cleanly.
			deadline := time.Now().Add(time.Minute)
			for {
				got := readJournal(t, data)
				if len(got) >= total && len(spoolLeft(t, data)) == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("after a minute the journal has %d messages and the inbox has %v", len(got), spoolLeft(t, data))
				}
				time.Sleep(50 * time.Millisecond)
			}
			// The inbox is empty, so the agent has drained every copy made
			// while it was down, and each one was a message it had already.
			st, err := agentStatus(data)
			if err != nil {
				t.Fatalf("asking the last agent for its status: %v", err)
			}
			if st.IPC.Duplicates < uint64(copies) {
				t.Errorf("the last agent dropped %d duplicates, want at least the %d copies made while it was down", st.IPC.Duplicates, copies)
			}
			if err := proc.stop(); err != nil {
				t.Errorf("the agent didn't stop cleanly: %v", err)
			}

			got := readJournal(t, data)
			count := make(map[string]int)
			byVia := make(map[inbox.Via]int)
			for _, s := range got {
				count[s.ID]++
				byVia[s.Via]++
				if !want[s.ID] {
					t.Errorf("the journal has %s, which nobody sent", s.ID)
				}
			}
			for id := range want {
				if count[id] != 1 {
					t.Errorf("message %s is in the journal %d times, want once", id, count[id])
				}
			}
			if len(got) != total {
				t.Errorf("the journal has %d lines, want %d", len(got), total)
			}
			// Both ways in were used: the kills sent messages to the
			// spool, and the rest came over the socket or the pipe.
			wantVia := inbox.ViaSocket
			if tt.pipe {
				wantVia = inbox.ViaPipe
			}
			if byVia[inbox.ViaInbox] == 0 || byVia[wantVia] == 0 || byVia[inbox.ViaInbox]+byVia[wantVia] != len(got) {
				t.Errorf("the messages came in as %v, want some by %q and the rest by %q", byVia, inbox.ViaInbox, wantVia)
			}
			t.Logf("stored %d messages: %v; the last agent dropped %d duplicates", len(got), byVia, st.IPC.Duplicates)
		})
	}
}
