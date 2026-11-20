// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/pkg/redact"
)

const fixturesUsage = `Usage:
  pairtime fixtures scrub [flags] <dir>

Scrub takes out of captured harness fixtures what must not be committed
(DESIGN 5.1): this machine's account name, home directory, hostname and git
name and email, the values given with the flags below, anything one of
gitleaks' rules calls a secret, and personal data that passes its checksum.
Every file under <dir> is rewritten in place with a [TYPE] placeholder for
each span it removes, and one line is printed per span, without the text it
removed, for the review by hand that has to follow. Run it with -n first to
see what it would change. PROVENANCE.md files are left alone.

It exits 1 when something needs that review before anything is committed: a
binary file it can't scrub, a symlink, or a file or directory name that
holds a removed value.

Flags (each value flag can be given more than once):
`

func init() {
	register(command{
		name:    "fixtures",
		summary: "Scrub captured harness fixtures: pairtime fixtures scrub <dir>",
		run:     runFixtures,
	})
}

// detectKnown reads this machine's known values. Tests replace it.
var detectKnown = machineKnown

func runFixtures(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "scrub":
			return runScrub(args[1:], stdout, stderr)
		case "help", "-h", "--help":
			fmt.Fprint(stdout, fixturesUsage)
			return 0
		}
		fmt.Fprintf(stderr, "pairtime fixtures: unknown command %q\n\n", args[0])
	}
	fmt.Fprint(stderr, fixturesUsage)
	return 2
}

type valueList []string

func (l *valueList) String() string { return strings.Join(*l, ", ") }

