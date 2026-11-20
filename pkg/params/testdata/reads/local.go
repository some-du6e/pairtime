// SPDX-License-Identifier: AGPL-3.0-or-later

package reads

// This file doesn't import pkg/params, so params below is a local value
// and LOCAL_S is its field, not a parameter.

type config struct{ LOCAL_S int64 }

func local() int64 {
	params := config{LOCAL_S: 5}
	return params.LOCAL_S
}
