// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

// These helpers serve the tests that run a real `pairtime agent` on a real
// desktop and read back what it recorded: agent_darwin_test.go on
// macos-latest and agent_desktop_test.go under Xvfb (platform.yml).

// sleepEnv, set to a duration, makes the test binary sleep that long and
// exit. A copy of the binary named claude, started that way, is a harness
// session process the agent's process scan counts, which makes a project
// context live (DESIGN 3.3) without a hook.
const sleepEnv = "PAIRTIME_TEST_SLEEP"

func init() {
	helpers[sleepEnv] = func(v string) int {
		d, err := time.ParseDuration(v)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		time.Sleep(d)
		return 0
	}
}

// fakeHarness writes a copy of the test binary named claude and returns its
// path. Started with sleepEnv set, it is a harness session process.
func fakeHarness(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(fake, b, 0o755); err != nil {
		t.Fatal(err)
	}
	return fake
}

// startFakeHarness starts a copy of the test binary named claude, which
// sleeps until the test ends.
func startFakeHarness(t *testing.T) {
	t.Helper()
	cmd := exec.Command(fakeHarness(t))
	cmd.Env = append(os.Environ(), sleepEnv+"=10m")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
}

// agentStatus is the part of GET /v1/status these tests read.
type agentStatus struct {
	Jobs []struct {
		Name string
		Runs uint64
	}
	Signals struct {
		Idle, Focus, Power, Lock signalStatus
		Session                  string
		Loop                     *struct {
			MainThread bool `json:"main_thread"`
			Dropped    uint64
		}
	}
	Problems map[string]string
}

type signalStatus struct{ Via, Unknown string }

func (s agentStatus) runs(job string) uint64 {
	for _, j := range s.Jobs {
		if j.Name == job {
			return j.Runs
		}
	}
	return 0
}

// agentRequest sends method path to the agent whose data directory is
// ptHome, with the token from its port.json.
func agentRequest(t *testing.T, ptHome, method, path string) *http.Response {
	t.Helper()
	pf, err := localhttp.ReadPortFile(home.Dirs{Data: ptHome}.PortFile())
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", pf.Port, path), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+pf.Token)
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return res
}

// readStatus returns the agent's GET /v1/status.
func readStatus(t *testing.T, ptHome string) agentStatus {
	t.Helper()
	res := agentRequest(t, ptHome, "GET", "/v1/status")
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/status = %s", res.Status)
	}
	var s agentStatus
	if err := json.NewDecoder(res.Body).Decode(&s); err != nil {
		t.Fatalf("decoding GET /v1/status: %v", err)
	}
	return s
}

// waitStatus polls GET /v1/status until ok says yes, for up to d.
func waitStatus(t *testing.T, ptHome string, d time.Duration, what string, ok func(agentStatus) bool) agentStatus {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		s := readStatus(t, ptHome)
		if ok(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("after %v the agent still hadn't %s; its status: %+v", d, what, s)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// stopAgent asks the agent to stop and waits for its process to exit, so
// that it has sealed everything it recorded.
func stopAgent(t *testing.T, ptHome string, cmd *exec.Cmd) {
	t.Helper()
	res := agentRequest(t, ptHome, "POST", "/v1/agent/stop")
	res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /v1/agent/stop = %s", res.Status)
	}
	if err := waitWithin(t, cmd, 30*time.Second); err != nil {
		t.Fatalf("the agent exited with %v", err)
	}
}

// recorded returns the events of kind k in the ledger of the data
// directory ptHome, in the order they were written.
func recorded(t *testing.T, ptHome string, k event.Kind) []event.Event {
	t.Helper()
	var evs []event.Event
	err := ledger.ReadBatches(home.Dirs{Data: ptHome}.Ledger(), 1, func(b ledger.Batch) error {
		for _, r := range b.Records {
			body, err := pkgledger.ParseBody(r.Body)
			if err != nil {
				return err
			}
			ev, err := body.Event()
			if err != nil {
				return err
			}
			if ev.Kind() == k {
				evs = append(evs, ev)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading the ledger back: %v", err)
	}
	return evs
}

// syncBuffer collects a process's output for a failing test to print.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
