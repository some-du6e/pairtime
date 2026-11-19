// SPDX-License-Identifier: AGPL-3.0-or-later

package version

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	stamped := &debug.BuildInfo{
		Main: debug.Module{Path: "github.com/some-du6e/pairtime", Version: "v0.0.0-20261119104900-1a2b3c4d5e6f"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b"},
			{Key: "vcs.time", Value: "2026-11-19T10:49:00Z"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
	tests := []struct {
		name                  string
		version, commit, date string
		bi                    *debug.BuildInfo
		want                  Info
	}{
		{
			name: "no build info",
			bi:   nil,
			want: Info{Version: "dev"},
		},
		{
			name:    "linker values win over build info",
			version: "v0.1.0", commit: "ffffffffffffffffffffffffffffffffffffffff", date: "2026-12-01T00:00:00Z",
			bi:   stamped,
			want: Info{Version: "v0.1.0", Commit: "ffffffffffffffffffffffffffffffffffffffff", Date: "2026-12-01T00:00:00Z", Modified: true},
		},
		{
			name:    "build info fills the gaps",
			version: "dev",
			bi:      stamped,
			want:    Info{Version: "v0.0.0-20261119104900-1a2b3c4d5e6f", Commit: "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b", Date: "2026-11-19T10:49:00Z", Modified: true},
		},
		{
			name:    "a (devel) module version is not a version",
			version: "dev",
			bi:      &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}},
			want:    Info{Version: "dev"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolve(tt.version, tt.commit, tt.date, tt.bi)
			tt.want.GoVersion = runtime.Version()
			tt.want.Platform = runtime.GOOS + "/" + runtime.GOARCH
			if got != tt.want {
				t.Errorf("resolve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestInfoString(t *testing.T) {
	tests := []struct {
		name string
		info Info
		want string
	}{
		{
			name: "release",
			info: Info{Version: "v0.1.0", Commit: "1a2b3c4d5e6f7a8b9c0d", Date: "2026-11-19T10:49:00Z", GoVersion: "go1.26.11", Platform: "linux/amd64"},
			want: "v0.1.0 (commit 1a2b3c4d5e6f, 2026-11-19T10:49:00Z, go1.26.11, linux/amd64)",
		},
		{
			name: "dirty tree",
			info: Info{Version: "dev", Commit: "1a2b3c4d5e6f7a8b9c0d", Modified: true, GoVersion: "go1.26.11", Platform: "windows/386"},
			want: "dev (commit 1a2b3c4d5e6f+dirty, go1.26.11, windows/386)",
		},
		{
			name: "nothing stamped",
			info: Info{Version: "dev", GoVersion: "go1.26.11", Platform: "darwin/arm64"},
			want: "dev (go1.26.11, darwin/arm64)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.info.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGet(t *testing.T) {
	info := Get()
	if info.Version == "" {
		t.Error("Get() returned an empty version")
	}
	if !strings.Contains(info.String(), runtime.GOOS+"/"+runtime.GOARCH) {
		t.Errorf("String() = %q, want it to name the platform", info.String())
	}
}
