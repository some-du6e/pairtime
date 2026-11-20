module github.com/some-du6e/pairtime

go 1.26

// golang.org/x/sys v0.48.0 and later declare go 1.26.0. go mod tidy would
// copy that here, and setup-go, which reads this file, would then install
// exactly Go 1.26.0 instead of the latest 1.26 patch release. v0.47.0 is the
// newest release that declares go 1.25.0.
require golang.org/x/sys v0.47.0

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/ebitengine/purego v0.11.3
	github.com/fsnotify/fsnotify v1.10.1
	github.com/godbus/dbus/v5 v5.2.4
	github.com/lib/pq v1.12.3
	github.com/zalando/go-keyring v0.2.8
	modernc.org/sqlite v1.59.0
)

require (
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	modernc.org/libc v1.75.7 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

// third_party/burntsushi-toml is BurntSushi/toml v1.6.0 with one patch: its
// internal package reads the local time zone offset on first use instead of
// at package initialization, which loaded time.Local before main in every
// pairtime process, and on Windows could read every time zone in the
// registry (docs/errata/2026-11-20-hook-p95-gate.md). Its PROVENANCE.md has
// the upstream commit and the patch. Drop this once upstream releases the fix.
replace github.com/BurntSushi/toml => ./third_party/burntsushi-toml
