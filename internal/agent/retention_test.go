// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/retention"
)

// TestRetentionPeriods checks the periods of the stores that sweep
// themselves against docs/retention.json's device entries, which
// TestDesignPeriods holds to DESIGN 4.4, so the agent deletes what the
// privacy notice says when it says (WP-LG6).
func TestRetentionPeriods(t *testing.T) {
	p, err := retention.Device()
	if err != nil {
		t.Fatal(err)
	}
	day := 24 * time.Hour
	cases := []struct {
		name string
		got  time.Duration
		want int
	}{
		{"the adapter host keeps a hook call it holds no longer than device.held_calls says", heldKeep, p.Held},
		{"the commit store keeps a day's commit records as long as device.commits says", commitsKeep, p.Commits},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != time.Duration(c.want)*day {
				t.Errorf("%v, want %d days", c.got, c.want)
			}
		})
	}
}
