// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/some-du6e/pairtime/internal/agent"
	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
	"github.com/some-du6e/pairtime/pkg/params"
	"github.com/some-du6e/pairtime/pkg/score"
)

const privacyUsage = `Usage: pairtime privacy show

Prints the next upload this computer will send to the pairtime server,
exactly as it will be sent (DESIGN 8.8). Standard output gets the upload's
JSON byte for byte, as it is before pairtime compresses it with gzip and
signs it; standard error says what it holds and where it goes. So

  pairtime privacy show > next-upload.json

keeps just the upload. pairtime prints nothing on standard output when
nothing is waiting to go, and when this computer isn't signed in or
config.toml keeps it local-only, which uploads nothing at all.

The upload is what goes unless pairtime records or scores something new
before it sends.
`

func init() {
	register(command{
		name:    "privacy",
		summary: "Show the next upload, byte for byte, before it leaves this computer",
		run:     runPrivacy,
	})
}

func runPrivacy(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "show" {
		if len(args) > 0 && (args[0] == "-h" || args[0] == "-help" || args[0] == "--help") {
			fmt.Fprint(stdout, privacyUsage)
			return 0
		}
		fmt.Fprint(stderr, privacyUsage)
		return 2
	}
	flags := flag.NewFlagSet("privacy show", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(flags.Output(), privacyUsage) }
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "pairtime privacy show: takes no arguments, got %q\n", flags.Args())
		return 2
	}
	dirs, err := home.Default()
	if err != nil {
		fmt.Fprintf(stderr, "pairtime privacy show: %v\n", err)
		return 1
	}
	cfg, _, err := config.Load(dirs.ConfigFile())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		// config.Load gives the private defaults with the error, and those
		// upload nothing.
		fmt.Fprintf(stderr, "pairtime privacy show: config.toml: %v\n", err)
	}
	creds, ok := agent.SignedIn(dirs, cfg)
	return showUpload(stdout, stderr, dirs, creds, ok)
}

// showUpload prints the next upload of the installation in dirs, which
// goes with creds when ok is set.
func showUpload(stdout, stderr io.Writer, dirs home.Dirs, creds uploader.Credentials, ok bool) int {
	if !ok {
		fmt.Fprint(stderr, notSignedIn)
		return 0
	}
	tables := params.Tables()
	p, err := uploader.Next(uploader.PreviewOptions{
		LedgerDir:   dirs.Ledger(),
		BucketDir:   dirs.Buckets(),
		DataDir:     dirs.Data,
		StateFile:   dirs.StateFile(),
		Params:      tables[len(tables)-1],
		Device:      score.Device{},
		Credentials: creds,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pairtime privacy show: can't work out the next upload: %v\n", err)
		return 1
	}
	if p.JSON == nil {
		fmt.Fprintf(stderr, "Nothing is waiting to go to %s: it has every batch root and minute this computer has recorded.\n", creds.Server)
		return 0
	}
	fmt.Fprint(stderr, describeUpload(p, creds.Server))
	if _, err := stdout.Write(p.JSON); err != nil {
		fmt.Fprintf(stderr, "pairtime privacy show: %v\n", err)
		return 1
	}
	return 0
}

// notSignedIn is what privacy show says when nothing is uploaded at all.
const notSignedIn = `Nothing: this computer uploads nothing. It isn't signed in to a pairtime
server, or config.toml says mode = "local", so everything pairtime records
stays here. pairtime login signs it in, and then this command shows each
upload before it goes.
`

// describeUpload says in plain words what upload p holds and where it goes.
func describeUpload(p uploader.Preview, server string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "This is the next upload from this computer to %s/api/v1/ingest/batches.\n", strings.TrimSuffix(server, "/"))
	b.WriteString("The JSON on standard output is all of it. pairtime compresses it with gzip and\n")
	b.WriteString("signs it with this computer's device key, and the request adds only its\n")
	b.WriteString("headers: your device token, this computer's device id, a request number, the\n")
	b.WriteString("time and the signature.\n\nIt holds:\n")
	if p.Batches > 0 {
		fmt.Fprintf(&b, "  - %s: hashes of your ledger's records, which prove when they\n", countOf(p.Batches, "batch root", "batch roots"))
		b.WriteString("    existed without saying what they are;\n")
	}
	if p.Buckets > 0 {
		fmt.Fprintf(&b, "  - %s: for each minute, the seconds credited to you and why, as\n", countOf(p.Buckets, "minute", "minutes"))
		b.WriteString("    reason codes, the seconds left out and why, how long your AI agent ran,\n")
		b.WriteString("    and which project, as a key only this computer can match to a folder;\n")
	}
	if p.Labels {
		b.WriteString("  - the labels: the versions of pairtime, of its scoring rules and of the AI\n")
		b.WriteString("    tools you use;\n")
	}
	b.WriteString("and no prompt, answer, code, file name or path, window title, command line\n")
	b.WriteString("or keystroke.\n")
	if p.Retry {
		b.WriteString("\nAn earlier try to send it failed, and pairtime sends it again exactly like this.\n")
	}
	b.WriteString("\n")
	return b.String()
}

// countOf writes n with the noun's singular or plural.
func countOf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
