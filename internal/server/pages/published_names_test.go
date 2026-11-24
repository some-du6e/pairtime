// SPDX-License-Identifier: AGPL-3.0-or-later

// An external test: package device imports pages, and devlog imports
// device, so only a test outside package pages can read devlog.Codes.
package pages_test

import (
	"testing"

	"github.com/some-du6e/pairtime/internal/server/devlog"
	"github.com/some-du6e/pairtime/internal/server/pages"
)

// TestBadgeNames checks that the owner's list has words for every badge
// code the server takes (devlog.Codes), so only a code a later version
// adds shows as itself.
func TestBadgeNames(t *testing.T) {
	for _, code := range devlog.Codes {
		t.Run("the badge code "+code+" has words on the list", func(t *testing.T) {
			if got := pages.BadgeName(code); got == code || got == "" {
				t.Errorf("BadgeName(%q) = %q, want words for it", code, got)
			}
		})
	}
	t.Run("a code the server doesn't take yet shows as itself", func(t *testing.T) {
		if got := pages.BadgeName("future_code"); got != "future_code" {
			t.Errorf("BadgeName(future_code) = %q, want the code", got)
		}
	})
}
