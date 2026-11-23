// SPDX-License-Identifier: AGPL-3.0-or-later

package wakacompat

import (
	"testing"
	"time"
)

func TestSeen(t *testing.T) {
	now := time.Date(2026, 11, 23, 14, 0, 0, 0, time.UTC)
	at := func(d time.Duration) float64 { return float64(now.Add(d).UnixNano()) / 1e9 }
	key := func(i byte) Key { return Key{i} }
	const window = 30 * 24 * time.Hour
	type step struct {
		add   bool // Add, else Check
		key   Key
		t     float64
		now   time.Time
		want  Status // for a Check
		count int    // Len after an Add, when not 0
	}
	cases := []struct {
		name  string
		limit int
		steps []step
	}{
		{"a heartbeat Seen wasn't told of is new", 10, []step{
			{key: key(1), t: at(-time.Minute), now: now, want: New},
		}},
		{"a heartbeat added is taken", 10, []step{
			{add: true, key: key(1), t: at(-time.Minute), now: now},
			{key: key(1), t: at(-time.Minute), now: now, want: Taken},
			{key: key(2), t: at(-time.Minute), now: now, want: New},
		}},
		{"a heartbeat from before the window is stale, added or not", 10, []step{
			{add: true, key: key(1), t: at(-time.Hour), now: now},
			{key: key(1), t: at(-time.Hour), now: now.Add(window), want: Stale},
			{key: key(2), t: at(-window - time.Second), now: now, want: Stale},
		}},
		{"a heartbeat from the future is new, then taken", 10, []step{
			{key: key(1), t: at(time.Hour), now: now, want: New},
			{add: true, key: key(1), t: at(time.Hour), now: now},
			{key: key(1), t: at(time.Hour), now: now, want: Taken},
		}},
		{"a key whose time left the window is forgotten when Seen next prunes", 10, []step{
			{add: true, key: key(1), t: at(-time.Hour), now: now, count: 1},
			{add: true, key: key(2), t: at(window), now: now.Add(window), count: 1},
		}},
		{"a key inside the window outlives a prune", 10, []step{
			{add: true, key: key(1), t: at(-time.Hour), now: now, count: 1},
			{add: true, key: key(2), t: at(2 * 24 * time.Hour), now: now.Add(2 * 24 * time.Hour), count: 2},
			{key: key(1), t: at(-time.Hour), now: now.Add(2 * 24 * time.Hour), want: Taken},
		}},
		{"at its limit Seen forgets the older half of the keys", 4, []step{
			{add: true, key: key(1), t: at(-4 * time.Minute), now: now},
			{add: true, key: key(2), t: at(-3 * time.Minute), now: now},
			{add: true, key: key(3), t: at(-2 * time.Minute), now: now},
			{add: true, key: key(4), t: at(-time.Minute), now: now, count: 4},
			{add: true, key: key(5), t: at(0), now: now, count: 3},
			{key: key(1), t: at(-4 * time.Minute), now: now, want: New},
			{key: key(3), t: at(-2 * time.Minute), now: now, want: Taken},
			{key: key(5), t: at(0), now: now, want: Taken},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := NewSeen(window, c.limit)
			for i, st := range c.steps {
				if st.add {
					s.Add(st.key, st.t, st.now)
					if st.count != 0 && s.Len() != st.count {
						t.Fatalf("step %d: Len = %d after Add, want %d", i, s.Len(), st.count)
					}
					continue
				}
				if got := s.Check(st.key, st.t, st.now); got != st.want {
					t.Fatalf("step %d: Check = %v, want %v", i, got, st.want)
				}
			}
		})
	}
}
