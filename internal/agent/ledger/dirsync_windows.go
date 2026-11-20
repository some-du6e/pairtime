// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

// syncDir does nothing on Windows, which can't flush a directory. NTFS
// journals the change itself, so a new file's name outlasts a power cut
// once the file's own data is flushed.
func syncDir(string) {}
