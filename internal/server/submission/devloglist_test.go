// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/store/storetest"
)

// TestDevlogWindows checks the lists of devlog windows a devlog-mode
// create freezes (DESIGN 6.2 D13; D-sb1-submissions, Q11 and Q12): the time
// between consecutive devlogs of any of P's projects, by when the server
// received each, none starting before the latest end of any window frozen
// on one of P's keys by a submission that wasn't withdrawn and went to a
// program that takes hours, and cut at the 180-day horizon.
func TestDevlogWindows(t *testing.T) {
	t1, t2, t3, t4 := t0.Add(-10*day), t0.Add(-8*day), t0.Add(-6*day), t0.Add(-4*day)
	// three publishes the laptop's devlogs dl1, dl2 and dl3 of p-app at t1,
	// t2 and t3.
	three := func(h *harness) {
		h.publish(u1, "d1", "k_app", "dl1", t1)
		h.publish(u1, "d1", "k_app", "dl2", t2)
		h.publish(u1, "d1", "k_app", "dl3", t3)
	}
	// list creates a devlog-mode submission of p-app and returns its
	// windows.
	list := func(t *testing.T, h *harness, program, id string) [][2]time.Time {
		t.Helper()
		return windowsOfView(h.create("sess:fresh", devlogs(program, id, "p-app"), http.StatusCreated))
	}
	tests := []struct {
		name string
		run  func(t *testing.T, h *harness)
	}{
		{
			name: "with devlogs at t1, t2 and t3 inside the horizon and no earlier submission, a devlog-mode create freezes exactly [t1, t2] and [t2, t3]",
			run: func(t *testing.T, h *harness) {
				three(h)
				if got, want := list(t, h, "hard-mode", "req-1"), [][2]time.Time{{t1, t2}, {t2, t3}}; !reflect.DeepEqual(got, want) {
					t.Errorf("froze %s, want %s", spans(got), spans(want))
				}
			},
		},
		{
			name: "with devlogs 200, 100 and 10 days before the freeze, it freezes [the freeze minus 180 days, the 100-day devlog] and [the 100-day devlog, the 10-day devlog]",
			run: func(t *testing.T, h *harness) {
				h.publish(u1, "d1", "k_app", "dl1", t0.Add(-200*day))
				h.publish(u1, "d1", "k_app", "dl2", t0.Add(-100*day))
				h.publish(u1, "d1", "k_app", "dl3", t0.Add(-10*day))
				want := [][2]time.Time{{t0.Add(-180 * day), t0.Add(-100 * day)}, {t0.Add(-100 * day), t0.Add(-10 * day)}}
				if got := list(t, h, "hard-mode", "req-1"); !reflect.DeepEqual(got, want) {
					t.Errorf("froze %s, want %s", spans(got), spans(want))
				}
			},
		},
		{
			name: "a devlog list never starts before the horizon: windows that end by it are left out",
			run: func(t *testing.T, h *harness) {
				h.publish(u1, "d1", "k_app", "dl1", t0.Add(-300*day))
				h.publish(u1, "d1", "k_app", "dl2", t0.Add(-180*day))
				want(t, h.call("sess:fresh", "POST", SubmissionsPath, devlogs("hard-mode", "req-1", "p-app")), http.StatusConflict, CodeNoDevlogWindows)
				h.publish(u1, "d1", "k_app", "dl3", t0.Add(-day))
				if got, want := list(t, h, "hard-mode", "req-1"), [][2]time.Time{{t0.Add(-180 * day), t0.Add(-day)}}; !reflect.DeepEqual(got, want) {
					t.Errorf("froze %s, want %s", spans(got), spans(want))
				}
			},
		},
		{
			name: "with devlogs at t1, t2 and t3 and a submission that froze [t1, t2] and [t2, t3], deleting the t3 devlog and publishing it again at t4 makes the next list [t3, t4]",
			run: func(t *testing.T, h *harness) {
				three(h)
				list(t, h, "hard-mode", "req-1")
				h.unpublish(u1, "dl3", t4)
				h.publish(u1, "d1", "k_app", "dl4", t4)
				if got, want := list(t, h, "stardance", "req-2"), [][2]time.Time{{t3, t4}}; !reflect.DeepEqual(got, want) {
					t.Errorf("froze %s, want %s", spans(got), spans(want))
				}
			},
		},
		{
			name: "a frozen explicit window's end bounds the next list the same way",
			run: func(t *testing.T, h *harness) {
				three(h)
				h.publish(u1, "d1", "k_app", "dl4", t4)
				h.create("ptd_laptop", window("hard-mode", "req-1", t1.Add(-day), t3), http.StatusCreated)
				if got, want := list(t, h, "stardance", "req-2"), [][2]time.Time{{t3, t4}}; !reflect.DeepEqual(got, want) {
					t.Errorf("froze %s, want %s", spans(got), spans(want))
				}
			},
		},
		{
			name: "once that submission is withdrawn the next list can hold [t2, t4]",
			run: func(t *testing.T, h *harness) {
				three(h)
				first := h.create("sess:fresh", devlogs("hard-mode", "req-1", "p-app"), http.StatusCreated)
				h.unpublish(u1, "dl3", t4)
				h.publish(u1, "d1", "k_app", "dl4", t4)
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath+"/"+first.ID+"/withdraw", ""), http.StatusOK, "")
				if got, want := list(t, h, "stardance", "req-2"), [][2]time.Time{{t1, t2}, {t2, t4}}; !reflect.DeepEqual(got, want) {
					t.Errorf("froze %s, want %s", spans(got), spans(want))
				}
			},
		},
		{
			name: "when a later submission freezes a window that ends before the latest end an earlier one froze, the next list still starts at that latest end",
			run: func(t *testing.T, h *harness) {
				three(h)
				list(t, h, "hard-mode", "req-1")
				h.create("ptd_laptop", window("stardance", "req-2", t1.Add(-2*day), t1), http.StatusCreated)
				h.publish(u1, "d1", "k_app", "dl4", t4)
				if got, want := list(t, h, "stardance", "req-3"), [][2]time.Time{{t3, t4}}; !reflect.DeepEqual(got, want) {
					t.Errorf("froze %s, want %s", spans(got), spans(want))
				}
			},
		},
		{
			name: "a list runs over the devlogs of every project in P, from either computer, merged",
			run: func(t *testing.T, h *harness) {
				h.publish(u1, "d1", "k_app", "dl1", t1)
				h.publish(u1, "d2", "k_app_desk", "dl2", t2)
				h.publish(u1, "d1", "k_web", "dl3", t3)
				v := h.create("sess:fresh", devlogs("hard-mode", "req-1", "p-app", "p-web"), http.StatusCreated)
				if got, want := windowsOfView(v), [][2]time.Time{{t1, t2}, {t2, t3}}; !reflect.DeepEqual(got, want) {
					t.Errorf("froze %s, want %s", spans(got), spans(want))
				}
			},
		},
		{
			name: "a calibration submission's windows bound no list, since calibration takes no hours",
			run: func(t *testing.T, h *harness) {
				three(h)
				list(t, h, "calibration", "req-1")
				if got, want := list(t, h, "hard-mode", "req-2"), [][2]time.Time{{t1, t2}, {t2, t3}}; !reflect.DeepEqual(got, want) {
					t.Errorf("froze %s, want %s", spans(got), spans(want))
				}
			},
		},
		{
			name: "the preview answers the list a create would freeze now and the frozen end it starts from, for the session and the computer alike",
			run: func(t *testing.T, h *harness) {
				three(h)
				list(t, h, "hard-mode", "req-1")
				h.unpublish(u1, "dl3", t4)
				h.publish(u1, "d1", "k_app", "dl4", t4)
				for _, c := range []struct{ as, target string }{
					{"sess:fresh", DevlogWindowsPath + "?project=p-app"},
					{"ptd_laptop", DevlogWindowsPath + "?project_key=k_app"},
					{"ptd_desktop", DevlogWindowsPath + "?project_key=k_app_desk"},
				} {
					rec := h.call(c.as, "GET", c.target, "")
					want(t, rec, http.StatusOK, "")
					var got devlogWindowsJSON
					if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
						t.Fatal(err)
					}
					if got.NotBefore == nil || !got.NotBefore.Equal(t3) || !got.Horizon.Equal(t0.Add(-180*day)) ||
						len(got.Windows) != 1 || !got.Windows[0].Start.Equal(t3) || !got.Windows[0].End.Equal(t4) {
						t.Errorf("%s %s answered %s, want not_before %s and the one window [%s, %s]", c.as, c.target, rec.Body.String(), t3, t3, t4)
					}
				}
				if got, want := list(t, h, "stardance", "req-2"), [][2]time.Time{{t3, t4}}; !reflect.DeepEqual(got, want) {
					t.Errorf("the create froze %s, want the preview's %s", spans(got), spans(want))
				}
			},
		},
		{
			name: "the preview refuses what a create would: unknown projects, keys from a session, and a program's credential",
			run: func(t *testing.T, h *harness) {
				want(t, h.call("sess:fresh", "GET", DevlogWindowsPath+"?project=p-other", ""), http.StatusBadRequest, CodeProjectUnknown)
				want(t, h.call("sess:fresh", "GET", DevlogWindowsPath+"?project_key=k_app", ""), http.StatusBadRequest, CodeQueryInvalid)
				want(t, h.call("ptd_laptop", "GET", DevlogWindowsPath, ""), http.StatusBadRequest, CodeQueryInvalid)
				want(t, h.call("ptd_laptop", "GET", DevlogWindowsPath+"?project=p-app&project_key=k_app", ""), http.StatusBadRequest, CodeQueryInvalid)
				want(t, h.call("prog:hard-mode", "GET", DevlogWindowsPath+"?project=p-app", ""), http.StatusForbidden, CodeProgramNotAllowed)
			},
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) { tt.run(t, newHarness(t, engine)) })
		}
	}
}

