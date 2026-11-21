// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/some-du6e/pairtime/internal/adapters"
	_ "github.com/some-du6e/pairtime/internal/adapters/claudecode" // registers itself
	"github.com/some-du6e/pairtime/pkg/event"
)

const adaptersUsage = `Usage:
  pairtime adapters list
  pairtime adapters test [--expected <file>] [--show-content] <dir>

list prints each harness pairtime has an adapter for, with the hook events
it reads.

test replays the hook calls captured in <dir> through their harness's
adapter, the way the agent takes them in, and diffs the records it makes
with the expected JSON (DESIGN 5.3). <dir> is a capture laid out as
testdata/fixtures/README.md says: a run, such as
testdata/fixtures/claude_code/2.1.327/linux/cli, or the OS folder that
holds runs and the PROVENANCE.md naming their harness and version. The
expected records are the adapter's own for that version, OS and run, or
the --expected file's for a single run. It prints one line per run and
exits 1 when a run doesn't replay to its expected records, when a call
doesn't parse, or when no capture it replayed is real (synthetic: false),
since an adapter can't ship with synthetic fixtures alone (DESIGN 5.1).

A record that differs is shown without its content: its event, whose keys
are symbols, and of its text, command line and edits only whether they
differ, by length. --show-content shows the records whole, prompts,
replies, command lines and file paths included, which a capture that
isn't scrubbed yet holds as the person typed and Claude wrote them.
`

func init() {
	register(command{
		name:    "adapters",
		summary: "List the harness adapters, or replay captured fixtures through one: pairtime adapters test <dir>",
		run:     runAdapters,
	})
}

func runAdapters(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "list":
			return adaptersList(stdout)
		case "test":
			return adaptersTest(args[1:], stdout, stderr)
		case "help", "-h", "--help":
			fmt.Fprint(stdout, adaptersUsage)
			return 0
		}
		fmt.Fprintf(stderr, "pairtime adapters: unknown command %q\n\n", args[0])
	}
	fmt.Fprint(stderr, adaptersUsage)
	return 2
}

func adaptersList(stdout io.Writer) int {
	for _, h := range adapters.Default.Harnesses() {
		a, _ := adapters.Default.New(h, adapters.FixtureOptions())
		var events []string
		for _, s := range a.Sources() {
			if s.Kind == adapters.SourceHook {
				events = append(events, s.Event)
			}
		}
		fmt.Fprintf(stdout, "%s\thooks: %s\n", h, strings.Join(events, ", "))
	}
	return 0
}

// capture is what PROVENANCE.md says about a capture.
type capture struct {
	harness, version string
	synthetic        bool
}

func readProvenance(dir string) (capture, error) {
	b, err := os.ReadFile(filepath.Join(dir, "PROVENANCE.md"))
	if err != nil {
		return capture{}, err
	}
	c := capture{synthetic: adapters.Synthetic(b)}
	lines := strings.Split(string(b), "\n")
	for _, l := range lines[1:] {
		l = strings.TrimSpace(l)
		if l == "---" {
			break
		}
		if v, ok := strings.CutPrefix(l, "harness:"); ok {
			c.harness = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(l, "harness_version:"); ok {
			c.version = strings.TrimSpace(v)
		}
	}
	if c.harness == "" || c.version == "" {
		return c, errors.New("PROVENANCE.md names no harness or harness_version")
	}
	return c, nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func adaptersTest(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("pairtime adapters test", flag.ContinueOnError)
	fl.SetOutput(stderr)
	expectedFile := fl.String("expected", "", "the expected records of a single run, as JSON Lines")
	showContent := fl.Bool("show-content", false, "show differing records whole, with the prompts, replies, command lines and paths they hold")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if fl.NArg() != 1 {
		fmt.Fprint(stderr, adaptersUsage)
		return 2
	}
	// The OS folder and the run are named by the path's last elements, so
	// "." or "cli" must become the folders they are first.
	dir, err := filepath.Abs(fl.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "pairtime adapters test: %v\n", err)
		return 1
	}
	osDir, runs := dir, []string(nil)
	if isDir(filepath.Join(dir, "hooks")) {
		osDir, runs = filepath.Dir(dir), []string{filepath.Base(dir)}
	} else {
		entries, err := os.ReadDir(dir)
		if err != nil {
			fmt.Fprintf(stderr, "pairtime adapters test: %v\n", err)
			return 1
		}
		for _, e := range entries {
			if e.IsDir() && isDir(filepath.Join(dir, e.Name(), "hooks")) {
				runs = append(runs, e.Name())
			}
		}
	}
	if len(runs) == 0 {
		fmt.Fprintf(stderr, "pairtime adapters test: %s holds no run with hooks/\n", dir)
		return 1
	}
	if *expectedFile != "" && len(runs) > 1 {
		fmt.Fprintln(stderr, "pairtime adapters test: --expected is for a single run")
		return 2
	}
	prov, err := readProvenance(osDir)
	if err != nil {
		fmt.Fprintf(stderr, "pairtime adapters test: %s: %v\n", osDir, err)
		return 1
	}
	h := event.Harness(prov.harness)
	if _, ok := adapters.Default.New(h, adapters.FixtureOptions()); !ok {
		fmt.Fprintf(stderr, "pairtime adapters test: pairtime has no adapter for %q\n", prov.harness)
		return 1
	}
	goos := adapters.GOOS(filepath.Base(osDir))
	failed := false
	slices.Sort(runs)
	for _, run := range runs {
		var want []byte
		if *expectedFile != "" {
			want, err = os.ReadFile(*expectedFile)
		} else {
			a, _ := adapters.Default.New(h, adapters.FixtureOptions())
			want, err = fs.ReadFile(a.Fixtures(), prov.version+"/"+filepath.Base(osDir)+"/"+run+"/expected.jsonl")
		}
		if err != nil {
			fmt.Fprintf(stderr, "FAIL %s: no expected records: %v\n", run, err)
			failed = true
			continue
		}
		lines, err := adapters.ReadLines(want)
		if err != nil {
			fmt.Fprintf(stderr, "FAIL %s: %v\n", run, err)
			failed = true
			continue
		}
		a, _ := adapters.Default.New(h, adapters.FixtureOptions())
		r := adapters.CheckRun(a, os.DirFS(filepath.Join(osDir, run)), goos, lines)
		if r.OK() {
			fmt.Fprintf(stdout, "ok   %s/%s/%s/%s: %d records\n", prov.harness, prov.version, filepath.Base(osDir), run, r.Records)
			continue
		}
		failed = true
		fmt.Fprintf(stdout, "FAIL %s/%s/%s/%s\n", prov.harness, prov.version, filepath.Base(osDir), run)
		if r.Err != nil {
			fmt.Fprintf(stdout, "  %v\n", r.Err)
		}
		calls := make([]string, 0, len(r.Failures))
		for c := range r.Failures {
			calls = append(calls, c)
		}
		slices.Sort(calls)
		for _, c := range calls {
			fmt.Fprintf(stdout, "  hooks/%s: %v\n", c, r.Failures[c])
		}
		diffs := r.Diffs
		if *showContent && r.Err == nil {
			diffs = adapters.DiffContent(r.Lines, lines, 20)
		}
		for _, d := range diffs {
			fmt.Fprintf(stdout, "  %s\n", d)
		}
	}
	if prov.synthetic {
		fmt.Fprintln(stdout, "FAIL the capture is synthetic, and an adapter can't ship with synthetic fixtures alone (DESIGN 5.1)")
		failed = true
	}
	if failed {
		return 1
	}
	return 0
}
