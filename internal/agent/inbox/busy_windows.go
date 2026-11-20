// SPDX-License-Identifier: AGPL-3.0-or-later

package inbox

import (
	"errors"

	"golang.org/x/sys/windows"
)

// busy reports whether err means another process holds the file for a
// moment in a way that shuts this open out. A drain renaming the spool
// holds it with delete access, and an open by a sender, which doesn't share
// delete, fails with a sharing violation until the rename is done.
func busy(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
