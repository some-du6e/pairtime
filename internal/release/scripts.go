// SPDX-License-Identifier: AGPL-3.0-or-later

package release

import (
	"fmt"
	"strings"
)

// InstallScript is one of the install scripts in install/ (docs/PLAN.md,
// WP-RL2). Each has two lines that a release fills in: the version the
// script installs and the release keys it trusts. The repository's copies
// leave both empty, so a script attached to a release installs that
// release and trusts exactly the keys its binaries trust. tools/release
// scripts stamps the copies a release carries, and pairtimed stamps the
// copy it serves at /install.sh and /install.ps1 (internal/server/pages)
// the same way, so both are the bytes the release's signed manifest lists.
type InstallScript struct {
	// Name is the script's file name, in install/, among a release's
	// files and in its manifest's install_scripts.
	Name string
	// versionLine and keysLine are the two lines as the repository's copy
	// has them, without their indentation.
	versionLine, keysLine string
	version               func(v string) string
	keys                  func(keys []string) string
}

// InstallScripts returns the install scripts a release carries, install.sh
// for macOS and Linux and install.ps1 for Windows, in that order.
func InstallScripts() []InstallScript {
	return []InstallScript{
		{
			Name:        "install.sh",
			versionLine: "pinned_version=''",
			keysLine:    "pinned_keys=''",
			version:     func(v string) string { return "pinned_version='" + v + "'" },
			// Keys hold no spaces, and the script splits the list on them.
			keys: func(keys []string) string { return "pinned_keys='" + strings.Join(keys, " ") + "'" },
		},
		{
			Name:        "install.ps1",
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
}

// Stamp returns src, the repository's copy of the script, with its
// version and keys lines filled in: the release version it installs and
// the release keys it trusts, each key in PublicKey.String's form. Each
// line must appear exactly once, alone on its line; its indentation is
// kept. The version and keys go inside single quotes, which ParseVersion
// and the key format keep out of both. The scripts must be printable
// ASCII with LF line ends: Windows PowerShell 5.1 reads a file that has no
// byte order mark in the system's legacy code page, and irm decodes a
// download served without a charset as ISO-8859-1.
func (s InstallScript) Stamp(src []byte, version string, keys []string) ([]byte, error) {
	if _, err := ParseVersion(version); err != nil {
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
