// SPDX-License-Identifier: AGPL-3.0-or-later

// Package skipped sits in a testdata directory, which the go command never
// builds, so the static check skips it too.
package skipped

import "github.com/some-du6e/pairtime/pkg/params"

var skipped = params.SKIPPED_S
