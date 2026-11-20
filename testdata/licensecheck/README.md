# licensecheck fixtures

Seeded modules for `tools/ci/licensecheck`, which ci.yml runs on them after
checking pairtime itself (docs/PLAN.md, WP-CI1):

- `bad` imports `example.com/licensecheck/gpl` through a `replace` directive.
  `gpl/LICENSE` is the GNU GPL 3.0, so the check must fail, naming that
  module.
- `good` imports `example.com/licensecheck/mit` the same way, and
  `mit/LICENSE` is the MIT license, so the check must pass.

The Go files carry pairtime's AGPL header like every Go file in the repo. The
`LICENSE` files are what the check reads.
