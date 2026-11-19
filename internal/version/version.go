// SPDX-License-Identifier: AGPL-3.0-or-later

// Package version reports which build of pairtime is running.
//
// Release and CI builds set the variables below with the linker:
//
//	go build -ldflags "\
//	  -X github.com/some-du6e/pairtime/internal/version.Version=v0.1.0 \
//	  -X github.com/some-du6e/pairtime/internal/version.Commit=$(git rev-parse HEAD) \
//	  -X github.com/some-du6e/pairtime/internal/version.Date=2026-11-19T10:49:00Z" \
//	  ./cmd/pairtime
//
// Builds that don't set them fall back to what the go command embeds on its
// own (the module version and the VCS stamp), so `go install` and a plain
// `go build` from a checkout still say where they came from.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// These are set with -ldflags "-X". They must stay package-level string
// variables, not constants, or the linker can't overwrite them.
var (
	// Version is the release version, such as "v0.1.0".
	Version = "dev"
	// Commit is the full git commit hash the binary was built from.
	Commit = ""
	// Date is the commit or build time, RFC 3339 in UTC.
	Date = ""
)

// Info describes one build.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	Date      string `json:"date,omitempty"`
	Modified  bool   `json:"modified,omitempty"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

// Get returns the running binary's build info.
func Get() Info {
	bi, _ := debug.ReadBuildInfo()
	return resolve(Version, Commit, Date, bi)
}

// resolve merges the linker-set values with the go command's own build
// info. Linker values win; build info only fills gaps.
func resolve(version, commit, date string, bi *debug.BuildInfo) Info {
	info := Info{
		Version:   version,
		Commit:    commit,
		Date:      date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if info.Version == "" {
		info.Version = "dev"
	}
	if bi == nil {
		return info
	}
	if info.Version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		info.Version = bi.Main.Version
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if info.Commit == "" {
				info.Commit = s.Value
			}
		case "vcs.time":
			if info.Date == "" {
				info.Date = s.Value
			}
		case "vcs.modified":
			info.Modified = s.Value == "true"
		}
	}
	return info
}

// String formats the info for a `version` command, for example
// "v0.1.0 (commit 1a2b3c4d5e6f, 2026-11-19T10:49:00Z, go1.26.11, linux/amd64)".
func (i Info) String() string {
	var details []string
	if i.Commit != "" {
		c := i.Commit
		if len(c) > 12 {
			c = c[:12]
		}
		if i.Modified {
			c += "+dirty"
		}
		details = append(details, "commit "+c)
	}
	if i.Date != "" {
		details = append(details, i.Date)
	}
	details = append(details, i.GoVersion, i.Platform)
	return fmt.Sprintf("%s (%s)", i.Version, strings.Join(details, ", "))
}