func (l *valueList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

func runScrub(args []string, stdout, stderr io.Writer) int {
	var k redact.Known
	fl := flag.NewFlagSet("pairtime fixtures scrub", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dry := fl.Bool("n", false, "print what would be removed, and change nothing")
	noDetect := fl.Bool("no-detect", false, "don't read this machine's account, home, hostname and git identity")
	fl.Var((*valueList)(&k.Accounts), "account", "an OS account `name` to remove")
	fl.Var((*valueList)(&k.Homes), "home", "a home `directory` to remove")
	fl.Var((*valueList)(&k.Hosts), "host", "a host`name` to remove")
	fl.Var((*valueList)(&k.GitNames), "git-name", "a git user.`name` to remove")
	fl.Var((*valueList)(&k.GitEmails), "git-email", "a git user.`email` to remove")
	fl.Var((*valueList)(&k.GitHub), "github", "a GitHub `username` to remove")
	fl.Var((*valueList)(&k.HCA), "hca", "a Hack Club Account `username` to remove")
	fl.Var((*valueList)(&k.Private), "private", "a private repo or folder `name` to remove")
	fl.Var((*valueList)(&k.Deny), "deny", "any other `word` or phrase to remove, such as a name")
	fl.Usage = func() {
		fmt.Fprint(stderr, fixturesUsage)
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fl.NArg() != 1 {
		fmt.Fprintf(stderr, "pairtime fixtures scrub: want one directory, got %d arguments\n\n", fl.NArg())
		fl.Usage()
		return 2
	}
	root := fl.Arg(0)
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		fmt.Fprintf(stderr, "pairtime fixtures scrub: %s isn't a directory\n", root)
		return 2
	}
	if !*noDetect {
		m := detectKnown()
		k.Accounts = append(k.Accounts, m.Accounts...)
		k.Homes = append(k.Homes, m.Homes...)
		k.Hosts = append(k.Hosts, m.Hosts...)
		k.GitNames = append(k.GitNames, m.GitNames...)
		k.GitEmails = append(k.GitEmails, m.GitEmails...)
	}
	r, err := redact.New(k)
	if err != nil {
		fmt.Fprintf(stderr, "pairtime fixtures scrub: %v\n", err)
		return 1
	}
	s := &scrubber{r: r, root: root, dry: *dry, stdout: stdout}
	if err := filepath.WalkDir(root, s.visit); err != nil {
		fmt.Fprintf(stderr, "pairtime fixtures scrub: %v\n", err)
		return 1
	}
	verb := "removed"
	if s.dry {
		verb = "would remove"
	}
	fmt.Fprintf(stdout, "pairtime fixtures scrub: %s %d spans from %d of %d files\n", verb, s.spans, s.changed, s.files)
	if s.review > 0 {
		fmt.Fprintf(stdout, "pairtime fixtures scrub: %d entries need a look by hand, listed above\n", s.review)
		return 1
	}
	return 0
}

// machineKnown is what this machine says about its user. A value it can't
// read is left out.
func machineKnown() redact.Known {
	var k redact.Known
	if u, err := user.Current(); err == nil {
		name := u.Username
		if i := strings.LastIndexByte(name, '\\'); i >= 0 {
			name = name[i+1:] // DOMAIN\name on Windows
		}
		k.Accounts = append(k.Accounts, name)
		if u.Name != "" && u.Name != name {
			k.GitNames = append(k.GitNames, u.Name) // the account's full name
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		k.Homes = append(k.Homes, home)
	}
	if host, err := os.Hostname(); err == nil {
		k.Hosts = append(k.Hosts, host)
		if short, _, ok := strings.Cut(host, "."); ok {
			k.Hosts = append(k.Hosts, short)
		}
	}
	for _, key := range []string{"user.name", "user.email"} {
		out, err := exec.Command("git", "config", "--get", key).Output()
		v := strings.TrimSpace(string(out))
		if err != nil || v == "" {
			continue
		}
		if key == "user.name" {
			k.GitNames = append(k.GitNames, v)
		} else {
			k.GitEmails = append(k.GitEmails, v)
		}
	}
	return k
}

type scrubber struct {
	r      *redact.Redactor
	root   string
	dry    bool
	stdout io.Writer

	files, changed, spans, review int
}

// shown is how a path appears in the output: relative to the root, with
// slashes, and redacted itself, so the listing never holds a removed value.
func (s *scrubber) shown(path string) (string, []redact.Span) {
	rel, err := filepath.Rel(s.root, path)
	if err != nil {
		rel = filepath.Base(path)
	}
	rel = filepath.ToSlash(rel)
	spans := s.r.Find(rel)
	return redact.Render(rel, spans), spans
}

func (s *scrubber) visit(path string, d fs.DirEntry, err error) error {
	shown, _ := s.shown(path)
	if err != nil {
		fmt.Fprintf(s.stdout, "%s: can't read: %v\n", shown, pathless(err))
		s.review++
		if d != nil && d.IsDir() {
			return fs.SkipDir
		}
		return nil
	}
	if path != s.root {
		if spans := redact.Merge(s.r.Find(d.Name())); len(spans) > 0 {
			fmt.Fprintf(s.stdout, "%s: the name holds %s; rename it by hand\n", shown, kinds(spans))
			s.review++
		}
	}
	switch {
	case d.IsDir():
		return nil
	case d.Type()&fs.ModeSymlink != 0:
		fmt.Fprintf(s.stdout, "%s: a symlink, not followed; review it by hand\n", shown)
		s.review++
		return nil
	case !d.Type().IsRegular():
		fmt.Fprintf(s.stdout, "%s: not a regular file; review it by hand\n", shown)
		s.review++
		return nil
	case d.Name() == "PROVENANCE.md":
		return nil
	}
	s.file(path, shown)
	return nil
}

func (s *scrubber) file(path, shown string) {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(s.stdout, "%s: can't read: %v\n", shown, pathless(err))
		s.review++
		return
	}
	s.files++
	if bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b) {
		fmt.Fprintf(s.stdout, "%s: binary, not scrubbed; review it by hand\n", shown)
		s.review++
		return
	}
	text := string(b)
	spans := redact.Merge(s.r.Find(text))
	if len(spans) == 0 {
		return
	}
	for _, sp := range spans {
		line, col := position(text, sp.Start)
		fmt.Fprintf(s.stdout, "%s:%d:%d: %s, %d bytes\n", shown, line, col, kinds([]redact.Span{sp}), sp.End-sp.Start)
	}
	s.spans += len(spans)
	s.changed++
	if s.dry {
		return
	}
	if err := replaceFile(path, []byte(redact.Render(text, spans))); err != nil {
		fmt.Fprintf(s.stdout, "%s: can't write: %v\n", shown, pathless(err))
		s.review++
	}
}

// kinds names the spans' types, with the gitleaks rule for a secret.
func kinds(spans []redact.Span) string {
	var out []string
	for _, sp := range spans {
		k := string(sp.Type)
		if sp.Rule != "" {
			k += " " + sp.Rule
		}
		out = append(out, k)
	}
	return strings.Join(out, ", ")
}

// position is the 1-based line and byte column of offset i.
func position(text string, i int) (line, col int) {
	before := text[:i]
	return strings.Count(before, "\n") + 1, i - strings.LastIndexByte(before, '\n')
}

// pathless drops the path an fs error carries, since the path may hold a
// value the listing must not show.
func pathless(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err
	}
	return err
}

// replaceFile writes data over path by renaming a temporary file into
// place, so an interrupted run never leaves half a file. It keeps the
// file's permission bits.
func replaceFile(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pairtime-scrub-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
