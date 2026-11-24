// SPDX-License-Identifier: AGPL-3.0-or-later

package writer

import "testing"

// FailResetAfterDurable makes the reset that ends each durable batch fail
// until t ends, with a statement SQLite refuses while it prepares it, so
// the connection stays at FULL. The next batch's own reset is still the
// real one. Call it before starting the writer, so the writer closes
// before t's cleanup puts the real reset back.
func FailResetAfterDurable(t testing.TB) {
	resetAfterDurable = "SELECT * FROM pairtime_no_such_table"
	t.Cleanup(func() { resetAfterDurable = syncNormal })
}
