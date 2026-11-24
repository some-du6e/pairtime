// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/store/storetest"
)

// flagsOf returns submission id's flags as code: detail, for messages and
// comparisons.
func (h *harness) flagsOf(id string) []string {
	h.t.Helper()
	flags, err := Flags(context.Background(), h.db.Read, id)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, f := range flags {
		out = append(out, f.Code+": "+f.Detail)
	}
	return out
}

// TestConcurrentDevices checks the one number the freeze computes
// (D-sb1-submissions, Q8): concurrent_devices, the minutes the frozen
// windows touch in which two of the account's devices credited one slot
// and at least one of them credited it to one of P's projects, stored as a
// flag only when there is one, and never for a program that takes no
// hours.
func TestConcurrentDevices(t *testing.T) {
	m := t0.Add(-3 * day)
	tests := []struct {
		name string
		// minutes stores the minutes before the create.
		minutes func(h *harness)
		// body is the create, from the laptop; empty means p-app to
		// hard-mode over [m, m + 10 minutes].
		body string
		want []string
	}{
		{
			name: "two devices crediting one slot to two projects outside P raise no concurrent_devices",
			minutes: func(h *harness) {
				h.minute(u1, "d1", m.Add(time.Minute), "p-web")
				h.minute(u1, "d2", m.Add(time.Minute), "p-tool")
			},
		},
		{
			name: "the same minute with one of them crediting a project in P raises it",
			minutes: func(h *harness) {
				h.minute(u1, "d1", m.Add(time.Minute), "p-app")
				h.minute(u1, "d2", m.Add(time.Minute), "p-tool")
			},
			want: []string{`concurrent_devices: {"minutes":1}`},
		},
		{
			name: "a slot P's project lost to another project still counts",
			minutes: func(h *harness) {
				h.minute(u1, "d1", m.Add(time.Minute), "p-web")
				h.minute(u1, "d2", m.Add(time.Minute), "p-app")
			},
			want: []string{`concurrent_devices: {"minutes":1}`},
		},
		{
			name: "two devices crediting different slots of a minute aren't concurrent",
			minutes: func(h *harness) {
				h.minute(u1, "d1", m.Add(time.Minute), "p-app")
				h.minute(u1, "d2", m.Add(time.Minute), "", "p-tool")
			},
		},
		{
			name: "each minute counts once, and minutes outside the windows don't count",
			minutes: func(h *harness) {
				for _, at := range []time.Duration{time.Minute, 2 * time.Minute, -time.Minute, 10 * time.Minute} {
					h.minute(u1, "d1", m.Add(at), "p-app")
					h.minute(u1, "d2", m.Add(at), "p-app")
				}
			},
			want: []string{`concurrent_devices: {"minutes":2}`},
		},
		{
			name: "a minute two windows touch counts once",
			minutes: func(h *harness) {
				h.publish(u1, "d1", "k_app", "dl1", m.Add(30*time.Second))
				h.publish(u1, "d1", "k_app", "dl2", m.Add(5*time.Minute+30*time.Second))
				h.publish(u1, "d1", "k_app", "dl3", m.Add(10*time.Minute+30*time.Second))
				h.minute(u1, "d1", m.Add(5*time.Minute), "p-app")
				h.minute(u1, "d2", m.Add(5*time.Minute), "p-tool")
			},
			body: devlogs("hard-mode", "req-1", "p-app"),
			want: []string{`concurrent_devices: {"minutes":1}`},
		},
		{
			name: "the minute a window starts in counts",
			minutes: func(h *harness) {
				h.minute(u1, "d1", m, "p-app")
				h.minute(u1, "d2", m, "p-tool")
			},
			body: window("hard-mode", "req-1", m.Add(30*time.Second), m.Add(10*time.Minute)),
			want: []string{`concurrent_devices: {"minutes":1}`},
		},
		{
			name: "another account's devices aren't the account's",
			minutes: func(h *harness) {
				h.minute(u1, "d1", m.Add(time.Minute), "p-app")
				h.minute(u2, "d3", m.Add(time.Minute), "p-other")
			},
		},
		{
			name: "a submission to a program that takes no hours gets no flags at all",
			minutes: func(h *harness) {
				h.minute(u1, "d1", m.Add(time.Minute), "p-app")
				h.minute(u1, "d2", m.Add(time.Minute), "p-tool")
			},
			body: window("calibration", "req-1", m, m.Add(10*time.Minute)),
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				h := newHarness(t, engine)
				tt.minutes(h)
				body := tt.body
				if body == "" {
					body = window("hard-mode", "req-1", m, m.Add(10*time.Minute))
				}
				as := "ptd_laptop"
				if strings.Contains(body, `"projects"`) {
					as = "sess:fresh"
				}
				rec := h.call(as, "POST", SubmissionsPath, body)
				want(t, rec, http.StatusCreated, "")
				v := view(t, rec)
				if got := h.flagsOf(v.ID); !slices.Equal(got, tt.want) {
					t.Errorf("flags %q, want %q", got, tt.want)
				}
				if strings.Contains(rec.Body.String(), FlagConcurrentDevices) {
					t.Errorf("the teen view shows the flag: %s", rec.Body.String())
				}
			})
		}
	}
}

