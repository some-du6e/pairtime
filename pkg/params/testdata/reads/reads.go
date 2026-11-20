// SPDX-License-Identifier: AGPL-3.0-or-later

// Package reads is a fixture for the static parameter check in
// static_test.go. The go command never builds testdata, so this package
// can read NOT_A_PARAM_S, which no table has.
package reads

import "github.com/some-du6e/pairtime/pkg/params"

func lead(t *params.Table) int64 {
	return t.Int(params.THINK_LEAD_S) + t.Int(params.NOT_A_PARAM_S)
}

func load() (*params.Table, error) { return params.ByVersion("0.1.0") }
