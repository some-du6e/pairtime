// SPDX-License-Identifier: AGPL-3.0-or-later

// Package dot dot-imports pkg/params, which would hide its parameter reads
// from the static check, so the check refuses it.
package dot

import . "github.com/some-du6e/pairtime/pkg/params"

var lead = THINK_LEAD_S