// TestRepoOverlap checks the freeze's repo_overlap flag (SP 10.2;
// D-sb1-submissions, Q11): a submission of another account on a shared
// repo fingerprint, frozen, not withdrawn and to a program that takes
// hours, whose windows overlap the new one's flags the new one with its
// id, and itself with the new one's while it is undecided. Fingerprints
// come through the Fingerprints interface, which finds none until WP-SV10.
func TestRepoOverlap(t *testing.T) {
	a, b, c := t0.Add(-6*day), t0.Add(-4*day), t0.Add(-2*day)
	shared := fakeFingerprints{
		{Account: u1, Key: Key{Device: "d1", ProjectKey: "k_app"}}: {"fp-repo"},
		{Account: u2, Key: Key{Device: "d3", ProjectKey: "k_app"}}: {"fp-repo", "fp-fork"},
	}
	detail := func(ids ...string) string {
		slices.Sort(ids)
		b, _ := json.Marshal(overlapDetail{Submissions: ids})
		return FlagRepoOverlap + ": " + string(b)
	}
	tests := []struct {
		name string
		fp   Fingerprints
		run  func(t *testing.T, h *harness)
	}{
		{
			name: "another account's submission on a shared fingerprint over an overlapping window flags both, each naming the other",
			fp:   shared,
			run: func(t *testing.T, h *harness) {
				theirs := h.create("ptd_other", window("hard-mode", "req-1", a, c), http.StatusCreated)
				mine := h.create("ptd_laptop", window("stardance", "req-1", a, b), http.StatusCreated)
				if got := h.flagsOf(mine.ID); !slices.Equal(got, []string{detail(theirs.ID)}) {
					t.Errorf("the new submission's flags %q, want it to name %s", got, theirs.ID)
				}
				if got := h.flagsOf(theirs.ID); !slices.Equal(got, []string{detail(mine.ID)}) {
					t.Errorf("the other submission's flags %q, want it to name %s", got, mine.ID)
				}
				later := h.create("ptd_laptop", window("hard-mode", "req-2", b, c), http.StatusCreated)
				if got := h.flagsOf(theirs.ID); !slices.Equal(got, []string{detail(mine.ID, later.ID)}) {
					t.Errorf("after a third freeze the other submission's flags %q, want them to name both", got)
				}
			},
		},
		{
			name: "windows that don't overlap raise nothing",
			fp:   shared,
			run: func(t *testing.T, h *harness) {
				theirs := h.create("ptd_other", window("hard-mode", "req-1", a, b), http.StatusCreated)
				mine := h.create("ptd_laptop", window("hard-mode", "req-1", b, c), http.StatusCreated)
				if got := append(h.flagsOf(mine.ID), h.flagsOf(theirs.ID)...); len(got) != 0 {
					t.Errorf("flags %q, want none", got)
				}
			},
		},
		{
			name: "a withdrawn submission raises nothing",
			fp:   shared,
			run: func(t *testing.T, h *harness) {
				theirs := h.create("ptd_other", window("hard-mode", "req-1", a, c), http.StatusCreated)
				want(t, h.call("ptd_other", "POST", SubmissionsPath+"/"+theirs.ID+"/withdraw", ""), http.StatusOK, "")
				mine := h.create("ptd_laptop", window("hard-mode", "req-1", a, b), http.StatusCreated)
				if got := h.flagsOf(mine.ID); len(got) != 0 {
					t.Errorf("flags %q, want none", got)
				}
			},
		},
		{
			name: "a decided submission flags the new one, and isn't flagged itself",
			fp:   shared,
			run: func(t *testing.T, h *harness) {
				theirs := h.create("ptd_other", window("hard-mode", "req-1", a, c), http.StatusCreated)
				h.move(theirs.ID, MetadataRevealed, Challenged, ContentRevealed, Checked, InReview, Decided)
				mine := h.create("ptd_laptop", window("hard-mode", "req-1", a, b), http.StatusCreated)
				if got := h.flagsOf(mine.ID); !slices.Equal(got, []string{detail(theirs.ID)}) {
					t.Errorf("the new submission's flags %q, want it to name %s", got, theirs.ID)
				}
				if got := h.flagsOf(theirs.ID); len(got) != 0 {
					t.Errorf("the decided submission's flags %q, want none", got)
				}
			},
		},
		{
			name: "a submission to a program that takes no hours neither raises nor gets it",
			fp:   shared,
			run: func(t *testing.T, h *harness) {
				calibration := h.create("ptd_other", window("calibration", "req-1", a, c), http.StatusCreated)
				mine := h.create("ptd_laptop", window("hard-mode", "req-1", a, b), http.StatusCreated)
				again := h.create("ptd_laptop", window("calibration", "req-2", a, b), http.StatusCreated)
				if got := append(append(h.flagsOf(calibration.ID), h.flagsOf(mine.ID)...), h.flagsOf(again.ID)...); len(got) != 0 {
					t.Errorf("flags %q, want none", got)
				}
			},
		},
		{
			name: "NoFingerprints, which pairtimed wires until WP-SV10, finds none",
			fp:   NoFingerprints{},
			run: func(t *testing.T, h *harness) {
				theirs := h.create("ptd_other", window("hard-mode", "req-1", a, c), http.StatusCreated)
				mine := h.create("ptd_laptop", window("hard-mode", "req-1", a, b), http.StatusCreated)
				if got := append(h.flagsOf(mine.ID), h.flagsOf(theirs.ID)...); len(got) != 0 {
					t.Errorf("flags %q, want none", got)
				}
			},
		},
		{
			name: "the flag shows in no teen or program view",
			fp:   shared,
			run: func(t *testing.T, h *harness) {
				h.create("ptd_other", window("hard-mode", "req-1", a, c), http.StatusCreated)
				mine := h.create("ptd_laptop", window("hard-mode", "req-1", a, b), http.StatusCreated)
				for _, c := range []struct{ as, target string }{
					{"ptd_laptop", SubmissionsPath + "/" + mine.ID},
					{"sess:fresh", SubmissionsPath},
					{"prog:hard-mode", SubmissionsPath + "/" + mine.ID},
					{"prog:hard-mode", SubmissionsPath},
				} {
					rec := h.call(c.as, "GET", c.target, "")
					want(t, rec, http.StatusOK, "")
					if strings.Contains(rec.Body.String(), FlagRepoOverlap) || strings.Contains(rec.Body.String(), "fp-repo") {
						t.Errorf("%s %s shows the flag: %s", c.as, c.target, rec.Body.String())
					}
				}
			},
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				tt.run(t, newHarness(t, engine, func(o *Options) { o.Fingerprints = tt.fp }))
			})
		}
	}
}
