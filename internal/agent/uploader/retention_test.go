// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/retention"
)

func TestUploadsPeriod(t *testing.T) {
	p, err := retention.Device()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		got  time.Duration
		want int
	}{
		{"uploads.jsonl keeps a past upload as long as device.uploads says", uploadsKeep, p.Uploads},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != time.Duration(c.want)*24*time.Hour {
				t.Errorf("%v, want %d days", c.got, c.want)
			}
		})
	}
}
