// SPDX-License-Identifier: AGPL-3.0-or-later

package darwin

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// backgroundEnv, set, makes the test binary the child TestBackground
// reads: it calls Background and reports what getpriority says of it.
const backgroundEnv = "PAIRTIME_TEST_BACKGROUND_CHILD"

type backgroundReport struct {
	Before, After bool
	Err           string
}

func backgroundChild() {
	var r backgroundReport
	var err error
	if r.Before, err = InBackground(); err == nil {
		if err = Background(); err == nil {
			r.After, err = InBackground()
		}
	}
	if err != nil {
		r.Err = err.Error()
	}
	b, _ := json.Marshal(r)
	fmt.Println(string(b))
}

// TestBackground checks Background in a child process: getpriority reads
// the process as in the background state after it, and not before.
func TestBackground(t *testing.T) {
	if os.Getenv(backgroundEnv) != "" {
		backgroundChild()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestBackground$")
	cmd.Env = append(os.Environ(), backgroundEnv+"=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, rerr := bufio.NewReader(stdout).ReadBytes('\n')
	werr := cmd.Wait()
	var r backgroundReport
	if err := json.Unmarshal(line, &r); err != nil {
		t.Fatalf("the child said %q (%v, %v): %v", line, rerr, werr, err)
	}
	cases := []struct {
		name string
		ok   bool
	}{
		{"a process starts outside the background state", r.Err == "" && !r.Before},
		{"after Background it is in it", r.Err == "" && r.After},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("the child reported %+v", r)
			}
		})
	}
}
