// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/tailers"
	"github.com/some-du6e/pairtime/pkg/event"
)

func init() {
	register(command{
		name:    "lookback",
		summary: "Read what the harnesses logged before pairtime, at low priority (the agent runs this)",
		run:     runLookback,
	})
}

const lookbackUsage = `Usage:
  pairtime lookback

The agent runs this to read what the harnesses logged before pairtime did:
the last 30 days of Claude Code's transcripts (DESIGN 5.9 step 9). It runs
at the lowest CPU and I/O priority the OS gives a process, and reads
nothing at any other. It takes the files to read on standard input, from
the agent, and writes each line, trimmed to what pairtime records of it,
to standard output. It isn't for running by hand.
`

// runLookback is lookback's reader, in a process of its own (tailers.Child).
func runLookback(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("lookback", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(flags.Output(), lookbackUsage) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 0 || !fromProgram(os.Stdin) {
		fmt.Fprint(stderr, lookbackUsage)
		return 2
	}
	return serveLookback(os.Stdin, stdout, stderr, lowerPriority)
}

// serveLookback is runLookback with its input and its priority call given:
// it lowers its priority before it reads a file, and refuses to read at
// any other.
func serveLookback(stdin io.Reader, stdout, stderr io.Writer, lower func() error) int {
	if err := lower(); err != nil {
		fmt.Fprintf(stderr, "pairtime lookback: reads nothing, since it can't lower its priority: %v\n", err)
		return 1
	}
	if err := tailers.ServeLookback(stdin, stdout, trimmer()); err != nil {
		fmt.Fprintf(stderr, "pairtime lookback: %v\n", err)
		return 1
	}
	return 0
}

// trimmer trims each line with its harness's adapter
// (adapters.LogReader.TrimLog). TrimLog reads no keys, but an adapter is
// made with some, so these get a random K_session that never leaves this
// process.
func trimmer() tailers.Trimmer {
	secret := make([]byte, hookpath.SessionSecretLen)
	rand.Read(secret)
	opts := adapters.Options{Keys: adapters.NewKeys(secret)}
	readers := map[event.Harness]adapters.LogReader{}
	for _, h := range adapters.Default.Harnesses() {
		if ad, ok := adapters.Default.New(h, opts); ok {
			if lr, ok := ad.(adapters.LogReader); ok {
				readers[h] = lr
			}
		}
	}
	return func(h event.Harness, line []byte) ([]byte, error) {
		lr := readers[h]
		if lr == nil {
			return nil, fmt.Errorf("no adapter of this build reads %s's logs", h)
		}
		return lr.TrimLog(line)
	}
}

// startLookback starts this binary's lookback command for the agent
// (agent.Options.Lookback): with no console window on Windows, and below
// normal priority from its start where the OS lets the agent say so
// (lookbackAttr), before it lowers its own priority all the way. The last
// line it wrote to standard error ends its Wait's error.
func startLookback() (tailers.Child, error) {
	exe, err := os.Executable()
	if err != nil {
		return tailers.Child{}, err
	}
	cmd := exec.Command(exe, "lookback")
	cmd.SysProcAttr = lookbackAttr()
	errOut := &lastBytes{max: 2048}
	cmd.Stderr = errOut
	in, err := cmd.StdinPipe()
	if err != nil {
		return tailers.Child{}, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return tailers.Child{}, err
	}
	if err := cmd.Start(); err != nil {
		return tailers.Child{}, err
	}
	return tailers.Child{In: in, Out: out,
		Wait: func() error {
			err := cmd.Wait()
			if err != nil {
				if s := errOut.lastLine(); s != "" {
					err = fmt.Errorf("%w: %s", err, s)
				}
			}
			return err
		},
		Kill: func() { cmd.Process.Kill() }}, nil
}

// lastBytes keeps the last max bytes written to it.
type lastBytes struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (l *lastBytes) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.b = append(l.b, p...)
	if len(l.b) > l.max {
		l.b = l.b[len(l.b)-l.max:]
	}
	return len(p), nil
}

// lastLine returns the last line written, without its newline.
func (l *lastBytes) lastLine() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := bytes.TrimRight(l.b, "\r\n")
	if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
		b = b[i+1:]
	}
	return strings.TrimSpace(string(b))
}
