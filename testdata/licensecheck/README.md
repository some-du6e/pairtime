# licensecheck fixtures

Seeded modules for `tools/ci/licensecheck`, which ci.yml runs on them after
checking pairtime itself (docs/PLAN.md, WP-CI1):

- `bad` imports `example.com/licensecheck/gpl` through a `replace` directive.
  `gpl/LICENSE` is the GNU GPL 3.0, so the check must fail, naming that
  module.
- `desktop` imports the same GPL module the same way, but only from
  `gpl_desktop_test.go`, under `//go:build linux && desktop` like the
  desktop tests in AGENTS.md. The check lists linux with `-tags desktop`
  too, so it must fail here as well.
- `race` imports it only from `gpl_race_test.go`, under `//go:build race`,
  which only `go test -race` builds. The check lists linux/amd64 with
  `-race` and `CGO_ENABLED=1`, the way nightly.yml runs, so it must fail
  here too.
- `good` imports `example.com/licensecheck/mit` the same way, and
  `mit/LICENSE` is the MIT license, so the check must pass.

The Go files carry pairtime's AGPL header like every Go file in the repo. The
`LICENSE` files are what the check reads.
