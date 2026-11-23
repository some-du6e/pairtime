// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/install"
)

const keepTextUsage = `Usage: pairtime privacy keep-text off|on

Turns pairtime's "keep prompt text" switch off or on: keep_prompt_text
under [privacy] in config.toml. Off, pairtime keeps no text of your
prompts, of the agent's final replies or of your pairtime track labels,
and deletes the text it kept, so a spot check of your hours can't see
those prompts and judges their hours on the rest of the evidence. On, it
keeps that text again from then on; what it deleted stays deleted.
pairtime's agent reads config.toml again whenever it changes.

It changes only that line of config.toml, or adds it, with a [privacy]
line at the end of a file that has none, as one written before this
version of pairtime has. It changes nothing in a config.toml that doesn't
parse, and says what to write by hand instead.

Exit status:
  0  config.toml says what you asked
  1  config.toml couldn't be changed, or something else in it keeps the
     switch from working; the message says what
  2  usage error
`

// runKeepText is `pairtime privacy keep-text off|on`: DESIGN 4.4's "keep
// prompt text" switch (docs/PLAN.md, WP-LG6), set in config.toml with the
// edit pairtime login makes there, so nobody has to get its tables right
// by hand.
func runKeepText(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("privacy keep-text", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(flags.Output(), keepTextUsage) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 1 || flags.Arg(0) != "off" && flags.Arg(0) != "on" {
		fmt.Fprintf(stderr, "pairtime privacy keep-text: takes off or on, got %q\n", flags.Args())
		return 2
	}
	keep := flags.Arg(0) == "on"
	fail := func(err error) int {
		fmt.Fprintf(stderr, "pairtime privacy keep-text: %s\n", install.Printable(err.Error()))
		return 1
	}
	dirs, err := home.Default()
	if err != nil {
		return fail(err)
	}
	name := dirs.ConfigFile()
	// Before the agent's first start, which writes config.toml, the folder
	// may not be there yet.
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return fail(err)
	}
	if err := config.SetKeepText(name, keep); err != nil {
		return fail(err)
	}
	cfg, problems, err := config.Load(name)
	if err != nil {
		return fail(err)
	}
	switch {
	case cfg.Privacy.TextHeld:
		fmt.Fprintf(stdout, "config.toml says keep_prompt_text = %t now, but something else in it keeps the switch from working, so pairtime keeps no new prompt text, and deletes none, until it's fixed:\n", keep)
	case keep:
		fmt.Fprintln(stdout, "config.toml says keep_prompt_text = true now: pairtime keeps the text of your prompts again from now on. The text it deleted before stays deleted.")
	default:
		fmt.Fprintln(stdout, "config.toml says keep_prompt_text = false now: pairtime keeps no new text of your prompts, and deletes the text it kept, at once while its agent runs, or else when it next starts.")
	}
	if len(problems) > 0 && !cfg.Privacy.TextHeld {
		fmt.Fprintln(stdout, "config.toml has other problems, which this didn't change:")
	}
	for _, p := range problems {
		fmt.Fprintf(stdout, "  %s\n", install.Printable(p))
	}
	if cfg.Privacy.TextHeld {
		return 1
	}
	return 0
}
