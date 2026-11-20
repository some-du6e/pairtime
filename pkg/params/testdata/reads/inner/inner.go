// SPDX-License-Identifier: AGPL-3.0-or-later

package inner

import "github.com/some-du6e/pairtime/pkg/params"

func sample(t *params.Table) int64 { return t.Int(params.SAMPLE_S) }
