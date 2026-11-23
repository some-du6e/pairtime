// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/some-du6e/pairtime/internal/agent"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
	"github.com/some-du6e/pairtime/pkg/params"
	"github.com/some-du6e/pairtime/pkg/score"
)

const privacyUsage = `Usage: pairtime privacy show
       pairtime privacy keep-text off|on
       pairtime privacy delete [--yes]

pairtime privacy keep-text off stops pairtime keeping the text of your
prompts, and deletes the text it kept; pairtime privacy keep-text on
keeps it again from then on. pairtime privacy delete deletes pairtime's
device key and everything pairtime recorded on this computer. Each one's
--help says more.

pairtime privacy show prints the next upload this computer will send to
the pairtime server, exactly as it will be sent (DESIGN 8.8). Standard
output gets the upload's JSON byte for byte, as it is before pairtime
compresses it with gzip and signs it; standard error says what it holds
and where it goes. So

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
		summary: "Show the next upload, byte for byte, before it leaves this computer, turn keeping your prompts' text off or on, or delete everything pairtime recorded",
		run:     runPrivacy,
	})
}

func runPrivacy(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "delete" {
		return runPrivacyDelete(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "keep-text" {
		return runKeepText(args[1:], stdout, stderr)
	}
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
	// As the agent's uploads do: a config.toml with an error keeps this
	// computer local-only until it's fixed.
	cfg, broken := agent.UploadConfig(dirs)
	if broken != "" && agent.Linked(dirs) {
		fmt.Fprintf(stderr, "Nothing: %s.\nThe error: %s\nOnce %s.\n", agent.ConfigBroken, broken, strings.TrimPrefix(agent.ConfigFixed, "once "))
		return 0
	}
	creds, ok, err := agent.SignedIn(dirs, cfg, agentKeyStores)
	if err != nil {
		fmt.Fprintf(stderr, "Nothing: this computer is linked to a server, but its sign-in doesn't read, so it uploads nothing until it does, and what pairtime records meanwhile stays on this computer.\nThe error: %v\n", err)
		return 0
	}
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
	if p.Fork > 0 {
		fmt.Fprintf(stderr, uploadsForked, p.Fork)
		return 0
	}
	if p.Mismatch {
		fmt.Fprint(stderr, uploadsMismatch)
		return 0
	}
	switch {
	case p.Stopped > 0 && p.NoMode:
		fmt.Fprint(stderr, uploadsStoppedNoMode)
	case p.Stopped > 0:
		fmt.Fprint(stderr, uploadsStopped)
	}
	if p.JSON == nil {
		if p.Stopped == 0 {
			fmt.Fprintf(stderr, "Nothing is waiting to go to %s: it has every batch root and minute this computer has recorded.\n", creds.Server)
		}
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

// uploadsStopped is what privacy show says while the batch roots stop at a
// batch sealed in local-only mode (uploader.Preview.Stopped), with both of
// its causes, as doctor gives them (uploader's localOnlyProblem).
const uploadsStopped = `Uploads are stopped. After this computer was linked, pairtime recorded
something in local-only mode: config.toml said mode = "local", or had an
error, for a while, or something made before the link, as a hook call that
waited, reached pairtime only after it. Nothing pairtime records in
local-only mode leaves this computer, so nothing it recorded from then on
goes. To upload again, run pairtime logout, then pairtime login; what it
records after that goes as usual.

`

// uploadsStoppedNoMode is what privacy show says while the batch roots stop
// at a batch an older pairtime version sealed, which recorded no mode, so
// it counts as local-only (uploader.Preview.NoMode).
const uploadsStoppedNoMode = `Uploads are stopped. An older version of pairtime sealed batches on this
computer without recording whether config.toml let them upload, so they
count as recorded in local-only mode, which never leaves this computer, and
nothing recorded after them goes either. To upload again, run pairtime
logout, then pairtime login; what it records after that goes as usual.

`

// uploadsForked is what privacy show says once nothing more goes under
// this computer's device ID (uploader.Preview.Fork).
const uploadsForked = `Uploads are stopped. The server's record of this computer's ledger differs
from it at batch %d, as after a home folder restored from an old backup. This
version of pairtime can't go on uploading under this computer's ID yet, so
nothing more goes, and what pairtime records stays on this computer.
`

// uploadsMismatch is what privacy show says once the server said this
// computer's device ID is another account's (uploader.Preview.Mismatch).
const uploadsMismatch = `Uploads are stopped. The server says this computer's device ID belongs to
another account. This version of pairtime can't go on uploading under this
computer's ID yet, so nothing more goes, and what pairtime records stays on
this computer.
`

// describeUpload says in plain words what upload p holds and where it goes.
func describeUpload(p uploader.Preview, server string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "This is the next upload from this computer to %s/api/v1/ingest/batches.\n", strings.TrimSuffix(server, "/"))
	b.WriteString("The JSON on standard output is all of its body. pairtime compresses it with\n")
	b.WriteString("gzip and signs it with this computer's device key. The request's headers carry\n")
	b.WriteString("your device token, this computer's device id, a request number, the time, a\n")
	b.WriteString("request id that starts with this computer's clock, the hash and signature of\n")
	b.WriteString("the compressed body, its content type and encoding, and pairtime-agent and its\n")
	b.WriteString("version as the User-Agent. The server sees this computer's IP address, as\n")
	b.WriteString("every website does. When the agent starts, it also asks the server where this\n")
	b.WriteString("computer's uploads stand, with the same token, device id and User-Agent.\n\nIt holds:\n")
	if p.Batches > 0 {
		fmt.Fprintf(&b, "  - %s: hashes of your ledger's records, which prove when they\n", countOf(p.Batches, "batch root", "batch roots"))
		b.WriteString("    existed without saying what they are;\n")
	}
	if p.Buckets > 0 {
		fmt.Fprintf(&b, "  - %s: for each minute, its time, the seconds credited to you with the\n", countOf(p.Buckets, "minute", "minutes"))
		b.WriteString("    reason, the kind of work and how strong the evidence is, the seconds left\n")
		b.WriteString("    out and why, how long your AI agent ran, which project, as a key only this\n")
		b.WriteString("    computer can match to a folder, whether the minute can still change, and\n")
		b.WriteString("    whether its records reached the server on time;\n")
	}
	if p.Labels {
		b.WriteString("  - the labels: the versions of pairtime, of its scoring rules and of the AI\n")
		b.WriteString("    tools you use, and whether the typing module is on and this computer is\n")
		b.WriteString("    a virtual machine;\n")
	}
	b.WriteString("and no prompt, answer, code, file name or path, window title, command line\n")
	b.WriteString("or keystroke.\n")
	if p.Retry {
		b.WriteString("\nAn earlier try to send it failed, and pairtime sends it again exactly like this.\n")
	}
	if p.More {
		b.WriteString("\nMore uploads like it wait behind this one, and go right after it.\n")
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