// TestFrozenAfterDevlogChange is PLAN WP-SB1's check that deleting or
// publishing a devlog after a freeze changes none of the submission's
// windows or numbers (DESIGN 7.5 step 2): the view and the flags read the
// same after as before.
func TestFrozenAfterDevlogChange(t *testing.T) {
	t1, t2, t3 := t0.Add(-10*day), t0.Add(-8*day), t0.Add(-6*day)
	tests := []struct {
		name   string
		change func(h *harness)
	}{
		{name: "deleting a devlog inside the frozen windows changes nothing", change: func(h *harness) { h.unpublish(u1, "dl2", t0) }},
		{name: "deleting the devlog that ends the last window changes nothing", change: func(h *harness) { h.unpublish(u1, "dl3", t0) }},
		{name: "publishing a devlog inside a frozen window changes nothing", change: func(h *harness) { h.publish(u1, "d1", "k_app", "dl-mid", t1.Add(day)) }},
		{name: "publishing a devlog after the windows changes nothing", change: func(h *harness) { h.publish(u1, "d1", "k_app", "dl-late", t0.Add(-day)) }},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				h := newHarness(t, engine)
				h.publish(u1, "d1", "k_app", "dl1", t1)
				h.publish(u1, "d1", "k_app", "dl2", t2)
				h.publish(u1, "d1", "k_app", "dl3", t3)
				h.minute(u1, "d1", t1.Add(time.Hour), "p-app")
				h.minute(u1, "d2", t1.Add(time.Hour), "p-tool")
				v := h.create("sess:fresh", devlogs("hard-mode", "req-1", "p-app"), http.StatusCreated)
				read := func() (string, []Flag) {
					rec := h.call("ptd_laptop", "GET", SubmissionsPath+"/"+v.ID, "")
					want(t, rec, http.StatusOK, "")
					flags, err := Flags(context.Background(), h.db.Read, v.ID)
					if err != nil {
						t.Fatal(err)
					}
					return rec.Body.String(), flags
				}
				before, flagsBefore := read()
				if len(flagsBefore) != 1 || flagsBefore[0].Code != FlagConcurrentDevices {
					t.Fatalf("flags %+v, want concurrent_devices, so the check covers a number", flagsBefore)
				}
				tt.change(h)
				if after, flagsAfter := read(); after != before || !reflect.DeepEqual(flagsAfter, flagsBefore) {
					t.Errorf("after the change the submission reads\n%s with %+v\nwant\n%s with %+v", after, flagsAfter, before, flagsBefore)
				}
			})
		}
	}
}
