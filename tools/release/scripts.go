// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/some-du6e/pairtime/internal/release"
)

// installScript is one of the install scripts in install/ (docs/PLAN.md,
// WP-RL2). Each has two lines that a release fills in: the version the
// script installs and the release keys it trusts. The repository's copies
// leave both empty, so a script attached to a release installs that
// release and trusts exactly the keys its binaries trust.
type installScript struct {
	name string // the file name, in install/ and among a release's files
	// versionLine and keysLine are the two lines as the repository's copy
	// has them, without their indentation.
	versionLine, keysLine string
	version               func(v string) string
	keys                  func(keys []string) string
}

var installScripts = []installScript{
	{
		name:        "install.sh",
		versionLine: "pinned_version=''",
		keysLine:    "pinned_keys=''",
		version:     func(v string) string { return "pinned_version='" + v + "'" },
		// Keys hold no spaces, and the script splits the list on them.
		keys: func(keys []string) string { return "pinned_keys='" + strings.Join(keys, " ") + "'" },
	},
	{
		name:        "install.ps1",
		versionLine: "$PinnedVersion = ''",
		keysLine:    "$PinnedKeys = @()",
		version:     func(v string) string { return "$PinnedVersion = '" + v + "'" },
		keys: func(keys []string) string {
			quoted := make([]string, len(keys))
			for i, k := range keys {
				quoted[i] = "'" + k + "'"
			}
			return "$PinnedKeys = @(" + strings.Join(quoted, ", ") + ")"
		},
	},
}

// stamp returns src with its version and keys lines filled in. Each line
// must appear exactly once, alone on its line; its indentation is kept.
// The version and keys go inside single quotes, which ParseVersion and
// the key format keep out of both. The scripts must be printable ASCII
// with LF line ends: Windows PowerShell 5.1 reads a file that has no byte
// order mark in the system's legacy code page, and irm decodes a download
// served without a charset as ISO-8859-1.
func (s installScript) stamp(src []byte, version string, keys []string) ([]byte, error) {
	if _, err := release.ParseVersion(version); err != nil {
		return nil, err
	}
	for i, b := range src {
		if (b < ' ' || b > '~') && b != '\n' {
			return nil, fmt.Errorf("byte %d is %#x, but the script must be printable ASCII with LF line ends", i, b)
		}
	}
	lines := strings.Split(string(src), "\n")
	found := map[string]int{}
	for i, line := range lines {
		body := strings.TrimLeft(line, " ")
		indent := line[:len(line)-len(body)]
		switch body {
		case s.versionLine:
			lines[i] = indent + s.version(version)
		case s.keysLine:
			lines[i] = indent + s.keys(keys)
		default:
			continue
		}
		found[body]++
	}
	for _, want := range []string{s.versionLine, s.keysLine} {
		if found[want] != 1 {
			return nil, fmt.Errorf("has %d lines that read %s, not one", found[want], want)
		}
	}
	return []byte(strings.Join(lines, "\n")), nil
}

func cmdScripts(args []string, stdout, stderr io.Writer) error {
	flags := newFlags("scripts", stderr)
	version := flags.String("version", "", "the release version the scripts install")
	in := flags.String("in", "install", "the directory holding install.sh and install.ps1")
	out := flags.String("out", "", "the directory to write the release's copies to; it must exist")
	if err := parse(flags, args, "version", "out"); err != nil {
		return err
	}
	keys, err := release.TrustedKeys()
	if err != nil {
		return fmt.Errorf("internal/release/release-keys.txt: %w", err)
	}
	texts := make([]string, len(keys))
	ids := make([]string, len(keys))
	for i, k := range keys {
		texts[i] = k.String()
		ids[i] = k.ID()
	}
	stamped := make([][]byte, len(installScripts))
	for i, s := range installScripts {
		src, err := os.ReadFile(filepath.Join(*in, s.name))
		if err != nil {
			return err
		}
		if stamped[i], err = s.stamp(src, *version, texts); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	// Both are checked before either is written.
	for i, s := range installScripts {
		if err := os.WriteFile(filepath.Join(*out, s.name), stamped[i], 0o644); err != nil {
			return err
		}
	}
	trusted := "none, so they refuse its manifest unless told which key to trust"
	if len(ids) > 0 {
		trusted = strings.Join(ids, ", ")
	}
	fmt.Fprintf(stdout, "wrote install.sh and install.ps1 to %s: they install %s and trust release keys %s\n", *out, *version, trusted)
	return nil
}
